# IrisLink

Claude skill + shared lobby experience for pairing two Claude Code sessions via a six-character one-time pad.

## Concept

IrisLink lets two people point their local Claude Code instances at the same lightweight web lobby and type a matching six-character code ("one-time pad"). The code seeds a shared secret that:

- Establishes a transient room in a rendezvous endpoint (HTTPS + WebSocket fallback).
- Derives symmetric encryption keys for browser ↔ Claude Code relays.
- Gives the IrisLink skill an ID it can watch for inside each Claude session.

Once both sides connect, every message is mediated by Claude: outbound text is optionally reframed/summarized before being relayed, and inbound text can be filtered, translated, or role-played. The mediation style is selectable per room.

## Components

1. **IrisLink skill** (`irislink/irislink.md`) — handles `/irislink` commands, validates codes, joins/leaves rooms, and orchestrates Claude-to-Claude relays.
2. **Connector** (`connectors/claude_proxy.py`) — FastAPI proxy on `localhost:8357` that bridges the skill to the rendezvous API.
3. **Rendezvous server** (`server/main.py`) — FastAPI service that manages rooms, participants, and messages.
4. **Helper utilities** (`connectors/irislink_helpers.py`) — CLI tools for OTP generation, HKDF derivation, file I/O, and LiteLLM-backed mediation.

## Quick Start

### 1. Install dependencies

```bash
cd server && pip install -r requirements.txt
cd ../connectors && pip install -r requirements.txt
```

### 2. Start the rendezvous server

```bash
cd server
uvicorn main:app --port 4173
```

### 3. Start the connector (one per Claude session)

```bash
python connectors/claude_proxy.py
# defaults to localhost:8357; IRISLINK_BASE_URL defaults to http://localhost:4173
```

### 4. Install the skill

Copy or symlink the skill into your Claude Code skills directory:

```bash
cp irislink/irislink.md ~/.claude/skills/irislink.md
```

### 5. Use it

**Person A** (in their Claude session):
```
/irislink create
```
Claude displays a 6-character code. Share it with Person B out-of-band.

**Person B** (in their Claude session):
```
/irislink join ABC123
```

Once both sides are connected, use `/irislink send <text>` to exchange messages. Claude mediates according to the active mode (`relay`, `mediate`, or `game-master`).

```
/irislink leave    # close the room and clean up
```

## Skill Roadmap

- MVP skill spec in `irislink/SKILL.md` — done.
- Define OTP registry shape (`rooms/<otp>.json`) and TTL enforcement.
- Implement `irislink bridge` mode where both Claude instances exchange structured envelopes.
- Add optional `game-master` persona that can insert creative prompts mid-chat.

## Repo Layout

```
IrisLink/
├── README.md            # overview + roadmap (this file)
├── docs/
│   ├── rendezvous.md    # detailed rendezvous + connector protocol
│   └── ui-safety.md     # consent + safety UI guidelines
│   └── web-ui.md        # OpenCode-inspired website + lobby layout
├── server/              # Express rendezvous API (`npm start`)
├── web/                 # Vite/React experience prototype (npm run dev)
└── irislink/
    └── SKILL.md         # Claude skill definition + orchestration spec
```

Future directories:

- `web/` — Vite/Svelte lobby app
- `connectors/` — local proxies for Claude Code, Cursor, etc.
- `docs/` — protocol diagrams, threat models, persona guides

## Next Steps

1. Flesh out the rendezvous protocol (HKDF inputs, room JSON schema, message envelopes, encryption design).
2. Build a CLI/web view that can mint and display OTP codes for easy pairing.
3. Prototype the mediation loop entirely inside the skill using mock transport calls, then backfill real network hooks.
4. Document trust assumptions and telemetry expectations so collaborators know how "one-time pad" is used in practice.

See `docs/rendezvous.md` for the detailed rendezvous API, HKDF inputs, and connector handshake.

See `docs/ui-safety.md` for product/UX guidance around consent, capability toggles, and audit trails that keep kickoff processes under human control.

See `docs/web-ui.md` for the top-down website and lobby layout plan that mirrors OpenCode’s command-forward visual language.

### Local Lobby Preview

```
cd web
npm install
npm run dev
```

The dev server spins up the hero + lobby preview described in `docs/web-ui.md` so you can iterate on the visual system before wiring it to the rendezvous backend.

### Rendezvous API (MVP)

```
cd server
npm install
npm start
```

The Express service listens on `PORT` (default `4173`) and exposes `/rooms`, `/rooms/:otp/join`, `/rooms/:otp/messages`, and related endpoints described in `docs/rendezvous.md`. During development the Vite dev server proxies `/api/*` to `http://localhost:4173`. In production Apache proxies `/api/*` on `irislink.nthmost.net` to the same service.

Ethernet-cable braids and rainbow-coded OTPs await. :)
