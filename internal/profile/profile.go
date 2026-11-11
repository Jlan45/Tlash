// Package profile stores subscriptions: their metadata and the raw config each one delivered.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

type Kind string

const (
	KindRemote Kind = "remote"
	KindLocal  Kind = "local"
)

const DefaultUserAgent = "clash.meta"

type Profile struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	Kind      Kind   `yaml:"kind"`
	URL       string `yaml:"url,omitempty"`
	UserAgent string `yaml:"user_agent,omitempty"`
	// UpdateInterval is how often scheduled updates refresh it; 0 disables.
	UpdateInterval time.Duration `yaml:"update_interval,omitempty"`
	// UseProxy fetches through the running core's mixed port.
	UseProxy  bool      `yaml:"use_proxy,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty"`
	Usage     *Usage    `yaml:"usage,omitempty"`
}

// Usage comes from the provider's subscription-userinfo header.
type Usage struct {
	Upload   int64     `yaml:"upload"`
	Download int64     `yaml:"download"`
	Total    int64     `yaml:"total"`
	Expire   time.Time `yaml:"expire,omitempty"`
}

func (u Usage) Used() int64 { return u.Upload + u.Download }

// Store is the profiles/ directory: index.yaml plus one <id>.yaml per profile.
type Store struct {
	dir string
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.yaml") }

// ContentPath is the profile's raw config as downloaded or imported. Tlash never edits it.
func (s *Store) ContentPath(id string) string { return filepath.Join(s.dir, id+".yaml") }

func (s *Store) List() ([]Profile, error) {
	b, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var idx struct {
		Profiles []Profile `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("解析订阅索引失败: %w", err)
	}
	return idx.Profiles, nil
}

func (s *Store) Get(id string) (Profile, error) {
	ps, err := s.List()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("订阅不存在: %s", id)
}

func (s *Store) saveIndex(ps []Profile) error {
	b, err := yaml.Marshal(struct {
		Profiles []Profile `yaml:"profiles"`
	}{ps})
	if err != nil {
		return err
	}
	return appsettings.WriteFileAtomic(s.indexPath(), b, 0o600)
}

// Put inserts or replaces p (matched by ID), writing content first when non-nil.
func (s *Store) Put(p Profile, content []byte) error {
	if p.ID == "" {
		return errors.New("订阅 ID 为空")
	}
	if content != nil {
		if err := appsettings.WriteFileAtomic(s.ContentPath(p.ID), content, 0o600); err != nil {
			return err
		}
	}
	ps, err := s.List()
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(ps, func(x Profile) bool { return x.ID == p.ID }); i >= 0 {
		ps[i] = p
	} else {
		ps = append(ps, p)
	}
	return s.saveIndex(ps)
}

func (s *Store) Delete(id string) error {
	ps, err := s.List()
	if err != nil {
		return err
	}
	ps = slices.DeleteFunc(ps, func(x Profile) bool { return x.ID == id })
	if err := s.saveIndex(ps); err != nil {
		return err
	}
	if err := os.Remove(s.ContentPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) ReadContent(id string) ([]byte, error) { return os.ReadFile(s.ContentPath(id)) }

func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
