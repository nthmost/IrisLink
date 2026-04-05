---
name: irislink
description: Use /irislink to pair two Claude Code sessions via a six-character OTP and relay or mediate messages between them through IrisLink
---

# IrisLink Skill

IrisLink pairs two Claude Code sessions through a short-lived room keyed by a six-character one-time pad. This skill handles room creation, joining, message sending, mediation, and cleanup.

## Prerequisites

Before any subcommand, verify the connector is running:

```bash
curl -s http://localhost:8357/status
```

If this fails or returns `{"detail": ...}`, tell the user:

> The IrisLink connector is not running. Start it with:
> ```
> python connectors/claude_proxy.py
> ```
> Then retry the command.

Also confirm the rendezvous server is reachable at `IRISLINK_BASE_URL` (default `http://localhost:4173`). If unreachable, tell the user to run `uvicorn main:app --port 4173` from the `server/` directory.

## State Files

All state lives under `~/.irislink/rooms/`:

| File | Contents |
|------|----------|
| `config.json` | `{"connector_url": "http://localhost:8357"}` — optional, overrides default connector port |
| `pending.json` | `{"otp": "ABC123", "room_id": "1f2e..."}` — written on create/join, deleted on leave |
| `<otp>.log` | Incoming messages, one line per entry |
| `<otp>.pid` | PID of the background polling loop |
| `<otp>.meta` | `{"handle": "...", "mode": "relay", "cursor": 0}` — local session metadata |

`config.json` is read by the helpers and hook automatically. To use a non-default connector port (e.g. when two sessions share a machine), write it before starting:

```bash
mkdir -p ~/.irislink
echo '{"connector_url": "http://localhost:8358"}' > ~/.irislink/config.json
```

## Seamless Relay Mode (UserPromptSubmit Hook)

Once a session is active, the `irislink_hook.py` script is registered as a `UserPromptSubmit` hook. This means the user can type messages naturally without any `/irislink` prefix — Claude will automatically relay each message and surface inbound messages before responding.

**Register the hook on create/join** by adding to `~/.claude/settings.json`:

```bash
python3 - << 'EOF'
import json, pathlib

settings_path = pathlib.Path.home() / ".claude" / "settings.json"
settings = json.loads(settings_path.read_text()) if settings_path.exists() else {}

hook = {
    "hooks": {
        "UserPromptSubmit": [
            {
                "matcher": "",
                "hooks": [
                    {
                        "type": "command",
                        "command": "python SKILL_DIR/connectors/irislink_hook.py"
                    }
                ]
            }
        ]
    }
}

# Merge hooks
existing_hooks = settings.get("hooks", {})
existing_ups = existing_hooks.get("UserPromptSubmit", [])
new_entry = hook["hooks"]["UserPromptSubmit"][0]
if not any(h.get("hooks", [{}])[0].get("command", "").endswith("irislink_hook.py") for h in existing_ups):
    existing_ups.append(new_entry)
existing_hooks["UserPromptSubmit"] = existing_ups
settings["hooks"] = existing_hooks

settings_path.write_text(json.dumps(settings, indent=2))
print("Hook registered.")
EOF
```

Replace `SKILL_DIR` with the absolute path to the IrisLink repo root.

**Remove the hook on leave** by running the inverse (filter out the irislink_hook.py entry from UserPromptSubmit hooks).

When the hook is active, every user message automatically:
1. Checks for an active IrisLink session
2. Relays the message to the room
3. Surfaces any new inbound messages
4. Then lets Claude respond normally

Explicit `/irislink` commands pass through the hook unchanged.

## Subcommands

---

### `/irislink` or `/irislink help`

If a room is active (pending.json exists), show its status:

```bash
curl -s http://localhost:8357/status
```

Display: OTP, room phase, TTL remaining, partner handle(s), current mode.

Then show:
```
Subcommands: create [mode] · join <OTP> [mode] · send <text> · mode <relay|mediate|game-master> · status · leave
```

If no room is active, show just the subcommand list and a brief description.

---

### `/irislink create [mode]`

Default mode is `relay`. Valid modes: `relay`, `mediate`, `game-master`.

**Steps:**

1. Generate OTP and derive room_id:
   ```bash
   OTP=$(python connectors/irislink_helpers.py generate_otp)
   ROOM_ID=$(python connectors/irislink_helpers.py derive_room_id $OTP)
   ```

2. Ask the user for their handle if not previously set (default: `operator`). Store it for this session.

3. Create the room on the rendezvous server:
   ```bash
   curl -s -X POST http://localhost:4173/rooms \
     -H "Content-Type: application/json" \
     -d "{\"handle\": \"<handle>\"}"
   ```
   The server generates its own OTP. Use the OTP returned in the response, not the one from step 1.

4. Write pending.json using the OTP from the server response:
   ```bash
   python connectors/irislink_helpers.py write_pending <otp_from_response> <room_id_derived_from_that_otp>
   ```
   Re-derive room_id from the server's OTP:
   ```bash
   ROOM_ID=$(python connectors/irislink_helpers.py derive_room_id <otp_from_response>)
   python connectors/irislink_helpers.py write_pending <otp_from_response> $ROOM_ID
   ```

5. Write session metadata:
   ```bash
   mkdir -p ~/.irislink/rooms
   echo '{"handle": "<handle>", "mode": "<mode>", "cursor": 0}' > ~/.irislink/rooms/<otp>.meta
   ```

6. Display the OTP prominently:
   ```
   ╔══════════════════════════════╗
   ║   IrisLink code: ABC123      ║
   ║   mode: relay                ║
   ║   Share this code with your partner.
   ╚══════════════════════════════╝
   ```

7. Start the background polling loop (see **Polling Loop** below).

8. Register the UserPromptSubmit hook (see **Seamless Relay Mode** above) so the user can type messages without `/irislink` prefix.

9. Tell the user: "Waiting for your partner to run `/irislink join <OTP>`. Once connected, just type your messages normally — no `/irislink` prefix needed."

---

### `/irislink join <OTP> [mode]`

Default mode is `relay`.

**Steps:**

1. Validate OTP format — must be exactly 6 characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`. If invalid, say: "That doesn't look like a valid IrisLink code. Codes are 6 characters using A-Z (no I or O) and 2-7."

2. Ask the user for their handle if not previously set (default: `operator`).

3. Join the room:
   ```bash
   curl -s -X POST http://localhost:4173/rooms/<OTP>/join \
     -H "Content-Type: application/json" \
     -d "{\"handle\": \"<handle>\"}"
   ```
   If response is 404: "That code doesn't exist or has expired. Ask your partner to create a new one."
   If response is 409: "That room already has two participants."
   If response is 410: "That code has expired. Ask your partner to create a new one."

4. Derive room_id and write pending.json:
   ```bash
   python connectors/irislink_helpers.py write_pending <OTP> $(python connectors/irislink_helpers.py derive_room_id <OTP>)
   ```

5. Write session metadata:
   ```bash
   echo '{"handle": "<handle>", "mode": "<mode>", "cursor": 0}' > ~/.irislink/rooms/<OTP>.meta
   ```

6. Show the room state returned by the join response: partner handle, phase, TTL.

7. Start the background polling loop (see **Polling Loop** below).

8. Register the UserPromptSubmit hook (see **Seamless Relay Mode** above).

9. Tell the user: "Joined room <OTP>. Just type your messages normally — they'll be relayed automatically. Use `/irislink leave` when done."

---

### `/irislink send <text>`

Requires an active room (pending.json exists).

**Steps:**

1. Read OTP and handle from pending.json / meta file.

2. If mode is not `relay`, mediate the text first:
   ```bash
   MEDIATED=$(python connectors/irislink_helpers.py mediate <mode> "<text>")
   ```
   Show the user both the original and mediated text, and ask: "Send this mediated version? (yes/edit/cancel)"

3. Send the message via the connector:
   ```bash
   python connectors/irislink_helpers.py post_message http://localhost:8357 <OTP> <handle> "<text_to_send>"
   ```

4. Confirm: "Sent."

---

### `/irislink mode <relay|mediate|game-master>`

Requires an active room.

**Steps:**

1. Validate mode is one of `relay`, `mediate`, `game-master`.

2. Update the rendezvous server:
   ```bash
   curl -s -X POST http://localhost:4173/rooms/<OTP>/mode \
     -H "Content-Type: application/json" \
     -d "{\"mode\": \"<mode>\"}"
   ```

3. Update the local meta file:
   ```bash
   python3 -c "
   import json, pathlib
   p = pathlib.Path('~/.irislink/rooms/<OTP>.meta').expanduser()
   d = json.loads(p.read_text())
   d['mode'] = '<mode>'
   p.write_text(json.dumps(d))
   "
   ```

4. Confirm: "Mode switched to <mode>."

---

### `/irislink status`

Requires an active room.

1. Poll the connector:
   ```bash
   curl -s "http://localhost:8357/events?room_otp=<OTP>&since=0"
   ```

2. Display: phase, TTL, participants and their status, last 5 messages, waitingOn.

3. Tail the log for recent activity:
   ```bash
   tail -10 ~/.irislink/rooms/<OTP>.log 2>/dev/null
   ```

---

### `/irislink leave`

**Steps:**

1. Read OTP from pending.json.

2. Close the room on the rendezvous server:
   ```bash
   curl -s -X DELETE http://localhost:4173/rooms/<OTP>
   ```

3. Kill the polling loop:
   ```bash
   kill $(cat ~/.irislink/rooms/<OTP>.pid 2>/dev/null) 2>/dev/null
   ```

4. Clean up state files:
   ```bash
   python connectors/irislink_helpers.py clear_pending
   rm -f ~/.irislink/rooms/<OTP>.pid
   rm -f ~/.irislink/rooms/<OTP>.meta
   ```
   Keep `<OTP>.log` as session history.

5. Deregister the UserPromptSubmit hook:
   ```bash
   python3 - << 'EOF'
   import json, pathlib
   p = pathlib.Path.home() / ".claude" / "settings.json"
   s = json.loads(p.read_text())
   ups = s.get("hooks", {}).get("UserPromptSubmit", [])
   s["hooks"]["UserPromptSubmit"] = [
       h for h in ups
       if not any("irislink_hook.py" in e.get("command", "") for e in h.get("hooks", []))
   ]
   p.write_text(json.dumps(s, indent=2))
   print("Hook removed.")
   EOF
   ```

6. Confirm: "Left room <OTP>. Session log saved at ~/.irislink/rooms/<OTP>.log"

---

## Polling Loop

Start this after `create` or `join`. It runs in the background and appends incoming messages to the log file. The skill watches the log and surfaces new entries to the user.

**Start the poller:**

Write this script to a temp file and run it in the background:

```bash
cat > /tmp/irislink_poll_<OTP>.sh << 'POLL'
#!/usr/bin/env bash
OTP="$1"
HANDLE="$2"
LOG=~/.irislink/rooms/${OTP}.log
META=~/.irislink/rooms/${OTP}.meta
CURSOR=0

while true; do
    RESULT=$(curl -s "http://localhost:8357/events?room_otp=${OTP}&since=${CURSOR}" 2>/dev/null)
    if [ -z "$RESULT" ]; then
        sleep 2
        continue
    fi

    PHASE=$(echo "$RESULT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('phase',''))" 2>/dev/null)
    NEXT=$(echo "$RESULT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('next',0))" 2>/dev/null)
    EVENTS=$(echo "$RESULT" | python3 -c "
import sys, json, datetime
d = json.load(sys.stdin)
for e in d.get('events', []):
    if e.get('sender') != sys.argv[1]:
        ts = datetime.datetime.fromtimestamp(e['timestamp']/1000).strftime('%H:%M:%S')
        print(f\"[{ts}] {e['sender']}: {e['text']}\")
" "$HANDLE" 2>/dev/null)

    if [ -n "$EVENTS" ]; then
        echo "$EVENTS" >> "$LOG"
    fi

    if [ -n "$NEXT" ] && [ "$NEXT" != "0" ]; then
        CURSOR=$NEXT
        # Update cursor in meta
        python3 -c "
import json, pathlib
p = pathlib.Path('~/.irislink/rooms/${OTP}.meta').expanduser()
try:
    d = json.loads(p.read_text())
    d['cursor'] = ${NEXT}
    p.write_text(json.dumps(d))
except: pass
" 2>/dev/null
    fi

    if [ "$PHASE" = "closed" ]; then
        echo "[$(date +%H:%M:%S)] Room closed." >> "$LOG"
        break
    fi

    sleep 2
done
POLL

chmod +x /tmp/irislink_poll_<OTP>.sh
/tmp/irislink_poll_<OTP>.sh <OTP> <handle> &
echo $! > ~/.irislink/rooms/<OTP>.pid
```

**Watch for new messages:**

After starting the poller, tail the log to show new arrivals. Check periodically or ask the user to run `/irislink status` to see the latest.

If you see new lines in the log that the user hasn't seen yet, surface them in your response.

---

## Mediation Modes

| Mode | Behaviour |
|------|-----------|
| `relay` | Pass messages through unchanged. No LLM call. |
| `mediate` | Rewrite outbound messages to be clearer and more considerate. Uses `loki/qwen-coder-14b` via LiteLLM. Show user both versions before sending. |
| `game-master` | Add a narrative flourish or creative GM prompt after each message. Uses `loki/qwen3-coder-30b` via LiteLLM. |

The mediation helper is: `python connectors/irislink_helpers.py mediate <mode> "<text>"`

---

## OTP Alphabet

Valid characters: `A B C D E F G H J K L M N P Q R S T U V W X Y Z 2 3 4 5 6 7 8 9`

Excluded to avoid visual confusion: `0 1 I O`

Codes are case-insensitive on input — always uppercase before use.

---

## Error Reference

| Situation | Response |
|-----------|----------|
| Connector not running | Prompt user to start `python connectors/claude_proxy.py` |
| Server not running | Prompt user to start `uvicorn main:app --port 4173` from `server/` |
| 404 on join | Code doesn't exist or expired — ask partner to create fresh |
| 409 on join | Room full (2 participants already) |
| 410 on any request | Room expired — run `/irislink leave` then create fresh |
| Invalid OTP format | Tell user the valid alphabet and length |
| Poller PID missing on leave | Skip kill, proceed with cleanup |
