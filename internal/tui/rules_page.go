package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

type rulesMsg struct {
	rules     []mihomoapi.Rule
	providers []mihomoapi.RuleProvider
	err       error
}

type rulesPage struct {
	sess      *session
	rules     []mihomoapi.Rule
	providers []mihomoapi.RuleProvider
	loadErr   error
	// showProviders switches the list from rules to rule providers.
	showProviders bool
	cursor        int
	offset        int
	filter        filterInput
}

func newRulesPage(sess *session) *rulesPage {
	return &rulesPage{sess: sess, filter: newFilterInput("过滤规则内容 / 类型 / 目标")}
}

func (p *rulesPage) title() string        { return "规则" }
func (p *rulesPage) capturingInput() bool { return p.filter.active }
func (p *rulesPage) activate() tea.Cmd    { return p.fetch() }

func (p *rulesPage) hints() []hint {
	if p.showProviders {
		return []hint{{"u", "更新选中规则集"}, {"U", "更新全部规则集"}, {"v", "查看规则"}, {"r", "刷新"}}
	}
	return []hint{{"/", "过滤"}, {"space", "临时停用 / 启用"}, {"v", "查看规则集"}, {"r", "刷新"}}
}

func (p *rulesPage) fetch() tea.Cmd {
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

func (p *rulesPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case rulesMsg:
		p.loadErr = msg.err
		if msg.rules != nil || msg.err == nil {
			p.rules, p.providers = msg.rules, msg.providers
		}
		p.clamp()
		return nil
	case tea.KeyPressMsg:
		if p.filter.active {
			cmd := p.filter.update(msg)
			p.cursor = 0
			return cmd
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *rulesPage) visibleRules() []mihomoapi.Rule {
	f := strings.ToLower(p.filter.value())
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

func (p *rulesPage) count() int {
	if p.showProviders {
		return len(p.providers)
	}
	return len(p.visibleRules())
}

func (p *rulesPage) clamp() { p.cursor = max(0, min(p.cursor, p.count()-1)) }

func (p *rulesPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	c := p.sess.client
	switch msg.String() {
	case "up", "k":
		p.cursor--
	case "down", "j":
		p.cursor++
	case "pgup", "ctrl+u":
		p.cursor -= 10
	case "pgdown", "ctrl+d":
		p.cursor += 10
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		p.cursor = p.count()
	case "v":
		p.showProviders = !p.showProviders
		p.cursor, p.offset = 0, 0
	case "r":
		return p.fetch()
	case "/":
		if !p.showProviders {
			return p.filter.start()
		}
	case "esc":
		p.filter.clear()
	case "U":
		if !p.showProviders || len(p.providers) == 0 {
			return nil
		}
		names := make([]string, len(p.providers))
		for i, pr := range p.providers {
			names[i] = pr.Name
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
	p.clamp()
	if p.count() == 0 {
		return nil
	}
	switch msg.String() {
	case "u":
		if !p.showProviders {
			return nil
		}
		name := p.providers[p.cursor].Name
		return p.sess.job("rule-providers", "更新规则集", func(ctx context.Context) (string, error) {
			return name + " 已更新", c.UpdateRuleProvider(ctx, name)
		}, p.fetch())
	case "space":
		if p.showProviders {
			return nil
		}
		r := p.visibleRules()[p.cursor]
		if r.Extra == nil {
			return notify("当前内核版本不支持临时停用规则")
		}
		disable := !r.Extra.Disabled
		label := map[bool]string{true: "已临时停用", false: "已启用"}[disable]
		return apiCall("修改规则失败", fmt.Sprintf("规则 %d %s（重载配置后恢复）", r.Index, label), func(ctx context.Context) error {
			return c.SetRulesDisabled(ctx, map[int]bool{r.Index: disable})
		}, p.fetch())
	}
	return nil
}

func (p *rulesPage) view(w, h int) string {
	tab := func(label string, on bool) string {
		if on {
			return styleSelected.Render(" " + label + " ")
		}
		return styleDim.Render(" " + label + " ")
	}
	header := styleTitle.Render("规则") + "  " + tab(fmt.Sprintf("规则 %d", len(p.rules)), !p.showProviders) + " " +
		tab(fmt.Sprintf("规则集 %d", len(p.providers)), p.showProviders) + styleDim.Render("  v 切换")
	header += "\n" + p.filter.line(w)
	if p.loadErr != nil && p.rules == nil {
		return header + "\n\n" + styleBad.Render("获取规则失败: "+p.loadErr.Error())
	}
	var lines []string
	if p.showProviders {
		lines = p.providerLines(w)
	} else {
		lines = p.ruleLines(w)
	}
	if len(lines) == 0 {
		return header + "\n\n" + styleDim.Render("没有内容")
	}
	listH := max(h-3, 1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listH {
		p.offset = p.cursor - listH + 1
	}
	p.offset = max(0, min(p.offset, len(lines)-listH))
	end := min(p.offset+listH, len(lines))
	return header + "\n\n" + strings.Join(lines[p.offset:end], "\n")
}

func (p *rulesPage) ruleLines(w int) []string {
	rules := p.visibleRules()
	payloadW := max(w-60, 16)
	lines := make([]string, 0, len(rules))
	for i, r := range rules {
		payload := r.Payload
		if payload == "" {
			payload = "-"
		}
		hits := ""
		if r.Extra != nil && r.Extra.HitCount > 0 {
			hits = fmt.Sprintf("命中 %d", r.Extra.HitCount)
		}
		line := fmt.Sprintf("%5d  %s %s  → %s  %s", r.Index, padRight(r.Type, 14),
			padRight(ansi.Truncate(payload, payloadW, "…"), payloadW), padRight(ansi.Truncate(r.Proxy, 18, "…"), 18), styleDim.Render(hits))
		disabled := r.Extra != nil && r.Extra.Disabled
		if disabled {
			line = styleDim.Render(ansi.Strip(line) + "  已停用")
		}
		if i == p.cursor {
			line = styleSelected.Render(padRight(ansi.Strip(line), w))
		}
		lines = append(lines, line)
	}
	return lines
}

func (p *rulesPage) providerLines(w int) []string {
	lines := make([]string, 0, len(p.providers))
	for i, pr := range p.providers {
		updated := "-"
		if !pr.UpdatedAt.IsZero() && pr.UpdatedAt.Year() > 1971 {
			updated = agoLabel(pr.UpdatedAt)
		}
		line := fmt.Sprintf(" %s  %s  %s  %s  更新于 %s", padRight(ansi.Truncate(pr.Name, 24, "…"), 24),
			padRight(pr.Behavior, 10), padRight(pr.VehicleType, 8), padLeft(fmt.Sprintf("%d 条", pr.RuleCount), 10), updated)
		if i == p.cursor {
			line = styleSelected.Render(padRight(line, w))
		}
		lines = append(lines, line)
	}
	return lines
}
