package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

// Refresh while visible every N status ticks; /proxies can be large on big subscriptions.
const proxiesRefreshTicks = 3

type proxiesMsg struct {
	proxies map[string]mihomoapi.Proxy
	err     error
}

type delayDoneMsg struct {
	key string // group name, or group+"\x00"+proxy
	err error
}

type proxyRow struct {
	group  string
	member string // empty for a group header
}

type proxiesPage struct {
	sess    *session
	proxies map[string]mihomoapi.Proxy
	loaded  bool
	loadErr error

	rows      []proxyRow
	cursor    int
	offset    int
	collapsed map[string]bool
	sort      proxySort
	testing   map[string]bool
	ticks     int

	filter    textinput.Model
	filtering bool
}

func newProxiesPage(sess *session) *proxiesPage {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "过滤节点名"
	return &proxiesPage{sess: sess, collapsed: map[string]bool{}, testing: map[string]bool{}, filter: ti}
}

func (p *proxiesPage) title() string        { return "代理" }
func (p *proxiesPage) capturingInput() bool { return p.filtering }

func (p *proxiesPage) activate() tea.Cmd {
	p.ticks = 0
	return p.fetch()
}

func (p *proxiesPage) hints() []hint {
	return []hint{
		{"enter", "选择节点 / 展开收起"},
		{"t", "测速（组或单个节点）"},
		{"/", "过滤"},
		{"s", "排序：" + p.sort.label()},
		{"←/→", "收起 / 展开当前组"},
		{"e", "全部展开 / 收起"},
		{"u", "取消固定（URLTest/Fallback）"},
		{"r", "刷新"},
	}
}

func (p *proxiesPage) fetch() tea.Cmd {
	c := p.sess.client
	return func() tea.Msg {
		proxies, err := c.Proxies(context.Background())
		return proxiesMsg{proxies: proxies, err: err}
	}
}

func (p *proxiesPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case proxiesMsg:
		p.loadErr = msg.err
		if msg.err == nil {
			p.proxies, p.loaded = msg.proxies, true
			p.rebuild()
		}
		return nil
	case delayDoneMsg:
		delete(p.testing, msg.key)
		if msg.err != nil {
			return tea.Batch(notifyErr("测速失败", msg.err), p.fetch())
		}
		return p.fetch()
	case statusTickMsg:
		p.ticks++
		if p.ticks%proxiesRefreshTicks == 0 {
			return p.fetch()
		}
		return nil
	case tea.KeyPressMsg:
		if p.filtering {
			return p.updateFilter(msg)
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *proxiesPage) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.filter.SetValue("")
		fallthrough
	case "enter":
		p.filtering = false
		p.filter.Blur()
		p.rebuild()
		return nil
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	p.rebuild()
	return cmd
}

func (p *proxiesPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		p.move(-1)
	case "down", "j":
		p.move(1)
	case "pgup", "ctrl+u":
		p.move(-10)
	case "pgdown", "ctrl+d":
		p.move(10)
	case "home", "g":
		p.move(-len(p.rows))
	case "end", "G":
		p.move(len(p.rows))
	case "/":
		p.filtering = true
		return p.filter.Focus()
	case "esc":
		if p.filter.Value() != "" {
			p.filter.SetValue("")
			p.rebuild()
		}
	case "s":
		p.sort = (p.sort + 1) % 3
		p.rebuild()
		return notify("排序：" + p.sort.label())
	case "r":
		return p.fetch()
	case "e":
		p.toggleAll()
	case "left", "h":
		if row, ok := p.current(); ok {
			p.collapsed[row.group] = true
			p.rebuild()
			p.focus(proxyRow{group: row.group})
		}
	case "right", "l":
		if row, ok := p.current(); ok && row.member == "" {
			delete(p.collapsed, row.group)
			p.rebuild()
		}
	case "enter", "space":
		return p.activateRow()
	case "t":
		return p.testCurrent()
	case "u":
		return p.unfixCurrent()
	}
	return nil
}

func (p *proxiesPage) current() (proxyRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return proxyRow{}, false
	}
	return p.rows[p.cursor], true
}

func (p *proxiesPage) move(delta int) {
	p.cursor = max(0, min(p.cursor+delta, len(p.rows)-1))
}

func (p *proxiesPage) focus(target proxyRow) {
	for i, r := range p.rows {
		if r == target {
			p.cursor = i
			return
		}
	}
}

func (p *proxiesPage) visibleGroups() []mihomoapi.Proxy {
	return orderedGroups(p.proxies, p.sess.config.Mode == "global")
}

// rebuild recomputes rows, keeping the cursor on the same row when it still exists.
func (p *proxiesPage) rebuild() {
	prev, hadPrev := p.current()
	filter := p.filter.Value()
	p.rows = p.rows[:0]
	for _, g := range p.visibleGroups() {
		members := sortedMembers(g, p.proxies, p.sort, filter)
		if filter != "" && len(members) == 0 {
			continue
		}
		p.rows = append(p.rows, proxyRow{group: g.Name})
		// An active filter expands every group so matches are visible.
		if filter == "" && p.collapsed[g.Name] {
			continue
		}
		for _, m := range members {
			p.rows = append(p.rows, proxyRow{group: g.Name, member: m})
		}
	}
	if hadPrev {
		p.focus(prev)
	}
	p.move(0)
}

func (p *proxiesPage) toggleAll() {
	groups := p.visibleGroups()
	anyExpanded := false
	for _, g := range groups {
		if !p.collapsed[g.Name] {
			anyExpanded = true
			break
		}
	}
	for _, g := range groups {
		if anyExpanded {
			p.collapsed[g.Name] = true
		} else {
			delete(p.collapsed, g.Name)
		}
	}
	row, _ := p.current()
	p.rebuild()
	p.focus(proxyRow{group: row.group})
}

func (p *proxiesPage) activateRow() tea.Cmd {
	row, ok := p.current()
	if !ok {
		return nil
	}
	if row.member == "" {
		p.collapsed[row.group] = !p.collapsed[row.group]
		p.rebuild()
		return nil
	}
	group := p.proxies[row.group]
	if !canSelect(group.Type) {
		return notify(group.Type + " 类型的组不能手动选择节点")
	}
	if group.Now == row.member && group.Fixed == "" {
		return nil
	}
	c, groupName, member := p.sess.client, row.group, row.member
	return apiCall("切换节点失败", groupName+" → "+member, func(ctx context.Context) error {
		return c.SelectProxy(ctx, groupName, member)
	}, p.fetch())
}

func (p *proxiesPage) delayTestFor(group string) mihomoapi.DelayTest {
	t := p.sess.delayTest
	if g, ok := p.proxies[group]; ok && g.TestURL != "" {
		t.URL = g.TestURL
	}
	return t
}

func (p *proxiesPage) testCurrent() tea.Cmd {
	row, ok := p.current()
	if !ok {
		return nil
	}
	c, t := p.sess.client, p.delayTestFor(row.group)
	if row.member == "" {
		key := row.group
		if p.testing[key] {
			return nil
		}
		p.testing[key] = true
		return func() tea.Msg {
			_, err := c.GroupDelay(context.Background(), key, t)
			return delayDoneMsg{key: key, err: err}
		}
	}
	key := row.group + "\x00" + row.member
	if p.testing[key] {
		return nil
	}
	p.testing[key] = true
	member := row.member
	return func() tea.Msg {
		_, err := c.ProxyDelay(context.Background(), member, t)
		// A failed probe is an expected result (shown as 超时), not an error worth a toast.
		var apiErr *mihomoapi.APIError
		if errors.As(err, &apiErr) {
			err = nil
		}
		return delayDoneMsg{key: key, err: err}
	}
}

func (p *proxiesPage) unfixCurrent() tea.Cmd {
	row, ok := p.current()
	if !ok {
		return nil
	}
	group := p.proxies[row.group]
	if group.Fixed == "" {
		return notify("该组没有固定的节点")
	}
	c := p.sess.client
	return apiCall("取消固定失败", "已取消固定，恢复自动选择", func(ctx context.Context) error {
		return c.UnfixProxy(ctx, group.Name)
	}, p.fetch())
}

func (p *proxiesPage) view(w, h int) string {
	header := styleTitle.Render("代理组") + styleDim.Render("  排序："+p.sort.label())
	if p.filtering {
		p.filter.SetWidth(max(w-4, 10))
		header += "\n" + p.filter.View()
	} else if v := p.filter.Value(); v != "" {
		header += "\n" + styleAccent.Render("过滤: "+v) + styleDim.Render("  (esc 清除)")
	} else {
		header += "\n"
	}
	listH := h - 3
	if !p.loaded {
		if p.loadErr != nil {
			return header + "\n\n" + styleBad.Render("加载失败: "+p.loadErr.Error())
		}
		return header + "\n\n" + styleDim.Render("加载中…")
	}
	if len(p.rows) == 0 {
		return header + "\n\n" + styleDim.Render("没有匹配的代理组")
	}

	// Keep the cursor inside the visible window.
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listH {
		p.offset = p.cursor - listH + 1
	}
	p.offset = max(0, min(p.offset, len(p.rows)-listH))

	nameW := 8
	for _, r := range p.rows {
		nameW = max(nameW, ansi.StringWidth(r.member))
	}
	nameW = min(nameW, 48, max(w-30, 8))
	lines := make([]string, 0, listH)
	for i := p.offset; i < len(p.rows) && len(lines) < listH; i++ {
		line := p.renderRow(p.rows[i], nameW)
		if i == p.cursor {
			line = styleSelected.Render(padRight(ansi.Strip(line), w))
		}
		lines = append(lines, line)
	}
	scroll := ""
	if len(p.rows) > listH {
		scroll = styleDim.Render(fmt.Sprintf("  %d/%d", p.cursor+1, len(p.rows)))
	}
	return header + scroll + "\n\n" + strings.Join(lines, "\n")
}

func (p *proxiesPage) renderRow(row proxyRow, nameW int) string {
	if row.member == "" {
		g := p.proxies[row.group]
		arrow := "▼"
		if p.collapsed[row.group] && p.filter.Value() == "" {
			arrow = "▶"
		}
		line := arrow + " " + styleBold.Render(g.Name) + "  " + styleDim.Render(g.Type) + "  → " + styleAccent.Render(g.Now)
		if g.Fixed != "" {
			line += styleWarn.Render(" (已固定)")
		}
		line += styleDim.Render(fmt.Sprintf("  %d 个", len(g.All)))
		if p.testing[row.group] {
			line += styleWarn.Render("  测速中…")
		}
		return line
	}

	group, member := p.proxies[row.group], p.proxies[row.member]
	mark := "○"
	if group.Now == row.member {
		mark = styleAccent.Render("●")
	}
	name := padRight(ansi.Truncate(row.member, nameW, "…"), nameW)
	kind := padRight(member.Type, 12)
	udp := "   "
	if member.UDP {
		udp = "UDP"
	}
	var delay string
	switch d, tested := member.LastDelay(); {
	case p.testing[row.group+"\x00"+row.member] || p.testing[row.group]:
		delay = styleWarn.Render("测速中")
	case !tested:
		delay = styleDim.Render("--")
	case d <= 0:
		delay = styleDim.Render("超时")
	default:
		delay = delayStyle(d).Render(fmt.Sprintf("%dms", d))
	}
	return "    " + mark + " " + name + " " + styleDim.Render(kind+" "+udp) + "  " + delay
}
