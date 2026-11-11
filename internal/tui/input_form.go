package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type formField struct {
	label string
	hint  string
	input textinput.Model
	// toggle fields flip between "是"/"否" on space instead of taking text.
	toggle bool
}

// inputForm is a small modal form. It owns the keyboard until submitted or cancelled.
type inputForm struct {
	title    string
	fields   []formField
	focus    int
	onSubmit func(values []string) tea.Cmd
	err      string
}

func newInputForm(title string, onSubmit func([]string) tea.Cmd) *inputForm {
	return &inputForm{title: title, onSubmit: onSubmit}
}

func (f *inputForm) addText(label, value, placeholder, hint string) *inputForm {
	ti := newTextInput("", placeholder)
	ti.SetValue(value)
	ti.CharLimit = 4096
	f.fields = append(f.fields, formField{label: label, hint: hint, input: ti})
	if len(f.fields) == 1 {
		f.fields[0].input.Focus()
	}
	return f
}

func (f *inputForm) addToggle(label string, on bool, hint string) *inputForm {
	ti := textinput.New()
	ti.SetValue(boolLabel(on))
	f.fields = append(f.fields, formField{label: label, hint: hint, input: ti, toggle: true})
	return f
}

func boolLabel(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func (f *inputForm) values() []string {
	out := make([]string, len(f.fields))
	for i, fl := range f.fields {
		out[i] = strings.TrimSpace(fl.input.Value())
	}
	return out
}

func (f *inputForm) setFocus(i int) tea.Cmd {
	f.fields[f.focus].input.Blur()
	f.focus = (i + len(f.fields)) % len(f.fields)
	if f.fields[f.focus].toggle {
		return nil
	}
	return f.fields[f.focus].input.Focus()
}

// update returns done=true when the form closed (submitted or cancelled).
func (f *inputForm) update(msg tea.KeyPressMsg) (cmd tea.Cmd, done bool) {
	switch msg.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		return f.setFocus(f.focus + 1), false
	case "shift+tab", "up":
		return f.setFocus(f.focus - 1), false
	case "enter":
		if f.focus < len(f.fields)-1 {
			return f.setFocus(f.focus + 1), false
		}
		return f.onSubmit(f.values()), true
	case "ctrl+s":
		return f.onSubmit(f.values()), true
	}
	cur := &f.fields[f.focus]
	if cur.toggle {
		if s := msg.String(); s == "space" || s == "left" || s == "right" || s == "y" || s == "n" {
			cur.input.SetValue(boolLabel(cur.input.Value() != "是"))
		}
		return nil, false
	}
	cur.input, cmd = cur.input.Update(msg)
	return cmd, false
}

func (f *inputForm) view(w int) string {
	labelW := 0
	for _, fl := range f.fields {
		labelW = max(labelW, ansi.StringWidth(fl.label))
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(f.title) + "\n\n")
	for i, fl := range f.fields {
		label := padRight(fl.label, labelW+2)
		fl.input.SetWidth(max(w-labelW-6, 10))
		var val string
		if fl.toggle {
			val = "[" + fl.input.Value() + "]"
			if i == f.focus {
				val = styleSelected.Render(val) + styleDim.Render("  空格切换")
			}
		} else {
			val = fl.input.View()
		}
		if i == f.focus {
			label = styleAccent.Render("▸ " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label + val + "\n")
		if fl.hint != "" {
			b.WriteString(strings.Repeat(" ", labelW+4) + styleDim.Render(fl.hint) + "\n")
		}
	}
	b.WriteString("\n" + renderHints([]hint{{"tab", "下一项"}, {"enter", "下一项 / 提交"}, {"ctrl+s", "提交"}, {"esc", "取消"}}))
	return b.String()
}

func (f *inputForm) hints() []hint {
	return []hint{{"tab", "下一项"}, {"ctrl+s", "提交"}, {"esc", "取消"}}
}
