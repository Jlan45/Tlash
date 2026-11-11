// Package appsettings reads and writes tlash.yaml, Tlash's own settings file.
package appsettings

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
}

type CoreSettings struct {
	Channel CoreChannel `yaml:"channel"`
	// Service is auto, systemd, systemd-user, openrc, procd or process.
	Service string `yaml:"service"`
}

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
