# IrisLink Safety UI Guidelines

This document describes how the IrisLink lobby and skill surfaces should expose kickoff safeguards, subagent controls, and audit affordances so humans stay in charge of automation. For the overall marketing + lobby layout, see `docs/web-ui.md`.

## Product Goals

1. **Informed consent** — Every automation (connector launch, game-master persona, subagent hook) requires an explicit, legible opt-in.
2. **Symmetric visibility** — Both interlocutors can see the current automation budget and any pending escalation requests.
3. **Fast reversibility** — It must be easy to revoke capabilities mid-session and broadcast that revocation to the partner.
4. **Evidence trail** — Each kickoff attempt is logged in a per-room history so participants can audit what happened.

## Lobby Layout

- **Room card** — Shows OTP, partner status, and current mediation mode. Include a `Safety` pill summarizing armed capabilities (e.g., `relay · no subagents`). Clicking opens the detailed panel below.
- **Safety panel** — Accordion with three tabs:
  - `Capabilities` — Toggle list for `Relay`, `Mediation`, `Game-master`, and any connector-defined subagents. Each toggle displays its scope, required resources, and whether the partner has also enabled it. Disabled toggles show the reason (e.g., partner declined, TTL expired).
  - `Kickoff Log` — Reverse-chronological log of automation attempts including who initiated, the capability requested, and outcome (`allowed`, `blocked`, `awaiting partner`). Provide filters and export-to-markdown button.
  - `Policies` — Persistent preferences pulled from `~/.irislink/prefs.json` (e.g., “always confirm connector launches”). Users can edit these defaults outside of a session.
- **Consent banner** — When a capability is requested, show a modal banner with three actions: `Allow once`, `Allow for session`, `Decline`. Include a short rationale supplied by the requester and highlight the resulting automation (e.g., “South-light wants to enable Web search subagent to answer API questions.”).

## Skill UX (`/irislink`)

- `status` output lists `Capabilities:` with colored chips: `relay (armed)`, `subagents (disarmed)`, `game-master (pending partner)`. Each chip links to `/irislink safety <capability>` for details.
- `/irislink allow <capability>` and `/irislink decline <capability>` mirror the lobby UI actions when the user is inside the CLI instead of the browser.
- `/irislink disarm <capability>` immediately revokes a capability, writes the event to history, and sends a `control` envelope so the partner knows automation changed.
- When a kickoff is blocked by policy, the skill surfaces the rule (“Auto-blocked: connectors may not start subagents without `--allow-subagents` flag”). Provide `/irislink prefs` to edit or temporarily override.

## Notification Patterns

- Use toast notifications inside the lobby for low-risk events (e.g., partner toggled `relay`). For higher-risk escalations (launching subagents, switching to `game-master`), require modal confirmation with inline diff of capability changes.
- Send an inbox-style banner summarizing “Safety changes since you left” when a user reconnects to an ongoing room.
- Mirror important alerts to the Claude transcript as structured narrator messages so both parties have a textual record (e.g., “System: north-star declined ‘Filesystem Agent’. No kickoff occurred.”).

## History & Auditing

- Append entries to `~/.irislink/history/<room_id>.md` using a dedicated `## Safety Events` section with timestamped bullet points, actor handles, capability names, and outcomes.
- Provide an export button in the lobby that bundles the transcript plus safety log and strips OTPs or tokens automatically.
- Offer an optional “live monitor” view showing recent safety events so observers (e.g., pair-programming mentors) can monitor escalations without joining the chat.

## Edge Cases

- **Simultaneous requests** — If both parties request a capability at the same time, merge into a single confirmation dialog listing each requester’s note.
- **Offline partner** — When one side is offline, block new capability grants unless the partner previously set `allow_offline_escalation=true`. Present a clear warning that consent is unilateral.
- **Policy drift** — If the local prefs disallow a capability that the partner is already using, surface a conflict badge and offer a “sync policies” dialog to reconcile differences.

These guidelines should keep IrisLink’s automation powerful but transparent. As new connectors and subagents arrive, extend the `Capabilities` catalog with human-readable descriptions and link to any required OAuth scopes or filesystem permissions.
