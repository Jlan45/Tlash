package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/profile"
	"github.com/Jlan45/Tlash/internal/scheduledtask"
)

// TaskName is the systemd unit / cron marker for scheduled tasks; a var so tests can isolate it.
var TaskName = "tlash-tasks"

const taskLogLimit = 256 << 10

func (w *Workspace) TaskLogPath() string { return filepath.Join(w.RuntimeDir, "tasks.log") }

// Scheduler returns how run-due is (or would be) registered on this host.
func (w *Workspace) Scheduler() (scheduledtask.Scheduler, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return scheduledtask.For(w.Service().Kind(), scheduledtask.Job{Name: TaskName, TlashBin: bin, DataDir: w.Dir})
}

// SetTasksEnabled registers or removes the periodic job.
func (w *Workspace) SetTasksEnabled(ctx context.Context, on bool) error {
	sched, err := w.Scheduler()
	if err != nil {
		return err
	}
	if on {
		err = sched.Install(ctx)
	} else {
		err = sched.Uninstall(ctx)
	}
	if err != nil {
		return err
	}
	return w.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.Enabled = on })
}

// RunDue updates every subscription and GEO database whose interval has elapsed.
func (w *Workspace) RunDue(ctx context.Context, now time.Time) error {
	logf, err := w.openTaskLog()
	if err != nil {
		return err
	}
	defer logf.Close()
	logLine := func(format string, args ...any) {
		fmt.Fprintf(logf, "%s %s\n", now.Format(time.DateTime), fmt.Sprintf(format, args...))
	}

	profiles, err := w.Profiles.List()
	if err != nil {
		return err
	}
	var failures []string
	for _, p := range profiles {
		if !profileDue(p, now) {
			continue
		}
		if _, err := w.UpdateProfile(ctx, p.ID); err != nil {
			logLine("订阅 %s 更新失败: %v", p.Name, err)
			failures = append(failures, p.Name)
			continue
		}
		logLine("订阅 %s 已更新", p.Name)
	}

	t := w.Settings().Tasks
	if t.GeoUpdateInterval > 0 && now.Sub(t.LastGeoUpdate) >= t.GeoUpdateInterval {
		if running, _ := w.Service().Running(ctx); running {
			if err := w.Client().UpgradeGeo(ctx); err != nil {
				logLine("GEO 数据库更新失败: %v", err)
				failures = append(failures, "GEO")
			} else {
				logLine("GEO 数据库已更新")
				_ = w.UpdateSettings(func(s *appsettings.Settings) { s.Tasks.LastGeoUpdate = now })
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("失败: %s", strings.Join(failures, "、"))
	}
	return nil
}

func profileDue(p profile.Profile, now time.Time) bool {
	return p.Kind == profile.KindRemote && p.UpdateInterval > 0 && now.Sub(p.UpdatedAt) >= p.UpdateInterval
}

// openTaskLog appends to tasks.log, keeping only the newer half once it grows past the limit.
func (w *Workspace) openTaskLog() (*os.File, error) {
	path := w.TaskLogPath()
	if b, err := os.ReadFile(path); err == nil && len(b) > taskLogLimit {
		keep := b[len(b)-taskLogLimit/2:]
		if i := strings.IndexByte(string(keep), '\n'); i >= 0 {
			keep = keep[i+1:]
		}
		_ = os.WriteFile(path, keep, 0o600)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// TaskLogTail returns the last n lines of tasks.log.
func (w *Workspace) TaskLogTail(n int) []string {
	f, err := os.Open(w.TaskLogPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
