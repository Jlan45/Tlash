package coreservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// process runs the core detached from the terminal, tracked by a pidfile.
// It's the fallback when no usable init system is present (containers, non-root without systemd).
type process struct {
	spec Spec
}

func (p *process) Kind() Kind                    { return KindProcess }
func (p *process) Install(context.Context) error { return nil }
func (p *process) Uninstall(ctx context.Context) error {
	return p.Stop(ctx)
}
func (p *process) Installed() bool { return true }
func (p *process) LogHint() string { return p.spec.LogPath }

func (p *process) AutoStart(context.Context) (bool, error) { return false, nil }

func (p *process) SetAutoStart(context.Context, bool) error {
	return errors.New("后台进程模式不支持开机自启")
}

func (p *process) pid() (int, bool) {
	b, err := os.ReadFile(p.spec.PIDPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	// Guard against PID reuse: the process must still be our core binary.
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return 0, false
	}
	argv0, _, _ := bytes.Cut(cmdline, []byte{0})
	if string(argv0) != p.spec.CorePath {
		return 0, false
	}
	return pid, true
}

func (p *process) Running(context.Context) (bool, error) {
	_, ok := p.pid()
	return ok, nil
}

func (p *process) Start(ctx context.Context) error {
	if _, ok := p.pid(); ok {
		return nil
	}
	logf, err := os.OpenFile(p.spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(p.spec.CorePath, p.spec.args()...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Dir = p.spec.HomeDir
	// New session: survives the TUI exiting and the SSH session closing.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// Reap it if it exits while we're alive, so a dead core doesn't linger as a zombie.
	go func() { _ = cmd.Wait() }()
	if err := os.WriteFile(p.spec.PIDPath, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return err
	}
	// Catch immediate failures (bad config, port in use) instead of reporting success.
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
	}
	if _, ok := p.pid(); !ok {
		return fmt.Errorf("内核启动后立即退出，查看日志: %s", p.spec.LogPath)
	}
	return nil
}

func (p *process) Stop(ctx context.Context) error {
	pid, ok := p.pid()
	if !ok {
		_ = os.Remove(p.spec.PIDPath)
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := p.pid(); !ok {
			_ = os.Remove(p.spec.PIDPath)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(p.spec.PIDPath)
	return nil
}

func (p *process) Restart(ctx context.Context) error {
	if err := p.Stop(ctx); err != nil {
		return err
	}
	return p.Start(ctx)
}
