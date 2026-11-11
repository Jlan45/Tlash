package workspace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/portowner"
	"github.com/Jlan45/Tlash/internal/profile"
)

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func profileFile(t *testing.T, dir, name, proxy string) string {
	p := filepath.Join(dir, name+".yaml")
	content := fmt.Sprintf(`proxies:
  - {name: %q, type: socks5, server: 127.0.0.1, port: 1}
proxy-groups:
  - {name: PROXY, type: select, proxies: [%q, DIRECT]}
rules:
  - MATCH,PROXY
`, proxy, proxy)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// openTestWorkspace returns a workspace in a short temp dir (unix socket paths are length-limited).
func openTestWorkspace(t *testing.T, service string) *Workspace {
	t.Helper()
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	dir, err := os.MkdirTemp("", "tlw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	if err := w.UpdateSettings(func(s *appsettings.Settings) {
		s.Core.Service = service
		s.Managed.MixedPort = &port
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := w.ImportCore(ctx, bin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Service().Uninstall(context.Background()) })
	return w
}

func TestWorkspaceLifecycle(t *testing.T) {
	w := openTestWorkspace(t, "process")
	runLifecycle(t, w)
}

// Opt-in: installs a real systemd user unit (with a test-only name) on this machine.
func TestWorkspaceLifecycleSystemdUser(t *testing.T) {
	if os.Getenv("TLASH_TEST_SYSTEMD_USER") == "" {
		t.Skip("TLASH_TEST_SYSTEMD_USER not set")
	}
	ServiceName = "tlash-mihomo-test"
	defer func() { ServiceName = "tlash-mihomo" }()
	w := openTestWorkspace(t, "systemd-user")
	runLifecycle(t, w)
	if ok, _ := w.Service().AutoStart(context.Background()); ok {
		t.Error("autostart should default to off")
	}
}

func runLifecycle(t *testing.T, w *Workspace) {
	ctx := context.Background()
	src := t.TempDir()

	// No profile yet: the core still starts on the direct-only config.
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if running, _ := w.Service().Running(ctx); !running {
		t.Fatal("not running after Start")
	}

	// The first profile becomes active and is hot-reloaded into the running core.
	a, err := w.AddLocal(ctx, profileFile(t, src, "a", "node-a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Settings().ActiveProfile != a.ID || a.Name != "a" {
		t.Fatalf("active=%q profile=%+v", w.Settings().ActiveProfile, a)
	}
	assertProxy(t, w, "node-a")

	b, err := w.AddLocal(ctx, profileFile(t, src, "b", "node-b"), "B")
	if err != nil {
		t.Fatal(err)
	}
	if w.Settings().ActiveProfile != a.ID {
		t.Fatal("adding a second profile must not switch")
	}
	if err := w.Activate(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	assertProxy(t, w, "node-b")

	// Managed keys reach the running core.
	newPort := freePort(t)
	if err := w.UpdateManaged(ctx, func(m *appsettings.Managed) { m.MixedPort = &newPort }); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := w.Client().Config(ctx); cfg.MixedPort != newPort {
		t.Fatalf("mixed-port = %d, want %d", cfg.MixedPort, newPort)
	}

	// A hand-edit that breaks the profile is rejected and the running config is untouched.
	if err := os.WriteFile(w.Profiles.ContentPath(b.ID), []byte("proxies:\n  - {name: x, type: nonsense}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.ReapplyAfterEdit(ctx, b.ID); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("want validation error, got %v", err)
	}
	assertProxy(t, w, "node-b")

	// Deleting the active profile falls back to direct-only.
	if err := w.DeleteProfile(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if w.Settings().ActiveProfile != "" {
		t.Fatal("active profile not cleared")
	}
	if ps, _ := w.Profiles.List(); len(ps) != 1 || ps[0].Kind != profile.KindLocal {
		t.Fatalf("profiles = %+v", ps)
	}

	if err := w.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if running, _ := w.Service().Running(ctx); running {
		t.Fatal("still running after Stop")
	}
}

func assertProxy(t *testing.T, w *Workspace, name string) {
	t.Helper()
	ps, err := w.Client().Proxies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ps[name]; !ok {
		t.Fatalf("proxy %q not loaded in running core", name)
	}
}

// A taken port fails Start with the culprit named, instead of a core that silently lacks it.
func TestStartReportsBusyPort(t *testing.T) {
	w := openTestWorkspace(t, "process")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	if err := w.UpdateSettings(func(s *appsettings.Settings) { s.Managed.MixedPort = &busy }); err != nil {
		t.Fatal(err)
	}
	err = w.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("mixed-port %d", busy)) || !strings.Contains(err.Error(), "pid") {
		t.Fatalf("want busy-port error naming the owner, got %v", err)
	}
	if running, _ := w.Service().Running(context.Background()); running {
		t.Fatal("core started despite the busy port")
	}

	// Switching a running core onto a taken port is reported too.
	free := freePort(t)
	if err := w.UpdateSettings(func(s *appsettings.Settings) { s.Managed.MixedPort = &free }); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer w.Stop(context.Background())
	err = w.UpdateManaged(context.Background(), func(m *appsettings.Managed) { m.MixedPort = &busy })
	if err == nil || !strings.Contains(err.Error(), "未能监听") {
		t.Fatalf("want listen failure after reload, got %v", err)
	}
}

// A core left over from this data dir (outside the current supervisor) is recognised and can be cleared.
func TestStartRecognisesStaleCore(t *testing.T) {
	w := openTestWorkspace(t, "process")
	ctx := context.Background()
	if err := w.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	orphan := exec.Command(w.Core.Current, "-d", w.RuntimeDir, "-f", w.ConfigPath)
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orphan.Process.Kill(); _, _ = orphan.Process.Wait() })
	go func() { _, _ = orphan.Process.Wait() }()
	port, _ := w.MixedPort()
	for i := 0; i < 50 && !portowner.Busy(port); i++ {
		time.Sleep(100 * time.Millisecond)
	}

	err := w.Start(ctx)
	var stale *StaleCoreError
	if !errors.As(err, &stale) || stale.PID != orphan.Process.Pid {
		t.Fatalf("want StaleCoreError for pid %d, got %v", orphan.Process.Pid, err)
	}
	if err := w.StopStaleCore(stale.PID); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start after clearing stale core: %v", err)
	}
	defer w.Stop(ctx)
	// Never touches processes that aren't this workspace's core.
	if err := w.StopStaleCore(os.Getpid()); err == nil {
		t.Fatal("StopStaleCore accepted a foreign process")
	}
}

func TestOtherTlashDir(t *testing.T) {
	if got := otherTlashDir("/etc/tlash/core/mihomo -d /etc/tlash/runtime -f /etc/tlash/runtime/config.yaml"); got != "/etc/tlash" {
		t.Errorf("otherTlashDir = %q", got)
	}
	if got := otherTlashDir("/usr/bin/mihomo -d /etc/mihomo"); got != "" {
		t.Errorf("non-tlash core matched: %q", got)
	}
}
