package runtimeconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

func gatewayConfig(t *testing.T, g appsettings.Gateway) map[string]any {
	t.Helper()
	g.Enabled = true
	out, err := Generate([]byte("dns:\n  enable: false\n  nameserver-policy: {'+.corp': 10.0.0.1}\nproxies: []\nrules:\n  - MATCH,DIRECT\n"), nil, appsettings.Managed{ResourceMirror: "https://m.example/"}, &g, "/s")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGatewayBlacklistWithCNBypass(t *testing.T) {
	got := gatewayConfig(t, appsettings.Gateway{
		BypassCN:     true,
		Devices:      []appsettings.Device{{MAC: "AA:BB:CC:DD:EE:FF", Name: "电视"}},
		ExcludeAddrs: []string{"203.0.113.7", "10.0.0.0/8"},
	})
	tun := got["tun"].(map[string]any)
	if tun["enable"] != true || tun["auto-route"] != true || tun["auto-redirect"] != true || tun["device"] != TunDevice || tun["stack"] != "mixed" {
		t.Errorf("tun = %v", tun)
	}
	if ex := tun["exclude-mac-address"].([]any); len(ex) != 1 || ex[0] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("exclude-mac-address = %v", tun["exclude-mac-address"])
	}
	if _, ok := tun["include-mac-address"]; ok {
		t.Error("blacklist mode must not set include-mac-address")
	}
	if r := tun["route-exclude-address"].([]any); len(r) != 2 || r[0] != "203.0.113.7/32" || r[1] != "10.0.0.0/8" {
		t.Errorf("route-exclude-address = %v", r)
	}
	if s := tun["route-exclude-address-set"].([]any); len(s) != 1 || s[0] != cnRuleSet {
		t.Errorf("route-exclude-address-set = %v", s)
	}
	rp := got["rule-providers"].(map[string]any)[cnRuleSet].(map[string]any)
	if !strings.HasPrefix(rp["url"].(string), "https://m.example/https://github.com/") || rp["format"] != "mrs" {
		t.Errorf("cn provider = %v", rp)
	}
	dns := got["dns"].(map[string]any)
	if dns["enable"] != true || dns["listen"] != "0.0.0.0:53" || dns["nameserver-policy"] == nil || dns["nameserver"] == nil {
		t.Errorf("dns = %v", dns)
	}
	if got["ipv6"] != false {
		t.Errorf("ipv6 = %v, want false for IPv4-only", got["ipv6"])
	}
}

func TestGatewayWhitelistIPv6NoBypass(t *testing.T) {
	got := gatewayConfig(t, appsettings.Gateway{
		DeviceMode: appsettings.DeviceModeSelected, IPv6: true, Stack: "system",
		Devices: []appsettings.Device{{MAC: "11:22:33:44:55:66"}},
	})
	tun := got["tun"].(map[string]any)
	if inc := tun["include-mac-address"].([]any); len(inc) != 1 || tun["stack"] != "system" {
		t.Errorf("tun = %v", tun)
	}
	if _, ok := tun["route-exclude-address-set"]; ok {
		t.Error("CN bypass off must not set route-exclude-address-set")
	}
	if _, ok := got["rule-providers"]; ok {
		t.Error("no rule provider expected without CN bypass")
	}
	if got["ipv6"] != true || got["dns"].(map[string]any)["ipv6"] != true {
		t.Errorf("ipv6 not enabled: %v", got)
	}
}

func TestGatewayOffLeavesTunAlone(t *testing.T) {
	out, err := Generate([]byte("tun: {enable: false}\nproxies: []\n"), nil, appsettings.Managed{}, &appsettings.Gateway{}, "/s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "enable: false") {
		t.Errorf("disabled gateway changed tun:\n%s", out)
	}
}

// The generated TUN config must be accepted by a real mihomo.
func TestGatewayConfigValidates(t *testing.T) {
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	g := appsettings.Gateway{Enabled: true, IPv6: true, Devices: []appsettings.Device{{MAC: "aa:bb:cc:dd:ee:ff"}}, ExcludeAddrs: []string{"203.0.113.7"}}
	out, err := Generate([]byte("proxies: []\nrules:\n  - MATCH,DIRECT\n"), nil, appsettings.Managed{}, &g, "/s")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), bin, dir, path); err != nil {
		t.Fatalf("mihomo rejected gateway config: %v\n%s", err, out)
	}
}
