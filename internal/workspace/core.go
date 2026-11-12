package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/corebinary"
	"github.com/Jlan45/Tlash/internal/webui"
)

// mutateSettings changes in-memory settings without saving.
func (w *Workspace) mutateSettings(fn func(*appsettings.Settings)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn(&w.settings)
}

func (w *Workspace) saveSettingsLocked() error { return appsettings.Save(w.Dir, w.Settings()) }

// UpdateManaged changes managed config keys and applies them, reverting if the result doesn't validate.
func (w *Workspace) UpdateManaged(ctx context.Context, fn func(*appsettings.Managed)) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	prev := w.Settings().Managed
	w.mutateSettings(func(s *appsettings.Settings) {
		// Copy pointer fields so reverting can't be undone by aliasing.
		s.Managed = cloneManaged(s.Managed)
		fn(&s.Managed)
	})
	if err := w.applyLocked(ctx); err != nil {
		w.mutateSettings(func(s *appsettings.Settings) { s.Managed = prev })
		return err
	}
	return w.saveSettingsLocked()
}

// CoreVersion is the installed core's version, or "" when none is installed.
func (w *Workspace) CoreVersion(ctx context.Context) (current, previous string) {
	current, _ = corebinary.Version(ctx, w.CorePaths().Current)
	if prev := w.CorePaths().Previous; prev != "" {
		previous, _ = corebinary.Version(ctx, prev)
	}
	return current, previous
}

// LatestCore checks the configured channel for the newest release.
func (w *Workspace) LatestCore(ctx context.Context) (corebinary.Release, error) {
	s := w.Settings()
	return corebinary.NewDownloader(s.Mirror).Latest(ctx, s.Core.Channel)
}

// InstallCore downloads r; a running core is restarted onto the new binary.
func (w *Workspace) InstallCore(ctx context.Context, r corebinary.Release, progress *atomic.Int64) (corebinary.InstallResult, error) {
	res, err := corebinary.NewDownloader(w.Settings().Mirror).Download(ctx, r, w.CorePaths(), progress)
	if err != nil {
		return res, err
	}
	return res, w.restartIfRunning(ctx)
}

func (w *Workspace) ImportCore(ctx context.Context, path string) (string, error) {
	v, err := corebinary.ImportFile(ctx, path, w.CorePaths())
	if err != nil {
		return "", err
	}
	return v, w.restartIfRunning(ctx)
}

func (w *Workspace) RollbackCore(ctx context.Context) error {
	if err := corebinary.Rollback(w.CorePaths()); err != nil {
		return err
	}
	return w.restartIfRunning(ctx)
}

func (w *Workspace) restartIfRunning(ctx context.Context) error {
	if running, _ := w.Service().Running(ctx); running {
		return w.Restart(ctx)
	}
	return nil
}

func cloneManaged(m appsettings.Managed) appsettings.Managed {
	clone := func(p *int) *int {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	cloneB := func(p *bool) *bool {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	cloneS := func(p *string) *string {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	m.MixedPort = clone(m.MixedPort)
	m.AllowLan, m.IPv6, m.UnifiedDelay, m.TCPConcurrent = cloneB(m.AllowLan), cloneB(m.IPv6), cloneB(m.UnifiedDelay), cloneB(m.TCPConcurrent)
	m.BindAddress, m.LogLevel, m.FindProcessMode = cloneS(m.BindAddress), cloneS(m.LogLevel), cloneS(m.FindProcessMode)
	if m.DNS != nil {
		d := *m.DNS
		d.FakeIPFilter, d.DefaultNameserver, d.Nameserver = slices.Clone(d.FakeIPFilter), slices.Clone(d.DefaultNameserver), slices.Clone(d.Nameserver)
		d.Fallback, d.ProxyServerNameserver = slices.Clone(d.Fallback), slices.Clone(d.ProxyServerNameserver)
		m.DNS = &d
	}
	if m.Sniffer != nil {
		sn := *m.Sniffer
		sn.HTTPPorts, sn.TLSPorts, sn.QUICPorts = slices.Clone(sn.HTTPPorts), slices.Clone(sn.TLSPorts), slices.Clone(sn.QUICPorts)
		sn.ForceDomain, sn.SkipDomain = slices.Clone(sn.ForceDomain), slices.Clone(sn.SkipDomain)
		m.Sniffer = &sn
	}
	return m
}

// ramBase is where RAM-resident cores go (/tmp is tmpfs on OpenWrt); a var for tests.
var ramBase = "/tmp"

// CorePaths locates the core for the configured storage location.
func (w *Workspace) CorePaths() corebinary.Paths {
	return w.corePathsFor(w.Settings().Core.Location)
}

func (w *Workspace) corePathsFor(location string) corebinary.Paths {
	data := filepath.Join(w.Dir, "core")
	sum := sha256.Sum256([]byte(w.Dir))
	ram := filepath.Join(ramBase, "tlash-"+hex.EncodeToString(sum[:4]), "mihomo")
	switch location {
	case appsettings.CoreCompressed:
		return corebinary.Paths{Current: ram, Archive: filepath.Join(data, "mihomo.gz")}
	case appsettings.CoreRAMOnly:
		return corebinary.Paths{Current: ram}
	}
	return corebinary.PathsIn(data)
}

// EnsureCore puts the core back in RAM after a reboot: unpacks the archive, or downloads
// the latest build of the configured channel for RAM-only storage.
func (w *Workspace) EnsureCore(ctx context.Context) error {
	paths := w.CorePaths()
	if _, err := os.Stat(paths.Current); err == nil {
		return nil
	}
	switch w.Settings().Core.Location {
	case appsettings.CoreCompressed:
		return corebinary.ExtractArchive(paths)
	case appsettings.CoreRAMOnly:
		r, err := w.LatestCore(ctx)
		if err != nil {
			return fmt.Errorf("内存中的内核已丢失，重新下载失败: %w", err)
		}
		_, err = corebinary.NewDownloader(w.Settings().Mirror).Download(ctx, r, paths, nil)
		return err
	}
	return nil
}

// SetCoreLocation moves the installed core to another storage location.
func (w *Workspace) SetCoreLocation(ctx context.Context, location string) error {
	if err := w.EnsureCore(ctx); err != nil {
		return err
	}
	from := w.CorePaths()
	data, err := os.ReadFile(from.Current)
	if err != nil {
		return fmt.Errorf("读取当前内核失败: %w", err)
	}
	to := w.corePathsFor(location)
	if _, err := corebinary.InstallBytes(ctx, data, to); err != nil {
		return err
	}
	if err := w.UpdateSettings(func(s *appsettings.Settings) { s.Core.Location = location }); err != nil {
		return err
	}
	// Drop files the new location doesn't use (that's the space being saved).
	keep := map[string]bool{to.Current: true, to.Previous: true, to.Archive: true}
	for _, f := range []string{from.Current, from.Previous, from.Archive} {
		if f != "" && !keep[f] {
			_ = os.Remove(f)
		}
	}
	// The service now points at a different binary path.
	return w.restartIfRunning(ctx)
}

// FreeSpace is the free space of the data dir's filesystem in bytes.
func (w *Workspace) FreeSpace() int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(w.Dir, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// DashboardDir is where mihomo serves the selected dashboard from (external-ui: ui).
func (w *Workspace) DashboardDir(name string) string {
	return filepath.Join(w.RuntimeDir, "ui", name)
}

// EnsureDashboard extracts the embedded zashboard when it's selected and missing or older
// than the one this Tlash carries. A directory without Tlash's stamp came from an online
// update and is left alone.
func (w *Workspace) EnsureDashboard() error {
	ver, ok := webui.Embedded()
	if !ok || w.Settings().Managed.Dashboard != webui.Name {
		return nil
	}
	dir := w.DashboardDir(webui.Name)
	if _, err := os.Stat(dir); err == nil {
		installed := webui.InstalledVersion(dir)
		if installed == "" || installed == ver {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	return webui.Extract(dir)
}
