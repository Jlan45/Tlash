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

const rulesRefreshEvery = 5

const (
	rulesTabDetail = iota
	rulesTabProviders
)

type rulesMsg struct {
	rules     []mihomoapi.Rule
	providers []mihomoapi.RuleProvider
	err       error
}

type rulesPanel struct {
	panelBase
	sess      *session
	rules     []mihomoapi.Rule
	providers []mihomoapi.RuleProvider
	loadErr   error
	ticks     int
	provList  cursorList
}

func newRulesPanel(sess *session) *rulesPanel { return &rulesPanel{sess: sess} }

func (p *rulesPanel) title() string            { return "规则" }
func (p *rulesPanel) badge() string            { return fmt.Sprintf("%d", len(p.rules)) }
func (p *rulesPanel) tabs() []string           { return []string{"详情", "规则集"} }
func (p *rulesPanel) filterHint() string       { return "过滤规则内容 / 类型 / 目标" }
func (p *rulesPanel) activate() tea.Cmd        { return p.fetch() }
func (p *rulesPanel) interactive(tab int) bool { return tab == rulesTabProviders }

func (p *rulesPanel) fetch() tea.Cmd {
	c := p.sess.client
	return func() tea.Msg {
		ctx := context.Background()
		rules, err := c.Rules(ctx)
		if err != nil {
			return rulesMsg{err: err}
		}
		provMap, err := c.RuleProviders(ctx)
		provs := make([]mihomoapi.RuleProvider, 0, len(provMap))
		for _, pr := range provMap {
			provs = append(provs, pr)
		}
		slices.SortFunc(provs, func(a, b mihomoapi.RuleProvider) int { return cmp.Compare(a.Name, b.Name) })
		return rulesMsg{rules: rules, providers: provs, err: err}
	}
}

func (p *rulesPanel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case rulesMsg:
		p.loadErr = msg.err
		if msg.rules != nil || msg.err == nil {
			p.rules, p.providers = msg.rules, msg.providers
		}
	case statusMsg:
		if p.sess.activePanel == p.title() {
			p.ticks++
			if p.ticks%rulesRefreshEvery == 0 {
				return p.fetch()
			}
		}
	}
	return nil
}

func (p *rulesPanel) visible() []mihomoapi.Rule {
	f := strings.ToLower(p.ls.filter.value())
	if f == "" {
		return p.rules
	}
	var out []mihomoapi.Rule
	for _, r := range p.rules {
		if strings.Contains(strings.ToLower(r.Type+","+r.Payload+","+r.Proxy), f) {
			out = append(out, r)
		}
	}
	return out
}

func (p *rulesPanel) current() (mihomoapi.Rule, bool) {
	vis := p.visible()
	if c := p.ls.cursor; c >= 0 && c < len(vis) {
		return vis[c], true
	}
	return mihomoapi.Rule{}, false
}

func (p *rulesPanel) rows(w int) []string {
	vis := p.visible()
	out := make([]string, len(vis))
	for i, r := range vis {
		payload := r.Payload
		if payload != "" {
			payload += " "
		}
		line := ansi.Truncate(r.Type+" "+payload+"→ "+r.Proxy, w, "…")
		if r.Extra != nil && r.Extra.Disabled {
			line = styleDim.Render(ansi.Strip(line))
		}
		out[i] = line
	}
	return out
}

func (p *rulesPanel) updateAllProviders() tea.Cmd {
	c := p.sess.client
	names := make([]string, len(p.providers))
	for i, pr := range p.providers {
		names[i] = pr.Name
	}
	if len(names) == 0 {
		return notify("没有规则集")
	}
	return p.sess.job("rule-providers", "更新规则集", func(ctx context.Context) (string, error) {
		for _, n := range names {
			if err := c.UpdateRuleProvider(ctx, n); err != nil {
				return "", fmt.Errorf("%s: %w", n, err)
			}
		}
		return fmt.Sprintf("已更新 %d 个规则集", len(names)), nil
	}, p.fetch())
}

func (p *rulesPanel) actions() []action {
	c := p.sess.client
	acts := []action{
		{"U", "更新全部规则集", p.updateAllProviders},
		{"r", "刷新", p.fetch},
	}
	r, ok := p.current()
	if !ok {
		return acts
	}
	return append([]action{{"space", "临时停用 / 启用此规则", func() tea.Cmd {
		if r.Extra == nil {
			return notify("当前内核版本不支持临时停用规则")
		}
		disable := !r.Extra.Disabled
		label := map[bool]string{true: "已临时停用", false: "已启用"}[disable]
		return apiCall("修改规则失败", fmt.Sprintf("规则 %d %s（重载配置后恢复）", r.Index, label), func(ctx context.Context) error {
			return c.SetRulesDisabled(ctx, map[int]bool{r.Index: disable})
		}, p.fetch())
	}}}, acts...)
}

func (p *rulesPanel) mainActions(tab int) []action {
	if tab != rulesTabProviders {
		return nil
	}
	c := p.sess.client
	return []action{{"u", "更新选中规则集", func() tea.Cmd {
		p.provList.clamp(len(p.providers))
		if len(p.providers) == 0 {
			return nil
		}
		name := p.providers[p.provList.cursor].Name
		return p.sess.job("rule-providers", "更新规则集", func(ctx context.Context) (string, error) {
			return name + " 已更新", c.UpdateRuleProvider(ctx, name)
		}, p.fetch())
	}}}
}

func (p *rulesPanel) mainKey(tab int, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if tab != rulesTabProviders {
		return nil, false
	}
	return nil, p.provList.key(msg, len(p.providers))
}

func (p *rulesPanel) renderMain(tab, w, h int, focused bool) string {
	if p.loadErr != nil && p.rules == nil {
		return styleBad.Render("获取规则失败: " + p.loadErr.Error())
	}
	if tab == rulesTabProviders {
		if len(p.providers) == 0 {
			return styleDim.Render("配置中没有规则集（rule-providers）")
		}
		rows := make([]string, len(p.providers))
		for i, pr := range p.providers {
			updated := "-"
			if pr.UpdatedAt.Year() > 1971 {
				updated = agoLabel(pr.UpdatedAt)
			}
			rows[i] = fmt.Sprintf(" %s  %s  %s  %s  更新于 %s", padRight(ansi.Truncate(pr.Name, 24, "…"), 24),
				padRight(pr.Behavior, 10), padRight(pr.VehicleType, 8), padLeft(fmt.Sprintf("%d 条", pr.RuleCount), 10), updated)
		}
		head := styleDim.Render(fmt.Sprintf("%d 个规则集", len(p.providers)))
		if !focused {
			head += styleDim.Render(" · enter 进入后 u 更新选中")
		}
		return head + "\n\n" + strings.Join(p.provList.renderRows(rows, w, max(h-2, 1), focused), "\n")
	}

	r, ok := p.current()
	if !ok {
		return styleDim.Render("没有规则")
	}
	var b strings.Builder
	row := func(label, value string) { b.WriteString(styleDim.Render(padRight(label, 10)) + value + "\n") }
	row("序号", fmt.Sprintf("%d / %d", r.Index, len(p.rules)))
	row("类型", r.Type)
	payload := r.Payload
	if payload == "" {
		payload = styleDim.Render("（无）")
	}
	row("内容", payload)
	row("目标", styleAccent.Render(r.Proxy))
	if r.Extra != nil {
		hit := fmt.Sprintf("%d 次", r.Extra.HitCount)
		if r.Extra.HitCount > 0 && r.Extra.HitAt.After(time.Unix(0, 0)) {
			hit += styleDim.Render(" · 最近 " + agoLabel(r.Extra.HitAt))
		}
		row("命中", hit)
		state := styleOK.Render("启用")
		if r.Extra.Disabled {
			state = styleWarn.Render("已临时停用")
		}
		row("状态", state+styleDim.Render("（space 切换，重载配置后恢复）"))
	}
	if r.Type == "RuleSet" {
		for _, pr := range p.providers {
			if pr.Name == r.Payload {
				row("规则集", fmt.Sprintf("%s · %d 条 · %s", pr.Behavior, pr.RuleCount, pr.VehicleType))
			}
		}
	}
	return b.String()
}
