package workspace

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/gatewaysys"
)

// fakeHost replaces root, the host network and the guard process for gateway tests.
func fakeHost(t *testing.T) (procSys string, guards *[]string) {
	t.Helper()
	dir := t.TempDir()
	procSys = filepath.Join(dir, "proc")
	for _, k := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		p := filepath.Join(procSys, k)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("0\n"), 0o644)
	}
	oldSys, oldRoot, oldGuard := hostSystem, isRoot, startGuard
	hostSystem = func() gatewaysys.System {
		return gatewaysys.System{
			ProcSys:   procSys,
			SysctlDir: filepath.Join(dir, "sysctl.d"),
			Run:       func(context.Context, string, ...string) ([]byte, error) { return []byte("-P FORWARD ACCEPT"), nil },
			LookPath:  func(string) (string, error) { return "/sbin/iptables", nil },
		}
	}
	isRoot = func() bool { return true }
	var started []string
	startGuard = func(_ *Workspace, token string) error { started = append(started, token); return nil }
	t.Cleanup(func() { hostSystem, isRoot, startGuard = oldSys, oldRoot, oldGuard })
	return procSys, &started
}

func ipForward(t *testing.T, procSys string) string {
	b, _ := os.ReadFile(filepath.Join(procSys, "net/ipv4/ip_forward"))
	return string(b)
}

func gatewayWorkspace(t *testing.T) *Workspace {
	w := openTestWorkspace(t, "process")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dnsPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if err := w.UpdateSettings(func(s *appsettings.Settings) {
		s.Gateway.DNSListen = "127.0.0.1:" + strconv.Itoa(dnsPort)
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestGatewayEnableConfirm(t *testing.T) {
	procSys, guards := fakeHost(t)
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51000 10.0.0.2 22")
	w := gatewayWorkspace(t)
	ctx := context.Background()

	deadline, err := w.EnableGateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop(ctx)
	if time.Until(deadline) <= 0 || len(*guards) != 1 {
		t.Fatalf("deadline %v guards %v", deadline, *guards)
	}
	g := w.Settings().Gateway
	if !g.Enabled || len(g.ExcludeAddrs) != 1 || g.ExcludeAddrs[0] != "203.0.113.7" {
		t.Fatalf("gateway settings = %+v", g)
	}
	if ipForward(t, procSys) != "1\n" {
		t.Fatal("ip_forward not enabled")
	}
	cfg, _ := os.ReadFile(w.ConfigPath)
	if !containsAll(string(cfg), "tun:", "auto-redirect: true", "203.0.113.7/32", "tlash-cn-ip") {
		t.Fatalf("runtime config lacks gateway:\n%s", cfg)
	}
	if _, ok := w.PendingGateway(); !ok {
		t.Fatal("no pending confirmation")
	}
	if err := w.ConfirmGateway(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.PendingGateway(); ok {
		t.Fatal("still pending after confirm")
	}
	// Confirmed: the guard must leave it alone.
	if err := w.RunGatewayGuard(ctx, (*guards)[0]); err != nil || !w.Settings().Gateway.Enabled {
		t.Fatal("guard rolled back a confirmed gateway")
	}

	// Option changes apply live; turning it off restores the host.
	if err := w.UpdateGateway(ctx, func(g *appsettings.Gateway) { g.BypassCN = false }); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := os.ReadFile(w.ConfigPath); containsAll(string(cfg), "tlash-cn-ip") {
		t.Fatal("CN bypass still in config after turning it off")
	}
	if err := w.DisableGateway(ctx); err != nil {
		t.Fatal(err)
	}
	if ipForward(t, procSys) != "0\n" || w.Settings().Gateway.Enabled {
		t.Fatal("disable did not restore the host")
	}
	if cfg, _ := os.ReadFile(w.ConfigPath); containsAll(string(cfg), "auto-redirect") {
		t.Fatal("tun still configured after disable")
	}
}

func TestGatewayGuardRollsBackUnconfirmed(t *testing.T) {
	procSys, guards := fakeHost(t)
	old := GatewayConfirmWindow
	GatewayConfirmWindow = 1500 * time.Millisecond
	defer func() { GatewayConfirmWindow = old }()
	w := gatewayWorkspace(t)
	ctx := context.Background()
	if _, err := w.EnableGateway(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop(ctx)
	// The guard (normally a detached process) times out and restores everything.
	if err := w.RunGatewayGuard(ctx, (*guards)[0]); err != nil {
		t.Fatal(err)
	}
	if w.Settings().Gateway.Enabled || ipForward(t, procSys) != "0\n" {
		t.Fatal("unconfirmed gateway was not rolled back")
	}
	if _, ok := w.PendingGateway(); ok {
		t.Fatal("pending file left after rollback")
	}
	// The rollback is persisted, not just in memory.
	if s, _, _ := appsettings.Load(w.Dir); s.Gateway.Enabled {
		t.Fatal("rollback not saved")
	}
}

func TestGatewayPreflightBlocks(t *testing.T) {
	fakeHost(t)
	w := gatewayWorkspace(t)
	isRoot = func() bool { return false }
	_ = w.UpdateSettings(func(s *appsettings.Settings) { s.Gateway.DeviceMode = appsettings.DeviceModeSelected })
	if _, err := w.EnableGateway(context.Background()); err == nil || !containsAll(err.Error(), "root", "没有选择任何设备") {
		t.Fatalf("want preflight failure, got %v", err)
	}
	if w.Settings().Gateway.Enabled {
		t.Fatal("enabled despite failed preflight")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
