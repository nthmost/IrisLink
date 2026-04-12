package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

const consoleURL = "https://console.anthropic.com"

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	cmd.Start() //nolint:errcheck
}

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

	styleSystem  = lipgloss.NewStyle().Foreground(colDimGray).Italic(true)
	styleClaude  = lipgloss.NewStyle().Foreground(colCyan).Italic(true)
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
	focusMode    = 2
	focusClaude  = 3
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
	isClaude bool
}

type incomingEnvMsg struct{ env transport.Envelope }
type selfSentMsg struct {
	text   string
	blocks []transport.ContextBlock
}
type sendErrMsg struct{ err error }
type apiKeyReceivedMsg struct{ key string }
type claudeResponseMsg struct {
	query    string
	response string
}

// ─── model ───────────────────────────────────────────────────────────────────

type tuiModel struct {
	messages        []chatMsg
	compose         textarea.Model
	otp             string
	handle          string
	mode            string
	client          *transport.Client
	incoming        chan transport.Envelope
	cfg             state.Config
	cwd             string
	width           int
	height          int
	showWaiting     bool
	sentFiles       map[string]bool  // files sent as context this session
	focusArea       int              // 0=compose, 1=sidebar tree, 2=mode, 3=claude
	sidebarCursor   int
	expanded        map[string]bool  // which dirs are expanded
	loginOverlay    bool             // true = show masked key input overlay
	loginInput      textinput.Model
	participants    map[string]bool  // currently connected handles
	maxParticipants int              // 0 = unlimited
	isCreator       bool             // creator enforces capacity
}

func initialModel(otp, handle, mode string, maxParticipants int, isCreator bool, client *transport.Client, incoming chan transport.Envelope, cfg state.Config, cwd string) tuiModel {
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
		compose:         ta,
		showWaiting:     isCreator, // creator waits; joiner doesn't
		otp:             otp,
		handle:          handle,
		mode:            mode,
		client:          client,
		incoming:        incoming,
		cfg:             cfg,
		cwd:             cwd,
		width:           80,
		height:          24,
		sentFiles:       make(map[string]bool),
		expanded:        make(map[string]bool),
		loginInput:      li,
		participants:    make(map[string]bool),
		maxParticipants: maxParticipants,
		isCreator:       isCreator,
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

func waitForAPIKey(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		return apiKeyReceivedMsg{key: <-ch}
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
			m.focusArea = (m.focusArea + 1) % 4
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

		case m.focusArea == focusMode && msg.Type == tea.KeyEnter:
			skipCompose = true
			modes := []string{"relay", "mediate", "game-master"}
			for i, mo := range modes {
				if m.mode == mo {
					m.mode = modes[(i+1)%len(modes)]
					break
				}
			}
			m.addSystem("mode: " + m.mode)

		case m.focusArea == focusClaude && msg.Type == tea.KeyEnter:
			skipCompose = true
			if m.cfg.ClaudeAPIKey == "" {
				keyCh, port, err := startAuthReceiver()
				if err != nil {
					m.addSystem("could not start auth server: " + err.Error())
				} else {
					openBrowser(fmt.Sprintf("http://localhost:%d", port))
					m.addSystem(fmt.Sprintf("browser opened — submit your key at localhost:%d", port))
					cmds = append(cmds, waitForAPIKey(keyCh))
				}
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
			if strings.HasPrefix(strings.ToLower(raw), "hey claude") {
				state.AppendLog(m.otp, m.handle, raw)
				cmds = append(cmds, m.askClaudeCmd(raw))
			} else {
				cmds = append(cmds, m.sendMsg(raw))
			}
		}

	case incomingEnvMsg:
		env := msg.env
		switch env.Type {
		case "presence":
			switch env.Text {
			case "joined":
				m.showWaiting = false
				m.participants[env.Sender] = true
				// Creator enforces capacity.
				if m.isCreator && m.maxParticipants > 0 && len(m.participants) > m.maxParticipants-1 {
					m.client.Publish(context.Background(), transport.Envelope{ //nolint:errcheck
						Type: "control",
						Text: "room_full",
					})
					m.addSystem(fmt.Sprintf("%s was turned away — room is full (%d)", env.Sender, m.maxParticipants))
				} else {
					m.addSystem(fmt.Sprintf("%s joined", env.Sender))
				}
			case "left":
				delete(m.participants, env.Sender)
				m.addSystem(fmt.Sprintf("%s left", env.Sender))
			default:
				m.addSystem(fmt.Sprintf("%s %s", env.Sender, env.Text))
			}
		case "control":
			if env.Text == "room_full" {
				m.addSystem("room is full — disconnecting")
				return m, tea.Quit
			}
		default:
			m.messages = append(m.messages, chatMsg{
				ts:     time.UnixMilli(env.Timestamp),
				sender: env.Sender,
				text:   env.Text,
			})
			state.AppendLog(m.otp, env.Sender, env.Text)
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
		state.AppendLog(m.otp, m.handle, msg.text)
		for _, b := range msg.blocks {
			m.sentFiles[b.Source] = true
		}

	case claudeResponseMsg:
		m.messages = append(m.messages, chatMsg{
			ts:       time.Now(),
			sender:   "claude",
			text:     msg.response,
			isClaude: true,
		})
		state.AppendLog(m.otp, "claude", msg.response)

	case sendErrMsg:
		m.addSystem("send error: " + msg.err.Error())

	case apiKeyReceivedMsg:
		m.cfg.ClaudeAPIKey = msg.key
		state.WriteConfig(m.cfg) //nolint:errcheck
		m.addSystem("logged in — Claude context enabled")
	}

	var composeCmd tea.Cmd
	if !skipCompose {
		m.compose, composeCmd = m.compose.Update(msg)
		cmds = append(cmds, composeCmd)
	}

	return m, tea.Batch(cmds...)
}

// askClaudeCmd handles a "Hey Claude" message: asks Claude directly and returns
// a claudeResponseMsg. The message is NOT forwarded to the other party.
func (m *tuiModel) askClaudeCmd(query string) tea.Cmd {
	apiKey := m.cfg.ClaudeAPIKey
	model := m.cfg.ClaudeModel
	return func() tea.Msg {
		if apiKey == "" {
			return claudeResponseMsg{query: query, response: "(no Claude API key — use tab→claude to log in)"}
		}
		resp, err := claude.Ask(apiKey, model, query)
		if err != nil {
			return claudeResponseMsg{query: query, response: "(error: " + err.Error() + ")"}
		}
		return claudeResponseMsg{query: query, response: resp}
	}
}

// sendMsg builds, mediates if needed, selects context, and publishes.
// Mediate and SelectContext run concurrently; both are bounded by a 15s timeout.
func (m *tuiModel) sendMsg(text string) tea.Cmd {
	apiKey := m.cfg.ClaudeAPIKey
	model := m.cfg.ClaudeModel
	mode := m.mode
	cwd := m.cwd
	client := m.client

	return func() tea.Msg {
		finalText := text
		var blocks []transport.ContextBlock

		if apiKey != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			type mediateResult struct {
				result claude.MediateResult
			}
			type contextResult struct{ blocks []transport.ContextBlock }

			mediateCh := make(chan mediateResult, 1)
			contextCh := make(chan contextResult, 1)

			if mode != "relay" {
				go func() {
					r, err := claude.Mediate(apiKey, model, mode, text)
					if err != nil {
						r = claude.MediateResult{Send: true, Text: text}
					}
					mediateCh <- mediateResult{r}
				}()
			} else {
				mediateCh <- mediateResult{claude.MediateResult{Send: true, Text: text}}
			}

			go func() {
				if blks, err := claude.SelectContext(apiKey, text, cwd); err == nil {
					contextCh <- contextResult{blks}
				} else {
					contextCh <- contextResult{}
				}
			}()

			var mediateR claude.MediateResult
			select {
			case r := <-mediateCh:
				mediateR = r.result
			case <-ctx.Done():
				mediateR = claude.MediateResult{Send: true, Text: text}
			}

			// If Claude wants clarification, surface the questions locally and abort send.
			if !mediateR.Send {
				return claudeResponseMsg{query: text, response: mediateR.Text}
			}
			finalText = mediateR.Text

			select {
			case r := <-contextCh:
				blocks = r.blocks
			case <-ctx.Done():
			}
		}

		env := transport.Envelope{
			Type:    "message",
			Text:    finalText,
			Context: blocks,
		}
		if err := client.Publish(context.Background(), env); err != nil {
			return sendErrMsg{err: err}
		}
		return selfSentMsg{text: finalText, blocks: blocks}
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

	// Split compose view into individual lines so the zip loop stays height-accurate.
	msgPaneLines := append(allLines, innerDiv)
	for _, line := range strings.Split(m.compose.View(), "\n") {
		msgPaneLines = append(msgPaneLines, line)
	}

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

	hint := stylePrompt.Render("  " + sendKey() + " to send  •  tab: compose/files/mode/claude  •  /help")
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

	selectedBg := lipgloss.NewStyle().
		Background(colDarkTeal).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true)

	if m.cfg.ClaudeAPIKey == "" {
		label := "  [ LOGIN ]"
		var btn string
		if focused {
			btn = selectedBg.Render(padToVisible(label, inner))
		} else {
			btn = styleSidebarHint.Render(label)
		}
		return []string{
			sep,
			styleSidebarHeader.Render(" claude"),
			btn,
			"",
			"",
			"",
			"",
			"",
		}
	}

	masked := "sk-ant-..."
	if len(m.cfg.ClaudeAPIKey) > 12 {
		masked = m.cfg.ClaudeAPIKey[:12] + "..."
	}

	label := "  ✓ claude"
	var header string
	if focused {
		header = selectedBg.Render(padToVisible(label, inner))
	} else {
		header = lipgloss.NewStyle().Foreground(colCyan).Bold(true).Render(label)
	}

	return []string{
		sep,
		header,
		styleSidebarHint.Render("  " + masked),
		"",
		"",
		"",
		"",
		styleSidebarHint.Render("  enter: logout"),
	}
}

// renderModePanel renders the mode selector at the top of the sidebar.
func (m tuiModel) renderModePanel(width int) []string {
	inner := width - 1
	focused := m.focusArea == focusMode

	activeBg := lipgloss.NewStyle().
		Background(colDarkTeal).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(colViolet).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(colDimGray)

	modes := []string{"relay", "mediate", "game-master"}
	var rows []string
	for _, mo := range modes {
		label := "  " + mo
		if mo == m.mode {
			label = "  ● " + mo
			if focused {
				rows = append(rows, activeBg.Render(padToVisible(label, inner)))
			} else {
				rows = append(rows, activeStyle.Render(label))
			}
		} else {
			rows = append(rows, inactiveStyle.Render(label))
		}
	}

	hint := styleSidebarHint.Render("  enter: cycle")

	return append([]string{styleSidebarHeader.Render(" mode")}, append(rows, hint)...)
}

// participantPanelHeight returns the number of lines renderParticipants will produce.
func (m tuiModel) participantPanelHeight() int {
	n := len(m.participants) + 1 // header + one per participant
	if n < 2 {
		n = 2 // header + "(waiting...)"
	}
	return n
}

// renderParticipants renders the live participant list.
func (m tuiModel) renderParticipants(width int) []string {
	inner := width - 1
	capLabel := ""
	if m.maxParticipants > 0 {
		capLabel = fmt.Sprintf(" (%d max)", m.maxParticipants)
	}
	lines := []string{styleSidebarHeader.Render(" who's here" + capLabel)}

	if len(m.participants) == 0 {
		lines = append(lines, styleSidebarHint.Render("  waiting..."))
		return lines
	}
	for handle := range m.participants {
		label := "  ● " + handle
		if lipgloss.Width(label) > inner {
			label = label[:inner]
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colPink).Render(label))
	}
	return lines
}

// renderSidebar returns lines for the context sidebar (width = sidebarW).
func (m tuiModel) renderSidebar(totalRows int) []string {
	inner := sidebarW - 1
	sep := styleDivider.Render(" " + strings.Repeat("─", inner-1))

	// mode(5) + sep(1) + participants(N) + sep(1) + context(1) + sep(1) + claude(8)
	modePanelLines := m.renderModePanel(sidebarW)
	lines := append(modePanelLines, sep)
	lines = append(lines, m.renderParticipants(sidebarW)...)
	lines = append(lines, sep)
	lines = append(lines, styleSidebarHeader.Render(" context"))
	lines = append(lines, sep)

	fixedLines := 5 + 1 + m.participantPanelHeight() + 1 + 1 + 1 + 8
	treeRows := totalRows - fixedLines
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
	lines = append(lines, m.renderClaudePanel(sidebarW)...)

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
		Width(boxW).Align(lipgloss.Center).Render("browser opened → create or copy an API key")
	urlLine := lipgloss.NewStyle().Foreground(colDimGray).Italic(true).
		Width(boxW).Align(lipgloss.Center).Render(consoleURL)
	hint := lipgloss.NewStyle().Foreground(colDimGray).Italic(true).
		Width(boxW).Align(lipgloss.Center).Render("enter to confirm  •  esc to cancel")

	input := lipgloss.NewStyle().Width(boxW).Align(lipgloss.Center).Render(m.loginInput.View())

	boxContent := strings.Join([]string{"", title, "", prompt, urlLine, "", input, "", hint, ""}, "\n")
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
	if cm.isClaude {
		tsStr := styleTimestamp.Render(cm.ts.Format("02 Jan 15:04"))
		headerLine := "  " + styleClaude.Render("claude") + "  " + tsStr
		textW := w - 4
		if textW < 10 {
			textW = 10
		}
		var bodyLines []string
		for _, para := range strings.Split(cm.text, "\n") {
			wrapped := styleClaude.Width(textW).Render(para)
			for _, line := range strings.Split(wrapped, "\n") {
				bodyLines = append(bodyLines, "  "+line)
			}
		}
		return append([]string{headerLine}, bodyLines...)
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

	case "/model":
		if len(parts) > 1 {
			resolved := resolveModel(parts[1])
			m.cfg.ClaudeModel = resolved
			state.WriteConfig(m.cfg) //nolint:errcheck
			m.addSystem("model: " + resolved)
		} else {
			current := m.cfg.ClaudeModel
			if current == "" {
				current = "claude-sonnet-4-6 (default)"
			}
			m.addSystem("model: " + current + "  •  usage: /model haiku|sonnet|opus|<full-id>")
		}

	case "/help":
		m.addSystem(sendKey() + ": send  •  tab: cycle focus  •  /leave  •  /mode relay|mediate|game-master  •  /model haiku|sonnet|opus")

	default:
		m.addSystem("unknown command: " + parts[0])
	}
	return m, nil
}

// resolveModel maps short aliases to full Anthropic model IDs.
// If the input is already a full ID (contains a hyphen after "claude-"), it is returned as-is.
func resolveModel(name string) string {
	switch strings.ToLower(name) {
	case "haiku":
		return "claude-haiku-4-5-20251001"
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-6"
	default:
		return name // pass through full model IDs unchanged
	}
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
