package tui

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Ticks fire immediately in tests; run() feeds them once without re-arming.
func init() {
	notifyDuration = time.Millisecond
	statusPollInterval = time.Millisecond
}

func teaWindowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// run executes cmd and feeds its messages back into the app, skipping long-lived
// commands (ticks, stream waits) so a test step always returns.
func run(app *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(30 * time.Second):
		if os.Getenv("TLASH_DEBUG_RUN") != "" {
			fmt.Printf("run: cmd timed out\n")
		}
		return
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			run(app, c)
		}
	case streamEventMsg, statusTickMsg, notifyExpireMsg, animTickMsg:
		app.Update(m)
	default:
		_, next := app.Update(m)
		run(app, next)
	}
}

func press(app *App, keys ...string) {
	for _, k := range keys {
		_, cmd := app.Update(key(k))
		run(app, cmd)
	}
}

func typeText(app *App, s string) {
	for _, r := range s {
		press(app, string(r))
	}
}

func screen(app *App) string { return ansi.Strip(app.render()) }

// panelKey is the digit that focuses the panel titled title.
func panelKey(t *testing.T, app *App, title string) string {
	t.Helper()
	for i, p := range app.panels {
		if p.title() == title {
			return fmt.Sprint(i + 1)
		}
	}
	t.Fatalf("no panel %q", title)
	return ""
}
