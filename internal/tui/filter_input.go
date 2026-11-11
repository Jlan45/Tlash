package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// filterInput is the "/ to filter" field shared by list pages.
// The zero value is usable: the text field is built on first start.
type filterInput struct {
	input  textinput.Model
	ready  bool
	active bool
}

func newFilterInput(placeholder string) filterInput {
	return filterInput{input: newTextInput("/ ", placeholder), ready: true}
}

func (f *filterInput) start(placeholder string) tea.Cmd {
	if !f.ready {
		*f = newFilterInput(placeholder)
	}
	f.active = true
	return f.input.Focus()
}

// update handles a key while typing: enter keeps the filter, esc clears it.
func (f *filterInput) update(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		f.input.SetValue("")
		fallthrough
	case "enter":
		f.active = false
		f.input.Blur()
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *filterInput) value() string { return f.input.Value() }
func (f *filterInput) clear()        { f.input.SetValue("") }

// line renders the field while typing, the active filter otherwise, or nothing.
func (f *filterInput) line(w int) string {
	switch {
	case f.active:
		f.input.SetWidth(max(w-4, 10))
		return f.input.View()
	case f.input.Value() != "":
		return styleAccent.Render("过滤: "+f.input.Value()) + styleDim.Render("  (esc 清除)")
	}
	return ""
}
