package appsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, exists, err := Load(dir)
	if err != nil || exists || *s.Managed.MixedPort != 7890 || s.Managed.Secret == "" {
		t.Fatalf("defaults: %+v exists=%v err=%v", s, exists, err)
	}
	s.Managed.ExternalController = "127.0.0.1:9090"
	s.Managed.Secret = ""
	s.Managed.IPv6 = nil
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, FileName))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}
	got, exists, err := Load(dir)
	if err != nil || !exists {
		t.Fatal(err)
	}
	// A TCP controller must never be saved without a secret.
	if got.Managed.Secret == "" {
		t.Error("secret not generated for TCP controller")
	}
	// nil means "use the profile's value" and must survive a round trip instead of reverting to defaults.
	if got.Managed.IPv6 != nil {
		t.Error("nil managed field came back non-nil")
	}
}
