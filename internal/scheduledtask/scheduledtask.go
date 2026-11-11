// Package scheduledtask registers the periodic `tlash task run-due` job with systemd timers or cron.
package scheduledtask

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Jlan45/Tlash/internal/coreservice"
)

// Interval is how often run-due fires; each task decides itself whether it's due.
const Interval = "15min"

type Scheduler interface {
	Label() string
	Installed(ctx context.Context) bool
	Install(ctx context.Context) error
	Uninstall(ctx context.Context) error
}

// Job is the command to run periodically.
type Job struct {
	Name     string // unit name / cron marker, e.g. tlash-tasks
	TlashBin string
	DataDir  string
}

func (j Job) command() string {
	return fmt.Sprintf("%s --dir %s task run-due", quote(j.TlashBin), quote(j.DataDir))
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`;&|<>()*?[]#~%") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// For picks the scheduler matching how the core itself is supervised.
func For(kind coreservice.Kind, job Job) (Scheduler, error) {
	switch kind {
	case coreservice.KindSystemd:
		return &systemdTimer{job: job, dir: "/etc/systemd/system"}, nil
	case coreservice.KindSystemdUser:
		cfg, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		return &systemdTimer{job: job, user: true, dir: filepath.Join(cfg, "systemd", "user")}, nil
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		return nil, errors.New("没有 systemd 也没有 crontab，无法注册定时任务")
	}
	return &cron{job: job}, nil
}

type systemdTimer struct {
	job  Job
	user bool
	dir  string
}

func (t *systemdTimer) Label() string {
	if t.user {
		return "systemd timer（用户级）"
	}
	return "systemd timer"
}

func (t *systemdTimer) systemctl(ctx context.Context, args ...string) error {
	if t.user {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *systemdTimer) servicePath() string { return filepath.Join(t.dir, t.job.Name+".service") }
func (t *systemdTimer) timerPath() string   { return filepath.Join(t.dir, t.job.Name+".timer") }

func (t *systemdTimer) units() (service, timer string) {
	service = fmt.Sprintf(`# Managed by Tlash; changes are overwritten.
[Unit]
Description=Tlash scheduled tasks (subscription and GEO updates)

[Service]
Type=oneshot
ExecStart=%s
`, t.job.command())
	timer = fmt.Sprintf(`# Managed by Tlash; changes are overwritten.
[Unit]
Description=Run Tlash scheduled tasks every %s

[Timer]
OnBootSec=5min
OnUnitActiveSec=%s
Persistent=true

[Install]
WantedBy=timers.target
`, Interval, Interval)
	return service, timer
}

func (t *systemdTimer) Installed(context.Context) bool {
	_, err := os.Stat(t.timerPath())
	return err == nil
}

func (t *systemdTimer) Install(ctx context.Context) error {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return err
	}
	service, timer := t.units()
	if err := os.WriteFile(t.servicePath(), []byte(service), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(t.timerPath(), []byte(timer), 0o644); err != nil {
		return err
	}
	if err := t.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	return t.systemctl(ctx, "enable", "--now", t.job.Name+".timer")
}

func (t *systemdTimer) Uninstall(ctx context.Context) error {
	if !t.Installed(ctx) {
		return nil
	}
	_ = t.systemctl(ctx, "disable", "--now", t.job.Name+".timer")
	for _, p := range []string{t.timerPath(), t.servicePath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return t.systemctl(ctx, "daemon-reload")
}

type cron struct{ job Job }

func (c *cron) Label() string { return "cron" }

func (c *cron) marker() string { return "# " + c.job.Name + ":" + c.job.DataDir }

func (c *cron) line() string {
	return "*/15 * * * * " + c.job.command() + " >/dev/null 2>&1 " + c.marker()
}

func (c *cron) read(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "crontab", "-l").Output()
	if err != nil {
		// "no crontab for user" exits non-zero; treat as empty.
		return nil, nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func (c *cron) write(ctx context.Context, lines []string) error {
	cmd := exec.CommandContext(ctx, "crontab", "-")
	cmd.Stdin = bytes.NewBufferString(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *cron) without(lines []string) []string {
	var keep []string
	for _, l := range lines {
		if !strings.HasSuffix(l, c.marker()) {
			keep = append(keep, l)
		}
	}
	return keep
}

func (c *cron) Installed(ctx context.Context) bool {
	lines, _ := c.read(ctx)
	return len(c.without(lines)) != len(lines)
}

func (c *cron) Install(ctx context.Context) error {
	lines, err := c.read(ctx)
	if err != nil {
		return err
	}
	return c.write(ctx, append(c.without(lines), c.line()))
}

func (c *cron) Uninstall(ctx context.Context) error {
	lines, err := c.read(ctx)
	if err != nil {
		return err
	}
	return c.write(ctx, c.without(lines))
}
