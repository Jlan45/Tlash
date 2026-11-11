package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// textViewer is a read-only scrollable overlay (config preview, connection details).
type textViewer struct {
	title  string
	lines  []string
	offset int
	height int
}

func newTextViewer(title, text string) *textViewer {
	return &textViewer{title: title, lines: strings.Split(strings.TrimRight(text, "\n"), "\n")}
}

// update returns true when the viewer should close.
func (v *textViewer) update(msg tea.KeyPressMsg) bool {
	page := max(v.height-3, 1)
	switch msg.String() {
	case "esc", "q", "enter":
		return true
	case "up", "k":
		v.offset--
	case "down", "j":
		v.offset++
	case "pgup", "ctrl+u", "b":
		v.offset -= page
	case "pgdown", "ctrl+d", "space", "f":
		v.offset += page
	case "home", "g":
		v.offset = 0
	case "end", "G":
		v.offset = len(v.lines)
	}
	return false
}

func (v *textViewer) view(w, h int) string {
	v.height = h
	bodyH := max(h-2, 1)
	v.offset = max(0, min(v.offset, len(v.lines)-bodyH))
	end := min(v.offset+bodyH, len(v.lines))
	header := styleTitle.Render(v.title) + styleDim.Render(fmt.Sprintf("  %d-%d / %d 行  j/k 滚动 · esc 关闭", v.offset+1, end, len(v.lines)))
	return header + "\n\n" + strings.Join(v.lines[v.offset:end], "\n")
}

func (v *textViewer) hints() []hint {
	return []hint{{"j/k", "滚动"}, {"space", "翻页"}, {"esc", "关闭"}}
}

// openInEditor suspends the TUI and edits path in $VISUAL / $EDITOR (default vi).
func openInEditor(path string, done func(error) tea.Msg) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	// $EDITOR may carry flags, e.g. "code --wait".
	parts := strings.Fields(editor)
	return tea.ExecProcess(exec.Command(parts[0], append(parts[1:], path)...), done)
}
