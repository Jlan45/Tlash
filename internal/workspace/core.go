package workspace

import (
	"context"
	"sync/atomic"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/corebinary"
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
	current, _ = corebinary.Version(ctx, w.Core.Current)
	previous, _ = corebinary.Version(ctx, w.Core.Previous)
	return current, previous
}

// LatestCore checks the configured channel for the newest release.
func (w *Workspace) LatestCore(ctx context.Context) (corebinary.Release, error) {
	s := w.Settings()
	return corebinary.NewDownloader(s.Mirror).Latest(ctx, s.Core.Channel)
}

// InstallCore downloads r; a running core is restarted onto the new binary.
func (w *Workspace) InstallCore(ctx context.Context, r corebinary.Release, progress *atomic.Int64) (corebinary.InstallResult, error) {
	res, err := corebinary.NewDownloader(w.Settings().Mirror).Download(ctx, r, w.Core, progress)
	if err != nil {
		return res, err
	}
	return res, w.restartIfRunning(ctx)
}

func (w *Workspace) ImportCore(ctx context.Context, path string) (string, error) {
	v, err := corebinary.ImportFile(ctx, path, w.Core)
	if err != nil {
		return "", err
	}
	return v, w.restartIfRunning(ctx)
}

func (w *Workspace) RollbackCore(ctx context.Context) error {
	if err := corebinary.Rollback(w.Core); err != nil {
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
	return m
}
