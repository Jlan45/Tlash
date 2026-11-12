package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

func TestWebAccess(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := hostAddrs
	hostAddrs = func() []string { return []string{"192.168.1.2", "10.8.0.1"} }
	defer func() { hostAddrs = old }()
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51000 198.51.100.9 22")
	t.Setenv("USER", "alice")

	if acc := w.WebAccess(); len(acc.Problems) != 2 {
		t.Fatalf("fresh install should list dashboard + controller problems: %+v", acc.Problems)
	}

	_ = w.UpdateSettings(func(s *appsettings.Settings) {
		s.Managed.Dashboard = "zashboard"
		s.Managed.ExternalController = "127.0.0.1:9090"
	})
	acc := w.WebAccess()
	if acc.Tunnel != "ssh -N -L 9090:127.0.0.1:9090 alice@198.51.100.9" || acc.LocalURL != "http://127.0.0.1:9090/ui" || len(acc.URLs) != 0 {
		t.Fatalf("loopback access = %+v", acc)
	}
	if acc.Downloaded || len(acc.Problems) != 1 || acc.Secret == "" {
		t.Fatalf("not-downloaded state = %+v", acc)
	}

	_ = os.MkdirAll(filepath.Join(w.RuntimeDir, "ui", "zashboard"), 0o700)
	_ = os.WriteFile(filepath.Join(w.RuntimeDir, "ui", "zashboard", "index.html"), []byte("<html>"), 0o600)
	_ = w.UpdateSettings(func(s *appsettings.Settings) { s.Managed.ExternalController = "0.0.0.0:9090" })
	acc = w.WebAccess()
	// The SSH server address comes first even though it isn't in the interface list here.
	if !acc.Downloaded || len(acc.Problems) != 0 || acc.Tunnel != "" ||
		strings.Join(acc.URLs, " ") != "http://198.51.100.9:9090/ui http://192.168.1.2:9090/ui http://10.8.0.1:9090/ui" {
		t.Fatalf("all-interfaces access = %+v", acc)
	}
}

func TestVirtualIface(t *testing.T) {
	for name, want := range map[string]bool{"docker0": true, "br-3f2a": true, "br-lan": false, "eth0": false, "virbr0": true, "tailscale0": false} {
		if got := virtualIface(name); got != want {
			t.Errorf("virtualIface(%q) = %v", name, got)
		}
	}
}
