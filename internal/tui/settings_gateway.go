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

func (p *settingsPanel) gateway() appsettings.Gateway {
	return p.sess.ws.Settings().Gateway.WithDefaults()
}

func (p *settingsPanel) updateGateway(okText string, fn func(*appsettings.Gateway)) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	return sess.job("gateway", "应用网关设置", func(ctx context.Context) (string, error) {
		return okText, ws.UpdateGateway(ctx, fn)
	}, p.refresh(), sess.refreshAll())
}

func (p *settingsPanel) gatewayItems() []settingItem {
	g := p.gateway()
	acts := map[string]func() tea.Cmd{}
	for _, a := range p.gatewayActions() {
		acts[a.key] = a.run
	}
	var state string
	switch {
	case !p.state.gateway.pending.IsZero():
		state = styleWarn.Render("● 等待确认（" + p.state.gateway.pending.Format("15:04:05") + " 前不确认将回滚）")
	case g.Enabled:
		state = styleOK.Render("● 已开启") + styleDim.Render("  TUN（"+g.Stack+"）")
	default:
		state = styleDim.Render("● 未开启")
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
		{label: "状态", value: state, hint: "t 开启 / 关闭", edit: acts["t"]},
		{label: "协议栈", value: g.Stack, hint: "mixed 兼顾性能与兼容；enter 切换", edit: p.cycleStack},
		{label: "IP 协议", value: ipLabel, hint: "enter 切换", edit: func() tea.Cmd {
			return p.updateGateway("IP 协议已切换", func(gw *appsettings.Gateway) { gw.IPv6 = !gw.IPv6 })
		}},
		{label: "代理范围", value: scope, hint: "enter 切换黑名单 / 白名单", edit: func() tea.Cmd {
			return p.updateGateway("代理范围已切换", func(gw *appsettings.Gateway) {
				if gw.DeviceMode == appsettings.DeviceModeSelected {
					gw.DeviceMode = appsettings.DeviceModeAll
				} else {
					gw.DeviceMode = appsettings.DeviceModeSelected
				}
			})
		}},
	}
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
		settingItem{label: "添加设备", value: styleDim.Render(fmt.Sprintf("局域网中发现 %d 台", len(p.state.gateway.devices))), hint: "a 从列表选择或手动输入 MAC", edit: acts["a"]},
		settingItem{label: "大陆 IP", value: cn, hint: "enter 切换", edit: func() tea.Cmd {
			return p.updateGateway("大陆 IP 处理方式已切换", func(gw *appsettings.Gateway) { gw.BypassCN = !gw.BypassCN })
		}},
		settingItem{label: "DNS 监听", value: p.dnsListenLabel(), hint: p.dnsListenHint(), edit: func() tea.Cmd {
			return showForm(newInputForm("DNS 监听地址", func(v []string) tea.Cmd {
				return p.updateGateway("DNS 监听已修改", func(gw *appsettings.Gateway) { gw.DNSListen = v[0] })
			}).addText("地址", g.DNSListen, p.sess.ws.GatewayDNSListen(), "留空使用默认值；53 端口被占用时可改为局域网 IP，如 192.168.1.2:53"))
		}},
		settingItem{label: "排除地址", value: exclude, hint: "这些地址不经过代理", edit: func() tea.Cmd {
			return showForm(newInputForm("排除地址", func(v []string) tea.Cmd {
				return p.updateGateway("排除地址已修改", func(gw *appsettings.Gateway) { gw.ExcludeAddrs = splitList(v[0]) })
			}).addText("地址", strings.Join(g.ExcludeAddrs, ", "), "203.0.113.7, 10.0.0.0/8", "开启网关时会自动加入当前 SSH 客户端地址；多项用逗号分隔"))
		}},
	)
	if len(p.state.gateway.checks) > 0 {
		items = append(items, settingItem{label: "环境检查"})
	}
	for _, c := range p.state.gateway.checks {
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

func (p *settingsPanel) cycleStack() tea.Cmd {
	stacks := []string{"mixed", "system", "gvisor"}
	next := stacks[(slices.Index(stacks, p.gateway().Stack)+1)%len(stacks)]
	return p.updateGateway("协议栈："+next, func(gw *appsettings.Gateway) { gw.Stack = next })
}

func (p *settingsPanel) gatewayActions() []action {
	ws, sess := p.sess.ws, p.sess
	return []action{
		{"t", "开启 / 关闭网关", func() tea.Cmd {
			if p.gateway().Enabled {
				return askConfirm("关闭网关？\n局域网设备将不再经过代理，系统设置会被还原。", func() tea.Cmd {
					return sess.job("gateway", "关闭网关", func(ctx context.Context) (string, error) {
						return "网关已关闭", ws.DisableGateway(ctx)
					}, p.refresh(), sess.refreshAll())
				})
			}
			return sess.job("gateway", "开启网关", func(ctx context.Context) (string, error) {
				_, err := ws.EnableGateway(ctx)
				return "", err
			}, p.refresh(), sess.refreshAll(), func() tea.Msg {
				if d, ok := ws.PendingGateway(); ok {
					return gatewayPendingMsg{deadline: d}
				}
				return nil
			})
		}},
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

// confirmGateway asks to keep a just-enabled gateway; declining rolls back immediately.
func (p *settingsPanel) confirmGateway(deadline time.Time) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	p.promptedFor = deadline
	prompt := fmt.Sprintf("网关已开启。确认网络正常后按 y 保留；\n按 n 立即回滚。%s 前不确认会自动回滚，\nSSH 断开也会照常回滚。", deadline.Format("15:04:05"))
	return askChoice(prompt,
		func() tea.Cmd {
			return func() tea.Msg {
				if err := ws.ConfirmGateway(); err != nil {
					return notifyMsg{text: err.Error(), isErr: true}
				}
				return tea.BatchMsg{notify("网关已保留"), p.refresh()}
			}
		},
		func() tea.Cmd {
			return sess.job("gateway", "回滚网关", func(ctx context.Context) (string, error) {
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
		return "OpenWrt 上由 dnsmasq 转发到这里"
	}
	return "局域网设备把 DNS 指向本机时使用"
}
