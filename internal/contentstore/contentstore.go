// Package contentstore is a directory of <id>.yaml content files plus an index.yaml holding
// ordered metadata for each item. Profiles and overrides both use it.
package contentstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// Item is metadata with a stable ID.
type Item interface{ ItemID() string }

type Store[T Item] struct {
	dir string
}

func New[T Item](dir string) *Store[T] { return &Store[T]{dir: dir} }

func (s *Store[T]) indexPath() string { return filepath.Join(s.dir, "index.yaml") }

func (s *Store[T]) ContentPath(id string) string { return filepath.Join(s.dir, id+".yaml") }

type index[T any] struct {
	Items []T `yaml:"items"`
}

func (s *Store[T]) List() ([]T, error) {
	b, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var idx index[T]
	if err := yaml.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", s.indexPath(), err)
	}
	return idx.Items, nil
}

func (s *Store[T]) Get(id string) (T, error) {
	items, err := s.List()
	if err != nil {
		var zero T
		return zero, err
	}
	for _, it := range items {
		if it.ItemID() == id {
			return it, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("不存在: %s", id)
}

func (s *Store[T]) save(items []T) error {
	b, err := yaml.Marshal(index[T]{Items: items})
	if err != nil {
		return err
	}
	return appsettings.WriteFileAtomic(s.indexPath(), b, 0o600)
}

// Put inserts or replaces it, writing content first when non-nil.
func (s *Store[T]) Put(it T, content []byte) error {
	if it.ItemID() == "" {
		return errors.New("ID 为空")
	}
	if content != nil {
		if err := appsettings.WriteFileAtomic(s.ContentPath(it.ItemID()), content, 0o600); err != nil {
			return err
		}
	}
	items, err := s.List()
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(items, func(x T) bool { return x.ItemID() == it.ItemID() }); i >= 0 {
		items[i] = it
	} else {
		items = append(items, it)
	}
	return s.save(items)
}

// Move shifts an item by delta positions in the index order.
func (s *Store[T]) Move(id string, delta int) error {
	items, err := s.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(items, func(x T) bool { return x.ItemID() == id })
	if i < 0 {
		return fmt.Errorf("不存在: %s", id)
	}
	j := max(0, min(i+delta, len(items)-1))
	if i == j {
		return nil
	}
	it := items[i]
	items = slices.Delete(items, i, i+1)
	items = slices.Insert(items, j, it)
	return s.save(items)
}

func (s *Store[T]) Delete(id string) error {
	items, err := s.List()
	if err != nil {
		return err
	}
	items = slices.DeleteFunc(items, func(x T) bool { return x.ItemID() == id })
	if err := s.save(items); err != nil {
		return err
	}
	if err := os.Remove(s.ContentPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store[T]) ReadContent(id string) ([]byte, error) { return os.ReadFile(s.ContentPath(id)) }

func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
