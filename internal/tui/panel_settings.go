package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/corebinary"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/webui"
	"github.com/Jlan45/Tlash/internal/workspace"
)

var dashboards = []string{"", "zashboard", "metacubexd"}

type geoFile struct {
	name    string
	size    int64
	modTime time.Time
}

type settingsStateMsg struct {
	schedLabel string
	schedOn    bool
	schedErr   error
	geoFiles   []geoFile
	taskLog    []string
	loginEnv   bool
	gateway    gatewayState
}

type latestCoreMsg struct {
	release corebinary.Release
	err     error
}

type dnsQueryMsg struct {
	query  string
	result mihomoapi.DNSResult
	err    error
}

// settingItem is one row in a settings category; step handles ←/→ (and enter) for
// switchable values, while edit handles enter for text inputs or actions.
type settingItem struct {
	label string
	value string
	hint  string
	step  func(delta int) tea.Cmd
	edit  func() tea.Cmd
}

// settingsCategory is one entry in the side list.
type settingsCategory struct {
	name    string
	items   func() []settingItem
	actions func() []action
	// footer renders below the rows (e.g. the last DNS lookup).
	footer func() string
}

type settingsPanel struct {
	panelBase
	sess       *session
	state      settingsStateMsg
	categories []settingsCategory
	cursors    map[string]*cursorList

	checkingCore bool
	progress     *atomic.Int64
	lookup       string
	// promptedFor is the pending gateway deadline already asked about.
	promptedFor time.Time
}

func newSettingsPanel(sess *session) *settingsPanel {
	p := &settingsPanel{sess: sess, cursors: map[string]*cursorList{}}
	if sess.ws != nil {
		p.categories = []settingsCategory{
			{name: "内核", items: p.coreItems, actions: p.coreActions},
			{name: "接管", items: func() []settingItem { return p.managedItems(managedRows) }, actions: p.managedActions},
			{name: "TUN", items: p.gatewayItems, actions: p.gatewayActions},
			{name: "DNS", items: func() []settingItem { return p.takeoverItems(dnsRows, "dns") }},
			{name: "嗅探", items: func() []settingItem { return p.takeoverItems(snifferRows, "sniffer") }},
			{name: "资源", items: p.resourceItems, actions: p.resourceActions},
			{name: "定时任务", items: p.taskItems, actions: p.taskActions},
			{name: "环境变量", items: p.envItems},
		}
	}
	p.categories = append(p.categories, settingsCategory{name: "工具", items: p.toolItems, footer: func() string { return p.lookup }})
	return p
}

func (p *settingsPanel) title() string        { return "设置" }
func (p *settingsPanel) tabs() []string       { return []string{"设置项"} }
func (p *settingsPanel) interactive(int) bool { return true }

func (p *settingsPanel) activate() tea.Cmd {
	if p.sess.ws == nil {
		return nil
	}
	return tea.Batch(p.refresh(), fetchCoreState(p.sess.ws, true))
}

func (p *settingsPanel) refresh() tea.Cmd {
	ws := p.sess.ws
	if ws == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st := settingsStateMsg{geoFiles: listGeoFiles(ws.RuntimeDir), taskLog: ws.TaskLogTail(5), loginEnv: workspace.LoginEnvEnabled(), gateway: loadGatewayState(ws)}
		if sched, err := ws.Scheduler(); err != nil {
			st.schedErr = err
		} else {
			st.schedLabel, st.schedOn = sched.Label(), sched.Installed(ctx)
		}
		return st
	}
}

func (p *settingsPanel) category() settingsCategory {
	p.ls.clamp(len(p.categories))
	return p.categories[p.ls.cursor]
}

func (p *settingsPanel) cursor(name string) *cursorList {
	c, ok := p.cursors[name]
	if !ok {
		c = &cursorList{}
		p.cursors[name] = c
	}
	return c
}

func (p *settingsPanel) rows(int) []string {
	out := make([]string, len(p.categories))
	for i, c := range p.categories {
		out[i] = c.name
	}
	return out
}

func (p *settingsPanel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case settingsStateMsg:
		p.state = msg
		// Reopened the TUI while a gateway enable is still unconfirmed: ask again.
		if d := msg.gateway.pending; !d.IsZero() && !d.Equal(p.promptedFor) {
			return p.confirmGateway(d)
		}
	case gatewayPendingMsg:
		if !msg.deadline.Equal(p.promptedFor) {
			return p.confirmGateway(msg.deadline)
		}
	case dnsQueryMsg:
		p.lookup = renderDNSResult(msg)
	case latestCoreMsg:
		p.checkingCore = false
		if msg.err != nil {
			return notifyErr("检查更新失败", msg.err)
		}
		return p.confirmDownload(msg.release)
	case jobDoneMsg:
		if strings.HasPrefix(msg.key, "tasks") || msg.key == "geo-update" || msg.key == "gateway" || msg.key == "login-env" {
			return p.refresh()
		}
	}
	return nil
}

func (p *settingsPanel) actions() []action {
	if c := p.category(); c.actions != nil {
		return c.actions()
	}
	return nil
}

func (p *settingsPanel) mainActions(int) []action {
	return []action{{"enter", "修改 / 切换选中项", func() tea.Cmd {
		items := p.category().items()
		cur := p.cursor(p.category().name)
		cur.clamp(len(items))
		if len(items) == 0 {
			return nil
		}
		it := items[cur.cursor]
		if it.step != nil {
			return it.step(1)
		}
		if it.edit != nil {
			return it.edit()
		}
		return nil
	}}}
}

func (p *settingsPanel) mainKey(_ int, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	items := p.category().items()
	cur := p.cursor(p.category().name)
	if cur.key(msg, len(items)) {
		return nil, true
	}
	cur.clamp(len(items))
	switch msg.String() {
	case "left":
		if len(items) > 0 && items[cur.cursor].step != nil {
			return items[cur.cursor].step(-1), true
		}
		return nil, true
	case "right":
		if len(items) > 0 && items[cur.cursor].step != nil {
			return items[cur.cursor].step(1), true
		}
		return nil, true
	}
	return nil, false
}

func (p *settingsPanel) renderMain(_ int, w, h int, focused bool) string {
	c := p.category()
	items := c.items()
	cur := p.cursor(c.name)
	labelW := 4
	for _, it := range items {
		labelW = max(labelW, len([]rune(it.label))*2)
	}
	labelW = min(labelW+2, 18)
	rows := make([]string, len(items))
	for i, it := range items {
		line := " " + padRight(it.label, labelW) + it.value
		if i == cur.cursor && focused && it.hint != "" {
			line += styleDim.Render("  " + it.hint)
		}
		rows[i] = line
	}
	head := styleTitle.Render(c.name)
	if !focused && len(items) > 0 {
		head += styleDim.Render("  enter 进入后逐项修改")
	}
	footer := ""
	if c.footer != nil {
		footer = c.footer()
	}
	bodyH := max(h-2-strings.Count(footer, "\n")-1, 1)
	out := head + "\n\n" + strings.Join(cur.renderRows(rows, w, bodyH, focused), "\n")
	if footer != "" {
		out += "\n\n" + footer
	}
	return out
}

func joinHint(base, suffix string) string {
	if base == "" {
		return suffix
	}
	return base + " · " + suffix
}

// managedItems turns managed rows into editable items.
func (p *settingsPanel) managedItems(rows []managedRow) []settingItem {
	m := p.sess.ws.Settings().Managed
	items := make([]settingItem, len(rows))
	for i, r := range rows {
		val, set := r.get(m)
		if !set {
			val = styleDim.Render("使用订阅值")
			if r.clear == nil {
				val = styleDim.Render("未设置")
			}
		}
		r := r
		if r.isBool || r.options != nil {
			items[i] = settingItem{label: r.label, value: val, hint: joinHint(r.hint, "←→ 切换"), step: func(delta int) tea.Cmd { return stepManaged(p.sess, r, delta) }}
		} else {
			items[i] = settingItem{label: r.label, value: val, hint: joinHint(r.hint, "enter 修改"), edit: func() tea.Cmd { return editManaged(p.sess, r) }}
		}
	}
	return items
}

func (p *settingsPanel) managedActions() []action {
	return []action{{"z", "选中项改用订阅自带值", func() tea.Cmd {
		cur := p.cursor("接管")
		cur.clamp(len(managedRows))
		row := managedRows[cur.cursor]
		if row.clear == nil {
			return notify(row.label + " 始终由 Tlash 接管")
		}
		return applyManaged(p.sess, row.label+" 改为使用订阅自带值", row.clear)
	}}}
}

func (p *settingsPanel) takeoverManaged(block string) bool {
	m := p.sess.ws.Settings().Managed
	if block == "dns" {
		return m.DNS != nil
	}
	return m.Sniffer != nil
}

func (p *settingsPanel) takeoverItems(rows []managedRow, block string) []settingItem {
	if !p.takeoverManaged(block) {
		return []settingItem{{label: "状态", value: styleDim.Render("使用订阅自带的配置"), hint: "←→ 切换（由 Tlash 接管）", step: func(int) tea.Cmd { return p.toggleTakeover(block) }}}
	}
	note := "覆盖订阅 dns 块中的这些项，其他项保留"
	if block == "sniffer" {
		note = "整体替换订阅的 sniffer 块"
	}
	items := []settingItem{{label: "状态", value: styleOK.Render("由 Tlash 接管"), hint: note + " · ←→ 交还订阅", step: func(int) tea.Cmd { return p.toggleTakeover(block) }}}
	return append(items, p.managedItems(rows)...)
}

func (p *settingsPanel) toggleTakeover(block string) tea.Cmd {
	name := map[string]string{"dns": "DNS", "sniffer": "嗅探"}[block]
	if p.takeoverManaged(block) {
		return askConfirm(fmt.Sprintf("交还 %s 设置给订阅？\nTlash 的 %s 设置会被清除。", name, name), func() tea.Cmd {
			return applyManaged(p.sess, name+" 已改用订阅自带配置", func(m *appsettings.Managed) {
				if block == "dns" {
					m.DNS = nil
				} else {
					m.Sniffer = nil
				}
			})
		})
	}
	return applyManaged(p.sess, name+" 已由 Tlash 接管（推荐默认值）", func(m *appsettings.Managed) {
		if block == "dns" {
			m.DNS = appsettings.DefaultDNS()
		} else {
			m.Sniffer = appsettings.DefaultSniffer()
		}
	})
}

func (p *settingsPanel) coreItems() []settingItem {
	s, ws := p.sess, p.sess.ws
	settings := ws.Settings()
	acts := map[string]func() tea.Cmd{}
	for _, a := range p.coreActions() {
		acts[a.key] = a.run
	}
	version := s.core.version
	switch {
	case !s.core.installed:
		version = styleWarn.Render("未安装")
	case version == "":
		version = styleDim.Render("读取中…")
	}
	if _, busy := s.busy["core-binary"]; busy && p.progress != nil {
		version += styleWarn.Render("  下载中 " + formatBytes(p.progress.Load()))
	} else if p.checkingCore {
		version += styleDim.Render("  检查更新中…")
	}
	prev := styleDim.Render("无")
	if s.core.prevVersion != "" {
		prev = s.core.prevVersion
	}
	mirror := "直连 GitHub"
	if settings.Mirror != "" {
		mirror = settings.Mirror
	}
	state := styleDim.Render("● 已停止")
	if s.core.running {
		state = styleOK.Render("● 运行中")
	}
	items := []settingItem{
		{label: "当前版本", value: version, hint: "D 下载 / 升级", edit: acts["D"]},
		{label: "上一版本", value: prev, hint: "b 回滚", edit: acts["b"]},
		{label: "更新通道", value: channelLabel(settings.Core.Channel), hint: "←→ 切换（切换后按 D 下载）", step: func(int) tea.Cmd { return p.toggleChannel() }},
		{label: "下载镜像", value: mirror, hint: "enter 修改", edit: p.editMirror},
		{label: "存放位置", value: coreLocationLabel(settings.Core.Location) + styleDim.Render("  数据目录剩余 "+freeLabel(ws.FreeSpace())), hint: "闪存小的路由器可改为压缩存放或只放内存；←→ 切换", step: p.cycleCoreLocation},
		{label: "托管方式", value: s.core.kind.Label()},
		{label: "运行状态", value: state, hint: "在 1 状态 面板按 s 启停"},
	}
	if s.core.kind != "process" {
		items = append(items, settingItem{label: "开机自启", value: onOff(s.core.autoStart), hint: "←→ 切换", step: func(int) tea.Cmd { return p.toggleAutoStart() }})
	}
	return append(items, settingItem{label: "内核日志", value: s.core.logHint})
}

func (p *settingsPanel) coreActions() []action {
	ws, sess := p.sess.ws, p.sess
	return []action{
		{"D", "下载 / 升级内核", func() tea.Cmd {
			if p.checkingCore {
				return nil
			}
			p.checkingCore = true
			return func() tea.Msg {
				r, err := ws.LatestCore(context.Background())
				return latestCoreMsg{release: r, err: err}
			}
		}},
		{"b", "回滚到上一版本", func() tea.Cmd {
			if sess.core.prevVersion == "" {
				return notify("没有可回滚的旧版本")
			}
			return askConfirm(fmt.Sprintf("回滚内核到 %s？", sess.core.prevVersion), func() tea.Cmd {
				return sess.job("core-binary", "回滚内核",
					func(ctx context.Context) (string, error) { return "已回滚", ws.RollbackCore(ctx) }, sess.refreshAll())
			})
		}},
		{"i", "导入本地内核文件", func() tea.Cmd {
			return showForm(newInputForm("导入本地内核", func(v []string) tea.Cmd {
				return sess.job("core-binary", "导入内核", func(ctx context.Context) (string, error) {
					ver, err := ws.ImportCore(ctx, expandHome(v[0]))
					return "已安装内核 " + ver, err
				}, sess.refreshAll())
			}).addText("文件路径", "", "/path/to/mihomo 或 mihomo-linux-*.gz", "支持未解压的 .gz 发布包"))
		}},
	}
}

func (p *settingsPanel) toggleChannel() tea.Cmd {
	ws := p.sess.ws
	next := appsettings.ChannelAlpha
	if ws.Settings().Core.Channel == appsettings.ChannelAlpha {
		next = appsettings.ChannelStable
	}
	if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Core.Channel = next }); err != nil {
		return notifyErr("保存失败", err)
	}
	return notify("已切换到" + channelLabel(next) + "，按 D 下载")
}

func (p *settingsPanel) editMirror() tea.Cmd {
	ws := p.sess.ws
	return showForm(newInputForm("下载镜像", func(v []string) tea.Cmd {
		mirror := v[0]
		if mirror != "" && !strings.HasPrefix(mirror, "http://") && !strings.HasPrefix(mirror, "https://") {
			return notify("镜像地址需要以 http:// 或 https:// 开头")
		}
		if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Mirror = mirror }); err != nil {
			return notifyErr("保存失败", err)
		}
		return notify("已保存下载镜像")
	}).addText("镜像前缀", ws.Settings().Mirror, "https://ghfast.top/", "拼接在 github.com 链接前；留空直连 GitHub"))
}

func (p *settingsPanel) toggleAutoStart() tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	on := !sess.core.autoStart
	svc := ws.Service()
	return sess.job("core-autostart", "设置开机自启", func(ctx context.Context) (string, error) {
		if err := svc.Install(ctx); err != nil {
			return "", err
		}
		if err := svc.SetAutoStart(ctx, on); err != nil {
			return "", err
		}
		return "开机自启已" + map[bool]string{true: "开启", false: "关闭"}[on], nil
	}, fetchCoreState(ws, false))
}

func (p *settingsPanel) confirmDownload(r corebinary.Release) tea.Cmd {
	sess, ws := p.sess, p.sess.ws
	prompt := fmt.Sprintf("下载 mihomo %s（%s）？\n%s", r.Version, channelLabel(r.Channel), r.Asset)
	if r.Version == sess.core.version {
		prompt = fmt.Sprintf("已是最新版本 %s，重新下载？", r.Version)
	}
	if sess.core.running {
		prompt += "\n完成后内核会自动重启。"
	}
	return askConfirm(prompt, func() tea.Cmd {
		progress := &atomic.Int64{}
		p.progress = progress
		return sess.job("core-binary", "下载内核", func(ctx context.Context) (string, error) {
			res, err := ws.InstallCore(ctx, r, progress)
			if err != nil {
				return "", err
			}
			if !res.Verified {
				return "已安装 " + res.Version + "（无法获取校验和，未校验）", nil
			}
			return "已安装 " + res.Version + "（sha256 校验通过）", nil
		}, sess.refreshAll())
	})
}

func channelLabel(c appsettings.CoreChannel) string {
	if c == appsettings.ChannelAlpha {
		return "预览版（Alpha）"
	}
	return "稳定版"
}

func (p *settingsPanel) resourceItems() []settingItem {
	settings := p.sess.ws.Settings()
	acts := map[string]func() tea.Cmd{}
	for _, a := range p.resourceActions() {
		acts[a.key] = a.run
	}
	var items []settingItem
	if len(p.state.geoFiles) == 0 {
		items = append(items, settingItem{label: "GEO 数据库", value: styleDim.Render("未下载（内核会在规则用到时自动下载）"), hint: "u 立即更新", edit: acts["u"]})
	}
	for i, f := range p.state.geoFiles {
		label := ""
		if i == 0 {
			label = "GEO 数据库"
		}
		items = append(items, settingItem{label: label, value: fmt.Sprintf("%s  %s  %s", padRight(f.name, 18), padLeft(formatBytes(f.size), 9), styleDim.Render(agoLabel(f.modTime))), hint: "u 立即更新", edit: acts["u"]})
	}
	mirror := "直连 GitHub"
	if settings.Managed.ResourceMirror != "" {
		mirror = settings.Managed.ResourceMirror
	}
	panel := dashboardLabel(settings.Managed.Dashboard)
	if settings.Managed.Dashboard != "" {
		if c := settings.Managed.ExternalController; c != "" {
			panel += "  " + styleAccent.Render("http://"+c+"/ui")
		} else {
			panel += "  " + styleWarn.Render("需要在“接管”里开启 TCP 控制接口才能访问")
		}
	}
	return append(items,
		settingItem{label: "资源下载", value: mirror, hint: "←→ 切换（使用内核的下载镜像）", step: func(int) tea.Cmd { return p.toggleResourceMirror() }},
		settingItem{label: "Web 面板", value: panel, hint: "←→ 切换 · P 在线更新到最新版 · 访问地址运行 tlash web 查看", step: p.switchDashboard},
	)
}

func (p *settingsPanel) resourceActions() []action {
	ws, sess := p.sess.ws, p.sess
	return []action{
		{"u", "立即更新 GEO 数据库", func() tea.Cmd {
			if !sess.core.running {
				return notify("内核未运行，启动后才能更新 GEO 数据库")
			}
			return sess.job("geo-update", "更新 GEO 数据库", func(ctx context.Context) (string, error) {
				if err := ws.Client().UpgradeGeo(ctx); err != nil {
					return "", err
				}
				_ = ws.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.LastGeoUpdate = time.Now() })
				return "GEO 数据库已更新", nil
			})
		}},
		{"P", "在线更新 Web 面板到最新版", func() tea.Cmd {
			name := ws.Settings().Managed.Dashboard
			if name == "" {
				return notify("还没有选择 Web 面板")
			}
			if !sess.core.running {
				return notify("内核未运行，启动后才能在线更新面板")
			}
			return sess.job("ui-update", "在线更新面板", func(ctx context.Context) (string, error) {
				if err := ws.Client().UpgradeUI(ctx); err != nil {
					return "", err
				}
				// No longer the embedded copy: don't let a later start swap it back.
				webui.ClearStamp(ws.DashboardDir(name))
				return name + " 已更新到最新版", nil
			}, p.refresh())
		}},
	}
}

func (p *settingsPanel) toggleResourceMirror() tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	settings := ws.Settings()
	mirror := ""
	if settings.Managed.ResourceMirror == "" {
		if settings.Mirror == "" {
			return notify("请先在“内核”里设置下载镜像")
		}
		mirror = settings.Mirror
	}
	return applyManaged(sess, map[bool]string{true: "资源下载已改走镜像", false: "资源下载已改为直连"}[mirror != ""],
		func(m *appsettings.Managed) { m.ResourceMirror = mirror })
}

func (p *settingsPanel) switchDashboard(delta int) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	if text, busy := coreConfigBusy(sess); busy {
		return notify(text + "，请稍候")
	}
	cur := ws.Settings().Managed.Dashboard
	idx := slices.Index(dashboards, cur)
	if idx < 0 {
		idx = 0
	}
	n := len(dashboards)
	next := dashboards[(idx+delta%n+n)%n]
	return sess.job("ui-update", "切换 Web 面板", func(ctx context.Context) (string, error) {
		if err := ws.UpdateManaged(ctx, func(m *appsettings.Managed) { m.Dashboard = next }); err != nil {
			return "", err
		}
		if _, embedded := webui.Embedded(); next == webui.Name && embedded {
			if err := ws.EnsureDashboard(); err != nil {
				return "", err
			}
			return "Web 面板：" + dashboardLabel(next), nil
		}
		if next != "" && sess.core.running {
			if err := ws.Client().UpgradeUI(ctx); err != nil {
				return "", err
			}
		}
		return "Web 面板：" + dashboardLabel(next), nil
	}, p.refresh(), sess.refreshAll())
}

func (p *settingsPanel) taskItems() []settingItem {
	settings := p.sess.ws.Settings()
	acts := map[string]func() tea.Cmd{}
	for _, a := range p.taskActions() {
		acts[a.key] = a.run
	}
	st := p.state
	var state string
	switch {
	case st.schedErr != nil:
		state = styleBad.Render(st.schedErr.Error())
	case st.schedOn:
		state = styleOK.Render("已开启") + styleDim.Render("  "+st.schedLabel+" · 每 15 分钟检查")
	default:
		state = styleDim.Render("未开启（" + st.schedLabel + "）")
	}
	geo := "不自动更新"
	if iv := settings.Tasks.GeoUpdateInterval; iv > 0 {
		geo = fmt.Sprintf("每 %s 小时", strconv.FormatFloat(iv.Hours(), 'f', -1, 64))
	}
	items := []settingItem{
		{label: "状态", value: state, hint: "←→ 切换", step: func(int) tea.Cmd { return p.toggleTasks() }},
		{label: "GEO 自动更新", value: geo, hint: "enter 修改", edit: p.editGeoInterval},
		{label: "订阅自动更新", value: styleDim.Render("在 2 订阅 面板按 m 为每个订阅设置间隔")},
	}
	if len(st.taskLog) == 0 {
		items = append(items, settingItem{label: "最近记录", value: styleDim.Render("暂无"), hint: "R 立即执行一次", edit: acts["R"]})
	}
	for i, l := range st.taskLog {
		label := ""
		if i == 0 {
			label = "最近记录"
		}
		items = append(items, settingItem{label: label, value: styleDim.Render(l), hint: "R 立即执行一次", edit: acts["R"]})
	}
	return items
}

func (p *settingsPanel) taskActions() []action {
	ws, sess := p.sess.ws, p.sess
	return []action{
		{"R", "立即执行到期任务", func() tea.Cmd {
			return sess.job("tasks-run", "执行到期任务", func(ctx context.Context) (string, error) {
				return "到期任务已执行", ws.RunDue(ctx, time.Now())
			}, sess.refreshAll())
		}},
	}
}

func (p *settingsPanel) toggleTasks() tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	on := !p.state.schedOn
	return sess.job("tasks", "设置定时任务", func(ctx context.Context) (string, error) {
		return "定时任务已" + map[bool]string{true: "开启", false: "关闭"}[on], ws.SetTasksEnabled(ctx, on)
	})
}

func (p *settingsPanel) editGeoInterval() tea.Cmd {
	ws := p.sess.ws
	cur := strconv.FormatFloat(ws.Settings().Tasks.GeoUpdateInterval.Hours(), 'f', -1, 64)
	return showForm(newInputForm("GEO 自动更新间隔", func(v []string) tea.Cmd {
		hours, err := strconv.ParseFloat(v[0], 64)
		if err != nil || hours < 0 {
			return notify("需要是非负数字（小时）")
		}
		if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.GeoUpdateInterval = time.Duration(hours * float64(time.Hour)) }); err != nil {
			return notifyErr("保存失败", err)
		}
		return notify("已保存")
	}).addText("间隔（小时）", cur, "24", "0 表示不自动更新；需要开启定时任务"))
}

func (p *settingsPanel) envItems() []settingItem {
	on := p.state.loginEnv
	return []settingItem{
		{label: "当前终端", value: styleAccent.Render(`eval "$(tlash env)"`), hint: `取消：eval "$(tlash env --unset)"`},
		{label: "登录时自动设置", value: onOff(on), hint: "←→ 切换（写入 " + workspace.LoginEnvPath + "）", step: func(int) tea.Cmd { return p.toggleLoginEnv() }},
	}
}

func (p *settingsPanel) toggleLoginEnv() tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	on := !p.state.loginEnv
	return sess.job("login-env", "设置登录环境变量", func(context.Context) (string, error) {
		if err := ws.SetLoginEnv(on); err != nil {
			return "", err
		}
		return "已" + map[bool]string{true: "写入 ", false: "删除 "}[on] + workspace.LoginEnvPath, nil
	})
}

func (p *settingsPanel) toolItems() []settingItem {
	c := p.sess.client
	var items []settingItem
	if ws := p.sess.ws; ws != nil {
		items = append(items, settingItem{label: "自动测速", value: onOff(!ws.Settings().AutoDelayTestOff),
			hint: "代理组面板停在某个组上时，超过 10 分钟没测过就自动测速；←→ 切换", step: func(int) tea.Cmd {
				off := !ws.Settings().AutoDelayTestOff
				if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.AutoDelayTestOff = off }); err != nil {
					return notifyErr("保存失败", err)
				}
				return notify("自动测速已" + map[bool]string{true: "关闭", false: "开启"}[off])
			}})
	}
	return append(items, []settingItem{
		{label: "DNS 查询", value: styleDim.Render("通过内核解析域名，结果反映当前 DNS 配置"), hint: "enter 查询", edit: func() tea.Cmd {
			return showForm(newInputForm("DNS 查询", func(v []string) tea.Cmd {
				name, qtype := v[0], strings.ToUpper(v[1])
				if qtype == "" {
					qtype = "A"
				}
				return func() tea.Msg {
					r, err := c.DNSQuery(context.Background(), name, qtype)
					return dnsQueryMsg{query: name + " " + qtype, result: r, err: err}
				}
			}).addText("域名", "", "example.com", "").addText("类型", "A", "A / AAAA / CNAME / TXT …", ""))
		}},
		{label: "清空 Fake-IP 缓存", value: "", hint: "enter 清空", edit: func() tea.Cmd {
			return apiCall("清空失败", "已清空 Fake-IP 缓存", func(ctx context.Context) error { return c.FlushFakeIP(ctx) })
		}},
		{label: "清空 DNS 缓存", value: "", hint: "enter 清空", edit: func() tea.Cmd {
			return apiCall("清空失败", "已清空 DNS 缓存", func(ctx context.Context) error { return c.FlushDNS(ctx) })
		}},
	}...)
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

func listGeoFiles(dir string) []geoFile {
	var out []geoFile
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".dat" && ext != ".mmdb" && ext != ".metadb" {
			continue
		}
		if info, err := e.Info(); err == nil {
			out = append(out, geoFile{name: e.Name(), size: info.Size(), modTime: info.ModTime()})
		}
	}
	return out
}

func dashboardLabel(d string) string {
	switch d {
	case "":
		return "不安装"
	case webui.Name:
		if v, ok := webui.Embedded(); ok {
			return d + "（内置 " + v + "）"
		}
	}
	return d + "（在线下载）"
}

var coreLocations = []string{appsettings.CoreInData, appsettings.CoreCompressed, appsettings.CoreRAMOnly}

func coreLocationLabel(loc string) string {
	switch loc {
	case appsettings.CoreCompressed:
		return "压缩存放（开机解压到内存，约占原大小 1/3）"
	case appsettings.CoreRAMOnly:
		return "只放内存（不占闪存，开机自动下载，不能回滚）"
	}
	return "数据目录"
}

func freeLabel(n int64) string {
	if n < 0 {
		return "未知"
	}
	return formatBytes(n)
}

func (p *settingsPanel) cycleCoreLocation(delta int) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	cur := ws.Settings().Core.Location
	idx := slices.Index(coreLocations, cur)
	if idx < 0 {
		idx = 0
	}
	n := len(coreLocations)
	next := coreLocations[(idx+delta%n+n)%n]
	if !sess.core.installed {
		if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Core.Location = next }); err != nil {
			return notifyErr("保存失败", err)
		}
		return tea.Batch(notify("存放位置："+coreLocationLabel(next)), fetchCoreState(ws, true))
	}
	prompt := fmt.Sprintf("内核改为%s？", coreLocationLabel(next))
	if next == appsettings.CoreRAMOnly {
		prompt += "\n开机时需要能访问 GitHub（或已设置的下载镜像）才能下载内核。"
	}
	return askConfirm(prompt, func() tea.Cmd {
		return sess.job("core-binary", "移动内核", func(ctx context.Context) (string, error) {
			return "内核已改为" + coreLocationLabel(next), ws.SetCoreLocation(ctx, next)
		}, sess.refreshAll())
	})
}
