package workspace

import (
	"context"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/portowner"
)

// inboundPort is a listener the runtime config asks for.
type inboundPort struct {
	key  string // config key, e.g. mixed-port
	port int
}

func inboundPorts(cfg []byte) []inboundPort {
	var c map[string]any
	if yaml.Unmarshal(cfg, &c) != nil {
		return nil
	}
	var out []inboundPort
	for _, k := range []string{"mixed-port", "port", "socks-port", "redir-port", "tproxy-port"} {
		if n, ok := c[k].(int); ok && n > 0 {
			out = append(out, inboundPort{key: k, port: n})
		}
	}
	return out
}

func reported(cfg mihomoapi.Config, key string) int {
	switch key {
	case "mixed-port":
		return cfg.MixedPort
	case "port":
		return cfg.Port
	case "socks-port":
		return cfg.SocksPort
	case "redir-port":
		return cfg.RedirPort
	case "tproxy-port":
		return cfg.TProxyPort
	}
	return 0
}

func busyError(ports []inboundPort) error {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = fmt.Sprintf("%s %d", p.key, p.port)
		if who := portowner.Describe(p.port); who != "" {
			parts[i] += " 被 " + who + " 占用"
		} else {
			parts[i] += " 已被占用"
		}
	}
	return fmt.Errorf("%s。请停止占用端口的程序，或在 设置 → 接管 修改端口", strings.Join(parts, "；"))
}

// checkPortsFree runs before the core starts: anything listening now isn't ours.
func checkPortsFree(cfg []byte) error {
	var busy []inboundPort
	for _, p := range inboundPorts(cfg) {
		if portowner.Busy(p.port) {
			busy = append(busy, p)
		}
	}
	if len(busy) > 0 {
		return busyError(busy)
	}
	return nil
}

// verifyListeners compares the ports the config asks for with what the core actually opened;
// mihomo only logs a failed listen and keeps running, reporting the port as 0.
func (w *Workspace) verifyListeners(ctx context.Context, cfg []byte) error {
	running, err := w.client.Config(ctx)
	if err != nil {
		return nil // reachability problems surface elsewhere
	}
	var failed []inboundPort
	for _, p := range inboundPorts(cfg) {
		if reported(running, p.key) != p.port {
			failed = append(failed, p)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("内核已运行，但端口未能监听：%w", busyError(failed))
	}
	return nil
}
