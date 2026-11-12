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
	out, err := Generate([]byte(profile), nil, m, nil, "/etc/tlash/runtime/mihomo.sock")
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
	out, err := Generate([]byte(MinimalProfile), nil, appsettings.Managed{ExternalController: "127.0.0.1:9090", Secret: "x"}, nil, "/s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "external-controller: 127.0.0.1:9090") {
		t.Errorf("missing TCP controller:\n%s", out)
	}
}

func TestGenerateRejectsNonMapping(t *testing.T) {
	if _, err := Generate([]byte("- a\n- b\n"), nil, appsettings.Managed{}, nil, "/s"); err == nil {
		t.Error("want error for list document")
	}
}

func TestExtractMsg(t *testing.T) {
	line := `time="2026" level=error msg="proxy 0: missing type"`
	if got := extractMsg(line); got != "proxy 0: missing type" {
		t.Errorf("extractMsg = %q", got)
	}
}

func TestGenerateMergesOverridesInOrder(t *testing.T) {
	profile := `dns:
  enable: true
  nameserver: [1.1.1.1]
  fallback: [8.8.8.8]
rules:
  - MATCH,PROXY
proxies: []
`
	overrides := []NamedOverride{
		{Name: "a", Content: []byte("+rules:\n  - DOMAIN,a.com,DIRECT\nrules+:\n  - DOMAIN,z.com,DIRECT\ndns:\n  nameserver: [223.5.5.5]\n")},
		{Name: "b", Content: []byte("# only comments\n")},
		{Name: "c", Content: []byte("+rules:\n  - DOMAIN,b.com,REJECT\nsniffer!:\n  enable: true\n")},
	}
	port := 7890
	out, err := Generate([]byte(profile), overrides, appsettings.Managed{MixedPort: &port}, nil, "/s")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DNS struct {
			Enable     bool
			Nameserver []string
			Fallback   []string
		}
		Rules     []string
		Sniffer   map[string]any
		MixedPort int `yaml:"mixed-port"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Rules, "|") != "DOMAIN,b.com,REJECT|DOMAIN,a.com,DIRECT|MATCH,PROXY|DOMAIN,z.com,DIRECT" {
		t.Errorf("rules = %v", got.Rules)
	}
	// Maps merge deeply; lists inside them replace.
	if !got.DNS.Enable || strings.Join(got.DNS.Nameserver, ",") != "223.5.5.5" || len(got.DNS.Fallback) != 1 {
		t.Errorf("dns = %+v", got.DNS)
	}
	if got.Sniffer["enable"] != true || got.MixedPort != 7890 {
		t.Errorf("sniffer=%v port=%d", got.Sniffer, got.MixedPort)
	}
}

func TestGenerateOverrideErrors(t *testing.T) {
	_, err := Generate([]byte("rules: {a: 1}\nproxies: []\n"), []NamedOverride{{Name: "坏", Content: []byte("+rules: [x]\n")}}, appsettings.Managed{}, nil, "/s")
	if err == nil || !strings.Contains(err.Error(), "坏") {
		t.Errorf("want named error, got %v", err)
	}
}

func TestGenerateDNSKeepsUnmanagedKeys(t *testing.T) {
	profile := "dns:\n  enable: false\n  nameserver-policy:\n    '+.corp': 10.0.0.1\n  fallback: [8.8.8.8]\nproxies: []\n"
	m := appsettings.Managed{DNS: appsettings.DefaultDNS(), Sniffer: appsettings.DefaultSniffer()}
	out, err := Generate([]byte(profile), nil, m, nil, "/s")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DNS     map[string]any
		Sniffer struct {
			Enable bool
			Sniff  map[string]struct{ Ports []any }
		}
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.DNS["enable"] != true || got.DNS["enhanced-mode"] != "fake-ip" || got.DNS["nameserver-policy"] == nil {
		t.Errorf("dns = %v", got.DNS)
	}
	// Managed DNS has no fallback, so the profile's is removed rather than mixed in.
	if _, ok := got.DNS["fallback"]; ok {
		t.Errorf("fallback should be removed: %v", got.DNS)
	}
	if !got.Sniffer.Enable || len(got.Sniffer.Sniff["HTTP"].Ports) != 2 || got.Sniffer.Sniff["HTTP"].Ports[0] != 80 {
		t.Errorf("sniffer = %+v", got.Sniffer)
	}
}

func TestGenerateResources(t *testing.T) {
	m := appsettings.Managed{ResourceMirror: "https://ghfast.top/", Dashboard: "zashboard"}
	out, err := Generate([]byte(MinimalProfile), nil, m, nil, "/s")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"geosite: https://ghfast.top/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
		"external-ui: ui", "external-ui-name: zashboard",
		"external-ui-url: https://ghfast.top/https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// Profiles commonly set port: 7890 too; it must not steal the managed mixed port.
func TestGenerateDropsInboundsOnMixedPort(t *testing.T) {
	port := 7890
	out, err := Generate([]byte("port: 7890\nsocks-port: 7891\nredir-port: 7890\nproxies: []\n"), nil, appsettings.Managed{MixedPort: &port}, nil, "/s")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = yaml.Unmarshal(out, &got)
	if _, ok := got["port"]; ok {
		t.Errorf("port 7890 kept alongside mixed-port: %v", got)
	}
	if _, ok := got["redir-port"]; ok {
		t.Errorf("redir-port 7890 kept: %v", got)
	}
	if got["socks-port"] != 7891 || got["mixed-port"] != 7890 {
		t.Errorf("unrelated ports changed: %v", got)
	}
}
