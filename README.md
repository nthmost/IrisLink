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

1. Person A runs `/irislink create` → gets a 6-char OTP, a room opens on the rendezvous server.
2. They share the code with Person B out-of-band (chat, clipboard, yell across the office).
3. Person B runs `/irislink join <OTP>` → both sides connect, room goes `active`.
4. A `UserPromptSubmit` hook fires on every message in each Claude session. It relays the message to the room and surfaces any inbound messages — no `/irislink` prefix required once connected.
5. `/irislink leave` closes the room and removes the hook.

## Components

Everything ships as a single Go binary: `irislink`.

| Subcommand | What it does |
|------------|-------------|
| `irislink server` | Rendezvous server — manages rooms, participants, messages (port 4173) |
| `irislink proxy` | Connector proxy — bridges the Claude skill to the rendezvous API (port 8357) |
| `irislink otp` | Generate a random 6-char Crockford Base32 OTP |
| `irislink room-id <otp>` | HKDF-SHA256 derive a room_id from an OTP |
| `irislink pending write/clear/connector` | Manage `~/.irislink/rooms/pending.json` |
| `irislink send <url> <otp> <from> <text>` | POST a message via connector |
| `irislink events <url> <otp> [since]` | GET events with cursor |
| `irislink mediate <mode> <text>` | Transform text via LiteLLM |
| `irislink hook` | UserPromptSubmit hook (stdin JSON → additionalContext JSON) |

The `/irislink` Claude Code skill lives at `irislink/irislink.md`. It instructs Claude how to use the binary for room lifecycle, message relay, and mediation.

## Install

**One-liner** (requires Go 1.21+):

```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
```

**Install the skill:**

```bash
mkdir -p ~/.claude/skills/irislink
curl -fsSL https://raw.githubusercontent.com/nthmost/IrisLink/main/irislink/irislink.md \
  -o ~/.claude/skills/irislink/SKILL.md
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

## Quick start — two machines

**Machine A** (runs the rendezvous server):

```bash
irislink server &   # port 4173
irislink proxy &    # port 8357
```

Note Machine A's LAN IP (e.g. `192.168.1.10`).

**Machine B** (joins — proxy points at Machine A's server):

```bash
IRISLINK_BASE_URL=http://192.168.1.10:4173 irislink proxy &
```

**Machine A's Claude session:**

```
/irislink create
```

Claude shows a 6-char code. Share it with Person B.

**Machine B's Claude session:**

```
/irislink join <CODE>
```

Once connected, both people type messages normally. No prefix needed — the hook relays everything automatically.

```
/irislink leave    # when done
```

## Quick start — same machine (two terminal tabs)

Person A uses the default connector port (8357). Person B needs a different one.

**Person A:**
```bash
irislink server &   # shared server
irislink proxy &    # connector on :8357
# open Claude Code — /irislink create
```

**Person B:**
```bash
# Set a different connector port
mkdir -p ~/.irislink
echo '{"connector_url":"http://localhost:8358"}' > ~/.irislink/config.json

irislink proxy --listen 8358 &
# open a second Claude Code window — /irislink join <CODE>
```

## Mediation modes

Switch with `/irislink mode <relay|mediate|game-master>` at any time.

| Mode | What Claude does |
|------|-----------------|
| `relay` | Pass-through. Messages arrive exactly as sent. No LLM call. |
| `mediate` | Rewrites each outbound message to be clearer and more considerate before sending. Uses `loki/qwen-coder-14b` via LiteLLM at `spartacus.local:4000`. |
| `game-master` | Adds a brief narrative flourish or creative prompt after each message. Uses `loki/qwen3-coder-30b`. |

## State files

All runtime state lives under `~/.irislink/`:

```
~/.irislink/
├── config.json              # {"connector_url": "http://localhost:8357"}
└── rooms/
    ├── pending.json         # active room: {"otp": "...", "room_id": "..."}
    ├── <otp>.meta           # {"handle": "...", "mode": "relay", "cursor": 0}
    ├── <otp>.log            # incoming messages (kept after leave)
    └── <otp>.pid            # background poller PID
```

`config.json` is the only file you need to create manually, and only when using a non-default connector port.

## Rendezvous API

The server exposes a simple REST API. All endpoints return `{"room": {...}}` on success or `{"error": "..."}` on failure.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/rooms` | Create room, returns OTP + initial state |
| `POST` | `/rooms/:otp/join` | Join existing room |
| `POST` | `/rooms/:otp/participants` | Update participant status |
| `POST` | `/rooms/:otp/mode` | Switch mediation mode |
| `POST` | `/rooms/:otp/messages` | Append message |
| `POST` | `/rooms/:otp/messages/:id/ack` | Acknowledge message |
| `GET`  | `/rooms/:otp` | Fetch room state |
| `DELETE` | `/rooms/:otp` | Close room immediately |

Room phases: `waiting → joined → active → closed`. Rooms expire after 15 minutes of inactivity. Invalid OTP formats return 400.

## Connector API

The connector proxy runs locally and bridges the skill to the rendezvous server.

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/status` | Version + room_attached state |
| `GET` | `/rooms/pending.json` | Serve `~/.irislink/rooms/pending.json` |
| `POST` | `/message` | Forward `{room_otp, sender, text}` to rendezvous |
| `GET` | `/events?room_otp=X&since=Y` | Fetch messages after cursor `Y` (unix ms) |
| `POST` | `/ack` | Acknowledge `{room_otp, message_id}` |

## OTP alphabet

Codes use Crockford Base32: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`

`0`, `1`, `I`, `O` are excluded to prevent visual confusion. Codes are case-insensitive on input.

## Repo layout

```
IrisLink/
├── cmd/irislink/main.go     # CLI entry point
├── internal/
│   ├── crypto/              # OTP generation, HKDF derivation
│   ├── proxy/               # connector proxy HTTP handler
│   ├── server/              # rendezvous server HTTP handler
│   └── state/               # pending.json + config.json I/O
├── irislink/
│   ├── irislink.md          # Claude Code skill (install to ~/.claude/skills/)
│   └── SKILL.md             # original spec (reference)
└── docs/
    ├── rendezvous.md        # detailed protocol spec
    ├── ui-safety.md         # consent + safety UI guidelines
    └── web-ui.md            # lobby UI design spec
```

## What's next

- End-to-end test harness (issue #3) — scripted two-session handshake
- WebSocket support on the rendezvous server — eliminate polling
- `irislink install` command that handles skill copy + shell profile update
- Signed envelopes (`X-IrisLink-Signature`) — HMAC-SHA256 per message
- Optional E2E encryption via libsodium for peers who don't trust the rendezvous host

See `docs/rendezvous.md` for the full protocol spec including HKDF derivation, envelope format, and planned WebSocket handshake.
