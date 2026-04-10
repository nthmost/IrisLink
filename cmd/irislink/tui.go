package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nthmost/IrisLink/internal/claude"
	"github.com/nthmost/IrisLink/internal/state"
	"github.com/nthmost/IrisLink/internal/transport"
)

// ─── colour palette ─────────────────────────────────────────────────────────

var (
	colCyan     = lipgloss.Color("#00d4ff")
	colPink     = lipgloss.Color("#ff2d78")
	colViolet   = lipgloss.Color("#c678dd")
	colDimBlue  = lipgloss.Color("#4a7a9b")
	colDarkTeal = lipgloss.Color("#1a3a5c")
	colDimGray  = lipgloss.Color("#555555")

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colCyan)

	styleHeaderDim = lipgloss.NewStyle().
			Foreground(colDimBlue)

	styleDivider = lipgloss.NewStyle().
			Foreground(colDarkTeal)

	styleSenderSelf  = lipgloss.NewStyle().Foreground(colViolet).Bold(true)
	styleSenderOther = lipgloss.NewStyle().Foreground(colPink).Bold(true)
	styleTimestamp   = lipgloss.NewStyle().Foreground(colDimBlue)

	styleTextSelf  = lipgloss.NewStyle().Foreground(colCyan)
	styleTextOther = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0e0e0"))

	styleSystem = lipgloss.NewStyle().Foreground(colDimGray).Italic(true)
	stylePrompt = lipgloss.NewStyle().Foreground(colDimBlue)
)

// ─── message types ───────────────────────────────────────────────────────────

type chatMsg struct {
	ts       time.Time
	sender   string
	text     string
	isSelf   bool
	isSystem bool
}

type incomingEnvMsg struct{ env transport.Envelope }
type selfSentMsg struct{ text string }
type sendErrMsg struct{ err error }

// ─── model ───────────────────────────────────────────────────────────────────

const composeRows = 5

type tuiModel struct {
	messages []chatMsg
	compose  textarea.Model
	otp      string
	handle   string
	mode     string
	client   *transport.Client
	incoming chan transport.Envelope
	cfg      state.Config
	cwd      string
	width    int
	height   int
}

func initialModel(otp, handle, mode string, client *transport.Client, incoming chan transport.Envelope, cfg state.Config, cwd string) tuiModel {
	ta := textarea.New()
	ta.Placeholder = "write something... (ctrl+d to send, /help for commands)"
	ta.Focus()
	ta.SetHeight(composeRows)
	ta.CharLimit = 0 // no limit
	ta.ShowLineNumbers = false

	// Remove the default lipgloss border — we draw our own dividers
	blurred, focused := textarea.DefaultStyles()
	noBorder := lipgloss.NewStyle()
	blurred.Base = noBorder
	focused.Base = noBorder
	blurred.CursorLine = lipgloss.NewStyle().Foreground(colCyan)
	focused.CursorLine = lipgloss.NewStyle().Foreground(colCyan)
	ta.BlurredStyle = blurred
	ta.FocusedStyle = focused

	ta.Prompt = ""

	return tuiModel{
		compose:  ta,
		otp:      otp,
		handle:   handle,
		mode:     mode,
		client:   client,
		incoming: incoming,
		cfg:      cfg,
		cwd:      cwd,
		width:    80,
		height:   24,
	}
}

func (m *tuiModel) addSystem(msg string) {
	m.messages = append(m.messages, chatMsg{
		ts:       time.Now(),
		text:     msg,
		isSystem: true,
	})
}

// ─── commands ────────────────────────────────────────────────────────────────

func waitForMsg(ch <-chan transport.Envelope) tea.Cmd {
	return func() tea.Msg {
		return incomingEnvMsg{env: <-ch}
	}
}

// ─── init ────────────────────────────────────────────────────────────────────

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		waitForMsg(m.incoming),
	)
}

// ─── update ──────────────────────────────────────────────────────────────────

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.compose.SetWidth(m.width - 2)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyCtrlD:
			raw := strings.TrimSpace(m.compose.Value())
			m.compose.Reset()
			if raw == "" {
				break
			}
			if strings.HasPrefix(raw, "/") {
				return m.handleSlash(raw)
			}
			cmds = append(cmds, m.sendMsg(raw))
		}

	case incomingEnvMsg:
		env := msg.env
		switch env.Type {
		case "presence":
			m.addSystem(fmt.Sprintf("%s %s", env.Sender, env.Text))
		default:
			m.messages = append(m.messages, chatMsg{
				ts:     time.UnixMilli(env.Timestamp),
				sender: env.Sender,
				text:   env.Text,
			})
			fileContext(m.cwd, env.Sender, env.Context)
		}
		cmds = append(cmds, waitForMsg(m.incoming))

	case selfSentMsg:
		m.messages = append(m.messages, chatMsg{
			ts:     time.Now(),
			sender: m.handle,
			text:   msg.text,
			isSelf: true,
		})

	case sendErrMsg:
		m.addSystem("send error: " + msg.err.Error())
	}

	var composeCmd tea.Cmd
	m.compose, composeCmd = m.compose.Update(msg)
	cmds = append(cmds, composeCmd)

	return m, tea.Batch(cmds...)
}

// sendMsg builds and publishes an envelope.
func (m *tuiModel) sendMsg(text string) tea.Cmd {
	return func() tea.Msg {
		finalText := text
		var blocks []transport.ContextBlock

		if m.cfg.ClaudeAPIKey != "" {
			if m.mode != "relay" {
				if rewritten, err := claude.Mediate(m.cfg.ClaudeAPIKey, m.mode, text); err == nil && rewritten != "" {
					finalText = rewritten
				}
			}
			if ctx, err := claude.SelectContext(m.cfg.ClaudeAPIKey, text, m.cwd); err == nil {
				blocks = ctx
			}
		}

		env := transport.Envelope{
			Type:    "message",
			Text:    finalText,
			Context: blocks,
		}
		if err := m.client.Publish(context.Background(), env); err != nil {
			return sendErrMsg{err: err}
		}
		return selfSentMsg{text: finalText}
	}
}

// ─── view ────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	divider := styleDivider.Render(strings.Repeat("─", w))
	heavyDiv := styleDivider.Render(strings.Repeat("━", w))

	// Header: IRISLINK [OTP] mode handle
	header := styleHeader.Render("IRISLINK") +
		"  " + styleHeaderDim.Render("["+m.otp+"]") +
		"  " + styleHeaderDim.Render(m.mode) +
		"  " + styleHeaderDim.Render(m.handle)

	// Available height for message scroll area.
	// Layout: header(1) + heavyDiv(1) + msgs + heavyDiv(1) + compose(composeRows) + hint(1) = height
	msgRows := m.height - composeRows - 4
	if msgRows < 1 {
		msgRows = 1
	}

	// Render messages into lines (each message may be multiple lines).
	var allLines []string
	for i, cm := range m.messages {
		if i > 0 {
			allLines = append(allLines, "") // blank line between messages
		}
		allLines = append(allLines, renderMsg(cm, w)...)
	}

	// Take the last msgRows lines.
	if len(allLines) > msgRows {
		allLines = allLines[len(allLines)-msgRows:]
	}
	// Pad top with empty lines.
	for len(allLines) < msgRows {
		allLines = append([]string{""}, allLines...)
	}
	msgBlock := strings.Join(allLines, "\n")

	hint := stylePrompt.Render("  ctrl+d to send  •  /help for commands")

	return strings.Join([]string{
		header,
		heavyDiv,
		msgBlock,
		heavyDiv,
		m.compose.View(),
		divider,
		hint,
	}, "\n")
}

// renderMsg returns the lines for one chat message.
// Long text is word-wrapped to fit within w.
func renderMsg(cm chatMsg, w int) []string {
	if cm.isSystem {
		return []string{styleSystem.Render("  ∙ " + cm.text)}
	}

	// Header line: sender  timestamp
	var senderStr string
	if cm.isSelf {
		senderStr = styleSenderSelf.Render("you")
	} else {
		senderStr = styleSenderOther.Render(cm.sender)
	}
	tsStr := styleTimestamp.Render(cm.ts.Format("02 Jan 15:04"))
	headerLine := "  " + senderStr + "  " + tsStr

	// Body: wrap text, indent each line.
	textW := w - 4
	if textW < 20 {
		textW = 20
	}
	var textStyle lipgloss.Style
	if cm.isSelf {
		textStyle = styleTextSelf.Width(textW)
	} else {
		textStyle = styleTextOther.Width(textW)
	}

	// Split on existing newlines first, then wrap each paragraph.
	var bodyLines []string
	for _, para := range strings.Split(cm.text, "\n") {
		wrapped := textStyle.Render(para)
		for _, line := range strings.Split(wrapped, "\n") {
			bodyLines = append(bodyLines, "  "+line)
		}
	}

	return append([]string{headerLine}, bodyLines...)
}

// ─── slash commands ──────────────────────────────────────────────────────────

func (m tuiModel) handleSlash(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return m, nil
	}
	switch parts[0] {
	case "/leave":
		return m, tea.Quit

	case "/login":
		if len(parts) > 1 {
			m.cfg.ClaudeAPIKey = parts[1]
			if err := state.WriteConfig(m.cfg); err != nil {
				m.addSystem("warning: could not save config: " + err.Error())
			} else {
				m.addSystem("logged in — Claude context enabled")
			}
		} else {
			m.addSystem("usage: /login <api-key>")
		}

	case "/mode":
		if len(parts) > 1 {
			m.mode = parts[1]
			m.addSystem("mode: " + m.mode)
		} else {
			m.addSystem("usage: /mode relay|mediate|game-master")
		}

	case "/help":
		m.addSystem("ctrl+d: send  •  /login <key>  •  /leave  •  /mode relay|mediate|game-master")

	default:
		m.addSystem("unknown command: " + parts[0])
	}
	return m, nil
}

// ─── context filing ──────────────────────────────────────────────────────────

func fileContext(cwd, sender string, blocks []transport.ContextBlock) {
	if len(blocks) == 0 {
		return
	}
	dir := filepath.Join(cwd, "irislink-context", sender)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	ts := time.Now().Format("2006-01-02T150405")
	for _, b := range blocks {
		name := filepath.Base(b.Source)
		path := filepath.Join(dir, ts+"-"+name)
		os.WriteFile(path, []byte(b.Content), 0o644) //nolint:errcheck
	}
}
