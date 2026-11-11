package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/override"
)

type overridesMsg struct {
	items []override.Override
	err   error
}

type overrideEditedMsg struct {
	id  string
	err error
}

type previewMsg struct {
	text string
	err  error
}

type overridesPage struct {
	sess    *session
	items   []override.Override
	loadErr error
	cursor  int
	form    *inputForm
	viewer  *textViewer
}

func newOverridesPage(sess *session) *overridesPage { return &overridesPage{sess: sess} }

func (p *overridesPage) title() string        { return "覆写" }
func (p *overridesPage) capturingInput() bool { return p.form != nil || p.viewer != nil }
func (p *overridesPage) activate() tea.Cmd    { return p.reload() }

func (p *overridesPage) hints() []hint {
	switch {
	case p.form != nil:
		return p.form.hints()
	case p.viewer != nil:
		return p.viewer.hints()
	}
	return []hint{
		{"a", "新建覆写"},
		{"space", "启用 / 停用"},
		{"e", "编辑"},
		{"p", "预览最终配置"},
		{"s", "作用范围：全部订阅 / 仅当前订阅"},
		{"J/K", "下移 / 上移（越靠后优先级越高）"},
		{"r", "重命名"},
		{"d", "删除"},
		{"B", "回退到上一份运行配置"},
	}
}

func (p *overridesPage) reload() tea.Cmd {
	store := p.sess.ws.Overrides
	return func() tea.Msg {
		items, err := store.List()
		return overridesMsg{items: items, err: err}
	}
}

func (p *overridesPage) current() (override.Override, bool) {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return override.Override{}, false
	}
	return p.items[p.cursor], true
}

func (p *overridesPage) edit(o override.Override) tea.Cmd {
	return openInEditor(p.sess.ws.Overrides.ContentPath(o.ID), func(err error) tea.Msg { return overrideEditedMsg{id: o.ID, err: err} })
}

func (p *overridesPage) update(msg tea.Msg) tea.Cmd {
	ws := p.sess.ws
	switch msg := msg.(type) {
	case overridesMsg:
		p.items, p.loadErr = msg.items, msg.err
		p.cursor = max(0, min(p.cursor, len(p.items)-1))
		return nil
	case overrideEditedMsg:
		if msg.err != nil {
			return notifyErr("编辑器退出异常", msg.err)
		}
		return p.sess.job("override-edit", "应用覆写", func(ctx context.Context) (string, error) {
			return "覆写已保存", ws.ReapplyAfterOverrideEdit(ctx, msg.id)
		}, p.reload())
	case previewMsg:
		if msg.err != nil {
			return notifyErr("生成配置失败", msg.err)
		}
		p.viewer = newTextViewer("最终配置预览（未校验）", msg.text)
		return nil
	case tea.KeyPressMsg:
		switch {
		case p.form != nil:
			cmd, done := p.form.update(msg)
			if done {
				p.form = nil
			}
			return cmd
		case p.viewer != nil:
			if p.viewer.update(msg) {
				p.viewer = nil
			}
			return nil
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *overridesPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "down", "j":
		p.cursor = min(p.cursor+1, max(len(p.items)-1, 0))
		return nil
	case "a":
		p.form = newInputForm("新建覆写", func(v []string) tea.Cmd {
			o, err := ws.AddOverride(v[0])
			if err != nil {
				return notifyErr("新建失败", err)
			}
			return tea.Sequence(p.reload(), p.edit(o))
		}).addText("名称", "", "例如：自定义规则", "创建后会打开编辑器；默认未启用，编辑完按 space 启用")
		return nil
	case "p":
		return func() tea.Msg {
			b, err := ws.Generate()
			return previewMsg{text: string(b), err: err}
		}
	case "B":
		return askConfirm("回退到上一份运行配置？\n只交换生成的配置文件，不改动订阅和覆写。", func() tea.Cmd {
			return sess.job("config-rollback", "回退配置", func(ctx context.Context) (string, error) {
				return "已回退到上一份配置", ws.RollbackConfig(ctx)
			}, sess.refreshAll())
		})
	}
	cur, ok := p.current()
	if !ok {
		return nil
	}
	switch msg.String() {
	case "space", "enter":
		on := !cur.Enabled
		return sess.job("override-toggle", "应用覆写", func(ctx context.Context) (string, error) {
			return cur.Name + map[bool]string{true: " 已启用", false: " 已停用"}[on], ws.SetOverrideEnabled(ctx, cur.ID, on)
		}, p.reload(), sess.refreshAll())
	case "e":
		return p.edit(cur)
	case "s":
		var scope []string
		if len(cur.Profiles) == 0 {
			active := ws.Settings().ActiveProfile
			if active == "" {
				return notify("当前没有启用的订阅")
			}
			scope = []string{active}
		}
		return sess.job("override-toggle", "应用覆写", func(ctx context.Context) (string, error) {
			return "已修改作用范围", ws.SetOverrideScope(ctx, cur.ID, scope)
		}, p.reload())
	case "J", "K":
		delta := 1
		if msg.String() == "K" {
			delta = -1
		}
		p.cursor = max(0, min(p.cursor+delta, len(p.items)-1))
		return sess.job("override-toggle", "调整顺序", func(ctx context.Context) (string, error) {
			return "", ws.MoveOverride(ctx, cur.ID, delta)
		}, p.reload())
	case "r":
		p.form = newInputForm("重命名覆写", func(v []string) tea.Cmd {
			if err := ws.RenameOverride(cur.ID, v[0]); err != nil {
				return notifyErr("重命名失败", err)
			}
			return p.reload()
		}).addText("名称", cur.Name, "", "")
	case "d":
		return askConfirm(fmt.Sprintf("删除覆写「%s」？", cur.Name), func() tea.Cmd {
			return sess.job("override-toggle", "删除覆写", func(ctx context.Context) (string, error) {
				return "已删除 " + cur.Name, ws.DeleteOverride(ctx, cur.ID)
			}, p.reload(), sess.refreshAll())
		})
	}
	return nil
}

func (p *overridesPage) scopeLabel(o override.Override) string {
	if len(o.Profiles) == 0 {
		return "全部订阅"
	}
	names := make([]string, 0, len(o.Profiles))
	for _, id := range o.Profiles {
		if pr, err := p.sess.ws.Profiles.Get(id); err == nil {
			names = append(names, pr.Name)
		} else {
			names = append(names, "(已删除)")
		}
	}
	return "仅 " + strings.Join(names, "、")
}

func (p *overridesPage) view(w, h int) string {
	switch {
	case p.form != nil:
		return p.form.view(w)
	case p.viewer != nil:
		return p.viewer.view(w, h)
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("覆写") + styleDim.Render("  按顺序合并到订阅之上，接管设置最后生效") + "\n\n")
	if p.loadErr != nil {
		return b.String() + styleBad.Render("读取覆写失败: "+p.loadErr.Error())
	}
	if len(p.items) == 0 {
		b.WriteString(styleDim.Render("还没有覆写。按 a 新建，例如在订阅规则前插入自定义规则：") + "\n\n")
		b.WriteString(styleDim.Render("  +rules:\n    - DOMAIN-SUFFIX,example.com,DIRECT") + "\n")
		return b.String()
	}
	nameW := 8
	for _, o := range p.items {
		nameW = max(nameW, ansi.StringWidth(o.Name))
	}
	nameW = min(nameW, max(w-30, 8), 32)
	for i, o := range p.items {
		mark, state := styleDim.Render("○"), styleDim.Render("停用")
		if o.Enabled {
			mark, state = styleOK.Render("●"), styleOK.Render("启用")
		}
		line := fmt.Sprintf(" %s %2d. %s  %s  %s", mark, i+1, padRight(ansi.Truncate(o.Name, nameW, "…"), nameW), state, styleDim.Render(p.scopeLabel(o)))
		if i == p.cursor {
			line = styleSelected.Render(padRight(ansi.Strip(line), w))
		}
		b.WriteString(line + "\n")
	}
	if cur, ok := p.current(); ok {
		b.WriteString("\n" + styleDim.Render("文件 "+p.sess.ws.Overrides.ContentPath(cur.ID)) + "\n")
	}
	return b.String()
}
