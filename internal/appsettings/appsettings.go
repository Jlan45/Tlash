// Package appsettings reads and writes tlash.yaml, Tlash's own settings file.
package appsettings

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

const FileName = "tlash.yaml"

type CoreChannel string

const (
	ChannelStable CoreChannel = "stable"
	ChannelAlpha  CoreChannel = "alpha"
)

type Settings struct {
	Core CoreSettings `yaml:"core"`
	// Mirror is prefixed to github.com download URLs, e.g. "https://ghfast.top/".
	Mirror        string  `yaml:"mirror"`
	ActiveProfile string  `yaml:"active_profile"`
	Managed       Managed `yaml:"managed"`
	Tasks         Tasks   `yaml:"tasks"`
	Gateway       Gateway `yaml:"gateway"`
}

// Gateway is transparent proxying for this machine and the LAN, built on mihomo's TUN.
type Gateway struct {
	Enabled bool `yaml:"enabled"`
	// Stack is the TUN network stack: mixed, system or gvisor.
	Stack string `yaml:"stack,omitempty"`
	// IPv6 also proxies IPv6; otherwise IPv6 traffic bypasses the TUN.
	IPv6 bool `yaml:"ipv6"`
	// DeviceMode "all" proxies every LAN device except Devices; "selected" proxies only Devices.
	DeviceMode string   `yaml:"device_mode,omitempty"`
	Devices    []Device `yaml:"devices,omitempty"`
	// BypassCN lets mainland-China IPs skip mihomo in the kernel (faster, but invisible to rules).
	BypassCN bool `yaml:"bypass_cn"`
	// DNSListen is where mihomo's DNS listens; empty picks a platform default (OpenWrt
	// forwards from dnsmasq to 127.0.0.1:1053, elsewhere LAN clients query 0.0.0.0:53).
	DNSListen string `yaml:"dns_listen,omitempty"`
	// ExcludeAddrs never enter the TUN (the SSH client that enabled the gateway is added here).
	ExcludeAddrs []string `yaml:"exclude_addrs,omitempty"`
}

type Device struct {
	MAC  string `yaml:"mac"`
	Name string `yaml:"name,omitempty"`
}

const (
	DeviceModeAll      = "all"
	DeviceModeSelected = "selected"
)

// WithDefaults fills unset gateway fields.
func (g Gateway) WithDefaults() Gateway {
	if g.Stack == "" {
		g.Stack = "mixed"
	}
	if g.DeviceMode != DeviceModeSelected {
		g.DeviceMode = DeviceModeAll
	}
	return g
}

// Tasks configures the periodic `tlash task run-due` job.
type Tasks struct {
	Enabled bool `yaml:"enabled"`
	// GeoUpdateInterval refreshes GEO databases this often; 0 disables.
	GeoUpdateInterval time.Duration `yaml:"geo_update_interval,omitempty"`
	LastGeoUpdate     time.Time     `yaml:"last_geo_update,omitempty"`
}

type CoreSettings struct {
	Channel CoreChannel `yaml:"channel"`
	// Service is auto, systemd, systemd-user, openrc, procd or process.
	Service string `yaml:"service"`
	// Location is where the core binary lives: "" (data dir), "compressed" (gzip in the data
	// dir, unpacked into RAM at boot) or "ram" (RAM only, downloaded at boot). For small flash.
	Location string `yaml:"location,omitempty"`
}

const (
	CoreInData     = ""
	CoreCompressed = "compressed"
	CoreRAMOnly    = "ram"
)

// Managed holds config keys Tlash forces onto every profile.
// A nil field leaves the profile's own value in place.
type Managed struct {
	MixedPort       *int    `yaml:"mixed_port,omitempty"`
	AllowLan        *bool   `yaml:"allow_lan,omitempty"`
	BindAddress     *string `yaml:"bind_address,omitempty"`
	IPv6            *bool   `yaml:"ipv6,omitempty"`
	LogLevel        *string `yaml:"log_level,omitempty"`
	UnifiedDelay    *bool   `yaml:"unified_delay,omitempty"`
	TCPConcurrent   *bool   `yaml:"tcp_concurrent,omitempty"`
	FindProcessMode *string `yaml:"find_process_mode,omitempty"`
	// ExternalController is the optional TCP API listener; empty disables it.
	// Unlike other fields it is always enforced, so profiles can't expose the API by accident.
	ExternalController string `yaml:"external_controller"`
	Secret             string `yaml:"secret"`
	// DNS, when set, overrides these keys inside the profile's dns block (others are kept).
	DNS *DNS `yaml:"dns,omitempty"`
	// Sniffer, when set, replaces the profile's sniffer block.
	Sniffer *Sniffer `yaml:"sniffer,omitempty"`
	// ResourceMirror, when set, points geox-url and the dashboard download through this prefix.
	ResourceMirror string `yaml:"resource_mirror,omitempty"`
	// Dashboard is "", "metacubexd" or "zashboard"; served at /ui on the TCP controller.
	Dashboard string `yaml:"dashboard,omitempty"`
}

type DNS struct {
	Enable                bool     `yaml:"enable"`
	Listen                string   `yaml:"listen,omitempty"`
	EnhancedMode          string   `yaml:"enhanced_mode"`
	FakeIPRange           string   `yaml:"fake_ip_range,omitempty"`
	FakeIPFilter          []string `yaml:"fake_ip_filter,omitempty"`
	DefaultNameserver     []string `yaml:"default_nameserver,omitempty"`
	Nameserver            []string `yaml:"nameserver,omitempty"`
	Fallback              []string `yaml:"fallback,omitempty"`
	ProxyServerNameserver []string `yaml:"proxy_server_nameserver,omitempty"`
}

// DefaultDNS is a reasonable starting point for servers in mainland China.
func DefaultDNS() *DNS {
	return &DNS{
		Enable:                true,
		EnhancedMode:          "fake-ip",
		FakeIPRange:           "198.18.0.1/16",
		FakeIPFilter:          []string{"*.lan", "+.local", "+.msftconnecttest.com", "+.msftncsi.com"},
		DefaultNameserver:     []string{"223.5.5.5", "119.29.29.29"},
		Nameserver:            []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
		ProxyServerNameserver: []string{"https://doh.pub/dns-query"},
	}
}

type Sniffer struct {
	Enable              bool     `yaml:"enable"`
	OverrideDestination bool     `yaml:"override_destination"`
	HTTPPorts           []string `yaml:"http_ports,omitempty"`
	TLSPorts            []string `yaml:"tls_ports,omitempty"`
	QUICPorts           []string `yaml:"quic_ports,omitempty"`
	ForceDomain         []string `yaml:"force_domain,omitempty"`
	SkipDomain          []string `yaml:"skip_domain,omitempty"`
}

func DefaultSniffer() *Sniffer {
	return &Sniffer{
		Enable:     true,
		HTTPPorts:  []string{"80", "8080-8880"},
		TLSPorts:   []string{"443", "8443"},
		QUICPorts:  []string{"443", "8443"},
		SkipDomain: []string{"Mijia Cloud", "+.push.apple.com"},
	}
}

func ptr[T any](v T) *T { return &v }

// Default is what a fresh install starts with.
func Default() Settings {
	return Settings{
		Core: CoreSettings{Channel: ChannelStable, Service: "auto"},
		Managed: Managed{
			MixedPort:     ptr(7890),
			AllowLan:      ptr(false),
			IPv6:          ptr(false),
			LogLevel:      ptr("info"),
			UnifiedDelay:  ptr(true),
			TCPConcurrent: ptr(true),
			Secret:        RandomSecret(),
		},
		Gateway: Gateway{BypassCN: true}.WithDefaults(),
	}
}

func RandomSecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Load reads dir/tlash.yaml; a missing file yields defaults with exists=false.
func Load(dir string) (s Settings, exists bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	// Start from zero, not Default(): an omitted managed key means "use the profile's value".
	s = Settings{}
	if err := yaml.Unmarshal(b, &s); err != nil {
		return Settings{}, true, fmt.Errorf("解析 %s 失败: %w", FileName, err)
	}
	s.normalize()
	return s, true, nil
}

func (s *Settings) normalize() {
	if s.Core.Channel != ChannelAlpha {
		s.Core.Channel = ChannelStable
	}
	if s.Core.Service == "" {
		s.Core.Service = "auto"
	}
	// A TCP controller without a secret would be an open proxy-control endpoint.
	if s.Managed.ExternalController != "" && s.Managed.Secret == "" {
		s.Managed.Secret = RandomSecret()
	}
}

// Save writes atomically with owner-only permissions (the file holds the API secret).
func Save(dir string, s Settings) error {
	s.normalize()
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, FileName), b, 0o600)
}

// WriteFileAtomic writes via a temp file and rename so readers never see partial content.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
