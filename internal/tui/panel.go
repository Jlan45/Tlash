package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// action is a single-key command. One table per panel drives key handling, the
// status-bar hints, the x menu and the ? help, so they can never disagree.
type action struct {
	key  string
	desc string
	run  func() tea.Cmd
}

// panel is one box in the left column plus the main-panel tabs describing its selection.
type panel interface {
	title() string
	// badge follows the title, e.g. a count.
	badge() string
	// activate runs when the panel gains focus.
	activate() tea.Cmd
	// update receives every non-key message.
	update(msg tea.Msg) tea.Cmd
	list() *listState
	// rows renders every (filtered) list row; the shell windows and highlights them.
	rows(w int) []string
	tabs() []string
	// renderMain draws a tab for the current selection. Non-interactive tabs may return
	// more lines than h; the shell scrolls them.
	renderMain(tab, w, h int, focused bool) string
	// interactive tabs own j/k etc. while the main panel is focused.
	interactive(tab int) bool
	mainKey(tab int, msg tea.KeyPressMsg) (tea.Cmd, bool)
	actions() []action
	mainActions(tab int) []action
	// filterHint is the "/" placeholder; empty means the list isn't filterable.
	filterHint() string
}

// panelBase provides defaults so panels only implement what they use.
type panelBase struct{ ls listState }

func (b *panelBase) badge() string                                { return "" }
func (b *panelBase) activate() tea.Cmd                            { return nil }
func (b *panelBase) list() *listState                             { return &b.ls }
func (b *panelBase) interactive(int) bool                         { return false }
func (b *panelBase) mainKey(int, tea.KeyPressMsg) (tea.Cmd, bool) { return nil, false }
func (b *panelBase) mainActions(int) []action                     { return nil }
func (b *panelBase) filterHint() string                           { return "" }

// listState is a cursor, scroll offset and optional filter over n rows.
type listState struct {
	cursor int
	offset int
	filter filterInput
}

func (l *listState) clamp(n int) { l.cursor = max(0, min(l.cursor, n-1)) }

func (l *listState) move(delta, n int) {
	l.cursor += delta
	l.clamp(n)
}

// window returns the visible [start, end) for height h, keeping the cursor in view.
func (l *listState) window(h, n int) (int, int) {
	if h <= 0 || n == 0 {
		return 0, 0
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+h {
		l.offset = l.cursor - h + 1
	}
	l.offset = max(0, min(l.offset, n-h))
	return l.offset, min(l.offset+h, n)
}

// cursorList is an interactive list inside a main tab (nodes, settings rows, providers).
type cursorList struct{ listState }

// key handles movement keys; reports whether it consumed the key.
func (c *cursorList) key(msg tea.KeyPressMsg, n int) bool {
	switch msg.String() {
	case "up", "k":
		c.move(-1, n)
	case "down", "j":
		c.move(1, n)
	case "pgup", "ctrl+u":
		c.move(-10, n)
	case "pgdown", "ctrl+d":
		c.move(10, n)
	case "home", "g":
		c.move(-n, n)
	case "end", "G":
		c.move(n, n)
	default:
		return false
	}
	return true
}

// renderRows windows rows to h lines and highlights the cursor when focused.
func (c *cursorList) renderRows(rows []string, w, h int, focused bool) []string {
	c.clamp(len(rows))
	start, end := c.window(h, len(rows))
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		line := rows[i]
		if i == c.cursor && focused {
			line = styleSelected.Render(padRight(ansi.Strip(line), w))
		}
		out = append(out, line)
	}
	return out
}

// drawBox draws a rounded box of exactly w×h cells with title set into the top border.
// h == 1 collapses the box to its title line.
func drawBox(title string, body []string, w, h int, focused bool) string {
	if w < 4 || h < 1 {
		return ""
	}
	border := lipgloss.NewStyle().Foreground(colorDim)
	if focused {
		border = lipgloss.NewStyle().Foreground(colorOK).Bold(true)
	}
	inner := w - 2
	title = ansi.Truncate(title, max(inner-2, 0), "…")
	fill := max(inner-1-ansi.StringWidth(title), 0)
	top := border.Render("╭─") + title + border.Render(strings.Repeat("─", fill)+"╮")
	if h == 1 {
		return top
	}
	lines := []string{top}
	side := border.Render("│")
	for i := 0; i < h-2; i++ {
		content := ""
		if i < len(body) {
			content = body[i]
		}
		lines = append(lines, side+padRight(ansi.Truncate(content, inner, "…"), inner)+side)
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

// tabStrip renders "概览 ─ 日志 ─ 运行配置" with the active tab highlighted.
func tabStrip(names []string, active int, focused bool) string {
	parts := make([]string, len(names))
	for i, n := range names {
		switch {
		case i == active && focused:
			parts[i] = styleSelected.Render(" " + n + " ")
		case i == active:
			parts[i] = styleTitle.Render(" " + n + " ")
		default:
			parts[i] = styleDim.Render(" " + n + " ")
		}
	}
	return strings.Join(parts, styleDim.Render("─"))
}
