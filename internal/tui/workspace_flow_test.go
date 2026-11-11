package tui

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/workspace"
)

func typeText(app *App, s string) {
	for _, r := range s {
		press(app, string(r))
	}
}

func pageIndex(t *testing.T, app *App, title string) string {
	for i, p := range app.pages {
		if p.title() == title {
			return fmt.Sprint(i + 1)
		}
	}
	t.Fatalf("no page %q", title)
	return ""
}

// From an empty data dir to a running, proxied core using only key presses.
func TestWorkspaceFlowFromScratch(t *testing.T) {
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	dir, err := os.MkdirTemp("", "tlt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_ = ws.UpdateSettings(func(s *appsettings.Settings) { s.Core.Service = "process"; s.Managed.MixedPort = &port })
	defer ws.Service().Uninstall(context.Background())

	profilePath := filepath.Join(t.TempDir(), "机场.yaml")
	_ = os.WriteFile(profilePath, []byte(`proxies:
  - {name: "香港 01", type: socks5, server: 127.0.0.1, port: 1}
proxy-groups:
  - {name: "🚀 节点选择", type: select, proxies: ["香港 01", DIRECT]}
rules:
  - MATCH,🚀 节点选择
`), 0o600)

	app := New(Options{Client: ws.Client(), Workspace: ws, DelayTest: mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(110, 32))
	run(app, app.sess.refreshAll())

	out := screen(app)
	if !strings.Contains(out, "开始使用") || !strings.Contains(out, "● 已停止") {
		t.Fatalf("expected setup checklist:\n%s", out)
	}

	// 1. Import the core from a local file on the core page.
	press(app, pageIndex(t, app, "内核"), "i")
	typeText(app, bin)
	press(app, "enter")
	if !ws.CoreInstalled() || app.sess.core.version == "" {
		t.Fatalf("core not installed:\n%s", screen(app))
	}
	t.Logf("core page:\n%s", screen(app))

	// 2. Import a profile; the first one becomes active.
	press(app, pageIndex(t, app, "订阅"), "i")
	typeText(app, profilePath)
	press(app, "enter", "enter")
	if out = screen(app); !strings.Contains(out, "● 机场") {
		t.Fatalf("profile not imported/active:\n%s", out)
	}

	// 3. Start from the overview.
	press(app, "1", "s")
	run(app, fetchStatus(app.sess.client))
	if out = screen(app); !strings.Contains(out, "● 运行中") || !strings.Contains(out, "机场") {
		t.Fatalf("core not running:\n%s", out)
	}
	t.Logf("overview:\n%s", out)

	// Cancelling a confirm must not leave a job marked busy.
	press(app, "s", "n")
	if len(app.sess.busy) != 0 || !app.sess.core.running {
		t.Fatalf("cancelled stop left busy=%v running=%v", app.sess.busy, app.sess.core.running)
	}

	// 4. The profile's nodes are live.
	press(app, "2")
	if out = screen(app); !strings.Contains(out, "🚀 节点选择") || !strings.Contains(out, "香港 01") {
		t.Fatalf("proxies missing:\n%s", out)
	}

	// 5. Toggle a managed setting; it reaches the running core.
	press(app, pageIndex(t, app, "内核"), "down", "enter")
	cfg, err := ws.Client().Config(context.Background())
	if err != nil || !cfg.AllowLan {
		t.Fatalf("allow-lan not applied: %+v %v\n%s", cfg, err, screen(app))
	}

	// 6. Stop with confirmation.
	press(app, "s", "y")
	if app.sess.core.running {
		t.Fatalf("still running:\n%s", screen(app))
	}
}
