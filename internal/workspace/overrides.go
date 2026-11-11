package workspace

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Jlan45/Tlash/internal/override"
)

// AddOverride creates a disabled override from the template; the user edits then enables it.
func (w *Workspace) AddOverride(name string) (override.Override, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return override.Override{}, errors.New("名称不能为空")
	}
	o := override.Override{ID: override.NewID(), Name: name}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return o, w.Overrides.Put(o, []byte(override.Template))
}

// changeOverride mutates one override and re-applies, reverting the metadata if the config breaks.
func (w *Workspace) changeOverride(ctx context.Context, id string, fn func(*override.Override)) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	prev, err := w.Overrides.Get(id)
	if err != nil {
		return err
	}
	next := prev
	next.Profiles = slices.Clone(prev.Profiles)
	fn(&next)
	if err := w.Overrides.Put(next, nil); err != nil {
		return err
	}
	if err := w.applyLocked(ctx); err != nil {
		_ = w.Overrides.Put(prev, nil)
		return err
	}
	return nil
}

func (w *Workspace) SetOverrideEnabled(ctx context.Context, id string, on bool) error {
	return w.changeOverride(ctx, id, func(o *override.Override) { o.Enabled = on })
}

// SetOverrideScope limits an override to the given profiles (none = all profiles).
func (w *Workspace) SetOverrideScope(ctx context.Context, id string, profileIDs []string) error {
	return w.changeOverride(ctx, id, func(o *override.Override) { o.Profiles = profileIDs })
}

func (w *Workspace) RenameOverride(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	o, err := w.Overrides.Get(id)
	if err != nil {
		return err
	}
	o.Name = name
	return w.Overrides.Put(o, nil)
}

func (w *Workspace) MoveOverride(ctx context.Context, id string, delta int) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if err := w.Overrides.Move(id, delta); err != nil {
		return err
	}
	if err := w.applyLocked(ctx); err != nil {
		_ = w.Overrides.Move(id, -delta)
		return err
	}
	return nil
}

func (w *Workspace) DeleteOverride(ctx context.Context, id string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	o, err := w.Overrides.Get(id)
	if err != nil {
		return err
	}
	if err := w.Overrides.Delete(id); err != nil {
		return err
	}
	if o.Enabled {
		return w.applyLocked(ctx)
	}
	return nil
}

// ReapplyAfterOverrideEdit validates a hand-edited override and applies it if enabled.
func (w *Workspace) ReapplyAfterOverrideEdit(ctx context.Context, id string) error {
	content, err := w.Overrides.ReadContent(id)
	if err != nil {
		return err
	}
	if err := override.Validate(content); err != nil {
		return err
	}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	o, err := w.Overrides.Get(id)
	if err != nil {
		return err
	}
	if !o.AppliesTo(w.Settings().ActiveProfile) {
		return nil
	}
	return w.applyLocked(ctx)
}
