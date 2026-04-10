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

	styleSidebarHeader = lipgloss.NewStyle().Foreground(colDimBlue).Bold(true)
	styleSidebarFile   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	styleSidebarSent   = lipgloss.NewStyle().Foreground(colCyan)
	styleSidebarHint   = lipgloss.NewStyle().Foreground(colDimGray).Italic(true)
)

const (
	composeRows = 5
	sidebarW    = 26
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

type tuiModel struct {
	messages    []chatMsg
	compose     textarea.Model
	otp         string
	handle      string
	mode        string
	client      *transport.Client
	incoming    chan transport.Envelope
	cfg         state.Config
	cwd         string
	width       int
	height      int
	showWaiting bool
	sentFiles   map[string]bool // files sent as context this session
}

func initialModel(otp, handle, mode string, client *transport.Client, incoming chan transport.Envelope, cfg state.Config, cwd string, showWaiting bool) tuiModel {
	ta := textarea.New()
	ta.Placeholder = "write something... (alt+enter to send, /help for commands)"
	ta.Focus()
	ta.SetHeight(composeRows)
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

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
		compose:     ta,
		showWaiting: showWaiting,
		otp:         otp,
		handle:      handle,
		mode:        mode,
		client:      client,
		incoming:    incoming,
		cfg:         cfg,
		cwd:         cwd,
		width:       80,
		height:      24,
		sentFiles:   make(map[string]bool),
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
	skipCompose := false

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		msgW := m.width - sidebarW - 2
		if msgW < 20 {
			msgW = 20
		}
		m.compose.SetWidth(msgW)

	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit

		case msg.Alt && msg.Type == tea.KeyEnter:
			// Alt+Enter sends — don't forward to textarea
			skipCompose = true
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
			if env.Text == "joined" {
				m.showWaiting = false
			}
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
	if !skipCompose {
		m.compose, composeCmd = m.compose.Update(msg)
		cmds = append(cmds, composeCmd)
	}

	return m, tea.Batch(cmds...)
}

// sendMsg builds, mediates if needed, selects context, and publishes.
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
				for _, b := range blocks {
					m.sentFiles[b.Source] = true
				}
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

	otpStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff"))
	header := styleHeader.Render("IRISLINK") +
		"  " + otpStyle.Render(m.otp) +
		"  " + styleHeaderDim.Render(m.mode) +
		"  " + styleHeaderDim.Render(m.handle)

	heavyDiv := styleDivider.Render(strings.Repeat("━", w))

	if m.showWaiting {
		return m.renderWaitingPopover(header, heavyDiv, w)
	}

	// Layout: header(1) + heavyDiv(1) + body(msgRows+composeRows+1) + divider(1) + hint(1)
	bodyRows := m.height - 4
	if bodyRows < composeRows+2 {
		bodyRows = composeRows + 2
	}
	msgRows := bodyRows - composeRows - 2 // -2 for inner divider + blank
	if msgRows < 1 {
		msgRows = 1
	}

	msgW := w - sidebarW - 1 // 1 for the │ separator
	if msgW < 20 {
		msgW = 20
	}

	// ── message pane ──────────────────────────────────────────────────────────
	var allLines []string
	for i, cm := range m.messages {
		if i > 0 {
			allLines = append(allLines, "")
		}
		allLines = append(allLines, renderMsg(cm, msgW)...)
	}
	if len(allLines) > msgRows {
		allLines = allLines[len(allLines)-msgRows:]
	}
	for len(allLines) < msgRows {
		allLines = append([]string{""}, allLines...)
	}

	innerDiv := styleDivider.Render(strings.Repeat("─", msgW))
	composePart := m.compose.View()

	msgPaneLines := append(allLines, innerDiv, composePart)

	// ── sidebar ───────────────────────────────────────────────────────────────
	sidebarLines := m.renderSidebar(bodyRows)

	// ── zip panes side by side ────────────────────────────────────────────────
	sep := styleDivider.Render("│")
	maxLines := len(msgPaneLines)
	if len(sidebarLines) > maxLines {
		maxLines = len(sidebarLines)
	}
	var bodyParts []string
	for i := 0; i < maxLines; i++ {
		left := ""
		if i < len(msgPaneLines) {
			left = msgPaneLines[i]
		}
		// Pad left to msgW so the separator stays aligned.
		left = padToVisible(left, msgW)

		right := ""
		if i < len(sidebarLines) {
			right = sidebarLines[i]
		}
		bodyParts = append(bodyParts, left+sep+right)
	}
	body := strings.Join(bodyParts, "\n")

	hint := stylePrompt.Render("  alt+enter to send  •  /help for commands")
	divider := styleDivider.Render(strings.Repeat("─", w))

	return strings.Join([]string{header, heavyDiv, body, divider, hint}, "\n")
}

// renderSidebar returns lines for the context sidebar (width = sidebarW).
func (m tuiModel) renderSidebar(totalRows int) []string {
	inner := sidebarW - 1 // leave 1 char padding after │

	title := styleSidebarHeader.Render(" context")
	sep := styleDivider.Render(" " + strings.Repeat("─", inner-1))

	lines := []string{title, sep}

	files := cwdFiles(m.cwd)
	if len(files) == 0 {
		lines = append(lines, styleSidebarHint.Render(" (empty dir)"))
	}
	for _, f := range files {
		label := " " + f
		if len(label) > inner {
			label = " …" + label[len(label)-(inner-2):]
		}
		if m.sentFiles[f] {
			lines = append(lines, styleSidebarSent.Render(label))
		} else {
			lines = append(lines, styleSidebarFile.Render(label))
		}
	}

	if m.cfg.ClaudeAPIKey == "" {
		lines = append(lines, "")
		lines = append(lines, styleSidebarHint.Render(" /login to enable"))
		lines = append(lines, styleSidebarHint.Render(" auto-context"))
	}

	// Pad to totalRows.
	for len(lines) < totalRows {
		lines = append(lines, "")
	}
	return lines
}

// padToVisible pads s to width w ignoring ANSI escape sequences.
func padToVisible(s string, w int) string {
	vis := lipgloss.Width(s)
	if vis >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vis)
}

// ─── waiting popover ─────────────────────────────────────────────────────────

func (m tuiModel) renderWaitingPopover(header, heavyDiv string, w int) string {
	boxW := 36
	if boxW > w-4 {
		boxW = w - 4
	}

	// Space out the characters and use bright white for legibility.
	spaced := strings.Join(strings.Split(m.otp, ""), "  ")
	otpBig := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).
		Width(boxW).Align(lipgloss.Center).Render(spaced)
	label := lipgloss.NewStyle().Foreground(colDimBlue).
		Width(boxW).Align(lipgloss.Center).Render("share this code with your partner")
	waiting := lipgloss.NewStyle().Foreground(colCyan).Italic(true).
		Width(boxW).Align(lipgloss.Center).Render("waiting for connection...")

	boxContent := strings.Join([]string{"", label, "", otpBig, "", waiting, ""}, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colDarkTeal).
		Padding(0, 2).
		Render(boxContent)

	h := m.height - 3
	centered := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
	hint := stylePrompt.Render("  ctrl+c to quit")

	return strings.Join([]string{header, heavyDiv, centered, hint}, "\n")
}

// ─── message rendering ───────────────────────────────────────────────────────

func renderMsg(cm chatMsg, w int) []string {
	if cm.isSystem {
		return []string{styleSystem.Render("  ∙ " + cm.text)}
	}

	var senderStr string
	if cm.isSelf {
		senderStr = styleSenderSelf.Render("you")
	} else {
		senderStr = styleSenderOther.Render(cm.sender)
	}
	tsStr := styleTimestamp.Render(cm.ts.Format("02 Jan 15:04"))
	headerLine := "  " + senderStr + "  " + tsStr

	textW := w - 4
	if textW < 10 {
		textW = 10
	}
	var textStyle lipgloss.Style
	if cm.isSelf {
		textStyle = styleTextSelf.Width(textW)
	} else {
		textStyle = styleTextOther.Width(textW)
	}

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
		m.addSystem("alt+enter: send  •  /login <key>  •  /leave  •  /mode relay|mediate|game-master")

	default:
		m.addSystem("unknown command: " + parts[0])
	}
	return m, nil
}

// ─── filesystem helpers ──────────────────────────────────────────────────────

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	".cache": true, "__pycache__": true, "irislink-context": true,
}

// cwdFiles returns relative file paths up to 2 levels deep, skipping noise.
func cwdFiles(root string) []string {
	var files []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
			continue
		}
		if e.IsDir() {
			sub, _ := os.ReadDir(filepath.Join(root, e.Name()))
			for _, se := range sub {
				if strings.HasPrefix(se.Name(), ".") || se.IsDir() {
					continue
				}
				files = append(files, e.Name()+"/"+se.Name())
			}
		} else {
			files = append(files, e.Name())
		}
		if len(files) >= 30 {
			break
		}
	}
	return files
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
