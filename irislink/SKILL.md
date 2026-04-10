---
name: irislink
description: Use /irislink to pair two Claude Code sessions via a six-character OTP and relay or mediate messages between them through IrisLink
---

# IrisLink Skill

IrisLink pairs two Claude Code sessions through a short-lived room keyed by a six-character one-time pad.

All operations go through the `irislink` binary. If `irislink` is not on PATH, install it:

```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
# or download a release binary to ~/bin/irislink
```

## State Files

All state lives under `~/.irislink/`:

| File | Contents |
|------|---------|
| `config.json` | `{"connector_url": "http://localhost:8357"}` — optional, overrides default port |
| `rooms/pending.json` | Active room `{"otp": "...", "room_id": "..."}` |
| `rooms/<otp>.log` | Incoming messages |
| `rooms/<otp>.pid` | Background poller PID |
| `rooms/<otp>.meta` | `{"handle": "...", "mode": "relay", "cursor": 0}` |

## Prerequisites

Check connector:
```bash
curl -s $(irislink pending connector)/status
```

If it fails, start it:
```bash
irislink proxy &
```

Check rendezvous server:
```bash
curl -s http://localhost:4173/rooms/AAAAAA 2>/dev/null | grep -q error && echo "server up" || echo "server down"
```

If down:
```bash
irislink server &
```

## Seamless Relay Mode

Once a session is active, register the UserPromptSubmit hook so messages are relayed automatically without any `/irislink` prefix.

**Register (on create/join):**

```bash
python3 - << 'EOF'
import json, pathlib
p = pathlib.Path.home() / ".claude" / "settings.json"
s = json.loads(p.read_text()) if p.exists() else {}
entry = {"matcher": "", "hooks": [{"type": "command", "command": "irislink hook"}]}
ups = s.setdefault("hooks", {}).setdefault("UserPromptSubmit", [])
if not any("irislink hook" in str(h) for h in ups):
    ups.append(entry)
p.write_text(json.dumps(s, indent=2))
print("hook registered")
EOF
```

**Deregister (on leave):**

```bash
python3 - << 'EOF'
import json, pathlib
p = pathlib.Path.home() / ".claude" / "settings.json"
s = json.loads(p.read_text())
ups = s.get("hooks", {}).get("UserPromptSubmit", [])
s["hooks"]["UserPromptSubmit"] = [h for h in ups if "irislink hook" not in str(h)]
p.write_text(json.dumps(s, indent=2))
print("hook removed")
EOF
```

## Subcommands

---

### `/irislink` or `/irislink help`

```bash
cat ~/.irislink/rooms/pending.json 2>/dev/null
irislink pending connector
```

If a room is active, show OTP, phase, TTL, participants, mode.
If not, show available subcommands.

---

### `/irislink create [mode]`

Default mode: `relay`. Valid: `relay`, `mediate`, `game-master`.

1. Ask the user for their handle (default: `operator`).

2. Create the room:
   ```bash
   curl -s -X POST http://localhost:4173/rooms \
     -H "Content-Type: application/json" \
     -d '{"handle": "<handle>"}'
   ```

3. Write pending.json (use OTP from server response):
   ```bash
   OTP=<otp_from_response>
   irislink pending write $OTP $(irislink room-id $OTP)
   ```

4. Write session metadata:
   ```bash
   mkdir -p ~/.irislink/rooms
   printf '{"handle":"%s","mode":"%s","cursor":0}' "<handle>" "<mode>" \
     > ~/.irislink/rooms/<OTP>.meta
   ```

5. Display prominently:
   ```
   ╔══════════════════════╗
   ║  IrisLink: ABC123    ║
   ║  mode: relay         ║
   ╚══════════════════════╝
   Share this code with your partner.
   ```

6. Start the poller (see **Polling Loop**).

7. Register the hook (see **Seamless Relay Mode**).

8. Tell the user: "Waiting for partner. Once they join, just type — messages relay automatically."

---

### `/irislink join <OTP> [mode]`

Default mode: `relay`.

1. Validate OTP — 6 chars from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`. Uppercase it.

2. Ask handle (default: `operator`).

3. Join:
   ```bash
   curl -s -X POST http://localhost:4173/rooms/<OTP>/join \
     -H "Content-Type: application/json" \
     -d '{"handle": "<handle>"}'
   ```
   - 404 → code doesn't exist or expired
   - 409 → room full
   - 410 → expired

4. Write pending.json:
   ```bash
   irislink pending write <OTP> $(irislink room-id <OTP>)
   ```

5. Write metadata:
   ```bash
   mkdir -p ~/.irislink/rooms
   printf '{"handle":"%s","mode":"%s","cursor":0}' "<handle>" "<mode>" \
     > ~/.irislink/rooms/<OTP>.meta
   ```

6. Show room state from join response, then display:

   ```
    ___      _     _     _       _
   |_ _|_ __(_)___| |   (_)_ __ | | __
    | || '__| / __| |   | | '_ \| |/ /
    | || |  | \__ \ |___| | | | |   <
   |___|_|  |_|___/_____|_|_| |_|_|\_\

   connected  •  room: <OTP>  •  mode: <mode>
   partner: <handle>
   ```

7. Start the poller.

8. Register the hook.

9. Tell the user: "Joined. Just type your messages. `/irislink leave` when done."

---

### `/irislink send <text>`

```bash
OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])")
HANDLE=$(python3 -c "import json; print(json.load(open(f'/Users/nthmost/.irislink/rooms/{\"$OTP\"}.meta'))['handle'])")
MODE=$(python3 -c "import json; print(json.load(open(f'/Users/nthmost/.irislink/rooms/{\"$OTP\"}.meta'))['mode'])")
CONNECTOR=$(irislink pending connector)
```

If mode is not `relay`:
```bash
irislink mediate $MODE "<text>"
```
Show both versions, confirm.

Send:
```bash
irislink send $CONNECTOR $OTP $HANDLE "<text>"
```

---

### `/irislink mode <relay|mediate|game-master>`

```bash
OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])")
curl -s -X POST http://localhost:4173/rooms/$OTP/mode \
  -H "Content-Type: application/json" \
  -d '{"mode": "<mode>"}'
# Update meta
python3 -c "
import json, pathlib
p = pathlib.Path('/Users/nthmost/.irislink/rooms/$OTP.meta')
d = json.loads(p.read_text()); d['mode'] = '<mode>'; p.write_text(json.dumps(d))
"
```

---

### `/irislink status`

```bash
OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])")
CONNECTOR=$(irislink pending connector)
irislink events $CONNECTOR $OTP 0
tail -10 ~/.irislink/rooms/$OTP.log 2>/dev/null || echo "(no messages yet)"
```

---

### `/irislink leave`

```bash
OTP=$(python3 -c "import json; print(json.load(open('/Users/nthmost/.irislink/rooms/pending.json'))['otp'])" 2>/dev/null)
curl -s -X DELETE http://localhost:4173/rooms/$OTP
kill $(cat ~/.irislink/rooms/$OTP.pid 2>/dev/null) 2>/dev/null; true
irislink pending clear
rm -f ~/.irislink/rooms/$OTP.pid ~/.irislink/rooms/$OTP.meta
```

Deregister the hook (see **Seamless Relay Mode**).

Confirm: "Left room $OTP. Log at ~/.irislink/rooms/$OTP.log"

---

## Polling Loop

```bash
OTP=<otp>
HANDLE=<handle>
CONNECTOR=$(irislink pending connector)
LOG=~/.irislink/rooms/${OTP}.log
mkdir -p ~/.irislink/rooms

cat > /tmp/irislink_poll_${OTP}.sh << POLL
#!/usr/bin/env bash
OTP="$OTP"
HANDLE="$HANDLE"
CONNECTOR="$CONNECTOR"
LOG="$LOG"
CURSOR=0
PREV_PHASE=""

while true; do
    RESULT=\$(irislink events \$CONNECTOR \$OTP \$CURSOR 2>/dev/null)
    [ -z "\$RESULT" ] && sleep 2 && continue

    PHASE=\$(echo "\$RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('phase',''))" 2>/dev/null)
    NEXT=\$(echo "\$RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('next',0))" 2>/dev/null)

    if [ "\$PHASE" = "active" ] && [ "\$PREV_PHASE" != "active" ]; then
        echo ""
        echo " ___      _     _     _       _    "
        echo "|_ _|_ __(_)___| |   (_)_ __ | | __"
        echo " | || '__| / __| |   | | '_ \| |/ /"
        echo " | || |  | \__ \ |___| | | | |   < "
        echo "|___|_|  |_|___/_____|_|_| |_|_|\_\\"
        echo ""
        echo "connected  •  room: $OTP"
        echo "partner has joined — just type!"
        echo ""
    fi
    PREV_PHASE=\$PHASE

    echo "\$RESULT" | python3 -c "
import sys, json, datetime
d = json.load(sys.stdin)
for e in d.get('events', []):
    if e.get('sender') != '$HANDLE':
        ts = datetime.datetime.fromtimestamp(e['timestamp']/1000).strftime('%H:%M:%S')
        print(f'[{ts}] {e[\"sender\"]}: {e[\"text\"]}')
" >> "\$LOG" 2>/dev/null

    [ -n "\$NEXT" ] && [ "\$NEXT" != "0" ] && CURSOR=\$NEXT
    [ "\$PHASE" = "closed" ] && echo "[room closed]" >> "\$LOG" && break
    sleep 2
done
POLL

chmod +x /tmp/irislink_poll_${OTP}.sh
/tmp/irislink_poll_${OTP}.sh &
echo $! > ~/.irislink/rooms/${OTP}.pid
echo "Poller started."
```

---

## Mediation Modes

| Mode | Behaviour |
|------|-----------|
| `relay` | Pass-through, no LLM |
| `mediate` | Rewrites for clarity via `loki/qwen-coder-14b` |
| `game-master` | Adds GM narrative via `loki/qwen3-coder-30b` |

Call: `irislink mediate <mode> "<text>"`

---

## OTP Alphabet

Valid: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (no 0, 1, I, O)
Always uppercase before use.

---

## Error Reference

| Situation | Fix |
|-----------|-----|
| `irislink: command not found` | `go install github.com/nthmost/IrisLink/cmd/irislink@latest` |
| Connector not responding | `irislink proxy &` |
| Server not responding | `irislink server &` |
| 404 on join | Code expired — ask partner for a new one |
| 409 on join | Room full |
| 410 anywhere | Room expired — leave and start fresh |
