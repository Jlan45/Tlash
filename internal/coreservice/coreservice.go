// Package coreservice runs mihomo under the host's init system (or as a plain background process).
package coreservice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Kind string

const (
	KindSystemd     Kind = "systemd"
	KindSystemdUser Kind = "systemd-user"
	KindOpenRC      Kind = "openrc"
	KindProcd       Kind = "procd"
	KindProcess     Kind = "process"
)

func (k Kind) Label() string {
	switch k {
	case KindSystemd:
		return "systemd"
	case KindSystemdUser:
		return "systemd（用户级）"
	case KindOpenRC:
		return "OpenRC"
	case KindProcd:
		return "procd（OpenWrt）"
	default:
		return "后台进程"
	}
}

// Spec describes the core process to run.
type Spec struct {
	Name       string // service name, e.g. tlash-mihomo
	CorePath   string
	HomeDir    string
	ConfigPath string
	LogPath    string // used where the init system doesn't capture output
	PIDPath    string
	// PreStart runs before the core and may fail without stopping it (gateway host setup).
	PreStart []string
}

func (s Spec) preStartLine() string {
	parts := make([]string, len(s.PreStart))
	for i, a := range s.PreStart {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func (s Spec) args() []string { return []string{"-d", s.HomeDir, "-f", s.ConfigPath} }

func (s Spec) commandLine() string {
	parts := []string{shellQuote(s.CorePath)}
	for _, a := range s.args() {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`;&|<>()*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Manager controls one way of supervising the core.
type Manager interface {
	Kind() Kind
	// Install writes (or refreshes) the service definition; it's idempotent.
	Install(ctx context.Context) error
	Uninstall(ctx context.Context) error
	Installed() bool
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Running(ctx context.Context) (bool, error)
	AutoStart(ctx context.Context) (bool, error)
	SetAutoStart(ctx context.Context, on bool) error
	// LogHint tells the user where the core's own stdout ends up.
	LogHint() string
}

// runner executes a command and returns combined output; swapped in tests.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out, nil
}

// Detect picks the supervisor for this host. setting overrides detection unless it's "auto".
func Detect(setting string) Kind {
	if setting != "" && setting != "auto" {
		return Kind(setting)
	}
	root := os.Geteuid() == 0
	pid1, _ := os.ReadFile("/proc/1/comm")
	switch strings.TrimSpace(string(pid1)) {
	case "systemd":
		if root {
			return KindSystemd
		}
		if os.Getenv("XDG_RUNTIME_DIR") != "" {
			return KindSystemdUser
		}
	case "procd":
		if root {
			return KindProcd
		}
	}
	if root && fileExists("/sbin/openrc-run") && fileExists("/run/openrc") {
		return KindOpenRC
	}
	return KindProcess
}

// New builds the manager for kind.
func New(kind Kind, spec Spec) (Manager, error) {
	switch kind {
	case KindSystemd, KindSystemdUser:
		return newSystemd(spec, kind == KindSystemdUser, execRunner)
	case KindOpenRC:
		return &initScript{spec: spec, kind: KindOpenRC, run: execRunner, dir: "/etc/init.d"}, nil
	case KindProcd:
		return &initScript{spec: spec, kind: KindProcd, run: execRunner, dir: "/etc/init.d"}, nil
	case KindProcess:
		return &process{spec: spec}, nil
	}
	return nil, fmt.Errorf("未知的服务类型: %s", kind)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeIfChanged avoids needless daemon reloads; reports whether the file changed.
func writeIfChanged(path, content string, perm os.FileMode) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		return false, err
	}
	return true, os.Chmod(path, perm)
}
