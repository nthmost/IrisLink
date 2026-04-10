package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
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

	focusCompose = 0
	focusSidebar = 1
	focusClaude  = 2
)

func sendKey() string {
	if runtime.GOOS == "darwin" {
		return "opt+enter"
	}
	return "alt+enter"
}

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
	messages      []chatMsg
	compose       textarea.Model
	otp           string
	handle        string
	mode          string
	client        *transport.Client
	incoming      chan transport.Envelope
	cfg           state.Config
	cwd           string
	width         int
	height        int
	showWaiting   bool
	sentFiles     map[string]bool // files sent as context this session
	focusArea     int             // 0=compose, 1=sidebar tree, 2=claude panel
	sidebarCursor int
	expanded      map[string]bool // which dirs are expanded
	loginOverlay  bool            // true = show masked key input overlay
	loginInput    textinput.Model
}

func initialModel(otp, handle, mode string, client *transport.Client, incoming chan transport.Envelope, cfg state.Config, cwd string, showWaiting bool) tuiModel {
	ta := textarea.New()
	ta.Placeholder = "write something... (" + sendKey() + " to send, /help for commands)"
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

	li := textinput.New()
	li.Placeholder = "sk-ant-..."
	li.EchoMode = textinput.EchoPassword
	li.CharLimit = 200
	li.Width = 30

	if cfg.ClaudeAPIKey == "" {
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			cfg.ClaudeAPIKey = v
			state.WriteConfig(cfg) //nolint:errcheck
		}
	}

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
		expanded:    make(map[string]bool),
		loginInput:  li,
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

	// Handle login overlay before normal processing.
	if m.loginOverlay {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyEsc:
				m.loginOverlay = false
				m.loginInput.Blur()
				skipCompose = true
				return m, nil
			case tea.KeyEnter:
				key := strings.TrimSpace(m.loginInput.Value())
				if key != "" {
					m.cfg.ClaudeAPIKey = key
					state.WriteConfig(m.cfg) //nolint:errcheck
					m.addSystem("logged in — Claude context enabled")
				}
				m.loginOverlay = false
				m.loginInput.Blur()
				skipCompose = true
				return m, nil
			default:
				var liCmd tea.Cmd
				m.loginInput, liCmd = m.loginInput.Update(msg)
				return m, liCmd
			}
		default:
			var liCmd tea.Cmd
			m.loginInput, liCmd = m.loginInput.Update(msg)
			return m, liCmd
		}
	}

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

		case msg.Type == tea.KeyTab:
			skipCompose = true
			m.focusArea = (m.focusArea + 1) % 3
			if m.focusArea == focusCompose {
				m.compose.Focus()
				m.loginInput.Blur()
			} else {
				m.compose.Blur()
				m.loginInput.Blur()
			}

		case m.focusArea == focusSidebar && msg.Type == tea.KeyUp:
			skipCompose = true
			if m.sidebarCursor > 0 {
				m.sidebarCursor--
			}

		case m.focusArea == focusSidebar && msg.Type == tea.KeyDown:
			skipCompose = true
			items := m.buildSidebarItems()
			if m.sidebarCursor < len(items)-1 {
				m.sidebarCursor++
			}

		case m.focusArea == focusSidebar && (msg.Type == tea.KeyEnter || msg.String() == " "):
			skipCompose = true
			items := m.buildSidebarItems()
			if m.sidebarCursor < len(items) {
				item := items[m.sidebarCursor]
				if item.isDir {
					m.expanded[item.path] = !m.expanded[item.path]
				}
			}

		case m.focusArea == focusClaude && msg.Type == tea.KeyEnter:
			skipCompose = true
			if m.cfg.ClaudeAPIKey == "" {
				// open login overlay
				m.loginOverlay = true
				m.loginInput.SetValue("")
				m.loginInput.Focus()
			} else {
				// logout
				m.cfg.ClaudeAPIKey = ""
				state.WriteConfig(m.cfg) //nolint:errcheck
				m.addSystem("logged out")
			}

		case m.focusArea == focusCompose && msg.Alt && msg.Type == tea.KeyEnter:
			// Alt/Opt+Enter sends — don't forward to textarea
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

	if m.loginOverlay {
		return m.renderLoginOverlay(w)
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

	hint := stylePrompt.Render("  " + sendKey() + " to send  •  tab: browse files  •  /help")
	divider := styleDivider.Render(strings.Repeat("─", w))

	return strings.Join([]string{header, heavyDiv, body, divider, hint}, "\n")
}

// sidebarItem is one navigable row in the sidebar tree.
type sidebarItem struct {
	label string
	path  string
	isDir bool
	depth int
}

// buildSidebarItems expands the tree based on m.expanded.
func (m tuiModel) buildSidebarItems() []sidebarItem {
	top := cwdEntries(m.cwd)
	var items []sidebarItem
	for _, e := range top {
		arrow := "▶ "
		if m.expanded[e.path] {
			arrow = "▼ "
		}
		label := e.label
		if e.isDir {
			label = arrow + e.label
		}
		items = append(items, sidebarItem{label: label, path: e.path, isDir: e.isDir, depth: 0})
		if e.isDir && m.expanded[e.path] {
			children, _ := os.ReadDir(filepath.Join(m.cwd, e.path))
			for _, c := range children {
				if strings.HasPrefix(c.Name(), ".") || c.IsDir() {
					continue
				}
				childPath := e.path + "/" + c.Name()
				items = append(items, sidebarItem{
					label: "  " + c.Name(),
					path:  childPath,
					isDir: false,
					depth: 1,
				})
			}
		}
	}
	return items
}

// renderClaudePanel renders the claude login/status panel at the bottom of the sidebar.
func (m tuiModel) renderClaudePanel(width int) []string {
	inner := width - 1
	sep := styleDivider.Render(" " + strings.Repeat("─", inner-1))

	focused := m.focusArea == focusClaude

	if m.cfg.ClaudeAPIKey == "" {
		label := "  [ LOGIN ]"
		var btn string
		if focused {
			btn = lipgloss.NewStyle().
				Background(colDarkTeal).
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Render(padToVisible(label, inner))
		} else {
			btn = styleSidebarHint.Render(label)
		}
		return []string{sep, btn, styleSidebarHint.Render("  claude context"), ""}
	}

	// logged in
	masked := "sk-ant-..."
	if len(m.cfg.ClaudeAPIKey) > 8 {
		masked = m.cfg.ClaudeAPIKey[:8] + "..."
	}
	statusStyle := lipgloss.NewStyle().Foreground(colCyan)
	label := "  ✓ claude"
	var header string
	if focused {
		header = lipgloss.NewStyle().
			Background(colDarkTeal).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Render(padToVisible(label, inner))
	} else {
		header = statusStyle.Render(label)
	}
	keyLine := styleSidebarHint.Render("  " + masked)
	hintLine := styleSidebarHint.Render("  enter: logout")
	return []string{sep, header, keyLine, hintLine}
}

// renderSidebar returns lines for the context sidebar (width = sidebarW).
func (m tuiModel) renderSidebar(totalRows int) []string {
	inner := sidebarW - 1

	title := styleSidebarHeader.Render(" context")
	sep := styleDivider.Render(" " + strings.Repeat("─", inner-1))
	lines := []string{title, sep}

	// Reserve last 5 rows for the claude panel (4 lines + 1 buffer).
	treeRows := totalRows - 6
	if treeRows < 0 {
		treeRows = 0
	}

	items := m.buildSidebarItems()
	if len(items) == 0 {
		lines = append(lines, styleSidebarHint.Render(" (empty dir)"))
	}
	selectedBg := lipgloss.NewStyle().
		Background(colDarkTeal).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true)

	treeLines := []string{}
	for i, item := range items {
		label := " " + item.label
		if lipgloss.Width(label) > inner {
			label = " …" + item.label[len(item.label)-(inner-3):]
		}
		sent := m.sentFiles[item.path]
		if !sent && item.isDir {
			for k := range m.sentFiles {
				if strings.HasPrefix(k, item.path+"/") {
					sent = true
					break
				}
			}
		}
		selected := m.focusArea == focusSidebar && i == m.sidebarCursor
		switch {
		case selected:
			treeLines = append(treeLines, selectedBg.Render(padToVisible(label, inner)))
		case sent:
			treeLines = append(treeLines, styleSidebarSent.Render(label))
		case item.isDir:
			treeLines = append(treeLines, styleSidebarHeader.Render(label))
		default:
			treeLines = append(treeLines, styleSidebarFile.Render(label))
		}
	}

	// Trim or pad tree section to treeRows.
	if len(treeLines) > treeRows {
		treeLines = treeLines[:treeRows]
	}
	for len(treeLines) < treeRows {
		treeLines = append(treeLines, "")
	}

	lines = append(lines, treeLines...)

	// Append claude panel at the bottom.
	claudeLines := m.renderClaudePanel(sidebarW)
	lines = append(lines, claudeLines...)

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

// ─── login overlay ───────────────────────────────────────────────────────────

func (m tuiModel) renderLoginOverlay(w int) string {
	boxW := 40
	if boxW > w-4 {
		boxW = w - 4
	}

	title := lipgloss.NewStyle().Foreground(colCyan).Bold(true).
		Width(boxW).Align(lipgloss.Center).Render("CLAUDE LOGIN")
	prompt := lipgloss.NewStyle().Foreground(colDimBlue).
		Width(boxW).Align(lipgloss.Center).Render("paste your Anthropic API key")
	hint := lipgloss.NewStyle().Foreground(colDimGray).Italic(true).
		Width(boxW).Align(lipgloss.Center).Render("enter to confirm  •  esc to cancel")

	input := lipgloss.NewStyle().Width(boxW).Align(lipgloss.Center).Render(m.loginInput.View())

	boxContent := strings.Join([]string{"", title, "", prompt, "", input, "", hint, ""}, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colDarkTeal).
		Padding(0, 2).
		Render(boxContent)

	h := m.height
	if h < 10 {
		h = 10
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
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

	case "/mode":
		if len(parts) > 1 {
			m.mode = parts[1]
			m.addSystem("mode: " + m.mode)
		} else {
			m.addSystem("usage: /mode relay|mediate|game-master")
		}

	case "/help":
		m.addSystem(sendKey() + ": send  •  tab: cycle focus (compose/files/claude)  •  /leave  •  /mode relay|mediate|game-master")

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

// cwdEntry is one line in the sidebar: either a dir (with count) or a file.
type cwdEntry struct {
	label string
	isDir bool
	path  string // relative path, for sentFiles lookup
}

// cwdEntries returns a compact directory summary for the sidebar.
// Directories show as "dirname/  (N)" and loose root-level files show by name.
func cwdEntries(root string) []cwdEntry {
	top, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []cwdEntry
	for _, e := range top {
		if strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
			continue
		}
		if e.IsDir() {
			sub, _ := os.ReadDir(filepath.Join(root, e.Name()))
			n := 0
			for _, s := range sub {
				if !strings.HasPrefix(s.Name(), ".") && !s.IsDir() {
					n++
				}
			}
			label := fmt.Sprintf("%s/ (%d)", e.Name(), n)
			out = append(out, cwdEntry{label: label, isDir: true, path: e.Name()})
		} else {
			out = append(out, cwdEntry{label: e.Name(), isDir: false, path: e.Name()})
		}
		if len(out) >= 20 {
			break
		}
	}
	return out
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
