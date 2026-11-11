package workspace

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jlan45/Tlash/internal/appsettings"
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
