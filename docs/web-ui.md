# IrisLink Web Experience

This document outlines the top-down product view for the IrisLink website/lobby. The direction leans into an OpenCode-inspired interface: deliberate typography, command-line motifs, and a responsive layout that feels like a living terminal rather than a generic marketing page.

## Page Structure

```
┌──────────────────────────────────────────────────────────────┐
│ Sticky Nav: IrisLink glyph · Docs · Safety · Launch Lobby    │
├──────────────────────────────────────────────────────────────┤
│ Hero Split                                                   │
│ ┌───────────────┐ ┌───────────────────────────────────────┐  │
│ │ Command Stack │ │ Story column (headline + CTA)        │  │
│ │ (fake CLI)    │ │ - Pair Claude to Claude              │  │
│ │ - /irislink ? │ │ - Explanation                        │  │
│ │ - create      │ │ - Start session button               │  │
│ └───────────────┘ └───────────────────────────────────────┘  │
├──────────────────────────────────────────────────────────────┤
│ Capability Grid (cards for Lobby, Connector, Skill, Safety)  │
├──────────────────────────────────────────────────────────────┤
│ Live Lobby Preview (actual OTP entry panel + status chips)   │
├──────────────────────────────────────────────────────────────┤
│ Safety Console (ties into docs/ui-safety.md)                 │
├──────────────────────────────────────────────────────────────┤
│ Footer: repo links · status badge · privacy                 │
└──────────────────────────────────────────────────────────────┘
```

## Visual Language

- **Typography** — Use a monospace/tech pairing similar to OpenCode: headlines in `Space Grotesk` (or another geometric sans) paired with `IBM Plex Mono` for command elements.
- **Color** — Deep navy background with saturated accent lines (teal/orange). Terminal panes use translucent layers (+1 elevation) and subtle scan-line textures.
- **Motion** — Staggered slide-in for command stack, typing effect for `/irislink create relay`, and pulse animation on the OTP chip when both parties connect.
- **Grid** — 12-column desktop grid that collapses to stacked sections on mobile. Terminal components keep a max width to avoid overwhelming narrow screens.

## Key Sections

### Hero Command Stack

- Simulated CLI with live-typing slash commands. Users can click a command to copy it or open docs.
- Displays contextual hints (“Need a partner? Share the six-character pad.”).
- On scroll, shrinks into a floating widget anchored bottom-right so visitors can trigger `/irislink status` anywhere on the page.

### Capability Grid

- Four cards with icon badges: `Lobby`, `Connector`, `Skill`, `Safety`. Each card includes two bullet promises and a `Learn more` link to respective docs (e.g., `docs/rendezvous.md`, `docs/ui-safety.md`).
- Cards should look like toggle switches; hovering shows the automation budget phrase (“Subagents: disarmed by default”).

### Live Lobby Preview

- Functional component (wired later) that mirrors the actual lobby layout: OTP display, partner list, mediation mode selector, and the Safety pill.
- Includes a `Demo Mode` button so solo visitors can experience the handshake with scripted responses.
- Provides quick copy of `/irislink join <code>` command.

### Safety Console

- Mirrors `docs/ui-safety.md`. Shows log entries, capability toggles, and consent banner mockups.
- Should look like stacked terminal panes with colored chips (green = armed, amber = pending, red = blocked).
- Include a CTA linking to “Read the full Safety UI guidelines” for transparency.

### Footer & Support

- Include status indicator (whether rendezvous API is online), link to status page, and contact email.
- Provide anchors to GitHub repo and OpenCode instructions for contributors.

## Mobile Layout

- Collapsed nav with hamburger; Launch Lobby button stays sticky at bottom.
- Command stack becomes a swipeable carousel of prompts.
- Safety console condenses into accordions with simple toggles.
- OTP preview uses large touch targets (48px min height) for copy buttons.

## States & Feedback

| State | Visual Treatment |
|-------|------------------|
| Room waiting | OTP chip glows amber, partner slot shows “Waiting for match…”. |
| Joined | Pulse animation switches to teal, hero copy updates to “North-star is ready”. |
| Safety request | Modal slides up with capability diff, two CTA buttons (`Allow`, `Decline`). |
| Error (connector offline) | Inline alert bar at top of lobby preview with actionable text (`Run connectors/claude_proxy.py`). |

## Content Hooks

- Integrate short quotes or tooltips explaining why IrisLink exists (“Claude-to-Claude pairing without sharing your raw prompt”).
- Provide small “How it works” callouts referencing docs: HKDF keys (link to `docs/rendezvous.md`), safety toggles (link to `docs/ui-safety.md`).
- Offer `Open the lobby` CTA that jumps directly into the actual app when ready; until then, route to waitlist form.

## Implementation Notes

- Use a component library that supports CSS variables and theming (e.g., Svelte + UnoCSS). Avoid default Roboto/Inter—as per repo guidelines choose more expressive fonts.
- Keep CLI animations accessible: respect `prefers-reduced-motion` and provide plain text alternatives under each animated pane.
- Compose sections so they can later be embedded inside the actual lobby (shared components between marketing and app minimize drift).

This plan should give design + frontend collaborators a shared north star for building an OpenCode-flavored IrisLink interface that surfaces pairing controls, consent, and delight from the first pixel.
