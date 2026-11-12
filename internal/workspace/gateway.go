package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/gatewaysys"
	"github.com/Jlan45/Tlash/internal/portowner"
	"github.com/Jlan45/Tlash/internal/runtimeconfig"
)

// GatewayConfirmWindow is how long the user has to confirm before the gateway rolls back.
var GatewayConfirmWindow = 60 * time.Second

// Swapped in tests, which can't be root or touch the host network.
var (
	hostSystem = gatewaysys.Host
	isRoot     = func() bool { return os.Geteuid() == 0 }
	startGuard = (*Workspace).spawnGuard
)

type CheckLevel int

const (
	CheckOK CheckLevel = iota
	CheckWarn
	CheckFail // blocks enabling
)

type GatewayCheck struct {
	Name   string
	Level  CheckLevel
	Detail string
}

// gatewayPending is written when the gateway is turned on and removed on confirmation.
// A detached guard rolls back if it still exists at Deadline, even if SSH dropped.
type gatewayPending struct {
	Token    string              `json:"token"`
	Deadline time.Time           `json:"deadline"`
	Previous appsettings.Gateway `json:"previous"`
}

func (w *Workspace) pendingPath() string { return filepath.Join(w.RuntimeDir, "gateway-pending.json") }
func (w *Workspace) sysStatePath() string {
	return filepath.Join(w.RuntimeDir, "gateway-system.json")
}

// GatewayPreflight checks the host can act as a gateway. CheckFail entries block enabling.
func (w *Workspace) GatewayPreflight(ctx context.Context) []GatewayCheck {
	g := w.Settings().Gateway.WithDefaults()
	var checks []GatewayCheck
	add := func(name string, level CheckLevel, detail string) {
		checks = append(checks, GatewayCheck{Name: name, Level: level, Detail: detail})
	}
	if isRoot() {
		add("root 权限", CheckOK, "")
	} else {
		add("root 权限", CheckFail, "TUN 和转发需要 root：用 root 运行 tlash（数据目录为 /etc/tlash）")
	}
	if !w.CoreInstalled() {
		add("内核", CheckFail, "尚未安装内核")
	}
	if _, err := os.Stat("/dev/net/tun"); err == nil {
		add("/dev/net/tun", CheckOK, "")
	} else {
		add("/dev/net/tun", CheckFail, "缺少 TUN 设备：加载 tun 模块（modprobe tun），容器中需要映射 /dev/net/tun")
	}
	if hasNetfilter() {
		add("nftables / iptables", CheckOK, "")
	} else {
		add("nftables / iptables", CheckWarn, "未找到 nft 或 iptables，auto-redirect 可能无法工作")
	}
	if g.DeviceMode == appsettings.DeviceModeSelected && len(g.Devices) == 0 {
		add("代理范围", CheckFail, "“仅选中设备”模式下还没有选择任何设备")
	}
	// The DNS listener only matters before the gateway (and thus our listener) is on.
	if !g.Enabled {
		if err := checkListenable(g.DNSListen); err != nil {
			add("DNS 监听 "+g.DNSListen, CheckFail, err.Error())
		} else {
			add("DNS 监听 "+g.DNSListen, CheckOK, "")
		}
	}
	if ip := sshClientIP(); ip != "" {
		add("SSH 保护", CheckOK, "当前 SSH 客户端 "+ip+" 将排除在代理之外")
	}
	return checks
}

func hasNetfilter() bool {
	for _, bin := range []string{"nft", "iptables"} {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	_, err := os.Stat("/sys/module/nf_tables")
	return err == nil
}

// checkListenable tries the DNS address on both UDP and TCP and names any holder.
func checkListenable(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("地址格式应为 IP:端口，如 0.0.0.0:53")
	}
	if pc, err := net.ListenPacket("udp", addr); err != nil {
		return portBusyHint(host, portStr)
	} else {
		pc.Close()
	}
	if ln, err := net.Listen("tcp", addr); err != nil {
		return portBusyHint(host, portStr)
	} else {
		ln.Close()
	}
	return nil
}

func portBusyHint(host, portStr string) error {
	var port int
	fmt.Sscan(portStr, &port)
	who := "其他程序"
	if o, ok := portowner.Find(port); ok {
		who = o.Comm + fmt.Sprintf(" (pid %d)", o.PID)
	}
	msg := fmt.Sprintf("端口 %s 已被 %s 占用", portStr, who)
	if host == "0.0.0.0" || host == "" || host == "::" {
		if ip := lanIPv4(); ip != "" {
			msg += fmt.Sprintf("；可改为只监听局域网地址 %s:%s", ip, portStr)
		}
	}
	return errors.New(msg)
}

// lanIPv4 is the first private IPv4 on an up, non-loopback interface (a DNS listen suggestion).
func lanIPv4() string {
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Name == runtimeconfig.TunDevice {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
				return ipn.IP.String()
			}
		}
	}
	return ""
}

// sshClientIP is the address of the SSH session running Tlash, which must never be routed
// into the TUN or the user loses the session.
func sshClientIP() string {
	f := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// EnableGateway turns the gateway on and returns the confirm deadline. Unless ConfirmGateway
// is called by then, a detached guard process rolls everything back.
func (w *Workspace) EnableGateway(ctx context.Context) (time.Time, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	var failed []string
	for _, c := range w.GatewayPreflight(ctx) {
		if c.Level == CheckFail {
			failed = append(failed, c.Name+"："+c.Detail)
		}
	}
	if len(failed) > 0 {
		return time.Time{}, fmt.Errorf("环境检查未通过：%s", strings.Join(failed, "；"))
	}
	prev := w.Settings().Gateway
	next := prev.WithDefaults()
	next.Enabled = true
	if ip := sshClientIP(); ip != "" && !slices.Contains(next.ExcludeAddrs, ip) {
		next.ExcludeAddrs = append(slices.Clone(next.ExcludeAddrs), ip)
	}

	st, err := hostSystem().Apply(ctx, next.IPv6, runtimeconfig.TunDevice)
	if err != nil {
		return time.Time{}, err
	}
	if err := writeJSON(w.sysStatePath(), st); err != nil {
		_ = hostSystem().Revert(ctx, st, runtimeconfig.TunDevice)
		return time.Time{}, err
	}
	pending := gatewayPending{Token: randomToken(), Deadline: time.Now().Add(GatewayConfirmWindow), Previous: prev}
	if err := writeJSON(w.pendingPath(), pending); err != nil {
		_ = w.revertSystemLocked(ctx)
		return time.Time{}, err
	}

	w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = next })
	err = w.activateGatewayLocked(ctx)
	if err == nil {
		err = w.saveSettingsLocked()
	}
	if err != nil {
		w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = prev })
		_ = w.applyLocked(ctx)
		_ = w.revertSystemLocked(ctx)
		_ = os.Remove(w.pendingPath())
		return time.Time{}, err
	}
	if err := startGuard(w, pending.Token); err != nil {
		// Without a guard there is no safety net; undo rather than leave it unguarded.
		_, _ = w.rollbackLocked(ctx, pending.Token)
		return time.Time{}, fmt.Errorf("无法启动回滚保护进程: %w", err)
	}
	return pending.Deadline, nil
}

// activateGatewayLocked applies the config, starting the core if it isn't running.
func (w *Workspace) activateGatewayLocked(ctx context.Context) error {
	if running, _ := w.Service().Running(ctx); running {
		return w.applyLocked(ctx)
	}
	return w.startLocked(ctx)
}

// ConfirmGateway keeps the gateway on; the guard sees the pending file gone and exits.
func (w *Workspace) ConfirmGateway() error {
	err := os.Remove(w.pendingPath())
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("已超时并回滚，无需确认")
	}
	return err
}

// PendingGateway reports an unconfirmed enable and its deadline.
func (w *Workspace) PendingGateway() (time.Time, bool) {
	var p gatewayPending
	if readJSON(w.pendingPath(), &p) != nil {
		return time.Time{}, false
	}
	return p.Deadline, true
}

// RollbackGateway undoes an unconfirmed enable now.
func (w *Workspace) RollbackGateway(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	_, err := w.rollbackLocked(ctx, "")
	return err
}

// rollbackLocked restores the pre-enable state if a pending enable (matching token, unless
// empty) exists. It reports whether it rolled anything back.
func (w *Workspace) rollbackLocked(ctx context.Context, token string) (bool, error) {
	var p gatewayPending
	if readJSON(w.pendingPath(), &p) != nil || (token != "" && p.Token != token) {
		return false, nil
	}
	w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = p.Previous })
	errs := []error{w.saveSettingsLocked(), w.applyLocked(ctx), w.revertSystemLocked(ctx)}
	_ = os.Remove(w.pendingPath())
	return true, errors.Join(errs...)
}

// DisableGateway turns a confirmed gateway off.
func (w *Workspace) DisableGateway(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	_ = os.Remove(w.pendingPath())
	w.mutateSettings(func(s *appsettings.Settings) { s.Gateway.Enabled = false })
	return errors.Join(w.saveSettingsLocked(), w.applyLocked(ctx), w.revertSystemLocked(ctx))
}

// UpdateGateway changes gateway options, re-applying them if the gateway is on.
func (w *Workspace) UpdateGateway(ctx context.Context, fn func(*appsettings.Gateway)) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	prev := w.Settings().Gateway
	next := prev
	next.Devices, next.ExcludeAddrs = slices.Clone(prev.Devices), slices.Clone(prev.ExcludeAddrs)
	fn(&next)
	w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = next })
	if next.Enabled {
		if next.IPv6 != prev.IPv6 {
			// Forwarding for the other family has to follow the option.
			if err := w.reapplySystemLocked(ctx, next.IPv6); err != nil {
				w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = prev })
				return err
			}
		}
		if err := w.applyLocked(ctx); err != nil {
			w.mutateSettings(func(s *appsettings.Settings) { s.Gateway = prev })
			return err
		}
	}
	return w.saveSettingsLocked()
}

func (w *Workspace) reapplySystemLocked(ctx context.Context, ipv6 bool) error {
	if err := w.revertSystemLocked(ctx); err != nil {
		return err
	}
	st, err := hostSystem().Apply(ctx, ipv6, runtimeconfig.TunDevice)
	if err != nil {
		return err
	}
	return writeJSON(w.sysStatePath(), st)
}

func (w *Workspace) revertSystemLocked(ctx context.Context) error {
	var st gatewaysys.State
	if readJSON(w.sysStatePath(), &st) != nil {
		return nil
	}
	if err := hostSystem().Revert(ctx, st, runtimeconfig.TunDevice); err != nil {
		return err
	}
	return os.Remove(w.sysStatePath())
}

// PrepareGateway runs before the core starts (service pre-start hook): firewall rules don't
// survive a reboot, so re-add them while the gateway is on.
func (w *Workspace) PrepareGateway(ctx context.Context) error {
	if !w.Settings().Gateway.Enabled {
		return nil
	}
	var st gatewaysys.State
	if readJSON(w.sysStatePath(), &st) != nil {
		st2, err := hostSystem().Apply(ctx, w.Settings().Gateway.IPv6, runtimeconfig.TunDevice)
		if err != nil {
			return err
		}
		return writeJSON(w.sysStatePath(), st2)
	}
	return hostSystem().EnsureForward(ctx, st, runtimeconfig.TunDevice)
}

// RunGatewayGuard waits for the confirm deadline and rolls back if still unconfirmed.
func (w *Workspace) RunGatewayGuard(ctx context.Context, token string) error {
	for {
		var p gatewayPending
		if readJSON(w.pendingPath(), &p) != nil || p.Token != token {
			return nil // confirmed, rolled back, or superseded
		}
		if !time.Now().Before(p.Deadline) {
			w.opMu.Lock()
			_, err := w.rollbackLocked(ctx, token)
			w.opMu.Unlock()
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// spawnGuard starts `tlash gateway guard` in its own session so it outlives SSH and the TUI.
func (w *Workspace) spawnGuard(token string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(w.RuntimeDir, "gateway-guard.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, "--dir", w.Dir, "gateway", "guard", "--token", token)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func randomToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return appsettings.WriteFileAtomic(path, b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
