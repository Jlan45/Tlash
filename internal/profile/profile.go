// Package profile stores subscriptions: their metadata and the raw config each one delivered.
package profile

import (
	"time"

	"github.com/Jlan45/Tlash/internal/contentstore"
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

func (p Profile) ItemID() string { return p.ID }

// Store is the profiles/ directory. Profile content is the raw config as downloaded or
// imported; Tlash never edits it.
type Store = contentstore.Store[Profile]

func NewStore(dir string) *Store { return contentstore.New[Profile](dir) }

func NewID() string { return contentstore.NewID() }
