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

// connectionsPanel shows the snapshot the status poll fetches every second.
type connectionsPanel struct {
	panelBase
	sess   *session
	all    []connRow
	prev   map[string]mihomoapi.Connection
	prevAt time.Time
	paused bool
	sort   connSort
	selID  string
}

func newConnectionsPanel(sess *session) *connectionsPanel { return &connectionsPanel{sess: sess} }

func (p *connectionsPanel) title() string      { return "连接" }
func (p *connectionsPanel) badge() string      { return fmt.Sprintf("%d", len(p.all)) }
func (p *connectionsPanel) tabs() []string     { return []string{"详情", "全部连接"} }
func (p *connectionsPanel) filterHint() string { return "过滤主机 / 规则 / 节点 / 进程" }

func (p *connectionsPanel) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(statusMsg); ok && !p.paused && p.sess.connected {
		p.ingest(p.sess.conns, p.sess.connsAt)
	}
	return nil
}

func (p *connectionsPanel) ingest(conns []mihomoapi.Connection, at time.Time) {
	elapsed := at.Sub(p.prevAt).Seconds()
	next := make(map[string]mihomoapi.Connection, len(conns))
	rows := make([]connRow, 0, len(conns))
	for _, c := range conns {
		next[c.ID] = c
		r := connRow{conn: c}
		if old, ok := p.prev[c.ID]; ok && elapsed > 0 {
			r.downSpeed = int64(float64(c.Download-old.Download) / elapsed)
			r.upSpeed = int64(float64(c.Upload-old.Upload) / elapsed)
		}
		rows = append(rows, r)
	}
	// Remember the selection by ID so it follows its connection across refreshes.
	if cur, ok := p.current(); ok {
		p.selID = cur.conn.ID
	}
	p.prev, p.prevAt, p.all = next, at, rows
	p.resort()
}

func (p *connectionsPanel) resort() {
	slices.SortStableFunc(p.all, func(a, b connRow) int {
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
	for i, r := range p.visible() {
		if r.conn.ID == p.selID {
			p.ls.cursor = i
			return
		}
	}
}

func (p *connectionsPanel) visible() []connRow {
	f := strings.ToLower(p.ls.filter.value())
	if f == "" {
		return p.all
	}
	var out []connRow
	for _, r := range p.all {
		hay := strings.ToLower(connHost(r.conn) + " " + connRule(r.conn) + " " + connChain(r.conn) + " " + r.conn.Metadata.Process)
		if strings.Contains(hay, f) {
			out = append(out, r)
		}
	}
	return out
}

func (p *connectionsPanel) current() (connRow, bool) {
	vis := p.visible()
	if c := p.ls.cursor; c >= 0 && c < len(vis) {
		return vis[c], true
	}
	return connRow{}, false
}

func (p *connectionsPanel) rows(w int) []string {
	vis := p.visible()
	hostW := max(w-11, 8)
	out := make([]string, len(vis))
	for i, r := range vis {
		speed := styleDim.Render(padLeft("-", 10))
		if r.downSpeed > 0 {
			speed = styleOK.Render(padLeft(formatSpeed(r.downSpeed), 10))
		}
		out[i] = padRight(ansi.Truncate(connHost(r.conn), hostW, "…"), hostW) + " " + speed
	}
	return out
}

func (p *connectionsPanel) actions() []action {
	c := p.sess.client
	pause := "暂停刷新"
	if p.paused {
		pause = "继续刷新"
	}
	acts := []action{
		{"o", "排序：" + p.sort.label(), func() tea.Cmd {
			p.sort = (p.sort + 1) % 5
			p.resort()
			return notify("连接排序：" + p.sort.label())
		}},
		{"space", pause, func() tea.Cmd { p.paused = !p.paused; return nil }},
		{"D", "关闭全部连接", func() tea.Cmd {
			return askConfirm(fmt.Sprintf("关闭全部 %d 个连接？", len(p.all)), func() tea.Cmd {
				return apiCall("关闭连接失败", "已关闭所有连接", func(ctx context.Context) error { return c.CloseAllConnections(ctx) }, fetchStatus(c))
			})
		}},
	}
	cur, ok := p.current()
	if !ok {
		return acts
	}
	return append([]action{{"d", "关闭此连接", func() tea.Cmd {
		return apiCall("关闭连接失败", "已关闭 "+connHost(cur.conn), func(ctx context.Context) error {
			return c.CloseConnection(ctx, cur.conn.ID)
		}, fetchStatus(c))
	}}}, acts...)
}

func (p *connectionsPanel) renderMain(tab, w, h int, focused bool) string {
	if tab == 1 {
		return p.table(w)
	}
	cur, ok := p.current()
	if !ok {
		if !p.sess.connected {
			return styleDim.Render("未连接到内核")
		}
		return styleDim.Render("没有活动连接")
	}
	head := ""
	if p.paused {
		head = styleWarn.Render("已暂停刷新（space 继续）") + "\n\n"
	}
	return head + connDetail(cur.conn) + "\n" +
		styleDim.Render(fmt.Sprintf("实时速率  ↓ %s  ↑ %s", formatSpeed(cur.downSpeed), formatSpeed(cur.upSpeed)))
}

// table is every visible connection in columns, for wide terminals.
func (p *connectionsPanel) table(w int) string {
	vis := p.visible()
	var totalDown, totalUp int64
	for _, r := range p.all {
		totalDown += r.downSpeed
		totalUp += r.upSpeed
	}
	head := styleDim.Render(fmt.Sprintf("%d 个连接 · ↓ %s ↑ %s · 排序 %s", len(p.all), formatSpeed(totalDown), formatSpeed(totalUp), p.sort.label()))
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
	lines := []string{head, "", styleDim.Render(cols("主机", "网络", "规则", "链路", "↓速率", "↑速率", "↓总量", "时长"))}
	for i, r := range vis {
		line := cols(connHost(r.conn), r.conn.Metadata.Network, connRule(r.conn), connChain(r.conn),
			speedOrDash(r.downSpeed), speedOrDash(r.upSpeed), formatBytes(r.conn.Download), formatDuration(time.Since(r.conn.Start)))
		if i == p.ls.cursor {
			line = styleBold.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
