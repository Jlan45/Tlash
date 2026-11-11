// Package tui is the Tlash terminal interface.
package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// Vars so tests can shorten them.
var notifyDuration = 4 * time.Second

type hint struct{ key, desc string }

// page is one screen in the sidebar. Keys reach only the active page; other messages reach all.
type page interface {
	title() string
	activate() tea.Cmd
	update(msg tea.Msg) tea.Cmd
	view(width, height int) string
	hints() []hint
	// capturingInput is true while a text field owns the keyboard.
	capturingInput() bool
}

type notification struct {
	text    string
	isErr   bool
	expires time.Time
}

type notifyExpireMsg struct{}

type Options struct {
	Client    *mihomoapi.Client
	DelayTest mihomoapi.DelayTest
	LogLevel  string
	// Workspace enables core, profile and service management; nil attaches to an external core.
	Workspace *workspace.Workspace
}

type animTickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type App struct {
	sess    *session
	streams *coreStreams
	pages   []page
	logs    *logsPage
	active  int

	animating bool
	frame     int

	width, height int
	showHelp      bool
	confirm       *confirmMsg
	note          *notification
}

func New(opts Options) *App {
	sess := &session{client: opts.Client, delayTest: opts.DelayTest, ws: opts.Workspace, busy: map[string]string{}}
	streams := newCoreStreams(opts.Client)
	logLevel := opts.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	a := &App{sess: sess, streams: streams, logs: newLogsPage(sess, streams, logLevel)}
	a.pages = []page{newOverviewPage(sess), newProxiesPage(sess)}
	if sess.ws != nil {
		a.pages = append(a.pages, newProfilesPage(sess))
	}
	a.pages = append(a.pages, a.logs)
	if sess.ws != nil {
		a.pages = append(a.pages, newCorePage(sess))
	}
	return a
}

func (a *App) Init() tea.Cmd {
	a.streams.start(a.logs.level)
	return tea.Batch(a.sess.refreshAll(), statusTick(), a.streams.next(), a.pages[a.active].activate())
}

// Close stops background streams; call after the program exits.
func (a *App) Close() { a.streams.stop() }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.update(msg)
	// Animate the status-bar spinner while background jobs run.
	if len(a.sess.busy) > 0 && !a.animating {
		a.animating = true
		cmd = tea.Batch(cmd, animTick())
	}
	return a, cmd
}

func animTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return animTickMsg{} })
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case animTickMsg:
		a.frame++
		if len(a.sess.busy) == 0 {
			a.animating = false
			return nil
		}
		return animTick()

	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return nil

	case streamEventMsg:
		return tea.Batch(a.broadcast(msg.inner), a.streams.next())

	case statusTickMsg:
		cmds := []tea.Cmd{fetchStatus(a.sess.client), statusTick(), a.pages[a.active].update(msg)}
		if a.sess.ws != nil {
			cmds = append(cmds, fetchCoreState(a.sess.ws, false))
		}
		return tea.Batch(cmds...)

	case notifyMsg:
		a.note = &notification{text: msg.text, isErr: msg.isErr, expires: time.Now().Add(notifyDuration)}
		return tea.Tick(notifyDuration, func(time.Time) tea.Msg { return notifyExpireMsg{} })

	case notifyExpireMsg:
		if a.note != nil && time.Now().After(a.note.expires.Add(-50*time.Millisecond)) {
			a.note = nil
		}
		return nil

	case confirmMsg:
		a.confirm = &msg
		return nil

	case tea.KeyPressMsg:
		return a.handleKey(msg)

	case tea.MouseClickMsg:
		return a.handleClick(msg.Mouse())

	case tea.MouseWheelMsg:
		if a.confirm != nil {
			return nil
		}
		key := tea.KeyPressMsg{Code: tea.KeyDown}
		if msg.Button == tea.MouseWheelUp {
			key = tea.KeyPressMsg{Code: tea.KeyUp}
		}
		return a.pages[a.active].update(key)
	}
	return a.broadcast(msg)
}

func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case trafficMsg:
		a.sess.recordTraffic(mihomoapi.Traffic(msg))
	case memoryMsg:
		a.sess.memory = msg.InUse
	case coreStateMsg:
		a.sess.applyCoreState(msg)
	case jobDoneMsg:
		delete(a.sess.busy, msg.key)
	case statusMsg:
		a.sess.lastErr = msg.err
		a.sess.connected = msg.err == nil
		if msg.err == nil {
			a.sess.version, a.sess.config, a.sess.connCount = msg.version, msg.config, msg.connCount
		}
	}
	cmds := make([]tea.Cmd, 0, len(a.pages))
	for _, p := range a.pages {
		cmds = append(cmds, p.update(msg))
	}
	return tea.Batch(cmds...)
}

func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	if a.confirm != nil {
		c := a.confirm
		switch key {
		case "y", "Y", "enter":
			a.confirm = nil
			return c.onYes()
		case "n", "N", "esc", "q":
			a.confirm = nil
		}
		return nil
	}
	cur := a.pages[a.active]
	if cur.capturingInput() {
		return cur.update(msg)
	}
	if a.showHelp {
		a.showHelp = false
		if key == "?" || key == "esc" || key == "q" {
			return nil
		}
	}
	switch key {
	case "q":
		return tea.Quit
	case "?":
		a.showHelp = true
		return nil
	case "tab":
		return a.switchTo((a.active + 1) % len(a.pages))
	case "shift+tab":
		return a.switchTo((a.active + len(a.pages) - 1) % len(a.pages))
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if idx := int(key[0] - '1'); idx < len(a.pages) {
			return a.switchTo(idx)
		}
	}
	return cur.update(msg)
}

func (a *App) switchTo(idx int) tea.Cmd {
	if idx == a.active {
		return nil
	}
	a.active = idx
	a.showHelp = false
	return a.pages[idx].activate()
}

func (a *App) handleClick(m tea.Mouse) tea.Cmd {
	if a.confirm != nil || m.Button != tea.MouseLeft {
		return nil
	}
	// Sidebar rows: title on row 0, a blank row, then one row per page.
	if m.X < a.sidebarWidth() {
		if idx := m.Y - 2; idx >= 0 && idx < len(a.pages) {
			return a.switchTo(idx)
		}
	}
	return nil
}

func (a *App) sidebarWidth() int {
	if a.width < 64 {
		return 4
	}
	return 11
}

func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "Tlash"
	return v
}

func (a *App) render() string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}
	if a.width < 40 || a.height < 10 {
		return "终端太小，至少需要 40×10"
	}
	bodyH := a.height - 1
	sideW := a.sidebarWidth()
	contentW := a.width - sideW - 2 // sidebar border + one column gap

	var content string
	switch {
	case a.confirm != nil:
		content = renderConfirm(a.confirm.prompt, contentW, bodyH)
	case a.showHelp:
		content = a.renderHelp(contentW, bodyH)
	default:
		content = a.pages[a.active].view(contentW, bodyH)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		a.renderSidebar(sideW, bodyH),
		" ",
		fitBlock(content, contentW, bodyH),
	)
	return body + "\n" + a.renderStatusBar()
}

func (a *App) renderSidebar(w, h int) string {
	lines := []string{styleTitle.Render(ansi.Truncate("Tlash", w-1, "")), ""}
	for i, p := range a.pages {
		label := fmt.Sprintf("%d %s", i+1, p.title())
		if w < 8 {
			label = fmt.Sprintf(" %d", i+1)
		}
		label = padRight(label, w-1)
		if i == a.active {
			label = styleSelected.Render(label)
		}
		lines = append(lines, label)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(colorDim).
		Render(fitBlock(strings.Join(lines, "\n"), w-1, h))
}

func (a *App) renderStatusBar() string {
	s := a.sess
	var left string
	switch {
	case s.connected:
		left = styleOK.Render("● 运行中") + " " + s.version.Version +
			styleDim.Render(" │ ") + modeLabel(s.config.Mode) +
			styleDim.Render(" │ ") + "内存 " + formatBytes(s.memory) +
			styleDim.Render(" │ ") + fmt.Sprintf("连接 %d", s.connCount) +
			styleDim.Render(" │ ") + "↑" + formatSpeed(s.traffic.Up) + " ↓" + formatSpeed(s.traffic.Down)
	case s.ws != nil && s.core.known && !s.core.running:
		left = styleDim.Render("● 已停止")
	default:
		left = styleBad.Render("● 未连接")
	}
	if len(s.busy) > 0 {
		left += styleDim.Render(" │ ") + styleWarn.Render(spinnerFrames[a.frame%len(spinnerFrames)]+" "+busySummary(s.busy))
	}

	var right string
	switch {
	case a.note != nil && a.note.isErr:
		right = styleBad.Render(a.note.text)
	case a.note != nil:
		right = styleAccent.Render(a.note.text)
	case a.confirm != nil:
		right = renderHints([]hint{{"y", "确认"}, {"n", "取消"}})
	default:
		right = renderHints(append(shortHints(a.pages[a.active].hints(), 4), hint{"?", "帮助"}, hint{"q", "退出"}))
	}

	leftW, rightW := ansi.StringWidth(left), ansi.StringWidth(right)
	if leftW+rightW+2 > a.width {
		// Notifications take priority over the status summary on narrow terminals.
		left = ansi.Truncate(left, max(a.width-rightW-2, 0), "…")
		leftW = ansi.StringWidth(left)
		if leftW+rightW+2 > a.width {
			right = ansi.Truncate(right, max(a.width-leftW-2, 0), "…")
			rightW = ansi.StringWidth(right)
		}
	}
	gap := max(a.width-leftW-rightW, 1)
	return left + strings.Repeat(" ", gap) + right
}

func (a *App) renderHelp(w, h int) string {
	lines := []string{styleTitle.Render("快捷键 · " + a.pages[a.active].title()), ""}
	add := func(hs []hint) {
		for _, x := range hs {
			lines = append(lines, "  "+styleKey.Render(padRight(x.key, 14))+x.desc)
		}
	}
	add(a.pages[a.active].hints())
	lines = append(lines, "", styleTitle.Render("全局"), "")
	add([]hint{
		{"1-9", "切换页面"},
		{"tab / shift+tab", "下一页 / 上一页"},
		{"鼠标", "点击侧栏切页，滚轮滚动"},
		{"?", "显示 / 关闭帮助"},
		{"q / ctrl+c", "退出（不影响内核运行）"},
	})
	lines = append(lines, "", styleDim.Render("按任意键关闭"))
	return fitBlock(strings.Join(lines, "\n"), w, h)
}

func renderConfirm(prompt string, w, h int) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarn).
		Padding(1, 3).
		Render(prompt + "\n\n" + renderHints([]hint{{"y", "确认"}, {"n", "取消"}}))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func renderHints(hs []hint) string {
	parts := make([]string, len(hs))
	for i, x := range hs {
		parts[i] = styleKey.Render(x.key) + " " + styleDim.Render(x.desc)
	}
	return strings.Join(parts, "  ")
}

func shortHints(hs []hint, n int) []hint {
	if len(hs) > n {
		return hs[:n]
	}
	return hs
}

func busySummary(busy map[string]string) string {
	texts := make([]string, 0, len(busy))
	for _, t := range busy {
		texts = append(texts, t)
	}
	slices.Sort(texts)
	return strings.Join(texts, "、") + "…"
}

func modeLabel(mode string) string {
	switch mode {
	case "rule":
		return "规则"
	case "global":
		return "全局"
	case "direct":
		return "直连"
	default:
		return mode
	}
}

// fitBlock truncates or pads s to exactly w×h cells so layouts never wrap.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = padRight(ansi.Truncate(l, w, "…"), w)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
