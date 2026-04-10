---
name: irislink
description: Use /irislink to start an encrypted chat session with another person using the IrisLink TUI
---

# IrisLink Skill

IrisLink is a standalone TUI binary. This skill's job is to check that it is installed and then hand off to the binary. Everything else — the chat interface, file context, Claude login, mediation — happens inside the TUI.

## Prerequisites

IrisLink requires an MQTT broker reachable by both parties. Configure `~/.irislink/config.json`:

```json
{
  "broker_url": "mqtt://homeassistant.local:1883",
  "broker_user": "irislink",
  "broker_pass": "yourpassword"
}
```

## Installation Check

Before running any IrisLink command, verify the binary is available:

```bash
which irislink
```

If not found, install from source (recommended — module proxy may lag behind):

```bash
git clone https://github.com/nthmost/IrisLink
cd IrisLink
go build -o ~/go/bin/irislink ./cmd/irislink/
```

Or via `go install`:

```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
```

Make sure `~/go/bin` is on `PATH`.

## Usage

### `/irislink create [handle]`

Ask the user for their handle (default: `operator`), then run:

```bash
irislink create <handle>
```

The TUI launches. It shows the 6-character OTP and waits for the other person to join. Tell the user to share the code out-of-band.

### `/irislink join <OTP> [handle]`

Ask the user for their handle (default: `operator`), then run:

```bash
irislink join <OTP> <handle>
```

The TUI opens and both sides are connected.

### That's it

The binary handles everything from here: chat, file context, Claude login, mode switching, and disconnect. The TUI has a built-in `/help` command.

## Error Reference

| Situation | Fix |
|-----------|-----|
| `irislink: command not found` | Install per the instructions above |
| `cannot connect to broker` | Check `broker_url` in `~/.irislink/config.json` |
| Code expired or wrong | Ask your partner to run `irislink create` again for a fresh code |
