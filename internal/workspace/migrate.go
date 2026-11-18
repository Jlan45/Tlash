package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// Older releases also installed for regular users: binary in ~/.local/bin, data in
// ~/.local/share/tlash, core under a systemd user service (or a plain process) and tasks in a
// user timer or crontab. Tlash now runs only as root; this moves such an install over.

// legacyMarker is left in a legacy dir once it has been migrated or declined.
const legacyMarker = ".moved-to-root"

// LegacyInstall is a per-user install found for the user who ran sudo.
type LegacyInstall struct {
	User *user.User
	Dir  string
}

// FindLegacyInstall looks for $SUDO_USER's old data dir that hasn't been handled yet.
func FindLegacyInstall() (LegacyInstall, bool) {
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return LegacyInstall{}, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return LegacyInstall{}, false
	}
	dir := filepath.Join(u.HomeDir, ".local", "share", "tlash")
	if !Exists(dir) {
		return LegacyInstall{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, legacyMarker)); err == nil {
		return LegacyInstall{}, false
	}
	return LegacyInstall{User: u, Dir: dir}, true
}

// Decline remembers not to offer the migration again.
func (l LegacyInstall) Decline() error {
	return os.WriteFile(filepath.Join(l.Dir, legacyMarker), []byte("declined\n"), 0o644)
}

// MigrateLegacy stops the old per-user core and tasks, removes the old binary, and copies
// subscriptions, overrides, settings and the core binary into target (which must not hold
// subscriptions yet). The old data dir is left in place.
func MigrateLegacy(ctx context.Context, l LegacyInstall, target string) error {
	if entries, _ := os.ReadDir(filepath.Join(target, "profiles")); len(entries) > 0 {
		return fmt.Errorf("%s 中已有订阅，没有迁移；旧数据仍在 %s", target, l.Dir)
	}
	l.stopServices(ctx)

	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, sub := range []string{"profiles", "overrides", "core"} {
		if err := copyTree(filepath.Join(l.Dir, sub), filepath.Join(target, sub)); err != nil {
			return fmt.Errorf("复制 %s 失败: %w", sub, err)
		}
	}
	s, _, err := appsettings.Load(l.Dir)
	if err != nil {
		return err
	}
	// The old supervisor (systemd-user / process) doesn't apply to root.
	s.Core.Service = ""
	if err := appsettings.Save(target, s); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.Dir, legacyMarker), []byte("migrated to "+target+"\n"), 0o644)
}

// stopServices is best effort: each piece may not exist.
func (l LegacyInstall) stopServices(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	name := l.User.Username

	// systemd user units, reached from root through the user's manager.
	userctl := func(args ...string) {
		_ = exec.CommandContext(ctx, "systemctl", append([]string{"--user", "-M", name + "@"}, args...)...).Run()
	}
	userctl("disable", "--now", ServiceName+".service", TaskName+".timer")
	unitDir := filepath.Join(l.User.HomeDir, ".config", "systemd", "user")
	for _, f := range []string{ServiceName + ".service", TaskName + ".service", TaskName + ".timer"} {
		_ = os.Remove(filepath.Join(unitDir, f))
	}
	userctl("daemon-reload")

	// A core started as a plain background process.
	if b, err := os.ReadFile(filepath.Join(l.Dir, "runtime", "mihomo.pid")); err == nil {
		var pid int
		if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &pid); err == nil && pid > 0 {
			if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil &&
				bytes.HasPrefix(cmdline, []byte(filepath.Join(l.Dir, "core"))) {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	}

	// The user's crontab entry for `task run-due`.
	if out, err := exec.CommandContext(ctx, "crontab", "-u", name, "-l").Output(); err == nil {
		marker := "# " + TaskName + ":" + l.Dir
		var keep []string
		changed := false
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if strings.HasSuffix(line, marker) {
				changed = true
				continue
			}
			keep = append(keep, line)
		}
		if changed {
			cmd := exec.CommandContext(ctx, "crontab", "-u", name, "-")
			cmd.Stdin = strings.NewReader(strings.Join(keep, "\n") + "\n")
			_ = cmd.Run()
		}
	}

	// The old binary usually comes first in PATH and would shadow the root install.
	_ = os.Remove(filepath.Join(l.User.HomeDir, ".local", "bin", "tlash"))
}

// copyTree copies regular files and directories; a missing src is not an error.
func copyTree(src, dst string) error {
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, out, info.Mode().Perm())
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
