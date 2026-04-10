# IrisLink Architecture

Current implementation as of April 2026.

## Overview

```
  Person A terminal                        Person B terminal
  ─────────────────                        ─────────────────
  irislink create alice                    irislink join ABC123 bob
        │                                        │
        ▼                                        ▼
  ┌─────────────┐    encrypted MQTT    ┌─────────────────┐
  │ irislink    │ ──────────────────► │  irislink TUI   │
  │ TUI (alice) │ ◄────────────────── │  (bob)          │
  └──────┬──────┘                     └────────┬────────┘
         │                                     │
         │ optional                            │ optional
         ▼                                     ▼
  ┌──────────────┐                    ┌──────────────────┐
  │ Anthropic    │                    │  Anthropic       │
  │ Claude API   │                    │  Claude API      │
  │ (context +   │                    │  (context +      │
  │  mediation)  │                    │   mediation)     │
  └──────────────┘                    └──────────────────┘
                         │
              ┌──────────▼──────────┐
              │   MQTT broker       │
              │   (Mosquitto, HA,   │
              │    HiveMQ, etc.)    │
              └─────────────────────┘
```

The binary is fully self-contained. There is no IrisLink server. The MQTT broker is commodity infrastructure that sees only ciphertext.

## Key Derivation

The OTP is the only shared secret. Everything else is derived from it before any network connection is made.

| Output | HKDF inputs | Length | Use |
|--------|-------------|--------|-----|
| `room_id` | IKM=OTP, salt=`irislink:v0`, info=`irislink-room` | 16 bytes → 32-char hex | MQTT topic namespace |
| `enc_key` | IKM=OTP, salt=`irislink:v0`, info=`irislink-e2e-key` | 32 bytes | NaCl secretbox key |

Hash function: HKDF-SHA256 (`golang.org/x/crypto/hkdf`).

The OTP itself never reaches the broker. Topic names use `room_id`. MQTT client IDs are random UUIDs.

## Envelope Structure

The cleartext payload that gets encrypted on the wire:

```go
type ContextBlock struct {
    Source  string `json:"source"`   // relative file path
    Content string `json:"content"`  // excerpt (max 500 chars)
}

type Envelope struct {
    Sender    string         `json:"sender"`
    Text      string         `json:"text"`
    Timestamp int64          `json:"timestamp"`  // Unix milliseconds
    Type      string         `json:"type"`       // "message" | "presence" | "control"
    Context   []ContextBlock `json:"context,omitempty"`
}
```

On publish: `json.Marshal(Envelope)` → `NaCl secretbox.Seal` → random 24-byte nonce prepended → MQTT payload.
On receive: strip nonce → `secretbox.Open` → `json.Unmarshal`. Messages that fail decryption are silently dropped.

## MQTT Topics

All topics are namespaced by `room_id` (derived from OTP — never the OTP itself):

| Topic | Purpose |
|-------|---------|
| `irislink/<room_id>/messages` | Chat messages and context blocks |
| `irislink/<room_id>/presence` | Join / leave announcements |
| `irislink/<room_id>/control` | Reserved for future control messages |

All subscriptions use QoS 1. The broker retains no message history.

## Context Flow

**Sending (when Claude API key is set):**

1. User presses Alt+Enter to send.
2. `claude.SelectContext(apiKey, text, cwd)` is called in a goroutine.
3. Claude reads files in the CWD (up to 50 KB total, skipping binaries, `.git`, `node_modules`, `vendor`, `dist`, `build`, `irislink-context`).
4. A prompt asks Claude which files or excerpts are most relevant to the message.
5. Claude returns a JSON array of `{"source": "<path>", "content": "<excerpt>"}`.
6. If mode is `mediate` or `game-master`, `claude.Mediate` rewrites or annotates the message text first.
7. The final `Envelope` (with `Context []ContextBlock`) is encrypted and published.
8. Files included in the context glow cyan in the sidebar (`sentFiles` map).

**Receiving:**

1. Incoming envelope is decrypted and unmarshalled.
2. If `env.Context` is non-empty, `fileContext(cwd, sender, blocks)` fires.
3. Each block is written to `<cwd>/irislink-context/<sender>/<timestamp>-<filename>`.

## Auth Flow (Claude API Key)

IrisLink avoids storing API keys in the skill or sending them over the network. Instead:

1. User tabs to the `[ LOGIN ]` panel in the TUI and presses Enter.
2. `startAuthReceiver()` binds a local HTTP server on a random port (`127.0.0.1:<port>`).
3. The binary opens a browser to `http://localhost:<port>`.
4. The browser page instructs the user to get an API key from `platform.claude.com/settings/keys` and paste it into a form.
5. On POST, the server sends the key over a Go channel (`keyCh`) and shuts itself down.
6. The TUI receives `apiKeyReceivedMsg`, stores the key in `cfg.ClaudeAPIKey`, and persists it to `~/.irislink/config.json`.

The key is only ever transmitted between `localhost` and the user's own browser. It is never sent to the MQTT broker or any IrisLink endpoint.

## OTP Alphabet

OTPs are 6 characters from Crockford Base32: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`. Characters `0`, `1`, `I`, and `O` are excluded to avoid visual confusion. Generated with `crypto/rand`.

## State Files

```
~/.irislink/
├── config.json         broker URL/credentials + claude_api_key
└── rooms/
    ├── pending.json    active room: {"otp": "ABC123", "room_id": "1f2e3c..."}
    └── <otp>.meta      {"handle": "alice", "mode": "relay", "cursor": 0}
```

`pending.json` is the single source of truth for whether a session is active. It is written on `create` / `join` and removed on `leave`. The `.meta` file persists handle and mode.

The `irislink leave` command (non-TUI) publishes a `presence/left` envelope, then clears `pending.json` and removes the `.meta` file. Inside the TUI, `/leave` quits bubbletea and the same cleanup runs in `session.go`.

## Source Layout

```
IrisLink/
├── cmd/irislink/
│   ├── main.go         CLI dispatch; low-level debug subcommands (otp, room-id,
│   │                   pending, send, mediate, version)
│   ├── session.go      runCreate / runJoin / runLeave; wraps TUI launch
│   ├── tui.go          Bubbletea model, view, update; sidebar file tree;
│   │                   slash command handler; context filing
│   └── auth.go         Local HTTP server for browser-based API key capture
├── internal/
│   ├── claude/
│   │   └── claude.go   SelectContext (file walker + Claude prompt) and
│   │                   Mediate (rewrite / GM annotation)
│   ├── crypto/
│   │   └── crypto.go   GenerateOTP, DeriveRoomID, DeriveEncKey, Seal, Open
│   ├── transport/
│   │   └── mqtt.go     MQTT v5 client (paho.golang); Envelope and ContextBlock
│   │                   types; encrypted Publish / handleMessage
│   └── state/
│       └── state.go    Config, Pending, Meta structs; Read/Write helpers for
│                       config.json, pending.json, <otp>.meta
├── irislink/
│   └── SKILL.md        Claude Code skill — install helper only
└── docs/
    └── architecture.md (this file)
```

## Dependencies

| Package | Role |
|---------|------|
| `github.com/charmbracelet/bubbletea` | TUI framework |
| `github.com/charmbracelet/bubbles` | Textarea and textinput widgets |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/eclipse/paho.golang` | MQTT v5 client |
| `github.com/google/uuid` | Random client IDs |
| `golang.org/x/crypto` | HKDF-SHA256 and NaCl secretbox |
