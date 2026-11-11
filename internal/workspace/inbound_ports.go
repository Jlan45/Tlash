package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/portowner"
)

// inboundPort is a listener the runtime config asks for.
type inboundPort struct {
	key  string // config key, e.g. mixed-port
	port int
}

func inboundPorts(cfg []byte) []inboundPort {
	var c map[string]any
	if yaml.Unmarshal(cfg, &c) != nil {
		return nil
	}
	var out []inboundPort
	for _, k := range []string{"mixed-port", "port", "socks-port", "redir-port", "tproxy-port"} {
		if n, ok := c[k].(int); ok && n > 0 {
			out = append(out, inboundPort{key: k, port: n})
		}
	}
	return out
}

func reported(cfg mihomoapi.Config, key string) int {
	switch key {
	case "mixed-port":
		return cfg.MixedPort
	case "port":
		return cfg.Port
	case "socks-port":
		return cfg.SocksPort
	case "redir-port":
		return cfg.RedirPort
	case "tproxy-port":
		return cfg.TProxyPort
	}
	return 0
}

// StaleCoreError means a core from this very data dir is running outside the current
// supervisor (e.g. after switching service type) and holds the ports.
type StaleCoreError struct {
	PID int
	err error
}

func (e *StaleCoreError) Error() string { return e.err.Error() }

// busyError explains why ports are taken. afterStart means our own supervised core is
// already running, so finding this workspace's core on a port is a clash inside the config.
func (w *Workspace) busyError(ports []inboundPort, afterStart bool) error {
	parts := make([]string, len(ports))
	stale := 0
	for i, p := range ports {
		parts[i] = fmt.Sprintf("%s %d ", p.key, p.port)
		o, ok := portowner.Find(p.port)
		switch {
		case !ok:
			parts[i] += "已被占用（无法识别占用进程，可能需要 root）"
		case w.ownsProcess(o) && afterStart:
			parts[i] += "与本配置中的其他入站冲突（多个入站用了同一端口）"
		case w.ownsProcess(o):
			stale = o.PID
			parts[i] += fmt.Sprintf("被本数据目录残留的旧内核占用（pid %d，不受当前托管方式管理）", o.PID)
		case otherTlashDir(o.Cmdline) != "":
			parts[i] += fmt.Sprintf("被另一个 Tlash 数据目录 %s 的内核占用（pid %d）。可能同时用 root 和普通用户装过 Tlash：用 tlash --dir %s 停止它，或改用不同端口",
				otherTlashDir(o.Cmdline), o.PID, otherTlashDir(o.Cmdline))
		default:
			parts[i] += "被 " + o.String() + " 占用"
		}
	}
	err := fmt.Errorf("%s。请停止占用端口的程序，或在 设置 → 接管 修改端口", strings.Join(parts, "；"))
	if stale != 0 {
		return &StaleCoreError{PID: stale, err: err}
	}
	return err
}

// ownsProcess reports whether o is a core started for this workspace's runtime dir.
func (w *Workspace) ownsProcess(o portowner.Owner) bool {
	return strings.Contains(o.Cmdline, "-d "+w.RuntimeDir+" ") || strings.HasSuffix(o.Cmdline, "-d "+w.RuntimeDir)
}

// otherTlashDir extracts the data dir from a Tlash-managed core's "-d <dir>/runtime".
func otherTlashDir(cmdline string) string {
	f := strings.Fields(cmdline)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "-d" && strings.HasSuffix(f[i+1], "/runtime") && strings.Contains(f[i+1], "tlash") {
			return strings.TrimSuffix(f[i+1], "/runtime")
		}
	}
	return ""
}

// StopStaleCore terminates a leftover core of this workspace so the supervised one can bind.
func (w *Workspace) StopStaleCore(pid int) error {
	o, ok := findPID(pid)
	if !ok || !w.ownsProcess(o) {
		return fmt.Errorf("进程 %d 不是本数据目录的内核，未处理", pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	_ = p.Signal(syscall.SIGTERM)
	for range 50 {
		if _, alive := findPID(pid); !alive {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return p.Signal(syscall.SIGKILL)
}

func findPID(pid int) (portowner.Owner, bool) {
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(cmdline) == 0 {
		return portowner.Owner{}, false
	}
	return portowner.Owner{PID: pid, Cmdline: strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))}, true
}

// checkPortsFree runs before the core starts: anything listening now isn't supervised by us.
func (w *Workspace) checkPortsFree(cfg []byte) error {
	var busy []inboundPort
	for _, p := range inboundPorts(cfg) {
		if portowner.Busy(p.port) {
			busy = append(busy, p)
		}
	}
	if len(busy) > 0 {
		return w.busyError(busy, false)
	}
	return nil
}

// verifyListeners compares the ports the config asks for with what the core actually opened;
// mihomo only logs a failed listen and keeps running, reporting the port as 0.
func (w *Workspace) verifyListeners(ctx context.Context, cfg []byte) error {
	running, err := w.client.Config(ctx)
	if err != nil {
		return nil // reachability problems surface elsewhere
	}
	var failed []inboundPort
	for _, p := range inboundPorts(cfg) {
		if reported(running, p.key) != p.port {
			failed = append(failed, p)
		}
	}
	if len(failed) > 0 {
		err := w.busyError(failed, true)
		var stale *StaleCoreError
		if errors.As(err, &stale) {
			return err
		}
		return fmt.Errorf("内核已运行，但端口未能监听：%w", err)
	}
	return nil
}
