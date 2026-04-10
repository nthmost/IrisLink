# IrisLink

IrisLink is a standalone terminal UI for two people to communicate over an encrypted MQTT channel, with optional Claude AI context attachment and mediation. No accounts, no hosted server — just an MQTT broker and a six-character code.

## TUI Layout

```
IRISLINK  ABC123  relay  alice
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  bob  02 Apr 15:32                    │ context
  hey, did you push the fix?           │ ────────────────────────
                                       │ ▶ cmd/ (4)
  you  02 Apr 15:33                    │ ▶ internal/ (8)
  just pushed — try pulling            │   go.mod
                                       │   README.md
  ∙ bob joined                         │
                                       │ ────────────────────────
─────────────────────────────────────  │   [ LOGIN ]
  write something...                   │   claude context
  (opt+enter to send, /help)           │

────────────────────────────────────────────────────────────────
  opt+enter to send  •  tab: browse files  •  /help
```

Left pane: scrolling message history + multi-line compose area.
Right sidebar: file tree of the current working directory (files sent as context glow cyan), with the Claude auth panel at the bottom.

## Quick Start

### Install

From source (recommended — module proxy may lag):

```bash
git clone https://github.com/nthmost/IrisLink
cd IrisLink
go build -o ~/go/bin/irislink ./cmd/irislink/
```

Or via `go install` (requires Go 1.21+):

```bash
go install github.com/nthmost/IrisLink/cmd/irislink@latest
```

Make sure `~/go/bin` is on your `PATH`.

### Configure

Create `~/.irislink/config.json` on both machines, pointing at the same broker:

```json
{
  "broker_url": "mqtt://homeassistant.local:1883",
  "broker_user": "irislink",
  "broker_pass": "yourpassword"
}
```

### Start a session

**Person A** — creates the room:

```bash
irislink create alice
```

The TUI shows a 6-character OTP and waits. Share it with Person B out-of-band.

**Person B** — joins with the code:

```bash
irislink join ABC123 bob
```

Both TUIs open. Start typing.

## TUI Controls

| Key | Action |
|-----|--------|
| Alt+Enter (Opt+Enter on macOS) | Send message |
| Tab | Cycle focus: compose → file tree → claude panel |
| Up / Down (sidebar focused) | Navigate file tree |
| Enter / Space (sidebar focused) | Expand or collapse a directory |
| Enter (claude panel focused) | Open browser auth (or logout if already logged in) |
| Ctrl+C | Quit immediately |

## Slash Commands

| Command | Effect |
|---------|--------|
| `/leave` | Disconnect and quit |
| `/mode relay` | Pass-through, no LLM (default) |
| `/mode mediate` | Claude rewrites messages for clarity before sending |
| `/mode game-master` | Claude adds a narrative GM note to each outgoing message |
| `/help` | Show key bindings and available commands |

## Modes

| Mode | What happens |
|------|--------------|
| `relay` | Messages sent as-is. No LLM call. |
| `mediate` | Claude rewrites outgoing messages to be clearer and more considerate (requires API key). |
| `game-master` | Claude appends a narrative GM note to each outgoing message (requires API key). |

Mode is per-session and local — each person sets their own.

## Claude AI Integration

Tab to the `[ LOGIN ]` button and press Enter. IrisLink opens a browser page on a local port with instructions to get an API key from `platform.claude.com/settings/keys`. Paste the key in the browser form; the TUI receives it automatically and stores it in `~/.irislink/config.json`.

Once logged in:

- **Context selection** — on every send, Claude reads files in the current working directory, picks relevant excerpts, and attaches them to the outgoing envelope. Files that were included glow cyan in the sidebar.
- **Context filing** — context blocks received from your partner are written to `irislink-context/<sender>/` in the CWD.
- **Mediation** — active only when mode is `mediate` or `game-master`.

To log out, tab to the claude panel and press Enter again.

## Broker Setup

Both parties need to reach the same MQTT v5 broker. The OTP and encryption key never reach the broker — only opaque ciphertext is published.

### Home Assistant Mosquitto add-on

1. Install the Mosquitto add-on from the add-on store.
2. In HA, go to **Settings → People → Users** (enable Advanced Mode in your profile first).
3. Click **Add User** — name it `irislink`, set a password, role: **User**.
4. Set `broker_url` to `mqtt://homeassistant.local:1883` in `config.json`.

Optional ACL to restrict the user to the `irislink/#` namespace:

```yaml
# Mosquitto add-on configuration
customize:
  active: true
  folder: mosquitto
```

`/config/mosquitto/acl.conf`:
```
user irislink
topic readwrite irislink/#
```

### Standalone Mosquitto

```bash
# macOS
brew install mosquitto && brew services start mosquitto

# Debian/Ubuntu
sudo apt install mosquitto mosquitto-clients
sudo systemctl enable --now mosquitto
```

Default config allows anonymous connections on port 1883, which is fine for local use. For password auth, see the [Mosquitto documentation](https://mosquitto.org/documentation/).

Public brokers (HiveMQ, EMQX Cloud, etc.) also work — use `mqtt://broker.hivemq.com:1883` for quick testing.

## Config Reference

`~/.irislink/config.json`

| Field | Default | Description |
|-------|---------|-------------|
| `broker_url` | `mqtt://localhost:1883` | MQTT broker URL (`mqtt://` or `mqtts://`) |
| `broker_user` | _(empty)_ | Broker username (optional) |
| `broker_pass` | _(empty)_ | Broker password (optional) |
| `claude_api_key` | _(empty)_ | Anthropic API key — set via TUI login or edit manually |

The `claude_api_key` field is also read from the `ANTHROPIC_API_KEY` environment variable on startup if not already set in config.

## Repo Layout

```
IrisLink/
├── cmd/irislink/
│   ├── main.go         CLI dispatch and debug subcommands
│   ├── session.go      create / join / leave, TUI launcher
│   ├── tui.go          Bubbletea TUI (model, view, update)
│   └── auth.go         Local HTTP server for browser-based key capture
├── internal/
│   ├── claude/         Context selection and mediation via Anthropic API
│   ├── crypto/         OTP generation, HKDF derivation, NaCl secretbox
│   ├── transport/      MQTT v5 client and Envelope type
│   └── state/          config.json, pending.json, and meta file R/W
├── irislink/
│   └── SKILL.md        Claude Code skill (install helper only)
└── docs/
    └── architecture.md
```

## Open Issues

- **[#1] goreleaser binaries** — pre-built binaries not yet published; build from source for now.
- **[#3] SSH key identity** — `go install` uses the default git identity; if you need a specific SSH key for private forks, clone and build manually.
