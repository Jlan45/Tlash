package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

const connPollInterval = time.Second

type connTickMsg struct{}

type connectionsMsg struct {
	snap mihomoapi.ConnectionsSnapshot
	at   time.Time
	err  error
}

type connSort int

const (
	connSortDownSpeed connSort = iota
	connSortUpSpeed
	connSortDownTotal
	connSortDuration
	connSortHost
)

func (s connSort) label() string {
	return [...]string{"下载速率", "上传速率", "下载总量", "时长", "主机"}[s]
}

// connRow is a connection plus speeds derived from the previous snapshot.
type connRow struct {
	conn      mihomoapi.Connection
	downSpeed int64
	upSpeed   int64
}

type connectionsPage struct {
	sess    *session
	rows    []connRow
	prev    map[string]mihomoapi.Connection
	prevAt  time.Time
	loadErr error
	polling bool
	paused  bool
	sort    connSort
	cursor  int
	offset  int
	selID   string

	filter filterInput
	viewer *textViewer
}

func newConnectionsPage(sess *session) *connectionsPage {
	return &connectionsPage{sess: sess, filter: newFilterInput("过滤主机 / 规则 / 节点 / 进程")}
}

func (p *connectionsPage) title() string        { return "连接" }
func (p *connectionsPage) capturingInput() bool { return p.filter.active || p.viewer != nil }

func (p *connectionsPage) activate() tea.Cmd {
	if p.polling {
		return nil
	}
	p.polling = true
	return p.fetch()
}

func (p *connectionsPage) hints() []hint {
	if p.viewer != nil {
		return p.viewer.hints()
	}
	pause := "暂停刷新"
	if p.paused {
		pause = "继续刷新"
	}
	return []hint{
		{"enter", "详情"},
		{"x", "关闭选中连接"},
		{"s", "排序：" + p.sort.label()},
		{"/", "过滤"},
		{"space", pause},
		{"X", "关闭全部连接"},
	}
}

func (p *connectionsPage) fetch() tea.Cmd {
	c := p.sess.client
	return func() tea.Msg {
		snap, err := c.Connections(context.Background())
		return connectionsMsg{snap: snap, at: time.Now(), err: err}
	}
}

func (p *connectionsPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case connectionsMsg:
		// Keep polling only while this page is on screen.
		next := tea.Tick(connPollInterval, func(time.Time) tea.Msg { return connTickMsg{} })
		p.loadErr = msg.err
		if msg.err == nil && !p.paused {
			p.ingest(msg.snap, msg.at)
		}
		return next
	case connTickMsg:
		if p.sess.activePage != p.title() {
			p.polling = false
			return nil
		}
		return p.fetch()
	case tea.KeyPressMsg:
		if p.viewer != nil {
			if p.viewer.update(msg) {
				p.viewer = nil
			}
			return nil
		}
		if p.filter.active {
			cmd := p.filter.update(msg)
			p.resort()
			return cmd
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *connectionsPage) ingest(snap mihomoapi.ConnectionsSnapshot, at time.Time) {
	elapsed := at.Sub(p.prevAt).Seconds()
	next := make(map[string]mihomoapi.Connection, len(snap.Connections))
	rows := make([]connRow, 0, len(snap.Connections))
	for _, c := range snap.Connections {
		next[c.ID] = c
		r := connRow{conn: c}
		if old, ok := p.prev[c.ID]; ok && elapsed > 0 {
			r.downSpeed = int64(float64(c.Download-old.Download) / elapsed)
			r.upSpeed = int64(float64(c.Upload-old.Upload) / elapsed)
		}
		rows = append(rows, r)
	}
	p.prev, p.prevAt, p.rows = next, at, rows
	p.resort()
}

func connHost(c mihomoapi.Connection) string {
	m := c.Metadata
	host := m.Host
	if host == "" {
		host = m.SniffHost
	}
	if host == "" {
		host = m.DestinationIP
	}
	return host + ":" + m.DestinationPort
}

func connChain(c mihomoapi.Connection) string {
	chain := slices.Clone(c.Chains)
	slices.Reverse(chain) // mihomo lists the final hop first
	return strings.Join(chain, " → ")
}

func connRule(c mihomoapi.Connection) string {
	if c.RulePayload != "" {
		return c.Rule + "," + c.RulePayload
	}
	return c.Rule
}

func (p *connectionsPage) visible() []connRow {
	f := strings.ToLower(p.filter.value())
	if f == "" {
		return p.rows
	}
	var out []connRow
	for _, r := range p.rows {
		hay := strings.ToLower(connHost(r.conn) + " " + connRule(r.conn) + " " + connChain(r.conn) + " " + r.conn.Metadata.Process)
		if strings.Contains(hay, f) {
			out = append(out, r)
		}
	}
	return out
}

func (p *connectionsPage) resort() {
	slices.SortStableFunc(p.rows, func(a, b connRow) int {
		switch p.sort {
		case connSortUpSpeed:
			return cmp.Or(cmp.Compare(b.upSpeed, a.upSpeed), cmp.Compare(a.conn.ID, b.conn.ID))
		case connSortDownTotal:
			return cmp.Or(cmp.Compare(b.conn.Download, a.conn.Download), cmp.Compare(a.conn.ID, b.conn.ID))
		case connSortDuration:
			return a.conn.Start.Compare(b.conn.Start)
		case connSortHost:
			return cmp.Or(strings.Compare(connHost(a.conn), connHost(b.conn)), cmp.Compare(a.conn.ID, b.conn.ID))
		default:
			return cmp.Or(cmp.Compare(b.downSpeed, a.downSpeed), cmp.Compare(b.conn.Download, a.conn.Download), cmp.Compare(a.conn.ID, b.conn.ID))
		}
	})
	// Follow the selected connection across refreshes.
	vis := p.visible()
	p.cursor = max(0, min(p.cursor, len(vis)-1))
	for i, r := range vis {
		if r.conn.ID == p.selID {
			p.cursor = i
			return
		}
	}
	if len(vis) > 0 {
		p.selID = vis[p.cursor].conn.ID
	}
}

func (p *connectionsPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	vis := p.visible()
	move := func(d int) {
		p.cursor = max(0, min(p.cursor+d, len(vis)-1))
		if len(vis) > 0 {
			p.selID = vis[p.cursor].conn.ID
		}
	}
	c := p.sess.client
	switch msg.String() {
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup", "ctrl+u":
		move(-10)
	case "pgdown", "ctrl+d":
		move(10)
	case "home", "g":
		move(-len(vis))
	case "end", "G":
		move(len(vis))
	case "s":
		p.sort = (p.sort + 1) % 5
		p.resort()
		return notify("排序：" + p.sort.label())
	case "space":
		p.paused = !p.paused
	case "/":
		return p.filter.start()
	case "esc":
		p.filter.clear()
		p.resort()
	case "X":
		return askConfirm(fmt.Sprintf("关闭全部 %d 个连接？", len(p.rows)), func() tea.Cmd {
			return apiCall("关闭连接失败", "已关闭所有连接", func(ctx context.Context) error { return c.CloseAllConnections(ctx) }, p.fetch())
		})
	}
	if p.cursor >= len(vis) || len(vis) == 0 {
		return nil
	}
	cur := vis[p.cursor].conn
	switch msg.String() {
	case "x":
		return apiCall("关闭连接失败", "已关闭 "+connHost(cur), func(ctx context.Context) error { return c.CloseConnection(ctx, cur.ID) }, p.fetch())
	case "enter":
		p.viewer = newTextViewer("连接详情", connDetail(cur))
	}
	return nil
}

func connDetail(c mihomoapi.Connection) string {
	m := c.Metadata
	var b strings.Builder
	row := func(k, v string) {
		if v != "" {
			b.WriteString(styleDim.Render(padRight(k, 10)) + v + "\n")
		}
	}
	row("主机", m.Host)
	row("嗅探域名", m.SniffHost)
	row("目标", m.DestinationIP+":"+m.DestinationPort)
	row("远端地址", m.RemoteDest)
	row("来源", m.SourceIP+":"+m.SourcePort)
	row("入站", m.InboundName+" ("+m.Type+")")
	row("网络", m.Network)
	row("进程", m.Process)
	row("规则", connRule(c))
	row("链路", connChain(c))
	row("流量", fmt.Sprintf("↓ %s  ↑ %s", formatBytes(c.Download), formatBytes(c.Upload)))
	row("开始于", c.Start.Local().Format(time.DateTime)+"（"+formatDuration(time.Since(c.Start))+"）")
	row("ID", c.ID)
	return b.String()
}

func (p *connectionsPage) view(w, h int) string {
	if p.viewer != nil {
		return p.viewer.view(w, h)
	}
	vis := p.visible()
	var totalDown, totalUp int64
	for _, r := range p.rows {
		totalDown += r.downSpeed
		totalUp += r.upSpeed
	}
	header := styleTitle.Render("连接") + styleDim.Render(fmt.Sprintf("  %d 个 · ↓ %s ↑ %s · 排序 %s", len(p.rows), formatSpeed(totalDown), formatSpeed(totalUp), p.sort.label()))
	if p.paused {
		header += styleWarn.Render("  已暂停")
	}
	header += "\n" + p.filter.line(w)
	if p.loadErr != nil && len(p.rows) == 0 {
		return header + "\n\n" + styleBad.Render("获取连接失败: "+p.loadErr.Error())
	}
	if len(vis) == 0 {
		return header + "\n\n" + styleDim.Render("没有活动连接")
	}

	// Fixed columns: network 4, two speeds 11, download total 9, duration 7, gaps.
	const fixedW = 4 + 11 + 11 + 9 + 7 + 6
	flex := max(w-fixedW, 20)
	hostW := flex * 4 / 10
	ruleW := flex * 25 / 100
	chainW := flex - hostW - ruleW - 2
	cols := func(host, netw, rule, chain, down, up, total, dur string) string {
		return padRight(ansi.Truncate(host, hostW, "…"), hostW) + " " +
			padRight(netw, 4) + " " +
			padRight(ansi.Truncate(rule, ruleW, "…"), ruleW) + " " +
			padRight(ansi.Truncate(chain, chainW, "…"), chainW) + " " +
			padLeft(down, 11) + padLeft(up, 11) + padLeft(total, 9) + " " + padLeft(dur, 7)
	}
	lines := []string{styleDim.Render(cols("主机", "网络", "规则", "链路", "↓速率", "↑速率", "↓总量", "时长"))}
	listH := max(h-4, 1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listH {
		p.offset = p.cursor - listH + 1
	}
	p.offset = max(0, min(p.offset, len(vis)-listH))
	for i := p.offset; i < len(vis) && i < p.offset+listH; i++ {
		r := vis[i]
		line := cols(connHost(r.conn), r.conn.Metadata.Network, connRule(r.conn), connChain(r.conn),
			speedOrDash(r.downSpeed), speedOrDash(r.upSpeed), formatBytes(r.conn.Download), formatDuration(time.Since(r.conn.Start)))
		if i == p.cursor {
			line = styleSelected.Render(padRight(line, w))
		}
		lines = append(lines, line)
	}
	return header + "\n\n" + strings.Join(lines, "\n")
}

func speedOrDash(n int64) string {
	if n <= 0 {
		return "-"
	}
	return formatSpeed(n)
}

func padLeft(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}
