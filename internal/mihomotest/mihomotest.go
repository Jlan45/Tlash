// Package mihomotest starts a real mihomo binary for integration tests.
// Tests using it are skipped unless TLASH_MIHOMO_BIN points at a mihomo executable.
package mihomotest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

// Proxies point at closed local ports, so delay tests fail fast without network access.
const configTemplate = `mixed-port: %d
mode: rule
log-level: info
external-controller-unix: mihomo.sock
proxies:
  - {name: "香港 01", type: socks5, server: 127.0.0.1, port: 1}
  - {name: "日本 01", type: socks5, server: 127.0.0.1, port: 2}
  - {name: "美国 01", type: http, server: 127.0.0.1, port: 3}
proxy-groups:
  - {name: "🚀 节点选择", type: select, proxies: ["♻️ 自动选择", "香港 01", "日本 01", "美国 01", DIRECT]}
  - {name: "♻️ 自动选择", type: url-test, proxies: ["香港 01", "日本 01", "美国 01"], url: "http://127.0.0.1:1/", interval: 3600}
  - {name: "🐟 漏网之鱼", type: select, proxies: ["🚀 节点选择", DIRECT]}
rules:
  - MATCH,🐟 漏网之鱼
`

// Instance is a running mihomo started by Start.
type Instance struct {
	Client    *mihomoapi.Client
	MixedPort int
}

// Start runs mihomo with the test config in a temp dir and returns a client on its unix socket.
func Start(t *testing.T) Instance {
	t.Helper()
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	// Short dir: unix socket paths are limited to ~104 bytes.
	dir, err := os.MkdirTemp("", "tlash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	port := freePort(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fmt.Sprintf(configTemplate, port)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-d", dir)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	client := mihomoapi.New(mihomoapi.Endpoint{UnixSocket: filepath.Join(dir, "mihomo.sock")}, "")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := client.Version(context.Background()); err == nil {
			return Instance{Client: client, MixedPort: port}
		} else if time.Now().After(deadline) {
			t.Fatalf("mihomo did not come up: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
