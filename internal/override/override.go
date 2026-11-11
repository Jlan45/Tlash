// Package override stores YAML fragments merged over profiles when generating the runtime config.
package override

import (
	"errors"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/contentstore"
)

type Override struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
	// Profiles limits the override to these profile IDs; empty applies it to all.
	Profiles []string `yaml:"profiles,omitempty"`
}

func (o Override) ItemID() string { return o.ID }

// AppliesTo reports whether o should be merged when profileID is active.
func (o Override) AppliesTo(profileID string) bool {
	return o.Enabled && (len(o.Profiles) == 0 || slices.Contains(o.Profiles, profileID))
}

type Store = contentstore.Store[Override]

func NewID() string { return contentstore.NewID() }

func NewStore(dir string) *Store { return contentstore.New[Override](dir) }

const Template = `# Tlash 覆写：按顺序合并到订阅配置之上（接管设置最后生效）
#
# 映射深度合并；列表和标量默认整体替换。键名标记：
#   +rules:   插入到原列表最前        rules+:  追加到原列表最后
#   dns!:     整体替换，不做深度合并
#
# 示例：
# +rules:
#   - DOMAIN-SUFFIX,example.com,DIRECT
# dns:
#   enhanced-mode: fake-ip
`

// Validate checks content parses as a mapping (or is empty / comments only).
func Validate(content []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("覆写不是有效的 YAML: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("覆写的顶层必须是映射（key: value）")
	}
	return nil
}
