// Package tui is the Tlash terminal interface: lazydocker-style stacked panels on the
// left, a tabbed main panel on the right describing the selection.
package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// Vars so tests can shorten them.
var notifyDuration = 4 * time.Second

// coreStatePollEvery polls the service (an exec) once per this many status ticks.
const coreStatePollEvery = 3

type hint struct{ key, desc string }

// capturer is implemented by panels whose main tab can hold a text field.
type capturer interface{ capturing() bool }

// summary panels show one item spread over several lines, so they have no row cursor.
type summary interface{ summary() bool }

func isSummary(p panel) bool {
	s, ok := p.(summary)
	return ok && s.summary()
}

// paster receives bracketed-paste text for a text field inside the main panel.
type paster interface{ paste(tea.PasteMsg) tea.Cmd }

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

type screenMode int

const (
	modeNormal screenMode = iota
	modeHalf              // main panel takes most of the width
	modeFull              // only the focused side panel or the main panel
)

// panelGeom records where a side panel was drawn, for mouse hit-testing.
type panelGeom struct {
	y0, y1   int // box rows [y0, y1)
	rowStart int // screen row of the first list row
	offset   int // list index shown at rowStart
}

type App struct {
	sess    *session
	streams *coreStreams
	panels  []panel
	status  *statusPanel

	focus  int
	inMain bool
	tabs   []int
	// scroll is the offset of each non-interactive main tab, keyed by panel and tab.
	scroll map[[2]int]int
	mode   screenMode

	confirm *confirmMsg
	form    *inputForm
	menu    *actionMenu
	help    bool
	note    *notification

	animating bool
	frame     int
	ticks     int

	width, height int
	sideW         int
	geoms         []panelGeom
}

func New(opts Options) *App {
	sess := &session{client: opts.Client, delayTest: opts.DelayTest, ws: opts.Workspace, busy: map[string]string{}}
	streams := newCoreStreams(opts.Client)
	logLevel := opts.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	a := &App{sess: sess, streams: streams, scroll: map[[2]int]int{}}
	a.status = newStatusPanel(sess, newLogView(sess, streams, logLevel))
	if sess.ws != nil {
		a.panels = []panel{a.status, newProfilesPanel(sess), newGroupsPanel(sess), newOverridesPanel(sess),
			newConnectionsPanel(sess), newRulesPanel(sess), newSettingsPanel(sess)}
	} else {
		// External core: Tlash doesn't own its config, so only runtime views.
		a.panels = []panel{a.status, newGroupsPanel(sess), newConnectionsPanel(sess), newRulesPanel(sess), newSettingsPanel(sess)}
	}
	a.tabs = make([]int, len(a.panels))
	return a
}

func (a *App) Init() tea.Cmd {
	a.streams.start(a.status.logs.level)
	cmds := []tea.Cmd{a.sess.refreshAll(), statusTick(), a.streams.next()}
	for _, p := range a.panels {
		cmds = append(cmds, p.activate())
	}
	return tea.Batch(cmds...)
}

// Close stops background streams; call after the program exits.
func (a *App) Close() { a.streams.stop() }

func (a *App) cur() panel { return a.panels[a.focus] }

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
		a.ticks++
		cmds := []tea.Cmd{fetchStatus(a.sess.client), statusTick()}
		if a.sess.ws != nil && a.ticks%coreStatePollEvery == 0 {
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
	case openFormMsg:
		a.form = msg.form
		return nil
	case tea.KeyPressMsg:
		return a.handleKey(msg)
	case tea.PasteMsg:
		return a.handlePaste(msg)
	case tea.MouseClickMsg:
		return a.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		if a.popupOpen() {
			return nil
		}
		code := tea.KeyDown
		if msg.Button == tea.MouseWheelUp {
			code = tea.KeyUp
		}
		return a.handleKey(tea.KeyPressMsg{Code: code})
	}
	return a.broadcast(msg)
}

func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case trafficMsg:
		a.sess.recordTraffic(mihomoapi.Traffic(msg))
	case memoryMsg:
		a.sess.memory = msg.InUse
	case statusMsg:
		a.sess.applyStatus(msg)
	case coreStateMsg:
		a.sess.applyCoreState(msg)
	case jobDoneMsg:
		delete(a.sess.busy, msg.key)
	}
	cmds := make([]tea.Cmd, 0, len(a.panels))
	for _, p := range a.panels {
		cmds = append(cmds, p.update(msg))
	}
	return tea.Batch(cmds...)
}

func (a *App) popupOpen() bool { return a.confirm != nil || a.form != nil || a.menu != nil || a.help }

func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	switch {
	case a.confirm != nil:
		c := a.confirm
		switch key {
		case "y", "Y", "enter":
			a.confirm = nil
			return c.onYes()
		case "n", "N", "esc", "q":
			a.confirm = nil
		}
		return nil
	case a.form != nil:
		cmd, done := a.form.update(msg)
		if done {
			a.form = nil
		}
		return cmd
	case a.menu != nil:
		cmd, done := a.menu.update(msg)
		if done {
			a.menu = nil
		}
		return cmd
	case a.help:
		a.help = false
		return nil
	}

	p := a.cur()
	// A text field inside the main panel owns every key until it closes.
	if c, ok := p.(capturer); ok && a.inMain && c.capturing() {
		cmd, _ := p.mainKey(a.tabs[a.focus], msg)
		return cmd
	}
	ls := p.list()
	if ls.filter.active {
		cmd := ls.filter.update(msg)
		ls.cursor = 0
		return cmd
	}

	// Keys every context shares.
	switch key {
	case "q":
		return tea.Quit
	case "?":
		a.help = true
		return nil
	case "x":
		a.menu = &actionMenu{title: p.title() + " · 可用操作", items: a.contextActions()}
		return nil
	case "+":
		a.mode = (a.mode + 1) % 3
		return nil
	case "_":
		a.mode = (a.mode + 2) % 3
		return nil
	case "[":
		return a.switchTab(-1)
	case "]":
		return a.switchTab(1)
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if idx := int(key[0] - '1'); idx < len(a.panels) {
			return a.focusPanel(idx)
		}
	}

	if a.inMain {
		return a.handleMainKey(msg)
	}
	return a.handleSideKey(msg)
}

// handlePaste delivers pasted text to whichever text field is open; terminals send
// pastes as one message (bracketed paste), not as key presses.
func (a *App) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if a.form != nil {
		return a.form.paste(msg)
	}
	if a.popupOpen() {
		return nil
	}
	p := a.cur()
	if ls := p.list(); ls.filter.active {
		ls.cursor = 0
		return ls.filter.paste(msg)
	}
	if ps, ok := p.(paster); ok && a.inMain {
		return ps.paste(msg)
	}
	return nil
}

func (a *App) handleSideKey(msg tea.KeyPressMsg) tea.Cmd {
	p := a.cur()
	ls := p.list()
	n := len(p.rows(a.sideW))
	switch msg.String() {
	case "tab", "right", "l":
		return a.focusPanel((a.focus + 1) % len(a.panels))
	case "shift+tab", "left", "h":
		return a.focusPanel((a.focus + len(a.panels) - 1) % len(a.panels))
	case "up", "k":
		// Moving past the first row continues into the panel above, at its last row.
		if ls.cursor <= 0 || isSummary(p) {
			if a.focus > 0 {
				cmd := a.focusPanel(a.focus - 1)
				prev := a.cur()
				prev.list().cursor = max(len(prev.rows(a.sideW))-1, 0)
				return cmd
			}
			return nil
		}
		ls.move(-1, n)
	case "down", "j":
		// Moving past the last row continues into the panel below, at its first row.
		if ls.cursor >= n-1 || isSummary(p) {
			if a.focus < len(a.panels)-1 {
				cmd := a.focusPanel(a.focus + 1)
				a.cur().list().cursor = 0
				return cmd
			}
			return nil
		}
		ls.move(1, n)
	case "pgup", "ctrl+u":
		ls.move(-10, n)
	case "pgdown", "ctrl+d":
		ls.move(10, n)
	case "home", "g":
		ls.move(-n, n)
	case "end", "G":
		ls.move(n, n)
	case "enter":
		a.inMain = true
	case "/":
		if p.filterHint() != "" {
			return ls.filter.start(p.filterHint())
		}
	case "esc":
		ls.filter.clear()
	default:
		return runAction(p.actions(), msg.String())
	}
	return nil
}

func (a *App) handleMainKey(msg tea.KeyPressMsg) tea.Cmd {
	p, tab := a.cur(), a.tabs[a.focus]
	switch msg.String() {
	case "esc", "left", "h", "tab":
		a.inMain = false
		return nil
	}
	if p.interactive(tab) {
		if cmd, ok := p.mainKey(tab, msg); ok {
			return cmd
		}
	} else if a.scrollMain(msg.String()) {
		return nil
	}
	if cmd := runAction(p.mainActions(tab), msg.String()); cmd != nil {
		return cmd
	}
	return runAction(p.actions(), msg.String())
}

// scrollMain moves a non-interactive tab; it reports whether the key was a scroll key.
func (a *App) scrollMain(key string) bool {
	k := [2]int{a.focus, a.tabs[a.focus]}
	page := max(a.height-6, 1)
	switch key {
	case "up", "k":
		a.scroll[k]--
	case "down", "j":
		a.scroll[k]++
	case "pgup", "ctrl+u":
		a.scroll[k] -= page
	case "pgdown", "ctrl+d":
		a.scroll[k] += page
	case "home", "g":
		a.scroll[k] = 0
	case "end", "G":
		a.scroll[k] = 1 << 30
	default:
		return false
	}
	a.scroll[k] = max(a.scroll[k], 0)
	return true
}

func runAction(acts []action, key string) tea.Cmd {
	for _, ac := range acts {
		if ac.key == key {
			return ac.run()
		}
	}
	return nil
}

func (a *App) contextActions() []action {
	p := a.cur()
	var acts []action
	if a.inMain {
		acts = append(acts, p.mainActions(a.tabs[a.focus])...)
	}
	return append(acts, p.actions()...)
}

func (a *App) focusPanel(idx int) tea.Cmd {
	a.inMain = false
	if idx == a.focus {
		return nil
	}
	a.focus = idx
	a.sess.activePanel = a.panels[idx].title()
	return a.panels[idx].activate()
}

func (a *App) switchTab(delta int) tea.Cmd {
	n := len(a.cur().tabs())
	if n == 0 {
		return nil
	}
	a.tabs[a.focus] = (a.tabs[a.focus] + delta + n) % n
	return nil
}

func (a *App) handleClick(m tea.Mouse) tea.Cmd {
	if a.popupOpen() || m.Button != tea.MouseLeft {
		return nil
	}
	if m.X >= a.sideW {
		a.inMain = true
		return nil
	}
	for i, g := range a.geoms {
		if m.Y < g.y0 || m.Y >= g.y1 {
			continue
		}
		cmd := a.focusPanel(i)
		if row := g.offset + m.Y - g.rowStart; m.Y >= g.rowStart && row < len(a.panels[i].rows(a.sideW)) {
			a.panels[i].list().cursor = row
		}
		return cmd
	}
	return nil
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
	if a.width < 40 || a.height < 12 {
		return "终端太小，至少需要 40×12"
	}
	bodyH := a.height - 1
	mode := a.mode
	if a.width < 80 {
		mode = modeFull // narrow: one area at a time
	}

	var body string
	switch {
	case mode == modeFull && !a.inMain:
		a.sideW = a.width
		body = a.renderSide(a.width, bodyH, true)
	case mode == modeFull:
		a.sideW = 0
		a.geoms = nil
		body = a.renderMain(a.width, bodyH)
	default:
		frac := 3
		if mode == modeHalf {
			frac = 5
		}
		a.sideW = max(min(a.width/frac, 50), 26)
		side := a.renderSide(a.sideW, bodyH, false)
		body = joinColumns(side, a.renderMain(a.width-a.sideW, bodyH))
	}
	screen := body + "\n" + a.renderStatusBar()

	switch {
	case a.confirm != nil:
		screen = overlay(screen, renderConfirm(a.confirm.prompt), a.width, a.height)
	case a.form != nil:
		screen = overlay(screen, popupBox(a.form.view(min(a.width-10, 80)), colorAccent), a.width, a.height)
	case a.menu != nil:
		screen = overlay(screen, popupBox(a.menu.view(), colorAccent), a.width, a.height)
	case a.help:
		screen = overlay(screen, popupBox(a.renderHelp(), colorAccent), a.width, a.height)
	}
	return screen
}

func joinColumns(left, right string) string {
	l, r := strings.Split(left, "\n"), strings.Split(right, "\n")
	out := make([]string, max(len(l), len(r)))
	for i := range out {
		if i < len(l) {
			out[i] = l[i]
		}
		if i < len(r) {
			out[i] += r[i]
		}
	}
	return strings.Join(out, "\n")
}

// sideHeights gives the focused panel the room; the others shrink to at most u rows.
func (a *App) sideHeights(h int, rowCounts []int, onlyFocused bool) []int {
	heights := make([]int, len(a.panels))
	if onlyFocused {
		heights[a.focus] = h
		return heights
	}
	for u := 3; u >= 0; u-- {
		used := 0
		for i, n := range rowCounts {
			if i == a.focus {
				continue
			}
			if u == 0 {
				heights[i] = 1 // title line only
			} else {
				heights[i] = 2 + max(min(n, u), 1)
			}
			used += heights[i]
		}
		if h-used >= 5 || u == 0 {
			heights[a.focus] = max(h-used, 1)
			return heights
		}
	}
	return heights
}

func (a *App) panelTitle(i int, p panel) string {
	t := fmt.Sprintf("[%d]-%s", i+1, p.title())
	if b := p.badge(); b != "" {
		t += " " + b
	}
	if f := p.list().filter.value(); f != "" && !p.list().filter.active {
		t += styleAccent.Render(" /" + f)
	}
	return t
}

func (a *App) renderSide(w, h int, onlyFocused bool) string {
	rowsByPanel := make([][]string, len(a.panels))
	counts := make([]int, len(a.panels))
	for i, p := range a.panels {
		rowsByPanel[i] = p.rows(w - 2)
		counts[i] = len(rowsByPanel[i])
	}
	// The rate box stays pinned under the panels; it shrinks to one line on short terminals.
	rateH := 0
	switch {
	case h >= 20:
		rateH = 4
	case h >= 14:
		rateH = 3
	}
	heights := a.sideHeights(h-rateH, counts, onlyFocused)
	a.geoms = make([]panelGeom, len(a.panels))
	var boxes []string
	y := 0
	for i, p := range a.panels {
		hh := heights[i]
		if hh == 0 {
			continue
		}
		focused := i == a.focus && !a.inMain
		ls := p.list()
		rows := rowsByPanel[i]
		ls.clamp(len(rows))
		bodyH := max(hh-2, 0)
		if ls.filter.active && i == a.focus {
			ls.filter.input.SetWidth(max(w-6, 8))
			bodyH--
		}
		start, end := ls.window(bodyH, len(rows))
		body := make([]string, 0, bodyH+1)
		if ls.filter.active && i == a.focus {
			body = append(body, ls.filter.input.View())
		}
		for r := start; r < end; r++ {
			line := rows[r]
			if r == ls.cursor && i == a.focus && !isSummary(p) {
				if focused {
					line = styleSelected.Render(padRight(ansi.Strip(line), w-2))
				} else {
					line = styleBold.Render(ansi.Strip(line))
				}
			}
			body = append(body, line)
		}
		if len(rows) == 0 && bodyH > 0 {
			body = append(body, styleDim.Render("（空）"))
		}
		a.geoms[i] = panelGeom{y0: y, y1: y + hh, rowStart: y + 1, offset: start}
		boxes = append(boxes, drawBox(a.panelTitle(i, p), body, w, hh, focused))
		y += hh
	}
	side := fitBlock(strings.Join(boxes, "\n"), w, h-rateH)
	if rateH > 0 {
		side += "\n" + a.renderRateBox(w, rateH)
	}
	return side
}

// renderRateBox is the always-visible traffic summary at the bottom of the left column.
func (a *App) renderRateBox(w, h int) string {
	s := a.sess
	inner := w - 2
	var body []string
	switch {
	case !s.connected:
		body = []string{styleDim.Render("未连接到内核")}
	case h >= 4:
		graphW := max(inner-13, 0)
		body = []string{
			styleOK.Render("↓ "+padLeft(formatSpeed(s.traffic.Down), 10)) + " " + styleOK.Render(sparkline(s.downHistory, graphW)),
			styleAccent.Render("↑ "+padLeft(formatSpeed(s.traffic.Up), 10)) + " " + styleAccent.Render(sparkline(s.upHistory, graphW)),
		}
	default:
		body = []string{styleOK.Render("↓ "+formatSpeed(s.traffic.Down)) + "  " + styleAccent.Render("↑ "+formatSpeed(s.traffic.Up))}
	}
	return drawBox(styleDim.Render("速率"), body, w, h, false)
}

func (a *App) renderMain(w, h int) string {
	p := a.cur()
	tab := a.tabs[a.focus]
	names := p.tabs()
	if tab >= len(names) {
		tab = 0
	}
	innerW, innerH := w-2, h-2
	content := p.renderMain(tab, innerW, innerH, a.inMain)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if !p.interactive(tab) && len(lines) > innerH {
		k := [2]int{a.focus, tab}
		a.scroll[k] = max(0, min(a.scroll[k], len(lines)-innerH))
		lines = lines[a.scroll[k]:]
	}
	return drawBox(tabStrip(names, tab, a.inMain), lines, w, h, a.inMain)
}

func (a *App) renderStatusBar() string {
	hints := []hint{}
	if a.inMain {
		hints = append(hints, hint{"esc", "返回"})
	}
	for _, ac := range a.contextActions() {
		hints = append(hints, hint{ac.key, ac.desc})
	}
	hints = shortHints(hints, 5)
	if len(a.cur().tabs()) > 1 {
		hints = append(hints, hint{"[ ]", "标签"})
	}
	hints = append(hints, hint{"x", "菜单"}, hint{"?", "帮助"})
	left := renderHints(hints)

	var right string
	switch {
	case a.note != nil && a.note.isErr:
		right = styleBad.Render(a.note.text)
	case a.note != nil:
		right = styleAccent.Render(a.note.text)
	case len(a.sess.busy) > 0:
		right = styleWarn.Render(spinnerFrames[a.frame%len(spinnerFrames)] + " " + busySummary(a.sess.busy))
	}
	leftW, rightW := ansi.StringWidth(left), ansi.StringWidth(right)
	if leftW+rightW+2 > a.width {
		// Notifications win over hints on narrow terminals.
		left = ansi.Truncate(left, max(a.width-rightW-2, 0), "…")
		leftW = ansi.StringWidth(left)
		if leftW+rightW+2 > a.width {
			right = ansi.Truncate(right, max(a.width-leftW-2, 0), "…")
			rightW = ansi.StringWidth(right)
		}
	}
	return left + strings.Repeat(" ", max(a.width-leftW-rightW, 1)) + right
}

func (a *App) renderHelp() string {
	keyW := 18
	lines := []string{styleTitle.Render("快捷键 · " + a.cur().title()), ""}
	add := func(k, d string) { lines = append(lines, "  "+styleKey.Render(padRight(k, keyW))+d) }
	for _, ac := range a.contextActions() {
		add(ac.key, ac.desc)
	}
	lines = append(lines, "", styleTitle.Render("全局"), "")
	for _, g := range []hint{
		{"←/→ h/l tab", "切换左侧面板"},
		{fmt.Sprintf("1-%d", len(a.panels)), "直接聚焦面板"},
		{"j/k ↑/↓ g/G", "移动 / 首尾"},
		{"enter / esc", "进入主面板 / 返回"},
		{"[ ]", "切换主面板标签"},
		{"/", "过滤当前列表"},
		{"x", "当前可用操作菜单"},
		{"+ / _", "屏幕模式：普通 / 主面板加宽 / 全屏"},
		{"鼠标", "点击面板或条目，滚轮滚动"},
		{"q / ctrl+c", "退出（不影响内核运行）"},
	} {
		add(g.key, g.desc)
	}
	lines = append(lines, "", styleDim.Render("按任意键关闭"))
	return strings.Join(lines, "\n")
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
