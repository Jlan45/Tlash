package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// openFormMsg asks the shell to show a form as a popup.
type openFormMsg struct{ form *inputForm }

func showForm(f *inputForm) tea.Cmd { return func() tea.Msg { return openFormMsg{form: f} } }

// actionMenu is the x popup: every action available in the current context.
type actionMenu struct {
	title  string
	items  []action
	cursor int
}

// update returns the command to run and whether the menu should close.
func (m *actionMenu) update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k := msg.String(); k {
	case "esc", "q", "x":
		return nil, true
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, len(m.items)-1)
	case "enter":
		if len(m.items) == 0 {
			return nil, true
		}
		return m.items[m.cursor].run(), true
	default:
		// Pressing an action's own key inside the menu runs it too.
		for _, it := range m.items {
			if it.key == k {
				return it.run(), true
			}
		}
	}
	return nil, false
}

func (m *actionMenu) view() string {
	keyW := 0
	for _, it := range m.items {
		keyW = max(keyW, ansi.StringWidth(it.key))
	}
	lines := []string{styleTitle.Render(m.title), ""}
	for i, it := range m.items {
		line := " " + styleKey.Render(padRight(it.key, keyW)) + "  " + it.desc + " "
		if i == m.cursor {
			line = styleSelected.Render(" " + padRight(it.key, keyW) + "  " + it.desc + " ")
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", styleDim.Render("enter 执行 · esc 关闭"))
	return strings.Join(lines, "\n")
}

func popupBox(content string, border color.Color) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 2).Render(content)
}

// overlay draws box centered over bg (a w×h screen).
func overlay(bg, box string, w, h int) string {
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	if bw > w {
		box = fitBlock(box, w, min(bh, h))
		bw, bh = w, min(bh, h)
	}
	x, y := max((w-bw)/2, 0), max((h-bh)/3, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(bg),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

func renderConfirm(prompt string) string {
	return popupBox(prompt+"\n\n"+renderHints([]hint{{"y", "确认"}, {"n", "取消"}}), colorWarn)
}
