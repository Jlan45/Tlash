package runtimeconfig

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// mergeOverride merges the mapping src into dst in place.
// Maps merge recursively; anything else replaces. Key markers: "+k" prepends to list k,
// "k+" appends to it, "k!" replaces k without merging.
func mergeOverride(dst, src *yaml.Node, path string) error {
	for i := 0; i+1 < len(src.Content); i += 2 {
		rawKey, val := src.Content[i].Value, src.Content[i+1]
		key, op := rawKey, ""
		switch {
		case strings.HasPrefix(rawKey, "+"):
			key, op = rawKey[1:], "prepend"
		case strings.HasSuffix(rawKey, "+"):
			key, op = strings.TrimSuffix(rawKey, "+"), "append"
		case strings.HasSuffix(rawKey, "!"):
			key, op = strings.TrimSuffix(rawKey, "!"), "replace"
		}
		where := path + key
		existing := lookup(dst, key)
		switch op {
		case "prepend", "append":
			if val.Kind != yaml.SequenceNode {
				return fmt.Errorf("覆写 %s: %s 的值必须是列表", where, rawKey)
			}
			if existing == nil {
				setKey(dst, key, val)
				continue
			}
			if existing.Kind != yaml.SequenceNode {
				return fmt.Errorf("覆写 %s: 订阅中的 %s 不是列表", where, key)
			}
			if op == "prepend" {
				existing.Content = append(append([]*yaml.Node{}, val.Content...), existing.Content...)
			} else {
				existing.Content = append(existing.Content, val.Content...)
			}
		case "replace":
			setKey(dst, key, val)
		default:
			if existing != nil && existing.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode {
				if err := mergeOverride(existing, val, where+"."); err != nil {
					return err
				}
				continue
			}
			setKey(dst, key, val)
		}
	}
	return nil
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
