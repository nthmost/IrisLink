---
name: irislink
description: Use /irislink to pair two Claude Code sessions via a six-character OTP and relay or mediate messages between them through IrisLink
---

# IrisLink Skill

IrisLink pairs two Claude Code sessions through a short-lived room keyed by a six-character one-time pad. The `irislink` binary handles all plumbing — room lifecycle, hook registration, and polling. This skill is the conversational interface.

If `irislink` is not on PATH:
```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
```

## Prerequisites

IrisLink requires no server. Just a `~/.irislink/config.json` pointing at an MQTT broker:

```json
{"broker_url": "mqtt://homeassistant.local:1883", "broker_user": "irislink", "broker_pass": "..."}
```

---

## Subcommands

### `/irislink create [mode]`

1. Ask the user for their handle (default: `operator`).
2. Run:
   ```bash
   irislink create <handle> [mode]
   ```
3. Display the output — the OTP box. Tell the user to share it with their partner.

---

### `/irislink join <OTP> [mode]`

1. Ask the user for their handle (default: `operator`).
2. Run:
   ```bash
   irislink join <OTP> <handle> [mode]
   ```
3. Display the output — the connected banner.

---

### `/irislink leave`

```bash
irislink leave
```

Display the confirmation.

---

### `/irislink mode <relay|mediate|game-master>`

```bash
OTP=$(python3 -c "import json; print(json.load(open('$HOME/.irislink/rooms/pending.json'))['otp'])")
curl -s -X POST http://localhost:4173/rooms/$OTP/mode \
  -H "Content-Type: application/json" \
  -d '{"mode": "<mode>"}'
python3 -c "
import json, pathlib
p = pathlib.Path('$HOME/.irislink/rooms/$OTP.meta')
d = json.loads(p.read_text()); d['mode'] = '<mode>'; p.write_text(json.dumps(d))
"
```

Confirm the mode switch.

---

### `/irislink status`

```bash
OTP=$(python3 -c "import json; print(json.load(open('$HOME/.irislink/rooms/pending.json'))['otp'])")
irislink events $(irislink pending connector) $OTP 0
tail -10 ~/.irislink/rooms/$OTP.log 2>/dev/null || echo "(no messages yet)"
```

---

### `/irislink` or `/irislink help`

If a room is active, show status. Otherwise list subcommands.

---

## Mediation Modes

| Mode | Behaviour |
|------|-----------|
| `relay` | Pass-through, no LLM |
| `mediate` | Rewrites for clarity via `loki/qwen-coder-14b` |
| `game-master` | Adds GM narrative via `loki/qwen3-coder-30b` |

---

## OTP Alphabet

Valid: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (no 0, 1, I, O). Always uppercase before use.

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
