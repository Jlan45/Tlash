package tui

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// From an empty data dir to a running, proxied core using only key presses.
func TestWorkspaceKeyboardFlowFromScratch(t *testing.T) {
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
	app.Update(teaWindowSize(120, 34))
	run(app, app.sess.refreshAll())

	out := screen(app)
	if !strings.Contains(out, "开始使用") || !strings.Contains(out, "● 已停止") {
		t.Fatalf("expected setup checklist:\n%s", out)
	}
	t.Logf("first run:\n%s", out)

	// 1. Settings → 内核 (first category): i imports a local core.
	press(app, panelKey(t, app, "设置"), "i")
	typeText(app, bin)
	press(app, "enter")
	if !ws.CoreInstalled() || app.sess.core.version == "" {
		t.Fatalf("core not installed:\n%s", screen(app))
	}

	// 2. Subscriptions: i imports a profile; the first one becomes active.
	press(app, panelKey(t, app, "订阅"), "i")
	typeText(app, profilePath)
	press(app, "enter", "enter")
	if out = screen(app); !strings.Contains(out, "● 机场") {
		t.Fatalf("profile not imported/active:\n%s", out)
	}

	// 3. Status: s starts the core.
	press(app, "1", "s")
	run(app, fetchStatus(app.sess.client))
	if out = screen(app); !strings.Contains(out, "● 运行中") {
		t.Fatalf("core not running:\n%s", out)
	}
	t.Logf("running:\n%s", out)

	// Cancelling a confirm must not leave a job marked busy.
	press(app, "s", "n")
	if len(app.sess.busy) != 0 || !app.sess.core.running {
		t.Fatalf("cancelled stop left busy=%v running=%v", app.sess.busy, app.sess.core.running)
	}

	// 4. Groups show the profile's nodes.
	press(app, panelKey(t, app, "代理组"))
	if out = screen(app); !strings.Contains(out, "🚀 节点选择") || !strings.Contains(out, "香港 01") {
		t.Fatalf("groups missing:\n%s", out)
	}

	// 5. Settings → 接管: enter the main panel, move to 允许局域网, enter toggles it live.
	press(app, panelKey(t, app, "设置"), "down", "enter", "down", "enter")
	cfg, err := ws.Client().Config(context.Background())
	if err != nil || !cfg.AllowLan {
		t.Fatalf("allow-lan not applied: %+v %v\n%s", cfg, err, screen(app))
	}

	// 6. Overrides: preview tab renders the generated config.
	press(app, panelKey(t, app, "覆写"), "]")
	if out = screen(app); !strings.Contains(out, "最终配置预览") || !strings.Contains(out, "external-controller-unix") {
		t.Fatalf("preview:\n%s", out)
	}

	// 7. Stop with confirmation.
	press(app, "1", "s", "y")
	if app.sess.core.running {
		t.Fatalf("still running:\n%s", screen(app))
	}
}

// A leftover core from this data dir is offered for cleanup instead of failing to start.
func TestStaleCorePromptsCleanup(t *testing.T) {
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	dir, err := os.MkdirTemp("", "tls")
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
	ctx := context.Background()
	if _, err := ws.ImportCore(ctx, bin); err != nil {
		t.Fatal(err)
	}
	defer ws.Service().Uninstall(ctx)
	if err := ws.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	orphan := exec.Command(ws.Core.Current, "-d", ws.RuntimeDir, "-f", ws.ConfigPath)
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = orphan.Process.Kill() }()
	go func() { _, _ = orphan.Process.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	app := New(Options{Client: ws.Client(), Workspace: ws, DelayTest: mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 30))
	run(app, fetchCoreState(ws, true))
	press(app, "s")
	if out := screen(app); !strings.Contains(out, "残留的旧内核") || app.confirm == nil {
		t.Fatalf("no cleanup prompt:\n%s", out)
	}
	press(app, "y")
	if !app.sess.core.running {
		t.Fatalf("core not running after cleanup:\n%s", screen(app))
	}
	_ = ws.Stop(ctx)
}
