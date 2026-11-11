package runtimeconfig

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

func TestGenerateOverlaysManagedKeys(t *testing.T) {
	profile := `# 机场配置
port: 7890
mixed-port: 1234 # 原端口
allow-lan: true
external-controller: 0.0.0.0:9090
secret: ""
proxies:
  - {name: a, type: socks5, server: 1.1.1.1, port: 1}
rules:
  - MATCH,DIRECT
`
	port, lan := 7999, false
	m := appsettings.Managed{MixedPort: &port, AllowLan: &lan, Secret: "s"}
	out, err := Generate([]byte(profile), m, "/etc/tlash/runtime/mihomo.sock")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["mixed-port"] != 7999 || got["allow-lan"] != false || got["port"] != 7890 {
		t.Errorf("managed keys not applied: %v", got)
	}
	if _, ok := got["external-controller"]; ok {
		t.Error("TCP controller should be removed when not enabled")
	}
	if got["external-controller-unix"] != "/etc/tlash/runtime/mihomo.sock" || got["secret"] != "s" {
		t.Errorf("controller keys: %v", got)
	}
	// Order and comments from the profile survive.
	s := string(out)
	if !strings.HasPrefix(s, "# 机场配置") || strings.Index(s, "port: 7890") > strings.Index(s, "proxies:") {
		t.Errorf("order/comments lost:\n%s", s)
	}
	// Unmanaged (nil) fields keep the profile's value.
	if _, ok := got["ipv6"]; ok {
		t.Error("nil managed field should not be added")
	}
}

func TestGenerateEnablesTCPController(t *testing.T) {
	out, err := Generate([]byte(MinimalProfile), appsettings.Managed{ExternalController: "127.0.0.1:9090", Secret: "x"}, "/s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "external-controller: 127.0.0.1:9090") {
		t.Errorf("missing TCP controller:\n%s", out)
	}
}

func TestGenerateRejectsNonMapping(t *testing.T) {
	if _, err := Generate([]byte("- a\n- b\n"), appsettings.Managed{}, "/s"); err == nil {
		t.Error("want error for list document")
	}
}

func TestExtractMsg(t *testing.T) {
	line := `time="2026" level=error msg="proxy 0: missing type"`
	if got := extractMsg(line); got != "proxy 0: missing type" {
		t.Errorf("extractMsg = %q", got)
	}
}
