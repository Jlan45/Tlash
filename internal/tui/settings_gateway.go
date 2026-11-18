package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/landevices"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// gatewayState is gathered off the UI goroutine: preflight binds ports and reads /proc.
type gatewayState struct {
	checks  []workspace.GatewayCheck
	devices []landevices.Device
	pending time.Time
}

func loadGatewayState(ws *workspace.Workspace) gatewayState {
	st := gatewayState{checks: ws.GatewayPreflight(context.Background()), devices: landevices.List(landevices.HostSources())}
	st.pending, _ = ws.PendingGateway()
	return st
}

type gatewayPendingMsg struct{ deadline time.Time }

var tunModes = []string{appsettings.TunOff, appsettings.TunLocal, appsettings.TunGateway}

func tunModeLabel(mode string) string {
	switch mode {
	case appsettings.TunLocal:
		return "仅本机"
	case appsettings.TunGateway:
		return "本机 + 局域网（网关）"
	default:
		return "关闭"
	}
}

func (p *settingsPanel) gateway() appsettings.Gateway {
	return p.sess.ws.Settings().Gateway.WithDefaults()
}

func (p *settingsPanel) updateGateway(okText string, fn func(*appsettings.Gateway)) tea.Cmd {
	if text, busy := coreConfigBusy(p.sess); busy {
		return notify(text + "，请稍候")
	}
	ws, sess := p.sess.ws, p.sess
	return sess.job("gateway", "应用 TUN 设置", func(ctx context.Context) (string, error) {
		return okText, ws.UpdateGateway(ctx, fn)
	}, p.refresh(), sess.refreshAll())
}

func (p *settingsPanel) stepTunMode(delta int) tea.Cmd {
	if text, busy := coreConfigBusy(p.sess); busy {
		return notify(text + "，请稍候")
	}
	ws, sess := p.sess.ws, p.sess
	g := p.gateway()
	cur := g.Mode()
	idx := slices.Index(tunModes, cur)
	if idx < 0 {
		idx = 0
	}
	n := len(tunModes)
	next := tunModes[(idx+delta%n+n)%n]
	if next == cur {
		return nil
	}
	if next == appsettings.TunOff {
		prompt := "关闭 TUN？\n本机流量将不再经过透明代理。"
		if g.IsGateway() {
			prompt = "关闭 TUN？\n本机和局域网设备将不再经过透明代理，系统设置会被还原。"
		}
		return askConfirm(prompt, func() tea.Cmd {
			return sess.job("gateway", "关闭 TUN", func(ctx context.Context) (string, error) {
				_, err := ws.SetTunMode(ctx, appsettings.TunOff)
				return "TUN 已关闭", err
			}, p.refresh(), sess.refreshAll())
		})
	}
	return sess.job("gateway", "切换 TUN 模式", func(ctx context.Context) (string, error) {
		_, err := ws.SetTunMode(ctx, next)
		return "", err
	}, p.refresh(), sess.refreshAll(), func() tea.Msg {
		if d, ok := ws.PendingGateway(); ok {
			return gatewayPendingMsg{deadline: d}
		}
		return nil
	})
}

func (p *settingsPanel) gatewayItems() []settingItem {
	g := p.gateway()
	mode := g.Mode()
	var state string
	switch {
	case !p.state.gateway.pending.IsZero():
		state = styleWarn.Render("● " + tunModeLabel(mode) + "（等待确认，" + p.state.gateway.pending.Format("15:04:05") + " 前不确认将回滚）")
	case g.Enabled:
		state = styleOK.Render("● ") + tunModeLabel(mode)
	default:
		state = styleDim.Render("关闭")
	}
	ipLabel := "仅 IPv4"
	if g.IPv6 {
		ipLabel = "IPv4 + IPv6"
	}
	scope := "全部设备，排除下列设备"
	if g.DeviceMode == appsettings.DeviceModeSelected {
		scope = "只代理下列设备"
	}
	cn := "交给 mihomo 规则"
	if g.BypassCN {
		cn = "内核层直接放行（不经过 mihomo）"
	}
	exclude := styleDim.Render("无")
	if len(g.ExcludeAddrs) > 0 {
		exclude = strings.Join(g.ExcludeAddrs, ", ")
	}
	items := []settingItem{
		{label: "模式", value: state, hint: "←→ 切换 · 开启后 60 秒内确认", step: p.stepTunMode},
		{label: "协议栈", value: g.Stack, hint: "mixed 兼顾性能与兼容；←→ 切换", step: p.cycleStack},
		{label: "IP 协议", value: ipLabel, hint: "←→ 切换", step: func(int) tea.Cmd {
			return p.updateGateway("IP 协议已切换", func(gw *appsettings.Gateway) { gw.IPv6 = !gw.IPv6 })
		}},
		{label: "大陆 IP", value: cn, hint: "←→ 切换", step: func(int) tea.Cmd {
			return p.updateGateway("大陆 IP 处理方式已切换", func(gw *appsettings.Gateway) { gw.BypassCN = !gw.BypassCN })
		}},
		{label: "排除地址", value: exclude, hint: "enter 修改 · 这些地址不经过代理", edit: func() tea.Cmd {
			return showForm(newInputForm("排除地址", func(v []string) tea.Cmd {
				return p.updateGateway("排除地址已修改", func(gw *appsettings.Gateway) { gw.ExcludeAddrs = splitList(v[0]) })
			}).addText("地址", strings.Join(g.ExcludeAddrs, ", "), "203.0.113.7, 10.0.0.0/8", "开启 TUN 时会自动加入当前 SSH 客户端地址；多项用逗号分隔"))
		}},
	}
	if g.IsGateway() {
		items = append(items, settingItem{label: "代理范围", value: scope, hint: "←→ 切换黑白名单", step: func(int) tea.Cmd {
			return p.updateGateway("代理范围已切换", func(gw *appsettings.Gateway) {
				if gw.DeviceMode == appsettings.DeviceModeSelected {
					gw.DeviceMode = appsettings.DeviceModeAll
				} else {
					gw.DeviceMode = appsettings.DeviceModeSelected
				}
			})
		}})
		for i, d := range g.Devices {
			label := ""
			if i == 0 {
				label = "设备"
			}
			mac := d.MAC
			items = append(items, settingItem{label: label, value: deviceLabel(d.Name, d.MAC, p.ipOf(d.MAC)), hint: "enter 移除", edit: func() tea.Cmd {
				return p.updateGateway("已移除 "+mac, func(gw *appsettings.Gateway) {
					gw.Devices = slices.DeleteFunc(gw.Devices, func(x appsettings.Device) bool { return x.MAC == mac })
				})
			}})
		}
		items = append(items,
			settingItem{label: "添加设备", value: styleDim.Render(fmt.Sprintf("局域网中发现 %d 台", len(p.state.gateway.devices))), hint: "a 或 enter 从列表选择或手动输入 MAC", edit: p.addDeviceMenu},
			settingItem{label: "DNS 监听", value: p.dnsListenLabel(), hint: p.dnsListenHint(), edit: func() tea.Cmd {
				return showForm(newInputForm("DNS 监听地址", func(v []string) tea.Cmd {
					return p.updateGateway("DNS 监听已修改", func(gw *appsettings.Gateway) { gw.DNSListen = v[0] })
				}).addText("地址", g.DNSListen, p.sess.ws.GatewayDNSListen(), "留空使用默认值；53 端口被占用时可改为局域网 IP，如 192.168.1.2:53"))
			}},
		)
	}
	var checks []workspace.GatewayCheck
	for _, c := range p.state.gateway.checks {
		if c.GatewayOnly && !g.IsGateway() {
			continue
		}
		checks = append(checks, c)
	}
	if len(checks) > 0 {
		items = append(items, settingItem{label: "环境检查"})
	}
	for _, c := range checks {
		mark := styleOK.Render("✓")
		switch c.Level {
		case workspace.CheckWarn:
			mark = styleWarn.Render("!")
		case workspace.CheckFail:
			mark = styleBad.Render("✗")
		}
		items = append(items, settingItem{value: mark + " " + c.Name + styleDim.Render("  "+c.Detail)})
	}
	return items
}

func (p *settingsPanel) ipOf(mac string) string {
	for _, d := range p.state.gateway.devices {
		if strings.EqualFold(d.MAC, mac) {
			return d.IP
		}
	}
	return ""
}

func deviceLabel(name, mac, ip string) string {
	parts := []string{}
	if name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, styleDim.Render(mac))
	if ip != "" {
		parts = append(parts, styleDim.Render(ip))
	}
	return strings.Join(parts, "  ")
}

func (p *settingsPanel) cycleStack(delta int) tea.Cmd {
	stacks := []string{"mixed", "system", "gvisor"}
	idx := slices.Index(stacks, p.gateway().Stack)
	if idx < 0 {
		idx = 0
	}
	n := len(stacks)
	next := stacks[(idx+delta%n+n)%n]
	return p.updateGateway("协议栈："+next, func(gw *appsettings.Gateway) { gw.Stack = next })
}

func (p *settingsPanel) gatewayActions() []action {
	if !p.gateway().IsGateway() {
		return nil
	}
	return []action{
		{"a", "添加设备", p.addDeviceMenu},
	}
}

func (p *settingsPanel) addDeviceMenu() tea.Cmd {
	g := p.gateway()
	var items []action
	for _, d := range p.state.gateway.devices {
		if slices.ContainsFunc(g.Devices, func(x appsettings.Device) bool { return strings.EqualFold(x.MAC, d.MAC) }) {
			continue
		}
		dev := appsettings.Device{MAC: d.MAC, Name: d.Name}
		label := deviceLabel(d.Name, d.MAC, d.IP)
		items = append(items, action{key: fmt.Sprint(len(items) + 1), desc: label, run: func() tea.Cmd {
			return p.updateGateway("已添加 "+dev.MAC, func(gw *appsettings.Gateway) { gw.Devices = append(gw.Devices, dev) })
		}})
	}
	items = append(items, action{key: "m", desc: "手动输入 MAC 地址", run: func() tea.Cmd {
		return showForm(newInputForm("添加设备", func(v []string) tea.Cmd {
			mac := strings.ToLower(strings.TrimSpace(v[0]))
			if strings.Count(mac, ":") != 5 {
				return notify("MAC 地址格式应为 aa:bb:cc:dd:ee:ff")
			}
			dev := appsettings.Device{MAC: mac, Name: v[1]}
			return p.updateGateway("已添加 "+mac, func(gw *appsettings.Gateway) { gw.Devices = append(gw.Devices, dev) })
		}).addText("MAC 地址", "", "aa:bb:cc:dd:ee:ff", "").addText("名称", "", "可选，如 客厅电视", ""))
	}})
	title := "添加到排除列表"
	if g.DeviceMode == appsettings.DeviceModeSelected {
		title = "添加到代理列表"
	}
	return showMenu(title, items)
}

// confirmGateway asks to keep a just-enabled TUN mode; declining rolls back immediately.
func (p *settingsPanel) confirmGateway(deadline time.Time) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	p.promptedFor = deadline
	modeName := tunModeLabel(p.gateway().Mode())
	prompt := fmt.Sprintf("TUN（%s）已开启。确认网络正常后按 y 保留；\n按 n 立即回滚。%s 前不确认会自动回滚，\nSSH 断开也会照常回滚。", modeName, deadline.Format("15:04:05"))
	return askChoice(prompt,
		func() tea.Cmd {
			return func() tea.Msg {
				if err := ws.ConfirmGateway(); err != nil {
					return notifyMsg{text: err.Error(), isErr: true}
				}
				return tea.BatchMsg{notify("TUN 模式已保留"), p.refresh()}
			}
		},
		func() tea.Cmd {
			return sess.job("gateway", "回滚 TUN", func(ctx context.Context) (string, error) {
				return "已回滚", ws.RollbackGateway(ctx)
			}, p.refresh(), sess.refreshAll())
		})
}

func (p *settingsPanel) dnsListenLabel() string {
	l := p.sess.ws.GatewayDNSListen()
	if p.sess.ws.Settings().Gateway.DNSListen == "" {
		l += styleDim.Render("（默认）")
	}
	return l
}

func (p *settingsPanel) dnsListenHint() string {
	if p.sess.ws.IsOpenWrt() {
		return "OpenWrt 上由 dnsmasq 转发到这里 · enter 修改"
	}
	return "局域网设备把 DNS 指向本机时使用 · enter 修改"
}
