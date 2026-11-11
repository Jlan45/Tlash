// Package workspace is a Tlash data directory and the operations that span its parts:
// settings, profiles, the core binary, the generated config and the core service.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/corebinary"
	"github.com/Jlan45/Tlash/internal/coreservice"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/override"
	"github.com/Jlan45/Tlash/internal/profile"
	"github.com/Jlan45/Tlash/internal/runtimeconfig"
)

// ServiceName is a var so tests can avoid clashing with a real install.
var ServiceName = "tlash-mihomo"

type Workspace struct {
	Dir        string
	RuntimeDir string
	ConfigPath string
	SocketPath string
	Core       corebinary.Paths
	Profiles   *profile.Store
	Overrides  *override.Store

	// opMu serializes multi-step operations; mu guards settings/service and is held only briefly,
	// so the UI can read settings while a slow operation (validation, startup) runs.
	opMu     sync.Mutex
	mu       sync.Mutex
	settings appsettings.Settings
	service  coreservice.Manager
	client   *mihomoapi.Client
}

// DefaultDir is /etc/tlash for root, else the per-user data dir.
func DefaultDir() string {
	if os.Geteuid() == 0 {
		return "/etc/tlash"
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "tlash")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "tlash")
}

// Exists reports whether dir already holds a Tlash workspace.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, appsettings.FileName))
	return err == nil
}

// Open loads (or initializes) the workspace in dir.
func Open(dir string) (*Workspace, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	w := &Workspace{
		Dir:        dir,
		RuntimeDir: filepath.Join(dir, "runtime"),
		Core:       corebinary.PathsIn(filepath.Join(dir, "core")),
		Profiles:   profile.NewStore(filepath.Join(dir, "profiles")),
		Overrides:  override.NewStore(filepath.Join(dir, "overrides")),
	}
	w.ConfigPath = filepath.Join(w.RuntimeDir, "config.yaml")
	w.SocketPath = filepath.Join(w.RuntimeDir, "mihomo.sock")
	for _, sub := range []string{"", "runtime", "core", "profiles", "overrides", "backups"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
	}
	s, exists, err := appsettings.Load(dir)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := appsettings.Save(dir, s); err != nil {
			return nil, err
		}
	}
	w.settings = s
	w.client = mihomoapi.New(mihomoapi.Endpoint{UnixSocket: w.SocketPath}, "")
	if err := w.rebuildService(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Workspace) rebuildService() error {
	// Caller holds mu (or is Open, before the workspace is shared).
	m, err := coreservice.New(coreservice.Detect(w.settings.Core.Service), coreservice.Spec{
		Name:       ServiceName,
		CorePath:   w.Core.Current,
		HomeDir:    w.RuntimeDir,
		ConfigPath: w.ConfigPath,
		LogPath:    filepath.Join(w.RuntimeDir, "mihomo.log"),
		PIDPath:    filepath.Join(w.RuntimeDir, "mihomo.pid"),
	})
	if err != nil {
		return err
	}
	w.service = m
	return nil
}

// Client talks to the core over the workspace's unix socket (no secret needed).
func (w *Workspace) Client() *mihomoapi.Client { return w.client }

func (w *Workspace) Service() coreservice.Manager {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.service
}

func (w *Workspace) Settings() appsettings.Settings {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.settings
}

// UpdateSettings mutates and persists settings; it doesn't regenerate the config.
func (w *Workspace) UpdateSettings(fn func(*appsettings.Settings)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	next := w.settings
	fn(&next)
	if err := appsettings.Save(w.Dir, next); err != nil {
		return err
	}
	serviceChanged := next.Core.Service != w.settings.Core.Service
	w.settings = next
	if serviceChanged {
		return w.rebuildService()
	}
	return nil
}

func (w *Workspace) CoreInstalled() bool {
	_, err := os.Stat(w.Core.Current)
	return err == nil
}

// Apply regenerates the runtime config, validates it and, if the core is running, hot-reloads it.
// On validation failure the previous config stays in place.
func (w *Workspace) Apply(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return w.applyLocked(ctx)
}

// Generate builds the runtime config for the current settings without writing or applying it.
func (w *Workspace) Generate() ([]byte, error) {
	settings := w.Settings()
	content := []byte(runtimeconfig.MinimalProfile)
	if id := settings.ActiveProfile; id != "" {
		b, err := w.Profiles.ReadContent(id)
		if err != nil {
			return nil, fmt.Errorf("读取订阅失败: %w", err)
		}
		content = b
	}
	overrides, err := w.Overrides.List()
	if err != nil {
		return nil, err
	}
	var named []runtimeconfig.NamedOverride
	for _, o := range overrides {
		if !o.AppliesTo(settings.ActiveProfile) {
			continue
		}
		b, err := w.Overrides.ReadContent(o.ID)
		if err != nil {
			return nil, fmt.Errorf("读取覆写「%s」失败: %w", o.Name, err)
		}
		named = append(named, runtimeconfig.NamedOverride{Name: o.Name, Content: b})
	}
	return runtimeconfig.Generate(content, named, settings.Managed, w.SocketPath)
}

func (w *Workspace) applyLocked(ctx context.Context) error {
	cfg, err := w.writeConfigLocked(ctx)
	if err != nil {
		return err
	}
	if running, _ := w.Service().Running(ctx); running {
		if err := w.client.ReloadConfig(ctx, w.ConfigPath); err != nil {
			return fmt.Errorf("配置已生成，但热重载失败: %w", err)
		}
		return w.verifyListeners(ctx, cfg)
	}
	return nil
}

// writeConfigLocked generates, validates and installs config.yaml without touching the core.
func (w *Workspace) writeConfigLocked(ctx context.Context) ([]byte, error) {
	cfg, err := w.Generate()
	if err != nil {
		return nil, err
	}
	staged := w.ConfigPath + ".new"
	if err := appsettings.WriteFileAtomic(staged, cfg, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	if w.CoreInstalled() {
		if err := runtimeconfig.Validate(ctx, w.Core.Current, w.RuntimeDir, staged); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(w.ConfigPath); err == nil {
		if err := os.Rename(w.ConfigPath, w.ConfigPath+".bak"); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(staged, w.ConfigPath); err != nil {
		return nil, err
	}
	return cfg, nil
}

// RollbackConfig restores the config that was in place before the last Apply.
func (w *Workspace) RollbackConfig(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	bak := w.ConfigPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return errors.New("没有上一份配置可以回退")
	}
	cur := w.ConfigPath + ".swap"
	if err := os.Rename(w.ConfigPath, cur); err != nil {
		return err
	}
	if err := os.Rename(bak, w.ConfigPath); err != nil {
		_ = os.Rename(cur, w.ConfigPath)
		return err
	}
	if err := os.Rename(cur, bak); err != nil {
		return err
	}
	if running, _ := w.Service().Running(ctx); running {
		return w.client.ReloadConfig(ctx, w.ConfigPath)
	}
	return nil
}

// Start installs the service definition if needed, writes a fresh config and starts the core.
func (w *Workspace) Start(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if !w.CoreInstalled() {
		return errors.New("尚未安装内核，请先在内核页下载")
	}
	if running, _ := w.Service().Running(ctx); running {
		return nil
	}
	if err := w.applyLocked(ctx); err != nil {
		return err
	}
	cfg, err := os.ReadFile(w.ConfigPath)
	if err != nil {
		return err
	}
	if err := w.checkPortsFree(cfg); err != nil {
		return err
	}
	if err := w.Service().Install(ctx); err != nil {
		return fmt.Errorf("注册服务失败: %w", err)
	}
	if err := w.Service().Start(ctx); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	if err := w.waitForAPI(ctx, 15*time.Second); err != nil {
		return err
	}
	return w.verifyListeners(ctx, cfg)
}

func (w *Workspace) Stop(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return w.Service().Stop(ctx)
}

// Restart restarts the core process (needed after a core upgrade; config changes hot-reload).
func (w *Workspace) Restart(ctx context.Context) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	// Regenerate first, so a restart also picks up profile or Tlash changes (e.g. after an upgrade).
	cfg, err := w.writeConfigLocked(ctx)
	if err != nil {
		return err
	}
	if err := w.Service().Install(ctx); err != nil {
		return err
	}
	if err := w.Service().Restart(ctx); err != nil {
		return err
	}
	if err := w.waitForAPI(ctx, 15*time.Second); err != nil {
		return err
	}
	return w.verifyListeners(ctx, cfg)
}

func (w *Workspace) waitForAPI(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := w.client.Version(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("内核已启动，但 %s 内控制接口无响应（日志: %s）", timeout, w.Service().LogHint())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// proxyForFetch returns the running core's mixed port as an HTTP proxy, or nil if unavailable.
func (w *Workspace) proxyForFetch(ctx context.Context) *url.URL {
	cfg, err := w.client.Config(ctx)
	if err != nil || cfg.MixedPort == 0 {
		return nil
	}
	return &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(cfg.MixedPort)}
}
