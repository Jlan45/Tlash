// Package gatewaysys applies and reverts the host settings a transparent gateway needs:
// IP forwarding (persisted across reboots) and FORWARD accept rules where the policy drops.
package gatewaysys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// System is the host; fields are swappable in tests.
type System struct {
	ProcSys   string // /proc/sys
	SysctlDir string // /etc/sysctl.d
	Run       func(ctx context.Context, name string, args ...string) ([]byte, error)
	LookPath  func(string) (string, error)
}

func Host() System {
	return System{
		ProcSys:   "/proc/sys",
		SysctlDir: "/etc/sysctl.d",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		LookPath: exec.LookPath,
	}
}

// State records what Apply changed so Revert can undo exactly that.
type State struct {
	PrevIPv4Forward string   `json:"prev_ipv4_forward,omitempty"`
	PrevIPv6Forward string   `json:"prev_ipv6_forward,omitempty"`
	SysctlFile      string   `json:"sysctl_file,omitempty"`
	ForwardRules    []string `json:"forward_rules,omitempty"` // iptables binaries we inserted rules with
}

const sysctlName = "99-tlash-gateway.conf"

const ruleComment = "tlash-gateway"

func (s System) path(key string) string {
	return filepath.Join(s.ProcSys, filepath.FromSlash(strings.ReplaceAll(key, ".", "/")))
}

func (s System) read(key string) string {
	b, _ := os.ReadFile(s.path(key))
	return strings.TrimSpace(string(b))
}

func (s System) write(key, v string) error {
	if err := os.WriteFile(s.path(key), []byte(v+"\n"), 0o644); err != nil {
		return fmt.Errorf("设置 %s 失败: %w", key, err)
	}
	return nil
}

// Apply enables forwarding and opens FORWARD for tunDev; on error it reverts what it did.
func (s System) Apply(ctx context.Context, ipv6 bool, tunDev string) (State, error) {
	st := State{PrevIPv4Forward: s.read("net.ipv4.ip_forward")}
	if err := s.write("net.ipv4.ip_forward", "1"); err != nil {
		return st, err
	}
	conf := "net.ipv4.ip_forward = 1\n"
	if ipv6 {
		st.PrevIPv6Forward = s.read("net.ipv6.conf.all.forwarding")
		if err := s.write("net.ipv6.conf.all.forwarding", "1"); err != nil {
			_ = s.Revert(ctx, st, tunDev)
			return st, err
		}
		conf += "net.ipv6.conf.all.forwarding = 1\n"
	}
	// Persist so forwarding survives a reboot while the gateway stays on.
	if err := os.MkdirAll(s.SysctlDir, 0o755); err == nil {
		file := filepath.Join(s.SysctlDir, sysctlName)
		if os.WriteFile(file, []byte("# Managed by Tlash gateway; removed when the gateway is turned off.\n"+conf), 0o644) == nil {
			st.SysctlFile = file
		}
	}
	bins := []string{"iptables"}
	if ipv6 {
		bins = append(bins, "ip6tables")
	}
	for _, bin := range bins {
		added, err := s.openForward(ctx, bin, tunDev)
		if err != nil {
			_ = s.Revert(ctx, st, tunDev)
			return st, err
		}
		if added {
			st.ForwardRules = append(st.ForwardRules, bin)
		}
	}
	return st, nil
}

// EnsureForward re-adds the FORWARD rules after a reboot (they don't persist on their own).
func (s System) EnsureForward(ctx context.Context, st State, tunDev string) error {
	for _, bin := range st.ForwardRules {
		if _, err := s.openForward(ctx, bin, tunDev); err != nil {
			return err
		}
	}
	return nil
}

// openForward inserts ACCEPT rules for the TUN device when the FORWARD policy drops
// (Docker and many firewalls set it), otherwise forwarded LAN UDP would be dropped.
func (s System) openForward(ctx context.Context, bin, tunDev string) (bool, error) {
	if _, err := s.LookPath(bin); err != nil {
		return false, nil
	}
	out, err := s.Run(ctx, bin, "-S", "FORWARD")
	if err != nil || !strings.Contains(string(out), "-P FORWARD DROP") {
		return false, nil
	}
	for _, dir := range []string{"-i", "-o"} {
		rule := []string{"FORWARD", dir, tunDev, "-m", "comment", "--comment", ruleComment, "-j", "ACCEPT"}
		if _, err := s.Run(ctx, bin, append([]string{"-C"}, rule...)...); err == nil {
			continue
		}
		if out, err := s.Run(ctx, bin, append([]string{"-I"}, rule...)...); err != nil {
			return false, fmt.Errorf("%s 添加转发放行规则失败: %s", bin, strings.TrimSpace(string(out)))
		}
	}
	return true, nil
}

// Revert restores forwarding values and removes the rules and file Apply created.
func (s System) Revert(ctx context.Context, st State, tunDev string) error {
	var errs []error
	if st.PrevIPv4Forward != "" {
		errs = append(errs, s.write("net.ipv4.ip_forward", st.PrevIPv4Forward))
	}
	if st.PrevIPv6Forward != "" {
		errs = append(errs, s.write("net.ipv6.conf.all.forwarding", st.PrevIPv6Forward))
	}
	if st.SysctlFile != "" {
		if err := os.Remove(st.SysctlFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, bin := range st.ForwardRules {
		for _, dir := range []string{"-i", "-o"} {
			rule := []string{"FORWARD", dir, tunDev, "-m", "comment", "--comment", ruleComment, "-j", "ACCEPT"}
			// Delete every copy; -C fails once none are left.
			for range 10 {
				if _, err := s.Run(ctx, bin, append([]string{"-C"}, rule...)...); err != nil {
					break
				}
				_, _ = s.Run(ctx, bin, append([]string{"-D"}, rule...)...)
			}
		}
	}
	return errors.Join(errs...)
}
