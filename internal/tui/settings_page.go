package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/workspace"
)

var dashboards = []string{"", "metacubexd", "zashboard"}

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
}

type settingsPage struct {
	sess  *session
	state settingsStateMsg
	form  *inputForm
}

func newSettingsPage(sess *session) *settingsPage { return &settingsPage{sess: sess} }

func (p *settingsPage) title() string        { return "设置" }
func (p *settingsPage) capturingInput() bool { return p.form != nil }
func (p *settingsPage) activate() tea.Cmd    { return p.refresh() }

func (p *settingsPage) hints() []hint {
	if p.form != nil {
		return p.form.hints()
	}
	return []hint{
		{"G", "立即更新 GEO 数据库"},
		{"m", "资源下载走镜像"},
		{"w", "Web 面板"},
		{"t", "定时任务开关"},
		{"g", "GEO 自动更新间隔"},
		{"r", "立即执行到期任务"},
		{"e", "登录时自动设置代理变量"},
	}
}

func (p *settingsPage) refresh() tea.Cmd {
	ws := p.sess.ws
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st := settingsStateMsg{geoFiles: listGeoFiles(ws.RuntimeDir), taskLog: ws.TaskLogTail(5), loginEnv: workspace.LoginEnvEnabled()}
		if sched, err := ws.Scheduler(); err != nil {
			st.schedErr = err
		} else {
			st.schedLabel, st.schedOn = sched.Label(), sched.Installed(ctx)
		}
		return st
	}
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

func (p *settingsPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case settingsStateMsg:
		p.state = msg
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

func (p *settingsPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	settings := ws.Settings()
	switch msg.String() {
	case "G":
		if !sess.core.running {
			return notify("内核未运行，启动后才能更新 GEO 数据库")
		}
		return sess.job("geo-update", "更新 GEO 数据库", func(ctx context.Context) (string, error) {
			if err := ws.Client().UpgradeGeo(ctx); err != nil {
				return "", err
			}
			_ = ws.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.LastGeoUpdate = time.Now() })
			return "GEO 数据库已更新", nil
		}, p.refresh())
	case "m":
		mirror := ""
		if settings.Managed.ResourceMirror == "" {
			if settings.Mirror == "" {
				return notify("请先在内核页按 M 设置下载镜像")
			}
			mirror = settings.Mirror
		}
		return applyManaged(sess, map[bool]string{true: "资源下载已改走镜像", false: "资源下载已改为直连"}[mirror != ""],
			func(m *appsettings.Managed) { m.ResourceMirror = mirror })
	case "w":
		next := dashboards[(slices.Index(dashboards, settings.Managed.Dashboard)+1)%len(dashboards)]
		apply := applyManaged(sess, "Web 面板："+dashboardLabel(next), func(m *appsettings.Managed) { m.Dashboard = next })
		if next == "" || !sess.core.running {
			return apply
		}
		// Download the panel right away once the new config is live.
		return tea.Sequence(apply, sess.job("ui-update", "下载 Web 面板", func(ctx context.Context) (string, error) {
			return dashboardLabel(next) + " 已下载", ws.Client().UpgradeUI(ctx)
		}))
	case "t":
		on := !p.state.schedOn
		return sess.job("tasks", "设置定时任务", func(ctx context.Context) (string, error) {
			return "定时任务已" + map[bool]string{true: "开启", false: "关闭"}[on], ws.SetTasksEnabled(ctx, on)
		}, p.refresh())
	case "g":
		cur := strconv.FormatFloat(settings.Tasks.GeoUpdateInterval.Hours(), 'f', -1, 64)
		p.form = newInputForm("GEO 自动更新间隔", func(v []string) tea.Cmd {
			hours, err := strconv.ParseFloat(v[0], 64)
			if err != nil || hours < 0 {
				return notify("需要是非负数字（小时）")
			}
			if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.GeoUpdateInterval = time.Duration(hours * float64(time.Hour)) }); err != nil {
				return notifyErr("保存失败", err)
			}
			return notify("已保存")
		}).addText("间隔（小时）", cur, "24", "0 表示不自动更新；需要开启定时任务")
	case "r":
		return sess.job("tasks-run", "执行到期任务", func(ctx context.Context) (string, error) {
			return "到期任务已执行", ws.RunDue(ctx, time.Now())
		}, p.refresh(), sess.refreshAll())
	case "e":
		on := !p.state.loginEnv
		return func() tea.Msg {
			if err := ws.SetLoginEnv(on); err != nil {
				return notifyMsg{text: err.Error(), isErr: true}
			}
			return tea.BatchMsg{notify("已" + map[bool]string{true: "写入 ", false: "删除 "}[on] + workspace.LoginEnvPath), p.refresh()}
		}
	}
	return nil
}

func dashboardLabel(d string) string {
	if d == "" {
		return "不安装"
	}
	return d
}

func (p *settingsPage) view(w, h int) string {
	if p.form != nil {
		return p.form.view(w)
	}
	ws, st := p.sess.ws, p.state
	settings := ws.Settings()
	var b strings.Builder
	row := func(label, value string) { b.WriteString("  " + styleDim.Render(padRight(label, 14)) + value + "\n") }

	b.WriteString(styleTitle.Render("资源") + "\n")
	if len(st.geoFiles) == 0 {
		row("GEO 数据库", styleDim.Render("未下载（内核会在规则用到时自动下载）"))
	}
	for i, f := range st.geoFiles {
		label := ""
		if i == 0 {
			label = "GEO 数据库"
		}
		row(label, fmt.Sprintf("%s  %s  %s", padRight(f.name, 18), padLeft(formatBytes(f.size), 9), styleDim.Render(agoLabel(f.modTime))))
	}
	row("", styleDim.Render("G 立即更新"))
	mirror := "直连 GitHub"
	if settings.Managed.ResourceMirror != "" {
		mirror = settings.Managed.ResourceMirror
	}
	row("资源下载", mirror+styleDim.Render("  m 切换（使用内核页的下载镜像）"))
	panel := dashboardLabel(settings.Managed.Dashboard)
	if settings.Managed.Dashboard != "" {
		if c := settings.Managed.ExternalController; c != "" {
			panel += "  " + styleAccent.Render("http://"+c+"/ui") + styleDim.Render("  密钥见内核页")
		} else {
			panel += "  " + styleWarn.Render("需要在内核页开启 TCP 控制接口才能访问")
		}
	}
	row("Web 面板", panel+styleDim.Render("  w 切换"))

	b.WriteString("\n" + styleTitle.Render("定时任务") + styleDim.Render("  每 "+"15 分钟检查一次") + "\n")
	switch {
	case st.schedErr != nil:
		row("状态", styleBad.Render(st.schedErr.Error()))
	case st.schedOn:
		row("状态", styleOK.Render("已开启")+styleDim.Render("  "+st.schedLabel+" · t 关闭"))
	default:
		row("状态", styleDim.Render("未开启")+styleDim.Render("  t 开启（"+st.schedLabel+"）"))
	}
	geo := "不自动更新"
	if iv := settings.Tasks.GeoUpdateInterval; iv > 0 {
		geo = fmt.Sprintf("每 %s 小时", strconv.FormatFloat(iv.Hours(), 'f', -1, 64))
	}
	row("GEO 自动更新", geo+styleDim.Render("  g 修改"))
	row("订阅自动更新", styleDim.Render("在订阅页按 m 为每个订阅设置更新间隔"))
	if len(st.taskLog) == 0 {
		row("最近记录", styleDim.Render("暂无 · r 立即执行一次"))
	}
	for i, l := range st.taskLog {
		label := ""
		if i == 0 {
			label = "最近记录"
		}
		row(label, styleDim.Render(l))
	}

	b.WriteString("\n" + styleTitle.Render("代理环境变量") + "\n")
	row("当前终端", styleAccent.Render(`eval "$(tlash env)"`)+styleDim.Render("   取消：eval \"$(tlash env --unset)\""))
	row("登录时自动设置", onOff(st.loginEnv)+styleDim.Render("  e 切换（写入 "+workspace.LoginEnvPath+"，需 root）"))
	return b.String()
}
