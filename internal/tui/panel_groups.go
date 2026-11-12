package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

// Re-fetch every N status messages while focused; /proxies can be large on big subscriptions.
const groupsRefreshEvery = 5

// autoTestAfter is how stale a group's delays may get before focusing it tests them again.
const autoTestAfter = 10 * time.Minute

const (
	groupsTabNodes = iota
	groupsTabInfo
)

type proxiesMsg struct {
	proxies map[string]mihomoapi.Proxy
	err     error
}

type delayDoneMsg struct {
	key string // group name, or group+"\x00"+proxy
	err error
}

// groupsPanel lists proxy groups; the main panel lists the selected group's nodes.
type groupsPanel struct {
	panelBase
	sess    *session
	proxies map[string]mihomoapi.Proxy
	loaded  bool
	loadErr error
	sort    proxySort
	testing map[string]bool
	ticks   int
	nodes   cursorList
	// nodesFor is the group the node cursor belongs to; switching groups resets it.
	nodesFor string
	// lastTested is when each group was last delay-tested (manually or automatically).
	lastTested map[string]time.Time
	// gridRows is how many rows one grid column had at the last render (for page keys).
	gridRows int
}

func newGroupsPanel(sess *session) *groupsPanel {
	return &groupsPanel{sess: sess, testing: map[string]bool{}, lastTested: map[string]time.Time{}}
}

func (p *groupsPanel) title() string            { return "代理组" }
func (p *groupsPanel) tabs() []string           { return []string{"节点", "信息"} }
func (p *groupsPanel) filterHint() string       { return "过滤节点名" }
func (p *groupsPanel) activate() tea.Cmd        { return p.fetch() }
func (p *groupsPanel) interactive(tab int) bool { return tab == groupsTabNodes }

func (p *groupsPanel) fetch() tea.Cmd {
	c := p.sess.client
	return func() tea.Msg {
		proxies, err := c.Proxies(context.Background())
		return proxiesMsg{proxies: proxies, err: err}
	}
}

// groups are the visible groups; an active filter keeps only groups with matching nodes.
func (p *groupsPanel) groups() []mihomoapi.Proxy {
	all := orderedGroups(p.proxies, p.sess.config.Mode == "global")
	f := p.ls.filter.value()
	if f == "" {
		return all
	}
	var out []mihomoapi.Proxy
	for _, g := range all {
		if len(sortedMembers(g, p.proxies, p.sort, f)) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func (p *groupsPanel) current() (mihomoapi.Proxy, bool) {
	gs := p.groups()
	if c := p.ls.cursor; c >= 0 && c < len(gs) {
		return gs[c], true
	}
	return mihomoapi.Proxy{}, false
}

func (p *groupsPanel) members(g mihomoapi.Proxy) []string {
	return sortedMembers(g, p.proxies, p.sort, p.ls.filter.value())
}

func (p *groupsPanel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case proxiesMsg:
		p.loadErr = msg.err
		if msg.err == nil {
			p.proxies, p.loaded = msg.proxies, true
		}
	case delayDoneMsg:
		delete(p.testing, msg.key)
		if msg.err != nil {
			return tea.Batch(notifyErr("测速失败", msg.err), p.fetch())
		}
		return p.fetch()
	case statusMsg:
		if p.sess.activePanel == p.title() {
			p.ticks++
			var cmds []tea.Cmd
			if p.ticks%groupsRefreshEvery == 0 {
				cmds = append(cmds, p.fetch())
			}
			// Checked every tick, so only a group the cursor rests on gets tested.
			if g, ok := p.current(); ok && p.autoTestOn() && time.Since(p.lastTested[g.Name]) > autoTestAfter {
				cmds = append(cmds, p.testGroup(g.Name))
			}
			return tea.Batch(cmds...)
		}
	}
	return nil
}

func (p *groupsPanel) rows(w int) []string {
	gs := p.groups()
	nameW := max(w/2, 6)
	out := make([]string, len(gs))
	for i, g := range gs {
		now := g.Now
		if g.Fixed != "" {
			now += "📌"
		}
		line := padRight(ansi.Truncate(g.Name, nameW, "…"), nameW) + " " + styleDim.Render("→ ") + styleAccent.Render(now)
		if p.testing[g.Name] {
			line += styleWarn.Render(" …")
		}
		out[i] = line
	}
	return out
}

func (p *groupsPanel) delayTestFor(group string) mihomoapi.DelayTest {
	t := p.sess.delayTest
	if g, ok := p.proxies[group]; ok && g.TestURL != "" {
		t.URL = g.TestURL
	}
	return t
}

func (p *groupsPanel) testGroup(name string) tea.Cmd {
	if p.testing[name] {
		return nil
	}
	p.testing[name] = true
	p.lastTested[name] = time.Now()
	c, t := p.sess.client, p.delayTestFor(name)
	return func() tea.Msg {
		_, err := c.GroupDelay(context.Background(), name, t)
		return delayDoneMsg{key: name, err: err}
	}
}

func (p *groupsPanel) actions() []action {
	acts := []action{
		{"T", "全部代理组测速", func() tea.Cmd {
			var cmds []tea.Cmd
			for _, g := range p.groups() {
				cmds = append(cmds, p.testGroup(g.Name))
			}
			return tea.Batch(cmds...)
		}},
		{"o", "排序：" + p.sort.label(), func() tea.Cmd {
			p.sort = (p.sort + 1) % 3
			return notify("节点排序：" + p.sort.label())
		}},
		{"P", "更新全部代理集", p.updateProviders},
		{"r", "刷新", p.fetch},
	}
	g, ok := p.current()
	if !ok {
		return acts
	}
	return append([]action{
		{"t", "整组测速", func() tea.Cmd { return p.testGroup(g.Name) }},
		{"u", "取消固定", func() tea.Cmd {
			if g.Fixed == "" {
				return notify("该组没有固定的节点")
			}
			c := p.sess.client
			return apiCall("取消固定失败", "已取消固定，恢复自动选择", func(ctx context.Context) error {
				return c.UnfixProxy(ctx, g.Name)
			}, p.fetch())
		}},
	}, acts...)
}

// mainActions are node-level actions in the 节点 tab.
func (p *groupsPanel) mainActions(tab int) []action {
	if tab != groupsTabNodes {
		return nil
	}
	return []action{
		{"enter", "切换到此节点", p.selectNode},
		{"t", "测试此节点", p.testNode},
	}
}

func (p *groupsPanel) currentNode() (mihomoapi.Proxy, string, bool) {
	g, ok := p.current()
	if !ok {
		return g, "", false
	}
	ms := p.members(g)
	if c := p.nodes.cursor; c >= 0 && c < len(ms) {
		return g, ms[c], true
	}
	return g, "", false
}

func (p *groupsPanel) selectNode() tea.Cmd {
	g, node, ok := p.currentNode()
	if !ok {
		return nil
	}
	if !canSelect(g.Type) {
		return notify(g.Type + " 类型的组不能手动选择节点")
	}
	if g.Now == node && g.Fixed == "" {
		return nil
	}
	c := p.sess.client
	return apiCall("切换节点失败", g.Name+" → "+node, func(ctx context.Context) error {
		return c.SelectProxy(ctx, g.Name, node)
	}, p.fetch())
}

func (p *groupsPanel) testNode() tea.Cmd {
	g, node, ok := p.currentNode()
	if !ok {
		return nil
	}
	key := g.Name + "\x00" + node
	if p.testing[key] {
		return nil
	}
	p.testing[key] = true
	c, t := p.sess.client, p.delayTestFor(g.Name)
	return func() tea.Msg {
		_, err := c.ProxyDelay(context.Background(), node, t)
		// A failed probe is an expected result (shown as 超时), not an error worth a toast.
		var apiErr *mihomoapi.APIError
		if errors.As(err, &apiErr) {
			err = nil
		}
		return delayDoneMsg{key: key, err: err}
	}
}

func (p *groupsPanel) mainKey(tab int, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if tab != groupsTabNodes {
		return nil, false
	}
	g, ok := p.current()
	if !ok {
		return nil, false
	}
	n := len(p.members(g))
	// In the grid, page keys jump a whole column.
	switch msg.String() {
	case "pgdown", "ctrl+d":
		p.nodes.move(max(p.gridRows, 1), n)
		return nil, true
	case "pgup", "ctrl+u":
		p.nodes.move(-max(p.gridRows, 1), n)
		return nil, true
	}
	return nil, p.nodes.key(msg, n)
}

func (p *groupsPanel) autoTestOn() bool {
	return p.sess.ws == nil || !p.sess.ws.Settings().AutoDelayTestOff
}

func (p *groupsPanel) updateProviders() tea.Cmd {
	c := p.sess.client
	return p.sess.job("proxy-providers", "更新代理集", func(ctx context.Context) (string, error) {
		provs, err := c.ProxyProviders(ctx)
		if err != nil {
			return "", err
		}
		n := 0
		for name, pr := range provs {
			// "default" and inline providers have nothing to fetch.
			if pr.VehicleType != "HTTP" && pr.VehicleType != "File" {
				continue
			}
			if err := c.UpdateProxyProvider(ctx, name); err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			n++
		}
		if n == 0 {
			return "没有需要更新的代理集", nil
		}
		return fmt.Sprintf("已更新 %d 个代理集", n), nil
	}, p.fetch())
}

func (p *groupsPanel) renderMain(tab, w, h int, focused bool) string {
	if !p.loaded {
		if p.loadErr != nil {
			return styleBad.Render("加载失败: " + p.loadErr.Error())
		}
		return styleDim.Render("加载中…")
	}
	g, ok := p.current()
	if !ok {
		return styleDim.Render("没有代理组")
	}
	if g.Name != p.nodesFor {
		// Start on the group's current node.
		p.nodesFor, p.nodes = g.Name, cursorList{}
		for i, m := range p.members(g) {
			if m == g.Now {
				p.nodes.cursor = i
			}
		}
	}
	if tab == groupsTabInfo {
		return p.groupInfo(g)
	}

	header := styleBold.Render(g.Name) + styleDim.Render(fmt.Sprintf("  %s · %d 个节点 · 排序 %s", g.Type, len(g.All), p.sort.label()))
	if !focused {
		header += styleDim.Render("  · enter 进入后选择节点")
	}
	members := p.members(g)
	nameW := 8
	for _, m := range members {
		nameW = max(nameW, ansi.StringWidth(m))
	}
	bodyH := max(h-2, 1)
	// Grid cells: mark, name, delay. Columns as wide as the longest name (capped at 24).
	gridName := min(nameW, 24)
	cellW := gridName + 10
	cols := (w + 2) / (cellW + 2)
	if cols < 2 || len(members) <= bodyH {
		// One column fits everything (or there's no room for two): keep type and UDP.
		p.gridRows = bodyH
		nameW = min(nameW, 40, max(w-30, 8))
		rows := make([]string, len(members))
		for i, m := range members {
			rows[i] = p.nodeRow(g, m, nameW)
		}
		return header + "\n\n" + strings.Join(p.nodes.renderRows(rows, w, bodyH, focused), "\n")
	}
	return header + p.renderGrid(g, members, gridName, cellW, cols, bodyH, focused)
}

// renderGrid lays nodes out column by column, a page at a time, so ↑/↓ walk the list in
// order and ←/→ stay free for switching tabs.
func (p *groupsPanel) renderGrid(g mihomoapi.Proxy, members []string, nameW, cellW, cols, rows int, focused bool) string {
	p.gridRows = rows
	p.nodes.clamp(len(members))
	perPage := rows * cols
	page := p.nodes.cursor / perPage
	start := page * perPage
	end := min(start+perPage, len(members))
	info := ""
	if pages := (len(members) + perPage - 1) / perPage; pages > 1 {
		info = styleDim.Render(fmt.Sprintf("  第 %d/%d 页", page+1, pages))
	}
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var cells []string
		for c := 0; c < cols; c++ {
			i := start + c*rows + r
			if i >= end {
				break
			}
			cell := p.nodeCell(g, members[i], nameW)
			if i == p.nodes.cursor && focused {
				cell = styleSelected.Render(padRight(ansi.Strip(cell), cellW))
			} else {
				cell = padRight(cell, cellW)
			}
			cells = append(cells, cell)
		}
		lines[r] = strings.Join(cells, "  ")
	}
	return info + "\n\n" + strings.Join(lines, "\n")
}

// nodeCell is a compact grid entry: mark, name and delay.
func (p *groupsPanel) nodeCell(g mihomoapi.Proxy, name string, nameW int) string {
	mark := "○"
	if g.Now == name {
		mark = styleAccent.Render("●")
	}
	return mark + " " + padRight(ansi.Truncate(name, nameW, "…"), nameW) + " " + padLeft(p.delayLabel(g, name), 7)
}

func (p *groupsPanel) delayLabel(g mihomoapi.Proxy, name string) string {
	switch d, tested := p.proxies[name].LastDelay(); {
	case p.testing[g.Name+"\x00"+name] || p.testing[g.Name]:
		return styleWarn.Render("测速中")
	case !tested:
		return styleDim.Render("--")
	case d <= 0:
		return styleDim.Render("超时")
	default:
		return delayStyle(d).Render(fmt.Sprintf("%dms", d))
	}
}

func (p *groupsPanel) nodeRow(g mihomoapi.Proxy, name string, nameW int) string {
	node := p.proxies[name]
	mark := "○"
	if g.Now == name {
		mark = styleAccent.Render("●")
	}
	udp := "   "
	if node.UDP {
		udp = "UDP"
	}
	delay := p.delayLabel(g, name)
	return " " + mark + " " + padRight(ansi.Truncate(name, nameW, "…"), nameW) + "  " +
		styleDim.Render(padRight(node.Type, 12)+" "+udp) + "  " + delay
}

func (p *groupsPanel) groupInfo(g mihomoapi.Proxy) string {
	var b strings.Builder
	row := func(label, value string) { b.WriteString(styleDim.Render(padRight(label, 10)) + value + "\n") }
	row("名称", styleBold.Render(g.Name))
	row("类型", g.Type)
	row("当前", g.Now)
	if g.Fixed != "" {
		row("已固定", g.Fixed+styleDim.Render("（u 取消固定）"))
	}
	testURL := g.TestURL
	if testURL == "" {
		testURL = p.sess.delayTest.URL + styleDim.Render("（默认）")
	}
	row("测速地址", testURL)
	row("成员", fmt.Sprintf("%d 个", len(g.All)))
	if !canSelect(g.Type) {
		row("", styleDim.Render(g.Type+" 由内核自动选择，不能手动切换"))
	}
	return b.String()
}
