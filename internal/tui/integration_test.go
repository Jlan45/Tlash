package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/mihomotest"
)

func teaWindowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// run executes cmd and feeds its messages back into the app, skipping
// long-lived commands (ticks, stream waits) that don't finish quickly.
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
	if os.Getenv("TLASH_DEBUG_RUN") != "" {
		fmt.Printf("run: %T\n", msg)
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			run(app, c)
		}
	case streamEventMsg, statusTickMsg, notifyExpireMsg, animTickMsg, connTickMsg:
		// Feed the event but don't re-arm the loop.
		app.Update(m)
	default:
		_, next := app.Update(m)
		run(app, next)
	}
}

// Ticks fire immediately in tests; run() feeds them once without re-arming.
func init() {
	notifyDuration = time.Millisecond
	statusPollInterval = time.Millisecond
}

func press(app *App, keys ...string) {
	for _, k := range keys {
		_, cmd := app.Update(key(k))
		run(app, cmd)
	}
}

func screen(app *App) string { return ansi.Strip(app.render()) }

func TestTUIAgainstRealMihomo(t *testing.T) {
	inst := mihomotest.Start(t)
	app := New(Options{Client: inst.Client, DelayTest: mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: 2 * time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(100, 28))
	run(app, fetchStatus(inst.Client))

	out := screen(app)
	if !strings.Contains(out, "● 运行中") || !strings.Contains(out, "规则") {
		t.Fatalf("overview not connected:\n%s", out)
	}

	press(app, "g")
	if app.sess.config.Mode != "global" {
		t.Fatalf("mode = %q after g", app.sess.config.Mode)
	}
	press(app, "r")

	press(app, pageIndex(t, app, "代理"))
	out = screen(app)
	t.Logf("proxies page:\n%s", out)
	for _, want := range []string{"🚀 节点选择", "♻️ 自动选择", "香港 01", "Selector"} {
		if !strings.Contains(out, want) {
			t.Fatalf("proxies page missing %q:\n%s", want, out)
		}
	}

	// Cursor starts on the first group header; move to its second member (香港 01) and select it.
	press(app, "down", "down", "enter")
	proxies, err := inst.Client.Proxies(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if now := proxies["🚀 节点选择"].Now; now != "香港 01" {
		t.Fatalf("selected %q, want 香港 01\n%s", now, screen(app))
	}

	// Delay test on a dead proxy shows 超时 rather than an error.
	press(app, "t")
	if out = screen(app); !strings.Contains(out, "超时") {
		t.Fatalf("no timeout shown after test:\n%s", out)
	}

	// Filtering narrows rows to matching members.
	press(app, "/", "日", "enter")
	out = screen(app)
	if strings.Contains(out, "美国 01") || !strings.Contains(out, "日本 01") {
		t.Fatalf("filter not applied:\n%s", out)
	}
	press(app, "esc")

	press(app, pageIndex(t, app, "日志"))
	app.Update(logMsg{at: time.Now(), entry: mihomoapi.LogEntry{Type: "warning", Payload: "[TCP] dial failed"}})
	if out = screen(app); !strings.Contains(out, "WARN") || !strings.Contains(out, "dial failed") {
		t.Fatalf("logs page:\n%s", out)
	}
	t.Logf("logs page:\n%s", out)
}
