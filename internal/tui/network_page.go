package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

type networkTab int

const (
	tabDNS networkTab = iota
	tabSniffer
	tabTools
)

var dnsRows = []managedRow{
	plainBoolRow("启用", "", func(m *appsettings.Managed) *bool { return &m.DNS.Enable }),
	plainStringRow("监听地址", "仅 TUN / 透明代理需要，如 0.0.0.0:1053；留空不监听", nil, func(m *appsettings.Managed) *string { return &m.DNS.Listen }),
	plainStringRow("增强模式", "", []string{"fake-ip", "redir-host", "normal"}, func(m *appsettings.Managed) *string { return &m.DNS.EnhancedMode }),
	plainStringRow("Fake-IP 范围", "", nil, func(m *appsettings.Managed) *string { return &m.DNS.FakeIPRange }),
	listRow("Fake-IP 过滤", "这些域名返回真实 IP", func(m *appsettings.Managed) *[]string { return &m.DNS.FakeIPFilter }),
	listRow("默认 DNS", "用于解析其他 DNS 服务器的域名，必须是 IP", func(m *appsettings.Managed) *[]string { return &m.DNS.DefaultNameserver }),
	listRow("主 DNS", "支持 UDP / DoH / DoT，如 https://doh.pub/dns-query", func(m *appsettings.Managed) *[]string { return &m.DNS.Nameserver }),
	listRow("备用 DNS", "留空不使用", func(m *appsettings.Managed) *[]string { return &m.DNS.Fallback }),
	listRow("代理节点 DNS", "解析节点域名专用", func(m *appsettings.Managed) *[]string { return &m.DNS.ProxyServerNameserver }),
}

var snifferRows = []managedRow{
	plainBoolRow("启用", "", func(m *appsettings.Managed) *bool { return &m.Sniffer.Enable }),
	plainBoolRow("覆盖目标地址", "用嗅探到的域名替换连接目标", func(m *appsettings.Managed) *bool { return &m.Sniffer.OverrideDestination }),
	listRow("HTTP 端口", "如 80, 8080-8880", func(m *appsettings.Managed) *[]string { return &m.Sniffer.HTTPPorts }),
	listRow("TLS 端口", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.TLSPorts }),
	listRow("QUIC 端口", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.QUICPorts }),
	listRow("强制嗅探域名", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.ForceDomain }),
	listRow("跳过嗅探域名", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.SkipDomain }),
}

type dnsQueryMsg struct {
	query  string
	result mihomoapi.DNSResult
	err    error
}

type networkPage struct {
	sess   *session
	tab    networkTab
	cursor int
	form   *inputForm
	// Last DNS lookup shown on the tools tab.
	lookup string
}

func newNetworkPage(sess *session) *networkPage {
	p := &networkPage{sess: sess}
	if sess.ws == nil {
		p.tab = tabTools
	}
	return p
}

func (p *networkPage) title() string        { return "网络" }
func (p *networkPage) capturingInput() bool { return p.form != nil }
func (p *networkPage) activate() tea.Cmd    { return nil }

func (p *networkPage) hints() []hint {
	if p.form != nil {
		return p.form.hints()
	}
	switch p.tab {
	case tabTools:
		return []hint{{"d", "DNS 查询"}, {"F", "清空 Fake-IP 缓存"}, {"C", "清空 DNS 缓存"}, {"v", "切换分页"}}
	default:
		return []hint{{"enter", "修改"}, {"T", "接管 / 交还订阅"}, {"v", "切换分页"}}
	}
}

func (p *networkPage) rows() []managedRow {
	switch p.tab {
	case tabDNS:
		return dnsRows
	case tabSniffer:
		return snifferRows
	}
	return nil
}

// managed reports whether Tlash currently owns the block shown on this tab.
func (p *networkPage) managed() bool {
	if p.sess.ws == nil {
		return false
	}
	m := p.sess.ws.Settings().Managed
	switch p.tab {
	case tabDNS:
		return m.DNS != nil
	case tabSniffer:
		return m.Sniffer != nil
	}
	return false
}

func (p *networkPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dnsQueryMsg:
		p.lookup = renderDNSResult(msg)
		return nil
	case tea.KeyPressMsg:
		if p.form != nil {
			cmd, done := p.form.update(msg)
			if done {
				p.form = nil
			}
			return cmd
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *networkPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	c := p.sess.client
	switch msg.String() {
	case "v", "right", "l":
		if p.sess.ws != nil {
			p.tab = (p.tab + 1) % 3
			p.cursor = 0
		}
		return nil
	case "left", "h":
		if p.sess.ws != nil {
			p.tab = (p.tab + 2) % 3
			p.cursor = 0
		}
		return nil
	}
	if p.tab == tabTools {
		switch msg.String() {
		case "d":
			p.form = newInputForm("DNS 查询", func(v []string) tea.Cmd {
				name, qtype := v[0], strings.ToUpper(v[1])
				if qtype == "" {
					qtype = "A"
				}
				return func() tea.Msg {
					r, err := c.DNSQuery(context.Background(), name, qtype)
					return dnsQueryMsg{query: name + " " + qtype, result: r, err: err}
				}
			}).addText("域名", "", "example.com", "通过内核的 DNS 解析，结果反映当前 DNS 配置").
				addText("类型", "A", "A / AAAA / CNAME / TXT …", "")
		case "F":
			return apiCall("清空失败", "已清空 Fake-IP 缓存", func(ctx context.Context) error { return c.FlushFakeIP(ctx) })
		case "C":
			return apiCall("清空失败", "已清空 DNS 缓存", func(ctx context.Context) error { return c.FlushDNS(ctx) })
		}
		return nil
	}

	rows := p.rows()
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
	case "down", "j":
		p.cursor = min(p.cursor+1, len(rows)-1)
	case "T":
		return p.toggleTakeover()
	case "enter":
		if !p.managed() {
			return notify("当前使用订阅自带的配置，按 T 由 Tlash 接管后再修改")
		}
		return editManaged(p.sess, rows[p.cursor], func(f *inputForm) { p.form = f })
	}
	return nil
}

func (p *networkPage) toggleTakeover() tea.Cmd {
	tab := p.tab // the job runs later; the user may have switched tabs by then
	name := map[networkTab]string{tabDNS: "DNS", tabSniffer: "嗅探"}[tab]
	if p.managed() {
		return askConfirm(fmt.Sprintf("交还 %s 设置给订阅？\nTlash 的 %s 设置会被清除。", name, name), func() tea.Cmd {
			return applyManaged(p.sess, name+" 已改用订阅自带配置", func(m *appsettings.Managed) {
				if tab == tabDNS {
					m.DNS = nil
				} else {
					m.Sniffer = nil
				}
			})
		})
	}
	return applyManaged(p.sess, name+" 已由 Tlash 接管（推荐默认值）", func(m *appsettings.Managed) {
		if tab == tabDNS {
			m.DNS = appsettings.DefaultDNS()
		} else {
			m.Sniffer = appsettings.DefaultSniffer()
		}
	})
}

func renderDNSResult(msg dnsQueryMsg) string {
	var b strings.Builder
	b.WriteString(styleBold.Render(msg.query) + "\n")
	switch {
	case msg.err != nil:
		b.WriteString(styleBad.Render("查询失败: " + msg.err.Error()))
	case len(msg.result.Answer) == 0:
		b.WriteString(styleWarn.Render(fmt.Sprintf("无结果（状态码 %d）", msg.result.Status)))
	default:
		for _, a := range msg.result.Answer {
			b.WriteString(fmt.Sprintf("  %s  %s\n", padRight(a.Data, 40), styleDim.Render(fmt.Sprintf("TTL %d", a.TTL))))
		}
	}
	return b.String()
}

func (p *networkPage) view(w, h int) string {
	if p.form != nil {
		return p.form.view(w)
	}
	tab := func(label string, t networkTab) string {
		if t == p.tab {
			return styleSelected.Render(" " + label + " ")
		}
		return styleDim.Render(" " + label + " ")
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("网络") + "  " + tab("DNS", tabDNS) + " " + tab("嗅探", tabSniffer) + " " + tab("工具", tabTools) + styleDim.Render("  v 切换") + "\n\n")

	if p.tab == tabTools {
		b.WriteString(styleDim.Render("d 查询域名 · F 清空 Fake-IP 缓存 · C 清空 DNS 缓存") + "\n\n")
		if p.lookup != "" {
			b.WriteString(p.lookup)
		}
		return b.String()
	}
	if p.sess.ws == nil {
		return b.String() + styleDim.Render("外部内核模式下不能修改配置")
	}
	if !p.managed() {
		b.WriteString(styleDim.Render("当前使用订阅自带的配置。") + "按 " + styleKey.Render("T") + " 由 Tlash 接管（会填入推荐的默认值）。\n")
		return b.String()
	}
	note := "覆盖订阅 dns 块中的这些项，其他项（如 nameserver-policy）保留"
	if p.tab == tabSniffer {
		note = "整体替换订阅的 sniffer 块"
	}
	b.WriteString(styleDim.Render(note+" · T 交还订阅") + "\n\n")
	b.WriteString(renderManagedRows(p.rows(), p.sess.ws.Settings().Managed, p.cursor, 14))
	return b.String()
}
