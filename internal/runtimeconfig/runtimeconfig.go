// Package runtimeconfig builds the config mihomo actually runs from a profile plus Tlash's managed settings.
package runtimeconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

const validateTimeout = 90 * time.Second

// Generate overlays managed settings onto profile content, preserving the profile's key order and comments.
func Generate(profile []byte, m appsettings.Managed, socketPath string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(profile, &doc); err != nil {
		return nil, fmt.Errorf("解析订阅配置失败: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("订阅配置的顶层不是映射")
	}

	setOpt(root, "mixed-port", m.MixedPort, intNode)
	setOpt(root, "allow-lan", m.AllowLan, boolNode)
	setOpt(root, "bind-address", m.BindAddress, strNode)
	setOpt(root, "ipv6", m.IPv6, boolNode)
	setOpt(root, "log-level", m.LogLevel, strNode)
	setOpt(root, "unified-delay", m.UnifiedDelay, boolNode)
	setOpt(root, "tcp-concurrent", m.TCPConcurrent, boolNode)
	setOpt(root, "find-process-mode", m.FindProcessMode, strNode)

	// The control API is always Tlash's: a unix socket, plus TCP only when explicitly enabled.
	setKey(root, "external-controller-unix", strNode(socketPath))
	if m.ExternalController != "" {
		setKey(root, "external-controller", strNode(m.ExternalController))
	} else {
		deleteKey(root, "external-controller")
	}
	setKey(root, "secret", strNode(m.Secret))

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Validate runs `mihomo -t` so a broken config never replaces a working one.
func Validate(ctx context.Context, corePath, homeDir, configPath string) error {
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, corePath, "-t", "-d", homeDir, "-f", configPath).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("配置校验超时（%s），可能在下载 GEO 数据库", validateTimeout)
	}
	return fmt.Errorf("配置校验失败: %s", summarizeTestOutput(out, err))
}

// summarizeTestOutput keeps mihomo's error lines and drops the info chatter.
func summarizeTestOutput(out []byte, err error) string {
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, "level=error") || strings.Contains(line, "level=fatal") ||
			strings.Contains(line, "test failed") || (!strings.Contains(line, "level=") && strings.TrimSpace(line) != "") {
			keep = append(keep, extractMsg(line))
		}
	}
	if len(keep) == 0 {
		return err.Error()
	}
	return strings.Join(keep, "; ")
}

// extractMsg pulls msg="..." out of a logrus line.
func extractMsg(line string) string {
	_, rest, ok := strings.Cut(line, `msg="`)
	if !ok {
		return strings.TrimSpace(line)
	}
	if s, err := strconv.Unquote(`"` + strings.TrimSuffix(strings.TrimSpace(rest), `"`) + `"`); err == nil {
		return s
	}
	return strings.TrimSuffix(rest, `"`)
}

func setOpt[T any](root *yaml.Node, key string, v *T, mk func(T) *yaml.Node) {
	if v != nil {
		setKey(root, key, mk(*v))
	}
}

func setKey(root *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			// Keep the comment attached to the key so the profile still reads naturally.
			val.LineComment = root.Content[i+1].LineComment
			root.Content[i+1] = val
			return
		}
	}
	root.Content = append(root.Content, strNode(key), val)
}

func deleteKey(root *yaml.Node, key string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
	}
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
}

func boolNode(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// MinimalProfile lets the core start before any subscription exists.
const MinimalProfile = `mode: direct
proxies: []
rules:
  - MATCH,DIRECT
`
