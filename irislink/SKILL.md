---
name: irislink
description: Use /irislink to pair two Claude Code sessions with a six-character OTP and mediate their chat through IrisLink
---

# IrisLink Skill Specification

IrisLink links two Claude Code instances through a short-lived room keyed by a six-character one-time pad. Each participant runs `/irislink` inside their Claude session and loads the IrisLink lobby web app. The skill coordinates OTP validation, room state, and the Claude-to-Claude mediation loop.

## Invocation

- `/irislink help` — show summary + available subcommands
- `/irislink create [mode]` — mint a new OTP code and host a room until a partner joins (default mode `relay`)
- `/irislink join <OTP> [mode]` — join an existing room created by another participant
- `/irislink mode <relay|mediate|game-master>` — switch mediation strategy for the active room
- `/irislink leave` — disengage, revoke tokens, and delete the local connector

Arguments are case-insensitive. OTPs must be six characters drawn from `A-Z2-7`.

## Room Lifecycle

1. **Code minting** — `/irislink create` generates a random OTP, shows it to the user, and copies it to the clipboard. The skill derives a room ID using HKDF:

   ```text
   pad = OTP uppercased (e.g., J9K4Z2)
   salt = "irislink:v0"
   info = "irislink-room"
   room_id = hex(HKDF-SHA256(pad, salt, info, 16 bytes))
   ```

   The skill writes `{ "otp": "J9K4Z2", "room_id": "1f2e..." }` to `~/.irislink/rooms/pending.json` so the lobby app can poll it.

2. **Lobby coordination** — The IrisLink lobby page (future `web/` app) polls `http://localhost:8357/rooms/pending.json` (exposed by the connector) to display the OTP. Once a partner enters the same code, both sides become `joined` inside the rendezvous API.

3. **State transitions** — Rooms move through `waiting → joined → active → closed`. TTL defaults to fifteen minutes; unused codes expire after five minutes and cannot rejoin after being marked `closed`.

4. **Cleanup** — `/irislink leave` deletes any cached room files (`~/.irislink/rooms/<room_id>.json`) and sends `DELETE /rooms/<room_id>` to the rendezvous API.

## Rendezvous API (draft)

Set `IRISLINK_BASE_URL` (default `https://irislink.naomimost.com/api`). The skill uses the following endpoints:

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/rooms` | Create room, returns `room_id`, `ws_url`, `secret` |
| `POST` | `/rooms/<room_id>/join` | Join existing room, returns peer metadata |
| `POST` | `/rooms/<room_id>/events` | Push message envelopes |
| `GET`  | `/rooms/<room_id>/events?since=<cursor>` | Poll for new envelopes if WS unavailable |
| `DELETE` | `/rooms/<room_id>` | Close room immediately |

All requests include `X-IrisLink-Signature: base64(HMAC-SHA256(body, secret))` where `secret` is derived from HKDF using `info = "irislink-signature"`.

## Message Envelopes

Each envelope is structured JSON so Claude can reason about origin, mode, and authenticity:

```json
{
  "room_id": "1f2e3c4a5b6d",
  "envelope_id": "uuid",
  "timestamp": 1722891241,
  "sender": {
    "role": "user" | "assistant" | "system",
    "handle": "call-sign"
  },
  "mode": "relay" | "mediate" | "game-master",
  "payload": {
    "type": "chat" | "status" | "control",
    "text": "original message text",
    "metadata": {
      "language": "en",
      "annotations": []
    }
  },
  "signatures": {
    "browser": "base64mac",
    "claude": "base64mac"
  }
}
```

## Mediation Modes

- **relay** — minimal pass-through. Claude simply ensures formatting is consistent (`markdown`, trimmed context) and prevents obvious prompt injection.
- **mediate** — Claude rewrites each outbound message per the partner's chosen persona (translator, tone shifter, summarizer). Claude emits both the rewritten message (for the peer) and a short narrator note (for transparency).
- **game-master** — Claude inserts system prompts that direct collaborative play (e.g., puzzle hints, improvisational prompts). Additional `control` envelopes allow Claude to pause/resume human input or inject timed events.

Users can switch modes mid-session via `/irislink mode X`. The skill broadcasts a `control` envelope to notify the peer.

## Local Connector Expectations

The skill assumes a lightweight connector listening on `localhost:8357` with:

- `GET /status` → returns current connector version and whether Claude is attached.
- `POST /message` → body `{"room_id":"...","payload":{...}}` pushes outbound envelopes.
- `GET /events?since=<cursor>` → streams inbound envelopes for the Claude session.
- `GET /rooms/pending.json` → reveals OTP + room metadata for the lobby page (read-only).

If the connector is missing, the skill prompts the user to run `python connectors/claude_proxy.py --listen 8357` (future script) and retry.

## Conversation Loop

1. User runs `/irislink join ABC123`.
2. Skill validates OTP format and tries to `POST /rooms/<room_id>/join`.
3. On success, skill subscribes to connector events and starts a watch loop:
   - Poll connector every 2s (or open SSE) for new envelopes.
   - For each inbound envelope, display to user (with mediator's note) and append to local transcript.
4. When user sends a message in Claude, intercept via the skill (structured mode) instead of default chat completion:
   - Ask user for message text if not already provided.
   - Build envelope, include `payload.text`, run chosen mediation template, send via connector `POST /message`.
   - Echo sanitized text locally.
5. Continue until `/irislink leave`, TTL expiry, or partner disconnect.

## Safety Rails

- OTPs expire after five minutes if no partner joins; warn user and auto-mint a new code.
- Close the room if either client detects conflicting OTP reuse (prevents hijacking).
- Strip personally identifiable info before writing transcripts to disk; transcripts live in `~/.irislink/history/<room_id>.md`.
- Provide `status` summary when `/irislink` is invoked without subcommand: show active room, code, partner handle, current mode, TTL remaining.

## TODO

- Implement the connector stub plus a mock rendezvous service for local testing.
- Add a test harness that simulates two Claude sessions exchanging envelopes.
- Document WebSocket handshake and optional libsodium E2E encryption for peers who do not trust the rendezvous service.
