# IrisLink Architecture

Current implementation as of April 2026.

## Overview

```
┌─────────────────────────────────────────────────────────────┐
│  Person A's machine                                          │
│                                                             │
│  ┌──────────────┐    hook     ┌───────────────────────────┐ │
│  │ Claude Code  │ ──────────► │  irislink hook            │ │
│  │ (skill)      │             │  (UserPromptSubmit)       │ │
│  └──────┬───────┘             └──────────────┬────────────┘ │
│         │ /irislink cmds                     │ additionalContext
│         ▼                                   ▼              │
│  ┌──────────────┐  REST   ┌───────────────────────────────┐ │
│  │  irislink    │ ──────► │  irislink proxy               │ │
│  │  (CLI)       │         │  localhost:8357               │ │
│  └──────────────┘         └──────────────┬────────────────┘ │
│                                          │ HTTP             │
└──────────────────────────────────────────┼─────────────────┘
                                           │
                              ┌────────────▼────────────┐
                              │  irislink server        │
                              │  localhost:4173         │
                              │  (rendezvous)           │
                              └────────────┬────────────┘
                                           │
┌──────────────────────────────────────────┼─────────────────┐
│  Person B's machine                      │                  │
│                                          │ HTTP             │
│  ┌──────────────┐         ┌─────────────▼─────────────────┐ │
│  │  irislink    │ ──────► │  irislink proxy               │ │
│  │  (CLI)       │  REST   │  localhost:8357               │ │
│  └──────────────┘         └──────────────┬────────────────┘ │
│                                          │ additionalContext │
│  ┌──────────────┐    hook  ┌─────────────▼─────────────────┐ │
│  │ Claude Code  │ ◄─────── │  irislink hook                │ │
│  │ (skill)      │          │  (UserPromptSubmit)           │ │
│  └──────────────┘          └───────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Binary structure

Everything is a single binary: `irislink` (`cmd/irislink/main.go`).

```
cmd/irislink/
└── main.go              CLI dispatch + all subcommand logic

internal/
├── crypto/crypto.go     OTP generation, HKDF-SHA256 room_id derivation
├── proxy/proxy.go       Connector proxy HTTP handler
├── server/server.go     Rendezvous server HTTP handler
└── state/state.go       pending.json + config.json read/write
```

Dependencies: `golang.org/x/crypto` for HKDF. Everything else is stdlib.

## Rendezvous server (`irislink server`)

- In-memory `map[string]*room` protected by `sync.RWMutex`
- Room TTL: 15 minutes, reset on join and on new message
- Background goroutine sweeps expired rooms every 60 seconds
- OTP validated on every endpoint via regex `^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$`
- Room phases derived on read: `waiting → joined → active → closed`

Room state shape:

```go
type room struct {
    OTP          string
    Mode         string        // relay | mediate | game-master
    Participants []Participant // max 2; status: present | joined
    Messages     []Message     // status: pending | acknowledged
    CreatedAt    int64         // unix ms
    ExpiresAt    int64         // unix ms
    ClosedAt     *int64        // nil until closed
}
```

## Connector proxy (`irislink proxy`)

Thin HTTP proxy running on `localhost:8357` (configurable). Stateless — reads `~/.irislink/rooms/pending.json` to know which room is active.

| Endpoint | Action |
|----------|--------|
| `GET /status` | Returns version + `room_attached` bool |
| `GET /rooms/pending.json` | Serves pending.json (for lobby) |
| `POST /message` | Forwards `{room_otp, sender, text}` → `POST /rooms/:otp/messages` |
| `GET /events?room_otp=X&since=Y` | Fetches room state, filters messages where `timestamp > since`, returns with cursor |
| `POST /ack` | Forwards `{room_otp, message_id}` → `POST /rooms/:otp/messages/:id/ack` |

The `since` parameter is a Unix millisecond timestamp. The response includes `next` (highest timestamp seen), `phase`, `ttlSeconds`, and `waitingOn`.

## Skill (`irislink/irislink.md`)

A Claude Code skill — a markdown file with YAML frontmatter that instructs Claude how to behave when `/irislink` is invoked.

Key design decisions:

**Binary calls instead of inline logic.** The skill tells Claude to run `irislink <subcommand>` rather than constructing HTTP calls manually. This avoids path issues, Python/pip fragmentation, and JSON-parsing in bash.

**Background poller.** After `create` or `join`, the skill starts a bash loop that calls `irislink events` every 2 seconds and appends inbound messages to `~/.irislink/rooms/<otp>.log`. Claude tails the log to surface new messages.

**UserPromptSubmit hook.** The `irislink hook` subcommand is registered as a Claude Code hook on `create`/`join` and removed on `leave`. It reads `pending.json` on every user message; if a session is active, it injects `additionalContext` telling Claude to relay the message first. Explicit `/irislink` commands bypass the hook.

## OTP and room_id

OTPs are 6 characters from Crockford Base32 (`ABCDEFGHJKLMNPQRSTUVWXYZ23456789`). Generated with `crypto/rand`.

`room_id` is derived via HKDF-SHA256:

```
IKM  = otp.upper()
salt = "irislink:v0"
info = "irislink-room"
len  = 16 bytes → 32-char lowercase hex
```

The server currently uses the OTP directly as its room key (not the derived `room_id`). The `room_id` is computed client-side and stored in `pending.json` for future use (signed envelopes, E2E encryption).

## Mediation

`irislink mediate <mode> <text>` calls the LiteLLM proxy at `spartacus.local:4000`:

| Mode | Model | System prompt |
|------|-------|--------------|
| `relay` | — | Pass-through, no LLM call |
| `mediate` | `loki/qwen-coder-14b` | Rewrite for clarity and consideration |
| `game-master` | `loki/qwen3-coder-30b` | Add narrative flourish or GM prompt |

## State files

```
~/.irislink/
├── config.json          {"connector_url": "http://localhost:8357"}
└── rooms/
    ├── pending.json     {"otp": "ABC123", "room_id": "1f2e3c4a..."}
    ├── <otp>.meta       {"handle": "north-star", "mode": "relay", "cursor": 1234567890}
    ├── <otp>.log        incoming messages, one per line
    └── <otp>.pid        background poller PID
```

`pending.json` is the single source of truth for whether a session is active. Both the hook and the skill read it. `clear_pending` (`irislink pending clear`) signals session end.

## What's not implemented yet

- **WebSocket relay** — currently the connector polls the rendezvous every 2s. The server has no push mechanism.
- **Signed envelopes** — `X-IrisLink-Signature` header is defined in the spec but not validated by the server.
- **E2E encryption** — room_id derivation is in place; the libsodium layer is not.
- **Persistent storage** — server state is in-memory; a restart loses all rooms.
- **Multi-machine rendezvous** — currently assumes both people can reach the same `localhost:4173`. A hosted instance at `irislink.nthmost.net` is the next step.
