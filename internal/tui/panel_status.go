package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

const (
	statusTabOverview = iota
	statusTabLogs
	statusTabConfig
)

// statusPanel is the core: state summary, traffic overview, logs and the generated config.
type statusPanel struct {
	panelBase
	sess     *session
	logs     *logView
	exitIP   *exitIPMsg
	checking bool
	config   fileCache
}

func newStatusPanel(sess *session, logs *logView) *statusPanel {
	return &statusPanel{sess: sess, logs: logs}
}

func (p *statusPanel) title() string { return "状态" }
func (p *statusPanel) summary() bool { return true }

func (p *statusPanel) tabs() []string {
	if p.sess.ws == nil {
		return []string{"概览", "日志"}
	}
	return []string{"概览", "日志", "运行配置"}
}

func (p *statusPanel) rows(w int) []string {
	s := p.sess
	var state string
	switch {
	case s.connected:
		state = styleOK.Render("● 运行中") + "  " + s.version.Version
	case s.ws != nil && s.core.known && !s.core.running:
		state = styleDim.Render("● 已停止")
		if !s.core.installed {
			state += styleWarn.Render("  未安装内核")
		}
	default:
		state = styleBad.Render("● 未连接")
	}
	summary := modeLabel(s.config.Mode)
	if s.ws != nil {
		if name, ok := activeProfile(s); ok {
			summary += " · " + name
		}
	}
	if s.connected {
		summary += fmt.Sprintf(" · %d 连接", s.connCount)
	}
	return []string{state, styleDim.Render(summary)}
}

func (p *statusPanel) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case exitIPMsg:
		p.exitIP, p.checking = &m, false
	case logMsg:
		p.logs.update(m)
	}
	return nil
}

func (p *statusPanel) interactive(tab int) bool { return tab == statusTabLogs }

func (p *statusPanel) capturing() bool { return p.logs.capturing() }

func (p *statusPanel) paste(msg tea.PasteMsg) tea.Cmd { return p.logs.paste(msg) }

func (p *statusPanel) mainKey(tab int, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if tab == statusTabLogs {
		return p.logs.key(msg)
	}
	return nil, false
}

func (p *statusPanel) mainActions(tab int) []action {
	if tab == statusTabLogs {
		return p.logs.actions()
	}
	return nil
}

func (p *statusPanel) actions() []action {
	s := p.sess
	c := s.client
	var acts []action
	if ws := s.ws; ws != nil {
		acts = append(acts,
			action{"s", map[bool]string{true: "停止内核", false: "启动内核"}[s.core.running], func() tea.Cmd { return toggleCore(s) }},
			action{"r", "重启内核", func() tea.Cmd {
				if !s.core.running {
					return notify("内核未运行")
				}
				return askConfirm("重启 mihomo 内核？\n现有连接会全部断开。", func() tea.Cmd {
					return s.job("core-service", "重启内核", func(ctx context.Context) (string, error) { return "内核已重启", ws.Restart(ctx) }, s.refreshAll())
				})
			}},
		)
	} else {
		acts = append(acts, action{"r", "重启内核", func() tea.Cmd {
			return askConfirm("重启 mihomo 内核？\n现有连接会全部断开。", func() tea.Cmd {
				return apiCall("重启失败", "内核正在重启", func(ctx context.Context) error { return c.Restart(ctx) }, fetchStatus(c))
			})
		}})
	}
	return append(acts,
		action{"m", "切换模式", func() tea.Cmd {
			if !s.connected {
				return notify("内核未运行")
			}
			next := map[string]string{"rule": "global", "global": "direct", "direct": "rule"}[s.config.Mode]
			if next == "" {
				next = "rule"
			}
			return apiCall("切换模式失败", "已切换到"+modeLabel(next)+"模式", func(ctx context.Context) error { return c.SetMode(ctx, next) }, fetchStatus(c))
		}},
		action{"i", "检测出口 IP", func() tea.Cmd {
			if p.checking {
				return nil
			}
			p.checking = true
			return checkExitIP(s.config)
		}},
	)
}

func (p *statusPanel) renderMain(tab, w, h int, focused bool) string {
	switch tab {
	case statusTabLogs:
		return p.logs.view(w, h)
	case statusTabConfig:
		content, err := p.config.read(p.sess.ws.ConfigPath)
		if err != nil {
			return styleDim.Render("还没有生成运行配置（启动内核或启用订阅后生成）")
		}
		return styleDim.Render("# "+p.sess.ws.ConfigPath+"（订阅 + 覆写 + 接管设置）") + "\n" + content
	}
	return p.overview(w, h)
}

func (p *statusPanel) overview(w, h int) string {
	s := p.sess
	var b strings.Builder
	row := func(label, value string) { b.WriteString(styleDim.Render(padRight(label, 10)) + value + "\n") }

	if s.ws != nil && s.core.known && !s.connected {
		return p.setupChecklist(&b, row)
	}
	if !s.connected {
		row("内核", styleBad.Render("● 未连接"))
		row("控制接口", s.client.Endpoint().String())
		if s.lastErr != nil {
			row("错误", styleBad.Render(s.lastErr.Error()))
		}
		b.WriteString("\n")
		if mihomoapi.IsUnauthorized(s.lastErr) {
			b.WriteString("密钥不正确，请用 --secret 指定 external-controller 的 secret。\n")
		} else {
			b.WriteString("请确认 mihomo 正在运行并开启了 external-controller 或 external-controller-unix，\n然后用 --controller 指定地址。每秒自动重试。\n")
		}
		return b.String()
	}

	row("内核", styleOK.Render("● 运行中")+"  "+versionLabel(s.version))
	if s.ws != nil {
		row("订阅", activeProfileLabel(s))
		row("托管", fmt.Sprintf("%s · 开机自启 %s", s.core.kind.Label(), onOff(s.core.autoStart)))
		row("网关", gatewaySummary(s.ws.Settings().Gateway))
	} else {
		row("控制接口", s.client.Endpoint().String())
	}
	row("出站模式", modeSwitcher(s.config.Mode)+styleDim.Render("   m 切换"))
	row("端口", portSummary(s.config))
	if s.ws != nil {
		if want := s.ws.Settings().Managed.MixedPort; want != nil && *want > 0 && s.config.MixedPort != *want {
			row("", styleBad.Render(fmt.Sprintf("混合端口 %d 未能监听（可能被其他程序占用），重启内核会显示占用者", *want)))
		}
	}
	row("网络", fmt.Sprintf("局域网访问 %s · IPv6 %s · TUN %s · 日志 %s",
		onOff(s.config.AllowLan), onOff(s.config.IPv6), onOff(s.config.Tun.Enable), s.config.LogLevel))
	b.WriteString("\n")
	row("实时速率", styleOK.Render("↓ "+padRight(formatSpeed(s.traffic.Down), 12))+styleAccent.Render("↑ "+formatSpeed(s.traffic.Up)))
	row("累计流量", "↓ "+padRight(formatBytes(s.traffic.DownTotal), 12)+"↑ "+formatBytes(s.traffic.UpTotal))
	row("内存", formatBytes(s.memory))
	row("活动连接", fmt.Sprintf("%d", s.connCount))
	switch {
	case p.checking:
		row("出口 IP", styleDim.Render("检测中…"))
	case p.exitIP != nil:
		row("出口 IP", "代理 "+p.exitIP.proxied.label())
		row("", styleDim.Render("直连 ")+p.exitIP.direct.label())
	default:
		row("出口 IP", styleDim.Render("按 i 检测（经 "+exitIPService+"）"))
	}

	// Graphs use whatever vertical space remains.
	used := strings.Count(b.String(), "\n")
	graphW := max(w-1, 10)
	if h-used >= 7 {
		b.WriteString("\n")
		b.WriteString(styleDim.Render(fmt.Sprintf("下行  峰值 %s", formatSpeed(peak(s.downHistory, graphW)))) + "\n")
		b.WriteString(styleOK.Render(sparkline(s.downHistory, graphW)) + "\n\n")
		b.WriteString(styleDim.Render(fmt.Sprintf("上行  峰值 %s", formatSpeed(peak(s.upHistory, graphW)))) + "\n")
		b.WriteString(styleAccent.Render(sparkline(s.upHistory, graphW)) + "\n")
	}
	return b.String()
}

// setupChecklist shows while a managed core isn't reachable.
func (p *statusPanel) setupChecklist(b *strings.Builder, row func(string, string)) string {
	s := p.sess
	state := styleDim.Render("● 已停止")
	if s.core.running {
		state = styleWarn.Render("● 服务运行中，但控制接口无响应")
	}
	row("内核", state)
	row("托管", s.core.kind.Label())
	row("数据目录", s.ws.Dir)
	if s.core.running && s.lastErr != nil {
		row("错误", styleBad.Render(s.lastErr.Error()))
		row("内核日志", s.core.logHint)
	}
	b.WriteString("\n" + styleTitle.Render("开始使用") + "\n\n")
	step := func(done bool, text, how string) {
		mark := styleDim.Render("○")
		if done {
			mark = styleOK.Render("✓")
		}
		b.WriteString("  " + mark + " " + padRight(text, 12) + styleDim.Render(how) + "\n")
	}
	_, hasProfile := activeProfile(s)
	step(s.core.installed, "安装内核", "7 设置 → 内核，按 D 下载（或 i 导入本地文件）")
	step(hasProfile, "添加订阅", "2 订阅，按 a 添加订阅链接（或 i 导入本地配置）")
	step(s.core.running, "启动内核", "在这里按 s（没有订阅时以直连模式运行）")
	return b.String()
}

func toggleCore(sess *session) tea.Cmd {
	ws := sess.ws
	if sess.core.running {
		return askConfirm("停止 mihomo 内核？\n代理将不可用，直到再次启动。", func() tea.Cmd {
			return sess.job("core-service", "停止内核",
				func(ctx context.Context) (string, error) { return "内核已停止", ws.Stop(ctx) }, sess.refreshAll())
		})
	}
	if !sess.core.installed {
		return notify("尚未安装内核：7 设置 → 内核，按 D 下载")
	}
	return sess.job("core-service", "启动内核",
		func(ctx context.Context) (string, error) { return "内核已启动", ws.Start(ctx) }, sess.refreshAll())
}

func activeProfile(s *session) (string, bool) {
	id := s.ws.Settings().ActiveProfile
	if id == "" {
		return "", false
	}
	p, err := s.ws.Profiles.Get(id)
	if err != nil {
		return "", false
	}
	return p.Name, true
}

func activeProfileLabel(s *session) string {
	if name, ok := activeProfile(s); ok {
		return name
	}
	return styleDim.Render("未选择（直连模式运行）")
}

func versionLabel(v mihomoapi.Version) string {
	if v.Meta {
		return "Mihomo Meta " + v.Version
	}
	return v.Version
}

func modeSwitcher(current string) string {
	parts := make([]string, 0, 3)
	for _, m := range []string{"rule", "global", "direct"} {
		label := " " + modeLabel(m) + " "
		if m == current {
			label = styleSelected.Render(label)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func portSummary(c mihomoapi.Config) string {
	var parts []string
	add := func(name string, port int) {
		if port > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", name, port))
		}
	}
	add("mixed", c.MixedPort)
	add("http", c.Port)
	add("socks", c.SocksPort)
	add("redir", c.RedirPort)
	add("tproxy", c.TProxyPort)
	if len(parts) == 0 {
		return styleDim.Render("未开启任何入站端口")
	}
	return strings.Join(parts, " · ")
}

func onOff(b bool) string {
	if b {
		return styleOK.Render("开")
	}
	return styleDim.Render("关")
}

func peak(samples []int64, window int) int64 {
	if len(samples) > window {
		samples = samples[len(samples)-window:]
	}
	var m int64
	for _, s := range samples {
		m = max(m, s)
	}
	return m
}

func gatewaySummary(g appsettings.Gateway) string {
	if !g.Enabled {
		return styleDim.Render("未开启（7 设置 → 网关）")
	}
	g = g.WithDefaults()
	parts := []string{styleOK.Render("已开启")}
	if g.DeviceMode == appsettings.DeviceModeSelected {
		parts = append(parts, fmt.Sprintf("只代理 %d 台设备", len(g.Devices)))
	} else if len(g.Devices) > 0 {
		parts = append(parts, fmt.Sprintf("排除 %d 台设备", len(g.Devices)))
	} else {
		parts = append(parts, "全部设备")
	}
	if g.BypassCN {
		parts = append(parts, "大陆 IP 直接放行")
	}
	if g.IPv6 {
		parts = append(parts, "IPv4 + IPv6")
	} else {
		parts = append(parts, "仅 IPv4")
	}
	return strings.Join(parts, " · ")
}
