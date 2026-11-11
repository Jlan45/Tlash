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

type overridesPanel struct {
	panelBase
	sess    *session
	items   []override.Override
	loadErr error
	content fileCache

	// The preview is regenerated only after something that could change it.
	previewGen  int
	previewFor  int
	previewText string
	previewErr  error
}

func newOverridesPanel(sess *session) *overridesPanel { return &overridesPanel{sess: sess} }

func (p *overridesPanel) title() string     { return "覆写" }
func (p *overridesPanel) tabs() []string    { return []string{"内容", "最终配置预览"} }
func (p *overridesPanel) activate() tea.Cmd { return p.reload() }

func (p *overridesPanel) reload() tea.Cmd {
	store := p.sess.ws.Overrides
	return func() tea.Msg {
		items, err := store.List()
		return overridesMsg{items: items, err: err}
	}
}

func (p *overridesPanel) current() (override.Override, bool) {
	if c := p.ls.cursor; c >= 0 && c < len(p.items) {
		return p.items[c], true
	}
	return override.Override{}, false
}

func (p *overridesPanel) edit(o override.Override) tea.Cmd {
	return openInEditor(p.sess.ws.Overrides.ContentPath(o.ID), func(err error) tea.Msg { return overrideEditedMsg{id: o.ID, err: err} })
}

func (p *overridesPanel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case overridesMsg:
		p.items, p.loadErr = msg.items, msg.err
		p.ls.clamp(len(p.items))
		p.previewGen++
	case jobDoneMsg, profilesMsg:
		p.previewGen++
	case overrideEditedMsg:
		if msg.err != nil {
			return notifyErr("编辑器退出异常", msg.err)
		}
		ws := p.sess.ws
		return p.sess.job("override-edit", "应用覆写", func(ctx context.Context) (string, error) {
			return "覆写已保存", ws.ReapplyAfterOverrideEdit(ctx, msg.id)
		}, p.reload())
	}
	return nil
}

func (p *overridesPanel) actions() []action {
	ws, sess := p.sess.ws, p.sess
	acts := []action{
		{"a", "新建覆写", func() tea.Cmd {
			return showForm(newInputForm("新建覆写", func(v []string) tea.Cmd {
				o, err := ws.AddOverride(v[0])
				if err != nil {
					return notifyErr("新建失败", err)
				}
				return tea.Sequence(p.reload(), p.edit(o))
			}).addText("名称", "", "例如：自定义规则", "创建后打开编辑器；默认未启用，编辑完按 space 启用"))
		}},
		{"B", "回退到上一份运行配置", func() tea.Cmd {
			return askConfirm("回退到上一份运行配置？\n只交换生成的配置文件，不改动订阅和覆写。", func() tea.Cmd {
				return sess.job("config-rollback", "回退配置", func(ctx context.Context) (string, error) {
					return "已回退到上一份配置", ws.RollbackConfig(ctx)
				}, sess.refreshAll())
			})
		}},
	}
	cur, ok := p.current()
	if !ok {
		return acts
	}
	move := func(delta int) tea.Cmd {
		p.ls.move(delta, len(p.items))
		return sess.job("override-change", "调整顺序", func(ctx context.Context) (string, error) {
			return "", ws.MoveOverride(ctx, cur.ID, delta)
		}, p.reload())
	}
	return append([]action{
		{"space", map[bool]string{true: "停用", false: "启用"}[cur.Enabled], func() tea.Cmd {
			on := !cur.Enabled
			return sess.job("override-change", "应用覆写", func(ctx context.Context) (string, error) {
				return cur.Name + map[bool]string{true: " 已启用", false: " 已停用"}[on], ws.SetOverrideEnabled(ctx, cur.ID, on)
			}, p.reload(), sess.refreshAll())
		}},
		{"e", "用编辑器打开", func() tea.Cmd { return p.edit(cur) }},
		{"s", "作用范围：全部订阅 / 仅当前订阅", func() tea.Cmd {
			var scope []string
			if len(cur.Profiles) == 0 {
				active := ws.Settings().ActiveProfile
				if active == "" {
					return notify("当前没有启用的订阅")
				}
				scope = []string{active}
			}
			return sess.job("override-change", "应用覆写", func(ctx context.Context) (string, error) {
				return "已修改作用范围", ws.SetOverrideScope(ctx, cur.ID, scope)
			}, p.reload())
		}},
		{"J", "下移（越靠后优先级越高）", func() tea.Cmd { return move(1) }},
		{"K", "上移", func() tea.Cmd { return move(-1) }},
		{"n", "重命名", func() tea.Cmd {
			return showForm(newInputForm("重命名覆写", func(v []string) tea.Cmd {
				if err := ws.RenameOverride(cur.ID, v[0]); err != nil {
					return notifyErr("重命名失败", err)
				}
				return p.reload()
			}).addText("名称", cur.Name, "", ""))
		}},
		{"d", "删除", func() tea.Cmd {
			return askConfirm(fmt.Sprintf("删除覆写「%s」？", cur.Name), func() tea.Cmd {
				return sess.job("override-change", "删除覆写", func(ctx context.Context) (string, error) {
					return "已删除 " + cur.Name, ws.DeleteOverride(ctx, cur.ID)
				}, p.reload(), sess.refreshAll())
			})
		}},
	}, acts...)
}

func (p *overridesPanel) scopeLabel(o override.Override) string {
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

func (p *overridesPanel) rows(w int) []string {
	nameW := max(w-14, 6)
	out := make([]string, len(p.items))
	for i, o := range p.items {
		mark := styleDim.Render("○")
		if o.Enabled {
			mark = styleOK.Render("●")
		}
		scope := "全部"
		if len(o.Profiles) > 0 {
			scope = "限定"
		}
		out[i] = mark + " " + padRight(ansi.Truncate(o.Name, nameW, "…"), nameW) + " " + styleDim.Render(scope)
	}
	return out
}

func (p *overridesPanel) renderMain(tab, w, h int, focused bool) string {
	if tab == 1 {
		if p.previewFor != p.previewGen || p.previewText == "" && p.previewErr == nil {
			b, err := p.sess.ws.Generate()
			p.previewText, p.previewErr, p.previewFor = string(b), err, p.previewGen
		}
		if p.previewErr != nil {
			return styleBad.Render("生成失败: " + p.previewErr.Error())
		}
		return styleDim.Render("# 订阅 → 覆写（按顺序）→ 接管设置；未经 mihomo 校验") + "\n" + p.previewText
	}
	if p.loadErr != nil {
		return styleBad.Render("读取覆写失败: " + p.loadErr.Error())
	}
	cur, ok := p.current()
	if !ok {
		return styleDim.Render("还没有覆写。按 a 新建，例如在订阅规则前插入自定义规则：") + "\n\n" +
			styleDim.Render("  +rules:\n    - DOMAIN-SUFFIX,example.com,DIRECT")
	}
	path := p.sess.ws.Overrides.ContentPath(cur.ID)
	content, err := p.content.read(path)
	if err != nil {
		return styleBad.Render("读取失败: " + err.Error())
	}
	state := styleDim.Render("停用")
	if cur.Enabled {
		state = styleOK.Render("启用")
	}
	head := fmt.Sprintf("%s  %s · %s", styleBold.Render(cur.Name), state, p.scopeLabel(cur))
	return head + "\n" + styleDim.Render("# "+path+"（e 编辑）") + "\n" + content
}
