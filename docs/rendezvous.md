# IrisLink Rendezvous Protocol

This document captures the working draft for the IrisLink rendezvous layer that glues two Claude Code sessions together. It expands on the short summary in `irislink/SKILL.md` and acts as the reference for backend implementers, connector authors, and anyone modeling the trust envelope.

## Actors & Terms

- **OTP** — Six-character code typed by both humans inside the lobby UI and `irislink` skill. Alphabet is Crockford Base32 (`A-Z2-7`) with no padding.
- **Pad** — Uppercased OTP string used as HKDF input material.
- **Room** — Short-lived record representing one IrisLink conversation. Identified by a derived `room_id`.
- **Connector** — Local helper listening on `localhost:8357` that proxies Claude traffic to the rendezvous API and serves small helper files to the lobby UI.
- **Rendezvous API** — HTTPS endpoint that persists room metadata, brokers WebSocket URLs, and relays envelopes.

All timestamps are UNIX seconds. Default TTL for a room is fifteen minutes (`900s`), with a stricter five-minute grace period for unpaired rooms.

## Key Derivation

IrisLink relies on HKDF-SHA256, seeded by the OTP-derived pad. The salt and `info` strings are constant so both sides derive identical secrets without extra coordination.

| Purpose | Salt | Info | Output |
|---------|------|------|--------|
| `room_id` | `"irislink:v0"` | `"irislink-room"` | 16 bytes, hex-encoded lower-case |
| `secret`  | `"irislink:v0"` | `"irislink-secret"` | 32 bytes |
| `signature_key` | `"irislink:v0"` | `"irislink-signature"` | 32 bytes |

The `room_id` is safe to share publicly (it merely gates access checks). `secret` stays server-side; the rendezvous service returns an opaque handle rather than the raw bytes to browsers. `signature_key` is held by both the connector and skill to MAC outbound envelopes.

## Room JSON Schema

Room state persists under `rooms/<room_id>.json` on disk (for local mocks) or in a database row (for the hosted rendezvous). A typical record looks like:

```json
{
  "room_id": "1f2e3c4a5b6d",
  "otp": "J9K4Z2",
  "state": "waiting",
  "mode": "relay",
  "created_at": 1722891241,
  "updated_at": 1722891241,
  "expires_at": 1722892141,
  "ws_url": "wss://irislink.naomimost.com/ws/1f2e3c4a5b6d",
  "participants": [
    {
      "connector_id": "c1",
      "handle": "north-star",
      "joined_at": 1722891241,
      "status": "present"
    }
  ],
  "ttl_seconds": 900
}
```

Valid `state` transitions: `waiting → joined → active → closed`. When TTL expires the service marks the room `closed` and rejects new joins with `410 Gone`.

## Rendezvous API Surface

Set `IRISLINK_BASE_URL` (defaults to `https://irislink.naomimost.com/api`). All JSON responses include `"room": {...}` on success or `{ "error": "message", "code": "slug" }` on failure. Requests that mutate room data must include:

```
X-IrisLink-Key: <connector api key or capability token>
X-IrisLink-Signature: base64(HMAC-SHA256(body, signature_key))
```

### POST /rooms

Creates a room and returns lobby metadata.

Request body:

```json
{ "otp": "ABC123", "mode": "relay", "client": "irislink-skill/0.1" }
```

Response body:

```json
{
  "room": {
    "room_id": "1f2e3c4a5b6d",
    "mode": "relay",
    "ttl_seconds": 900,
    "ws_url": "wss://...",
    "rest_url": "https://.../rooms/1f2e3c4a5b6d/events"
  },
  "secret_handle": "srv_9Qp..."
}
```

The `secret_handle` is redeemed later if the connector needs the raw `secret` for WebSocket auth.

### POST /rooms/<room_id>/join

Associates the caller with an existing room, verifies OTP freshness, and returns participant roster.

```json
{
  "room": { "state": "joined", "mode": "relay" },
  "peer": { "handle": "south-light", "status": "present" }
}
```

### POST /rooms/<room_id>/events

Pushes message envelopes. The rendezvous service stamps a `cursor` before fan-out.

```json
{ "envelope": { ... }, "cursor": "evt_2024-08-05T18:14:02Z" }
```

### GET /rooms/<room_id>/events?since=<cursor>

Long-poll or SSE endpoint for connectors that cannot keep a WebSocket alive. Returns `{ "events": [ ... ], "next": "cursor" }`.

### DELETE /rooms/<room_id>

Immediately tear down the room, revoke outstanding OTP, and notify both connectors via a `control` envelope (`payload.type = "status"`, `payload.text = "closed"`).

### MVP REST shim (implemented in `server/`)

For the first deploy the console talks to a lightweight REST version of this API:

- `POST /rooms` → mint OTP + creator handle
- `POST /rooms/:otp/join` → add/join second handle
- `POST /rooms/:otp/mode` → switch mediation mode
- `POST /rooms/:otp/messages` → append `{sender,text}` with `status="pending"`
- `POST /rooms/:otp/messages/:id/ack` → mark message acknowledged
- `GET /rooms/:otp` → fetch derived state (`phase`, `ttlSeconds`, `waitingOn` etc.)
- `DELETE /rooms/:otp` → close room immediately

Once the real rendezvous service lands these routes will either be deprecated or shimmed to call the full envelope-based API described above.

## Sequence Overview

### Creator Flow

1. User issues `/irislink create`.
2. Skill validates that no active room is attached, then mints OTP and derives `room_id`.
3. Skill calls `POST /rooms` and stores `{otp, room_id}` under `~/.irislink/rooms/pending.json` for the lobby.
4. Connector polls `/status` and `/rooms/pending.json`, exposing OTP to the lobby UI.
5. Rendezvous marks room `waiting` until a peer joins.

### Joiner Flow

1. User issues `/irislink join ABC123`.
2. Skill derives `room_id` deterministically, posts to `/rooms/<room_id>/join`.
3. On success, connectors on both sides negotiate WebSocket tokens and subscribe to `/events`.
4. Once both connectors report `present`, service marks room `active` and notifies parties with a `payload.type = "status"` envelope.

### Message Loop

1. Claude emits outbound text through the skill rather than directly replying.
2. Skill wraps text inside an envelope:

```json
{
  "room_id": "1f2e3c4a5b6d",
  "envelope_id": "uuid",
  "mode": "mediate",
  "timestamp": 1722891241,
  "sender": { "role": "user", "handle": "north-star" },
  "payload": { "type": "chat", "text": "..." }
}
```

3. Connector signs payload, posts to `/rooms/<room_id>/events`.
4. Rendezvous records cursor, forwards envelope to peer via WebSocket (or queues for pollers).
5. Peer connector validates signature, hands envelope to its Claude session, which renders user text and mediator annotations.

## Security & Safety Notes

See also `docs/ui-safety.md` for the user-facing consent flows that pair with these technical controls.

- **OTP reuse guard** — When a connector receives `409 Conflict` on `join`, it should warn users that the code is stale and auto-run `/irislink create` to mint a new one.
- **Integrity** — All envelopes must include both browser and Claude MACs when possible. If the connector cannot produce a browser MAC (e.g., lobby only), the rendezvous API adds `"browser": null` but still verifies the Claude MAC against `signature_key`.
- **Rate limits** — Cap room creation to 10/minute per connector key and message sends to 5/sec/room to stop flooding.
- **Transcripts** — Store sanitized markdown files in `~/.irislink/history/<room_id>.md` after each session. Strip OTPs, capability tokens, and personally identifying content.
- **Connector health** — `/status` should include `"uptime"`, current mode, and whether the lobby is attached. The skill surfaces this inside `/irislink status` for quick debugging.

Future updates will cover the optional libsodium layer for peers that want end-to-end secrecy beyond the rendezvous host.
