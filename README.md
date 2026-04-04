# IrisLink

Claude skill + shared lobby experience for pairing two Claude Code sessions via a six-character one-time pad.

## Concept

IrisLink lets two people point their local Claude Code instances at the same lightweight web lobby and type a matching six-character code ("one-time pad"). The code seeds a shared secret that:

- Establishes a transient room in a rendezvous endpoint (HTTPS + WebSocket fallback).
- Derives symmetric encryption keys for browser ↔ Claude Code relays.
- Gives the IrisLink skill an ID it can watch for inside each Claude session.

Once both sides connect, every message is mediated by Claude: outbound text is optionally reframed/summarized before being relayed, and inbound text can be filtered, translated, or role-played. The mediation style is selectable per room.

## Components

1. **IrisLink skill** (`irislink/SKILL.md`) — handles `/irislink` commands, validates codes, joins/leaves rooms, and orchestrates Claude-to-Claude relays.
2. **Lobby page** (future `/web/` directory) — minimal UI for copying/pasting the 6-character code, toggling mediation modes, and showing connection state.
3. **Connector script** (future `/connectors/`) — local helper that exposes Claude Code's API tunnel to the lobby page via WebSocket/SSE so Claude can intercept and transform every message.

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
