package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// Uninstall undoes everything Tlash set up on the host: gateway changes, scheduled tasks,
// the core service, the login proxy-env script and the RAM copy of the core. With purge the
// data dir goes too. The tlash binary itself is the caller's to remove.
func (w *Workspace) Uninstall(ctx context.Context, purge bool) error {
	var errs []error
	if _, pending := w.PendingGateway(); pending {
		errs = append(errs, w.RollbackGateway(ctx))
	} else if w.Settings().Gateway.Enabled {
		errs = append(errs, w.DisableGateway(ctx))
	}
	if sched, err := w.Scheduler(); err == nil && sched.Installed(ctx) {
		errs = append(errs, sched.Uninstall(ctx))
	}
	errs = append(errs, w.Service().Uninstall(ctx))
	if LoginEnvEnabled() {
		errs = append(errs, w.SetLoginEnv(false))
	}
	if loc := w.Settings().Core.Location; loc != appsettings.CoreInData {
		errs = append(errs, os.RemoveAll(filepath.Dir(w.CorePaths().Current)))
	}
	if purge {
		errs = append(errs, os.RemoveAll(w.Dir))
	}
	return errors.Join(errs...)
}
