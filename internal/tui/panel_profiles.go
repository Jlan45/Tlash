package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/profile"
	"github.com/Jlan45/Tlash/internal/workspace"
)

type profilesMsg struct {
	profiles []profile.Profile
	err      error
}

type editorDoneMsg struct {
	id  string
	err error
}

type profilesPanel struct {
	panelBase
	sess     *session
	profiles []profile.Profile
	loadErr  error
	content  fileCache
}

func newProfilesPanel(sess *session) *profilesPanel { return &profilesPanel{sess: sess} }

func (p *profilesPanel) title() string     { return "订阅" }
func (p *profilesPanel) tabs() []string    { return []string{"信息", "内容"} }
func (p *profilesPanel) activate() tea.Cmd { return p.reload() }

func (p *profilesPanel) reload() tea.Cmd {
	store := p.sess.ws.Profiles
	return func() tea.Msg {
		ps, err := store.List()
		return profilesMsg{profiles: ps, err: err}
	}
}

func (p *profilesPanel) current() (profile.Profile, bool) {
	if c := p.ls.cursor; c >= 0 && c < len(p.profiles) {
		return p.profiles[c], true
	}
	return profile.Profile{}, false
}

func (p *profilesPanel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case profilesMsg:
		p.profiles, p.loadErr = msg.profiles, msg.err
		p.ls.clamp(len(p.profiles))
	case editorDoneMsg:
		if msg.err != nil {
			return notifyErr("编辑器退出异常", msg.err)
		}
		ws := p.sess.ws
		return p.sess.job("profile-edit", "校验修改后的配置", func(ctx context.Context) (string, error) {
			return "修改已生效", ws.ReapplyAfterEdit(ctx, msg.id)
		}, p.reload())
	}
	return nil
}

func (p *profilesPanel) actions() []action {
	ws, sess := p.sess.ws, p.sess
	acts := []action{
		{"a", "添加订阅链接", func() tea.Cmd { return showForm(p.addRemoteForm()) }},
		{"i", "导入本地配置", func() tea.Cmd { return showForm(p.importForm()) }},
		{"U", "更新全部订阅", p.updateAll},
	}
	cur, ok := p.current()
	if !ok {
		return acts
	}
	return append([]action{
		{"space", "启用此订阅", func() tea.Cmd {
			if cur.ID == ws.Settings().ActiveProfile {
				return notify(cur.Name + " 已在使用中")
			}
			return sess.job("profile-activate", "切换订阅", func(ctx context.Context) (string, error) {
				return "已启用 " + cur.Name, ws.Activate(ctx, cur.ID)
			}, p.reload(), sess.refreshAll())
		}},
		{"u", "更新此订阅", func() tea.Cmd {
			if cur.Kind != profile.KindRemote {
				return notify("本地配置无法在线更新，按 e 编辑")
			}
			return sess.job("profile-update-"+cur.ID, "更新 "+cur.Name, func(ctx context.Context) (string, error) {
				_, err := ws.UpdateProfile(ctx, cur.ID)
				return cur.Name + " 已更新", err
			}, p.reload(), sess.refreshAll())
		}},
		{"e", "用编辑器打开", func() tea.Cmd { return p.openEditor(cur) }},
		{"m", "修改名称 / 链接 / UA / 更新间隔", func() tea.Cmd { return showForm(p.editMetaForm(cur)) }},
		{"d", "删除此订阅", func() tea.Cmd {
			return askConfirm(fmt.Sprintf("删除订阅「%s」？", cur.Name), func() tea.Cmd {
				return sess.job("profile-delete", "删除订阅", func(ctx context.Context) (string, error) {
					return "已删除 " + cur.Name, ws.DeleteProfile(ctx, cur.ID)
				}, p.reload(), sess.refreshAll())
			})
		}},
	}, acts...)
}

func (p *profilesPanel) updateAll() tea.Cmd {
	ws := p.sess.ws
	var remote []profile.Profile
	for _, pr := range p.profiles {
		if pr.Kind == profile.KindRemote {
			remote = append(remote, pr)
		}
	}
	if len(remote) == 0 {
		return notify("没有可在线更新的订阅")
	}
	return p.sess.job("profile-update-all", "更新全部订阅", func(ctx context.Context) (string, error) {
		var failed []string
		for _, pr := range remote {
			if _, err := ws.UpdateProfile(ctx, pr.ID); err != nil {
				failed = append(failed, pr.Name+": "+err.Error())
			}
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("%d/%d 个失败 — %s", len(failed), len(remote), strings.Join(failed, "；"))
		}
		return fmt.Sprintf("已更新 %d 个订阅", len(remote)), nil
	}, p.reload(), p.sess.refreshAll())
}

func (p *profilesPanel) addRemoteForm() *inputForm {
	ws := p.sess.ws
	return newInputForm("添加订阅", func(v []string) tea.Cmd {
		in := workspace.NewRemote{URL: v[0], Name: v[1], UserAgent: v[2], UseProxy: v[3] == "是"}
		return p.sess.job("profile-add", "下载订阅", func(ctx context.Context) (string, error) {
			pr, err := ws.AddRemote(ctx, in)
			return "已添加 " + pr.Name, err
		}, p.reload(), p.sess.refreshAll())
	}).
		addText("订阅链接", "", "https://...", "需要 Clash / mihomo 格式").
		addText("名称", "", "留空则使用订阅提供的名称", "").
		addText("User-Agent", "", profile.DefaultUserAgent, "部分机场根据 UA 返回不同格式").
		addToggle("通过内核代理下载", false, "订阅地址被墙时开启（需内核正在运行）")
}

func (p *profilesPanel) importForm() *inputForm {
	ws := p.sess.ws
	return newInputForm("导入本地配置", func(v []string) tea.Cmd {
		return p.sess.job("profile-add", "导入配置", func(ctx context.Context) (string, error) {
			pr, err := ws.AddLocal(ctx, expandHome(v[0]), v[1])
			return "已导入 " + pr.Name, err
		}, p.reload(), p.sess.refreshAll())
	}).
		addText("文件路径", "", "/path/to/config.yaml", "文件会被复制到 Tlash 数据目录").
		addText("名称", "", "留空则使用文件名", "")
}

func (p *profilesPanel) editMetaForm(cur profile.Profile) *inputForm {
	ws := p.sess.ws
	f := newInputForm("修改订阅", func(v []string) tea.Cmd {
		next := cur
		if v[0] != "" {
			next.Name = v[0]
		}
		if cur.Kind == profile.KindRemote {
			next.URL, next.UserAgent, next.UseProxy = v[1], v[2], v[4] == "是"
			hours, err := strconv.ParseFloat(strings.TrimSpace(v[3]), 64)
			if err != nil || hours < 0 {
				return notify("更新间隔需要是非负数字（小时）")
			}
			next.UpdateInterval = time.Duration(hours * float64(time.Hour))
		}
		return func() tea.Msg {
			if err := ws.SaveProfileMeta(next); err != nil {
				return notifyMsg{text: "保存失败: " + err.Error(), isErr: true}
			}
			return tea.BatchMsg{notify("已保存"), p.reload()}
		}
	}).addText("名称", cur.Name, "", "")
	if cur.Kind == profile.KindRemote {
		f.addText("订阅链接", cur.URL, "", "").
			addText("User-Agent", cur.UserAgent, profile.DefaultUserAgent, "").
			addText("更新间隔（小时）", strconv.FormatFloat(cur.UpdateInterval.Hours(), 'f', -1, 64), "0", "0 表示不自动更新（定时任务在后续版本提供）").
			addToggle("通过内核代理下载", cur.UseProxy, "")
	}
	return f
}

func (p *profilesPanel) openEditor(cur profile.Profile) tea.Cmd {
	return openInEditor(p.sess.ws.Profiles.ContentPath(cur.ID), func(err error) tea.Msg { return editorDoneMsg{id: cur.ID, err: err} })
}

func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return home + "/" + rest
		}
	}
	return path
}

func (p *profilesPanel) rows(w int) []string {
	active := p.sess.ws.Settings().ActiveProfile
	nameW := max(w-16, 6)
	out := make([]string, len(p.profiles))
	for i, pr := range p.profiles {
		mark := " "
		if pr.ID == active {
			mark = styleAccent.Render("●")
		}
		var info string
		switch {
		case pr.Kind == profile.KindLocal:
			info = styleDim.Render("本地")
		case pr.Usage != nil && pr.Usage.Total > 0:
			info = usagePercent(pr.Usage)
		default:
			info = styleDim.Render(agoLabel(pr.UpdatedAt))
		}
		out[i] = mark + " " + padRight(ansi.Truncate(pr.Name, nameW, "…"), nameW) + " " + info
	}
	return out
}

func usagePercent(u *profile.Usage) string {
	ratio := float64(u.Used()) / float64(u.Total)
	style := styleOK
	if ratio > 0.9 {
		style = styleBad
	} else if ratio > 0.7 {
		style = styleWarn
	}
	return style.Render(fmt.Sprintf("%3.0f%%", ratio*100))
}

func (p *profilesPanel) renderMain(tab, w, h int, focused bool) string {
	if p.loadErr != nil {
		return styleBad.Render("读取订阅失败: " + p.loadErr.Error())
	}
	cur, ok := p.current()
	if !ok {
		return styleDim.Render("还没有订阅。按 a 添加订阅链接，或按 i 导入本地配置文件。")
	}
	if tab == 1 {
		path := p.sess.ws.Profiles.ContentPath(cur.ID)
		content, err := p.content.read(path)
		if err != nil {
			return styleBad.Render("读取失败: " + err.Error())
		}
		return styleDim.Render("# "+path+"（原始订阅，Tlash 不改动；e 用编辑器打开）") + "\n" + content
	}
	return p.detail(cur)
}

func (p *profilesPanel) detail(pr profile.Profile) string {
	var b strings.Builder
	row := func(label, value string) { b.WriteString(styleDim.Render(padRight(label, 10)) + value + "\n") }
	name := styleBold.Render(pr.Name)
	if pr.ID == p.sess.ws.Settings().ActiveProfile {
		name += styleAccent.Render("  ● 使用中")
	}
	row("名称", name)
	if pr.Kind == profile.KindRemote {
		row("链接", maskURL(pr.URL))
		ua := pr.UserAgent
		if ua == "" {
			ua = profile.DefaultUserAgent + styleDim.Render("（默认）")
		}
		row("UA", ua)
		interval := "不自动更新"
		if pr.UpdateInterval > 0 {
			interval = fmt.Sprintf("每 %s 小时", strconv.FormatFloat(pr.UpdateInterval.Hours(), 'f', -1, 64))
		}
		row("更新间隔", interval)
		if pr.UseProxy {
			row("下载方式", "通过内核代理")
		}
	} else {
		row("类型", "本地配置")
	}
	if pr.Usage != nil {
		u := pr.Usage
		row("流量", usageLabel(u)+fmt.Sprintf("  已用 %s / 共 %s（↑ %s ↓ %s）", formatBytes(u.Used()), totalLabel(u.Total), formatBytes(u.Upload), formatBytes(u.Download)))
		if !u.Expire.IsZero() {
			row("到期", expireLabel(u.Expire))
		}
	}
	row("更新于", agoLabel(pr.UpdatedAt))
	row("文件", p.sess.ws.Profiles.ContentPath(pr.ID))
	return b.String()
}

// maskURL hides the path and query, which usually carry the access token.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "（无效链接）"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

func usageLabel(u *profile.Usage) string {
	if u == nil {
		return styleDim.Render("无流量信息")
	}
	if u.Total <= 0 {
		return "已用 " + formatBytes(u.Used())
	}
	ratio := float64(u.Used()) / float64(u.Total)
	const barW = 8
	filled := min(int(ratio*barW+0.5), barW)
	style := styleOK
	if ratio > 0.9 {
		style = styleBad
	} else if ratio > 0.7 {
		style = styleWarn
	}
	return style.Render(strings.Repeat("█", filled)) + styleDim.Render(strings.Repeat("░", barW-filled)) +
		fmt.Sprintf(" %.0f%%", ratio*100)
}

func totalLabel(total int64) string {
	if total <= 0 {
		return "不限"
	}
	return formatBytes(total)
}

func expireLabel(t time.Time) string {
	days := int(time.Until(t).Hours() / 24)
	label := t.Format("2006-01-02")
	switch {
	case time.Now().After(t):
		return styleBad.Render(label + "（已过期）")
	case days < 7:
		return styleWarn.Render(fmt.Sprintf("%s（剩 %d 天）", label, days))
	default:
		return fmt.Sprintf("%s（剩 %d 天）", label, days)
	}
}

func agoLabel(t time.Time) string {
	if t.IsZero() {
		return "从未"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
