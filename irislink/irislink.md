---
name: irislink
description: Use /irislink to pair two Claude Code sessions via a six-character OTP and relay or mediate messages between them through IrisLink
---

# IrisLink Skill

IrisLink pairs two Claude Code sessions through a short-lived room keyed by a six-character one-time pad. This skill handles room creation, joining, message sending, mediation, and cleanup.

## Key Paths

| Path | Purpose |
|------|---------|
| `~/.irislink/helpers` | Executable wrapper — use this for ALL helper calls |
| `~/.irislink/config.json` | Optional config: `{"connector_url": "http://localhost:8357"}` |
| `~/.irislink/rooms/pending.json` | Active room OTP + room_id |
| `~/.irislink/rooms/<otp>.log` | Incoming messages log |
| `~/.irislink/rooms/<otp>.pid` | Background poller PID |
| `~/.irislink/rooms/<otp>.meta` | `{"handle": "...", "mode": "relay", "cursor": 0}` |

Always use `~/.irislink/helpers <command>` — never call `python connectors/...` directly.

Get the connector URL with: `CONNECTOR=$(~/.irislink/helpers get_connector_url)`

## Prerequisites

Before any subcommand, check the connector is running:

```bash
CONNECTOR=$(~/.irislink/helpers get_connector_url)
curl -s $CONNECTOR/status
```

If this fails, tell the user:

> The IrisLink connector is not running. In the IrisLink repo directory, run:
> ```
> python3 connectors/claude_proxy.py
> ```
> Then retry.

Also check the rendezvous server at `http://localhost:4173`. If unreachable:

> The rendezvous server is not running. In the `server/` directory, run:
> ```
> python3 -m uvicorn main:app --port 4173
> ```

## Seamless Relay Mode (UserPromptSubmit Hook)

Once a session is active, register `irislink_hook.py` as a `UserPromptSubmit` hook so the user can type messages naturally without any `/irislink` prefix.

**Register on create/join:**

```bash
python3 - << 'HOOKEOF'
import json, pathlib

settings_path = pathlib.Path.home() / ".claude" / "settings.json"
settings = json.loads(settings_path.read_text()) if settings_path.exists() else {}

new_entry = {
    "matcher": "",
    "hooks": [{
        "type": "command",
        "command": "python3 /Users/nthmost/projects/git/IrisLink/connectors/irislink_hook.py"
    }]
}

hooks = settings.setdefault("hooks", {})
ups = hooks.setdefault("UserPromptSubmit", [])
if not any("irislink_hook.py" in str(h) for h in ups):
    ups.append(new_entry)

settings_path.write_text(json.dumps(settings, indent=2))
print("Hook registered.")
HOOKEOF
```

**Deregister on leave:**

```bash
python3 - << 'HOOKEOF'
import json, pathlib
p = pathlib.Path.home() / ".claude" / "settings.json"
s = json.loads(p.read_text())
ups = s.get("hooks", {}).get("UserPromptSubmit", [])
s["hooks"]["UserPromptSubmit"] = [
    h for h in ups
    if "irislink_hook.py" not in str(h)
]
p.write_text(json.dumps(s, indent=2))
print("Hook removed.")
HOOKEOF
```

## Subcommands

---

### `/irislink` or `/irislink help`

Check if a room is active:

```bash
cat ~/.irislink/rooms/pending.json 2>/dev/null
```

If active, show: OTP, phase, TTL, partners, mode. Then show available subcommands.

If no room, show:

```
Subcommands: create [mode] · join <OTP> [mode] · send <text> · mode <relay|mediate|game-master> · status · leave
```

---

### `/irislink create [mode]`

Default mode: `relay`. Valid: `relay`, `mediate`, `game-master`.

**Steps:**

1. Ask the user for their handle (default: `operator`).

2. Create the room:
   ```bash
   curl -s -X POST http://localhost:4173/rooms \
     -H "Content-Type: application/json" \
     -d '{"handle": "<handle>"}'
   ```
   Use the `otp` from the response for all subsequent steps.

3. Write pending.json:
   ```bash
   OTP=<otp_from_response>
   ROOM_ID=$(~/.irislink/helpers derive_room_id $OTP)
   ~/.irislink/helpers write_pending $OTP $ROOM_ID
   ```

4. Write session metadata:
   ```bash
   mkdir -p ~/.irislink/rooms
   echo '{"handle": "<handle>", "mode": "<mode>", "cursor": 0}' > ~/.irislink/rooms/<OTP>.meta
   ```

5. Display the OTP prominently:
   ```
   ╔══════════════════════════════╗
   ║   IrisLink code: ABC123      ║
   ║   mode: relay                ║
   ╚══════════════════════════════╝
   Share this code with your partner.
   ```

6. Start the background polling loop (see **Polling Loop** below).

7. Register the UserPromptSubmit hook (see **Seamless Relay Mode** above).

8. Tell the user: "Waiting for your partner. Once they join, just type your messages normally."

---

### `/irislink join <OTP> [mode]`

Default mode: `relay`.

**Steps:**

1. Validate OTP — exactly 6 chars from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`. If invalid: "That doesn't look right. IrisLink codes are 6 characters, A-Z (no I or O) and 2-7."

2. Uppercase the OTP.

3. Ask the user for their handle (default: `operator`).

4. Join the room:
   ```bash
   curl -s -X POST http://localhost:4173/rooms/<OTP>/join \
     -H "Content-Type: application/json" \
     -d '{"handle": "<handle>"}'
   ```
   - 404 → "That code doesn't exist or has expired."
   - 409 → "That room is already full."
   - 410 → "That code has expired."

5. Write pending.json:
   ```bash
   ROOM_ID=$(~/.irislink/helpers derive_room_id <OTP>)
   ~/.irislink/helpers write_pending <OTP> $ROOM_ID
   ```

6. Write session metadata:
   ```bash
   mkdir -p ~/.irislink/rooms
   echo '{"handle": "<handle>", "mode": "<mode>", "cursor": 0}' > ~/.irislink/rooms/<OTP>.meta
   ```

7. Show the room state from the join response: partner handle, phase, TTL.

8. Start the background polling loop (see **Polling Loop** below).

9. Register the UserPromptSubmit hook (see **Seamless Relay Mode** above).

10. Tell the user: "Joined. Just type your messages — they'll be relayed automatically. `/irislink leave` when done."

---

### `/irislink send <text>`

Requires an active room.

1. Read state:
   ```bash
   OTP=$(python3 -c "import json; d=json.load(open('/Users/nthmost/.irislink/rooms/pending.json')); print(d['otp'])")
   META=$(cat ~/.irislink/rooms/${OTP}.meta)
   HANDLE=$(echo $META | python3 -c "import sys,json; print(json.load(sys.stdin)['handle'])")
   MODE=$(echo $META | python3 -c "import sys,json; print(json.load(sys.stdin)['mode'])")
   CONNECTOR=$(~/.irislink/helpers get_connector_url)
   ```

2. If mode is not `relay`, mediate first:
   ```bash
   ~/.irislink/helpers mediate $MODE "<text>"
   ```
   Show both versions, confirm before sending.

3. Send:
   ```bash
   ~/.irislink/helpers post_message $CONNECTOR $OTP $HANDLE "<text>"
   ```

4. Confirm: "Sent."

---

### `/irislink mode <relay|mediate|game-master>`

1. Validate the mode value.

2. Update the server:
   ```bash
   curl -s -X POST http://localhost:4173/rooms/<OTP>/mode \
     -H "Content-Type: application/json" \
     -d '{"mode": "<mode>"}'
   ```

3. Update local meta:
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

```bash
OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])")
CONNECTOR=$(~/.irislink/helpers get_connector_url)
curl -s "$CONNECTOR/events?room_otp=${OTP}&since=0"
echo "---"
tail -10 ~/.irislink/rooms/${OTP}.log 2>/dev/null || echo "(no messages yet)"
```

Display: phase, TTL, participants, last messages, waitingOn.

---

### `/irislink leave`

1. Read OTP:
   ```bash
   OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])" 2>/dev/null)
   ```

2. Close room:
   ```bash
   curl -s -X DELETE http://localhost:4173/rooms/$OTP
   ```

3. Kill poller:
   ```bash
   kill $(cat ~/.irislink/rooms/${OTP}.pid 2>/dev/null) 2>/dev/null; true
   ```

4. Clean up:
   ```bash
   ~/.irislink/helpers clear_pending
   rm -f ~/.irislink/rooms/${OTP}.pid ~/.irislink/rooms/${OTP}.meta
   ```
   Keep `${OTP}.log` as history.

5. Deregister the hook (see **Seamless Relay Mode** above).

6. Confirm: "Left room $OTP. Log saved at ~/.irislink/rooms/${OTP}.log"

---

## Polling Loop

After `create` or `join`, start this background poller. It appends incoming messages to the log so you can surface them to the user.

```bash
OTP=<otp>
HANDLE=<handle>
CONNECTOR=$(~/.irislink/helpers get_connector_url)
LOG=~/.irislink/rooms/${OTP}.log
mkdir -p ~/.irislink/rooms

cat > /tmp/irislink_poll_${OTP}.sh << POLL
#!/usr/bin/env bash
OTP="$OTP"
HANDLE="$HANDLE"
CONNECTOR="$CONNECTOR"
LOG="$LOG"
CURSOR=0

while true; do
    RESULT=\$(curl -s "\${CONNECTOR}/events?room_otp=\${OTP}&since=\${CURSOR}" 2>/dev/null)
    if [ -z "\$RESULT" ]; then sleep 2; continue; fi

    PHASE=\$(echo "\$RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('phase',''))" 2>/dev/null)
    NEXT=\$(echo "\$RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('next',0))" 2>/dev/null)
    echo "\$RESULT" | python3 -c "
import sys, json, datetime
d = json.load(sys.stdin)
handle = '\$HANDLE'
for e in d.get('events', []):
    if e.get('sender') != handle:
        ts = datetime.datetime.fromtimestamp(e['timestamp']/1000).strftime('%H:%M:%S')
        print(f'[{ts}] {e[\"sender\"]}: {e[\"text\"]}')
" >> "\$LOG" 2>/dev/null

    if [ -n "\$NEXT" ] && [ "\$NEXT" != "0" ]; then
        CURSOR=\$NEXT
        python3 -c "
import json, pathlib
p = pathlib.Path('~/.irislink/rooms/\$OTP.meta').expanduser()
try:
    d = json.loads(p.read_text()); d['cursor'] = \$NEXT; p.write_text(json.dumps(d))
except: pass
" 2>/dev/null
    fi

    [ "\$PHASE" = "closed" ] && echo "[\$(date +%H:%M:%S)] Room closed." >> "\$LOG" && break
    sleep 2
done
POLL

chmod +x /tmp/irislink_poll_${OTP}.sh
/tmp/irislink_poll_${OTP}.sh &
echo $! > ~/.irislink/rooms/${OTP}.pid
echo "Poller started (PID $(cat ~/.irislink/rooms/${OTP}.pid))"
```

After starting the poller, periodically tail the log to show new arrivals:
```bash
tail -f ~/.irislink/rooms/<OTP>.log
```
Or check on demand with `/irislink status`.

---

## Mediation Modes

| Mode | Behaviour |
|------|-----------|
| `relay` | Pass-through. No LLM call. |
| `mediate` | Rewrites outbound messages for clarity. Uses `loki/qwen-coder-14b` via LiteLLM at `spartacus.local:4000`. Show user both versions before sending. |
| `game-master` | Adds narrative flourish after each message. Uses `loki/qwen3-coder-30b`. |

Call: `~/.irislink/helpers mediate <mode> "<text>"`

---

## OTP Alphabet

Valid: `A B C D E F G H J K L M N P Q R S T U V W X Y Z 2 3 4 5 6 7 8 9`
Excluded (visual confusion): `0 1 I O`
Always uppercase before use.

---

## Error Reference

| Situation | What to do |
|-----------|-----------|
| `~/.irislink/helpers` not found | Run the install step: copy irislink.md instructions |
| Connector not running | `python3 /Users/nthmost/projects/git/IrisLink/connectors/claude_proxy.py` |
| Server not running | `cd /Users/nthmost/projects/git/IrisLink/server && python3 -m uvicorn main:app --port 4173` |
| 404 on join | Code expired — ask partner to create new one |
| 409 on join | Room full |
| 410 anywhere | Room expired — `/irislink leave`, start fresh |
| Invalid OTP | Must be 6 chars from valid alphabet |
| Poller PID missing on leave | Skip kill, continue cleanup |
