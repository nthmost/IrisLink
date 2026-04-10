# IrisLink

Pair two Claude Code sessions with a six-character code. Once connected, messages flow between them through Claude — optionally rewritten, mediated, or narrated by a game-master persona.

```
Person A                          Person B
────────                          ────────
/irislink create                  /irislink join K7NP3Q
→ code: K7NP3Q   ── share ──→

hey, can you look at this diff?   [relay]  hey, can you look at this diff?
                                  sure, let me check...
[relay]  sure, let me check...
```

## How it works

1. Person A runs `/irislink create` → gets a 6-char OTP, connects to the MQTT broker.
2. They share the code with Person B out-of-band (chat, clipboard, yell across the office).
3. Person B runs `/irislink join <OTP>` → both sides subscribe to the same encrypted room topic.
4. A `UserPromptSubmit` hook fires on every message. It relays outbound messages and surfaces inbound — no `/irislink` prefix required once connected.
5. `/irislink leave` disconnects and removes the hook.

## Transport and security

IrisLink uses MQTT as its transport. It requires no server of its own — point it at any existing MQTT broker (Home Assistant's Mosquitto add-on, a local Mosquitto instance, HiveMQ, etc.).

**E2E encryption:** every payload is encrypted with NaCl secretbox (ChaCha20-Poly1305) using a key derived from the OTP via HKDF-SHA256. The broker operator cannot read message content.

**Privacy:** topic names use a HKDF-derived `room_id`, never the OTP itself. MQTT client IDs are random UUIDs. The OTP never reaches the broker.

## Install

**One-liner** (requires Go 1.21+):

```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
```

Make sure `~/go/bin` is on your `PATH` (add to `~/.zshrc` or `~/.bashrc`):

```bash
export PATH="$HOME/go/bin:$PATH"
```

**Install the skill:**

```bash
mkdir -p ~/.claude/skills/irislink
curl -fsSL https://raw.githubusercontent.com/nthmost/IrisLink/main/irislink/SKILL.md \
  -o ~/.claude/skills/irislink/SKILL.md
```

**No Go on the target machine?** Cross-compile from a machine that has it:

```bash
GOOS=linux GOARCH=amd64 go build -o irislink-linux ./cmd/irislink
scp irislink-linux user@host:~/bin/irislink
```

Common targets: `GOOS=linux GOARCH=arm64` (Raspberry Pi, Apple Silicon Linux), `GOOS=darwin GOARCH=arm64` (Apple Silicon Mac).

## Broker setup

IrisLink needs an MQTT broker reachable by both parties. Point it at one in `~/.irislink/config.json`:

```json
{
  "broker_url": "mqtt://homeassistant.local:1883",
  "broker_user": "irislink",
  "broker_pass": "yourpassword"
}
```

Leave out `broker_user`/`broker_pass` if the broker allows anonymous access.

### Home Assistant (Mosquitto add-on)

HA's Mosquitto add-on authenticates via HA's own user system. To create a dedicated IrisLink user:

1. Go to **Settings → People → Users** (enable Advanced Mode in your profile first).
2. Click **Add User** — name it `irislink`, set a password, role: **User**.
3. That username/password works directly for MQTT auth.
4. To restrict IrisLink to only the `irislink/#` topic namespace, add an ACL to the Mosquitto add-on config in **Settings → Add-ons → Mosquitto broker → Configuration**:

```yaml
customize:
  active: true
  folder: mosquitto
```

Then create `/config/mosquitto/acl.conf`:

```
user irislink
topic readwrite irislink/#
```

And set in the Mosquitto add-on config:

```yaml
acl_file: /config/mosquitto/acl.conf
```

Restart the add-on to apply.

### Standalone Mosquitto

```bash
# Create user
mosquitto_passwd -c /etc/mosquitto/passwd irislink

# /etc/mosquitto/conf.d/irislink.conf
password_file /etc/mosquitto/passwd
acl_file /etc/mosquitto/acl

# /etc/mosquitto/acl
user irislink
topic readwrite irislink/#
```

## Quick start

**Both machines** need a `~/.irislink/config.json` pointing at the same broker.

**Person A's Claude session:**
```
/irislink create
```

Claude shows a 6-char code. Share it with Person B.

**Person B's Claude session:**
```
/irislink join <CODE>
```

Once connected, type normally — the hook relays everything automatically.

```
/irislink leave    # when done
```

## Mediation modes

Switch with `/irislink mode <relay|mediate|game-master>` at any time.

| Mode | What Claude does |
|------|-----------------|
| `relay` | Pass-through. Messages arrive exactly as sent. No LLM call. |
| `mediate` | Rewrites each outbound message to be clearer and more considerate. |
| `game-master` | Adds a brief narrative flourish or creative prompt after each message. |

Mediation uses LiteLLM. Configure the endpoint in `~/.irislink/config.json` (default: `http://spartacus.local:4000`).

## Config reference

`~/.irislink/config.json`:

```json
{
  "broker_url": "mqtt://homeassistant.local:1883",
  "broker_user": "irislink",
  "broker_pass": "yourpassword"
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `broker_url` | `mqtt://localhost:1883` | MQTT broker URL (`mqtt://` or `mqtts://`) |
| `broker_user` | — | Broker username (optional) |
| `broker_pass` | — | Broker password (optional) |

## State files

All runtime state lives under `~/.irislink/`:

```
~/.irislink/
├── config.json              # broker connection settings
└── rooms/
    ├── pending.json         # active room: {"otp": "...", "room_id": "..."}
    ├── <otp>.meta           # {"handle": "...", "mode": "relay", "cursor": 0}
    ├── <otp>.log            # incoming messages (kept after leave)
    └── <otp>.pid            # background poller PID
```

## OTP alphabet

Codes use Crockford Base32: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`

`0`, `1`, `I`, `O` are excluded to prevent visual confusion. Codes are case-insensitive on input.

## Repo layout

```
IrisLink/
├── cmd/irislink/
│   ├── main.go              # CLI entry point + low-level subcommands
│   └── session.go           # create / join / leave / poll
├── internal/
│   ├── crypto/              # OTP generation, HKDF derivation, NaCl Seal/Open
│   ├── transport/           # MQTT v5 client, encrypted pub/sub
│   └── state/               # config.json + pending.json + meta I/O
├── irislink/
│   └── SKILL.md             # Claude Code skill
└── docs/
    ├── architecture.md      # transport and crypto design
    ├── ui-safety.md         # consent + safety UI guidelines
    └── web-ui.md            # lobby UI design spec
```

## What's next

- nthmost/IrisLink#1 — pre-built binaries via goreleaser + GitHub Actions
- nthmost/IrisLink#2 — MQTT transport (this is it, in progress)
- nthmost/IrisLink#3 — SSH key-based identity + encryption upgrade
