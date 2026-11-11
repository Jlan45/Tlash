package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

type overviewPage struct{ sess *session }

func newOverviewPage(sess *session) *overviewPage { return &overviewPage{sess: sess} }

func (p *overviewPage) title() string        { return "概览" }
func (p *overviewPage) activate() tea.Cmd    { return nil }
func (p *overviewPage) capturingInput() bool { return false }

func (p *overviewPage) hints() []hint {
	var hs []hint
	if p.sess.ws != nil {
		hs = append(hs, hint{"s", "启动 / 停止内核"})
	}
	return append(hs, []hint{
		{"r", "规则模式"},
		{"g", "全局模式"},
		{"d", "直连模式"},
		{"R", "重启内核"},
		{"X", "关闭所有连接"},
	}...)
}

func (p *overviewPage) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if ws := p.sess.ws; ws != nil {
		switch key.String() {
		case "s":
			return toggleCore(p.sess)
		case "R":
			return askConfirm("重启 mihomo 内核？\n现有连接会全部断开。", func() tea.Cmd {
				return p.sess.job("core-service", "重启内核",
					func(ctx context.Context) (string, error) { return "内核已重启", ws.Restart(ctx) }, p.sess.refreshAll())
			})
		}
	}
	if !p.sess.connected {
		return nil
	}
	c := p.sess.client
	refresh := fetchStatus(c)
	switch key.String() {
	case "r", "g", "d":
		mode := map[string]string{"r": "rule", "g": "global", "d": "direct"}[key.String()]
		if mode == p.sess.config.Mode {
			return nil
		}
		return apiCall("切换模式失败", "已切换到"+modeLabel(mode)+"模式",
			func(ctx context.Context) error { return c.SetMode(ctx, mode) }, refresh)
	case "R":
		return askConfirm("重启 mihomo 内核？\n现有连接会全部断开。", func() tea.Cmd {
			return apiCall("重启失败", "内核正在重启",
				func(ctx context.Context) error { return c.Restart(ctx) }, refresh)
		})
	case "X":
		return askConfirm(fmt.Sprintf("关闭全部 %d 个连接？", p.sess.connCount), func() tea.Cmd {
			return apiCall("关闭连接失败", "已关闭所有连接",
				func(ctx context.Context) error { return c.CloseAllConnections(ctx) }, refresh)
		})
	}
	return nil
}

func (p *overviewPage) view(w, h int) string {
	s := p.sess
	var b strings.Builder
	row := func(label, value string) {
		b.WriteString(styleDim.Render(padRight(label, 10)) + value + "\n")
	}

	b.WriteString(styleTitle.Render("概览") + "\n\n")
	if s.ws != nil && s.core.known && !s.connected {
		return p.viewStopped(&b, row)
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
			b.WriteString("请确认 mihomo 正在运行，并已开启 external-controller 或 external-controller-unix，\n")
			b.WriteString("然后用 --controller 指定地址。每 2 秒自动重试。\n")
		}
		return b.String()
	}

	row("内核", styleOK.Render("● 运行中")+"  "+versionLabel(s.version))
	if s.ws != nil {
		row("订阅", activeProfileLabel(s))
		row("托管", fmt.Sprintf("%s · 开机自启 %s", s.core.kind.Label(), onOff(s.core.autoStart)))
	} else {
		row("控制接口", s.client.Endpoint().String())
	}
	row("出站模式", modeSwitcher(s.config.Mode)+styleDim.Render("    r / g / d 切换"))
	row("端口", portSummary(s.config))
	row("网络", fmt.Sprintf("局域网访问 %s · IPv6 %s · TUN %s · 日志 %s",
		onOff(s.config.AllowLan), onOff(s.config.IPv6), onOff(s.config.Tun.Enable), s.config.LogLevel))
	b.WriteString("\n")
	row("实时速率", styleOK.Render("↓ "+padRight(formatSpeed(s.traffic.Down), 12))+styleAccent.Render("↑ "+formatSpeed(s.traffic.Up)))
	row("累计流量", "↓ "+padRight(formatBytes(s.traffic.DownTotal), 12)+"↑ "+formatBytes(s.traffic.UpTotal))
	row("内存", formatBytes(s.memory))
	row("活动连接", fmt.Sprintf("%d", s.connCount))

	// Graphs use whatever vertical space remains.
	used := strings.Count(b.String(), "\n")
	graphW := max(w-2, 10)
	if h-used >= 7 {
		b.WriteString("\n")
		b.WriteString(styleDim.Render(fmt.Sprintf("下行  峰值 %s", formatSpeed(peak(s.downHistory, graphW)))) + "\n")
		b.WriteString(styleOK.Render(sparkline(s.downHistory, graphW)) + "\n\n")
		b.WriteString(styleDim.Render(fmt.Sprintf("上行  峰值 %s", formatSpeed(peak(s.upHistory, graphW)))) + "\n")
		b.WriteString(styleAccent.Render(sparkline(s.upHistory, graphW)) + "\n")
	}
	return b.String()
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

// viewStopped is the workspace view while the core isn't reachable: a setup checklist.
func (p *overviewPage) viewStopped(b *strings.Builder, row func(string, string)) string {
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
		b.WriteString("  " + mark + " " + padRight(text, 14) + styleDim.Render(how) + "\n")
	}
	_, hasProfile := activeProfile(s)
	step(s.core.installed, "安装内核", "内核页按 D 下载，或按 i 导入本地文件")
	step(hasProfile, "添加订阅", "订阅页按 a 添加订阅链接，或按 i 导入本地配置")
	step(s.core.running, "启动内核", "在这里按 s 启动（没有订阅时以直连模式运行）")
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
		return notify("尚未安装内核，请先在内核页按 D 下载")
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
