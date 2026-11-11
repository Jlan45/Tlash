package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/profile"
)

// NewRemote describes a subscription the user is adding.
type NewRemote struct {
	URL       string
	Name      string
	UserAgent string
	UseProxy  bool
}

// AddRemote downloads and stores a subscription. The first profile added becomes active.
func (w *Workspace) AddRemote(ctx context.Context, in NewRemote) (profile.Profile, error) {
	in.URL = strings.TrimSpace(in.URL)
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return profile.Profile{}, errors.New("订阅链接需要以 http:// 或 https:// 开头")
	}
	p := profile.Profile{
		ID:        profile.NewID(),
		Name:      strings.TrimSpace(in.Name),
		Kind:      profile.KindRemote,
		URL:       in.URL,
		UserAgent: strings.TrimSpace(in.UserAgent),
		UseProxy:  in.UseProxy,
	}
	return w.refreshRemote(ctx, p, true)
}

// AddLocal imports a config file from disk.
func (w *Workspace) AddLocal(ctx context.Context, path, name string) (profile.Profile, error) {
	content, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return profile.Profile{}, err
	}
	if err := profile.Validate(content); err != nil {
		return profile.Profile{}, err
	}
	if name = strings.TrimSpace(name); name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	p := profile.Profile{ID: profile.NewID(), Name: name, Kind: profile.KindLocal, UpdatedAt: time.Now()}
	return p, w.storeNew(ctx, p, content)
}

func (w *Workspace) storeNew(ctx context.Context, p profile.Profile, content []byte) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if err := w.Profiles.Put(p, content); err != nil {
		return err
	}
	if w.Settings().ActiveProfile == "" {
		if err := w.activateLocked(ctx, p.ID); err != nil {
			return fmt.Errorf("订阅已保存，但启用失败: %w", err)
		}
	}
	return nil
}

// UpdateProfile re-downloads a remote profile and re-applies it if active.
func (w *Workspace) UpdateProfile(ctx context.Context, id string) (profile.Profile, error) {
	p, err := w.Profiles.Get(id)
	if err != nil {
		return profile.Profile{}, err
	}
	if p.Kind != profile.KindRemote {
		return p, errors.New("本地配置无法在线更新")
	}
	return w.refreshRemote(ctx, p, false)
}

func (w *Workspace) refreshRemote(ctx context.Context, p profile.Profile, isNew bool) (profile.Profile, error) {
	proxy := w.proxyForFetch(ctx)
	if !p.UseProxy {
		proxy = nil
	}
	res, err := profile.Fetch(ctx, p.URL, p.UserAgent, proxy)
	if err != nil {
		return p, err
	}
	if p.Name == "" {
		p.Name = res.Name
	}
	if p.Name == "" {
		p.Name = profile.NameFromURL(p.URL)
	}
	if p.UpdateInterval == 0 && res.UpdateInterval > 0 && isNew {
		p.UpdateInterval = res.UpdateInterval
	}
	p.Usage = res.Usage
	p.UpdatedAt = time.Now()
	if isNew {
		return p, w.storeNew(ctx, p, res.Content)
	}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if err := w.Profiles.Put(p, res.Content); err != nil {
		return p, err
	}
	if w.Settings().ActiveProfile == p.ID {
		return p, w.applyLocked(ctx)
	}
	return p, nil
}

// SaveProfileMeta updates editable metadata (name, URL, UA, interval, proxy flag).
func (w *Workspace) SaveProfileMeta(p profile.Profile) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return w.Profiles.Put(p, nil)
}

// Activate switches the running config to profile id.
func (w *Workspace) Activate(ctx context.Context, id string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return w.activateLocked(ctx, id)
}

func (w *Workspace) activateLocked(ctx context.Context, id string) error {
	prev := w.Settings().ActiveProfile
	w.mutateSettings(func(s *appsettings.Settings) { s.ActiveProfile = id })
	if err := w.applyLocked(ctx); err != nil {
		w.mutateSettings(func(s *appsettings.Settings) { s.ActiveProfile = prev })
		return err
	}
	return w.saveSettingsLocked()
}

// DeleteProfile removes a profile; deleting the active one falls back to a direct-only config.
func (w *Workspace) DeleteProfile(ctx context.Context, id string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if err := w.Profiles.Delete(id); err != nil {
		return err
	}
	if w.Settings().ActiveProfile != id {
		return nil
	}
	w.mutateSettings(func(s *appsettings.Settings) { s.ActiveProfile = "" })
	if err := w.saveSettingsLocked(); err != nil {
		return err
	}
	return w.applyLocked(ctx)
}

// ReapplyAfterEdit validates a profile the user edited by hand, re-applying it if active.
func (w *Workspace) ReapplyAfterEdit(ctx context.Context, id string) error {
	content, err := w.Profiles.ReadContent(id)
	if err != nil {
		return err
	}
	if err := profile.Validate(content); err != nil {
		return err
	}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.Settings().ActiveProfile == id {
		return w.applyLocked(ctx)
	}
	return nil
}
