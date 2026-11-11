package mihomoapi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/mihomotest"
)

func TestAgainstRealMihomo(t *testing.T) {
	inst := mihomotest.Start(t)
	c := inst.Client
	ctx := context.Background()

	v, err := c.Version(ctx)
	if err != nil || !v.Meta || v.Version == "" {
		t.Fatalf("Version() = %+v, %v", v, err)
	}

	if err := c.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.Config(ctx)
	if err != nil || cfg.Mode != "global" {
		t.Fatalf("Config() mode = %q, %v", cfg.Mode, err)
	}

	proxies, err := c.Proxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g := proxies["🚀 节点选择"]
	if !g.IsGroup() || g.Type != "Selector" || len(g.All) != 5 {
		t.Fatalf("unexpected group: %+v", g)
	}
	if err := c.SelectProxy(ctx, "🚀 节点选择", "日本 01"); err != nil {
		t.Fatal(err)
	}
	proxies, _ = c.Proxies(ctx)
	if now := proxies["🚀 节点选择"].Now; now != "日本 01" {
		t.Fatalf("Now = %q after select", now)
	}

	// The proxy's port is closed, so mihomo reports a failed probe as an API error.
	test := mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: 2 * time.Second}
	_, err = c.ProxyDelay(ctx, "香港 01", test)
	var apiErr *mihomoapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ProxyDelay on dead proxy: want APIError, got %v", err)
	}
	if _, err := c.GroupDelay(ctx, "♻️ 自动选择", test); err != nil && !errors.As(err, &apiErr) {
		t.Fatalf("GroupDelay: %v", err)
	}

	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	got := make(chan mihomoapi.Traffic, 1)
	go func() {
		_ = c.StreamTraffic(sctx, func(tr mihomoapi.Traffic) {
			select {
			case got <- tr:
			default:
			}
		})
	}()
	select {
	case <-got:
	case <-sctx.Done():
		t.Fatal("no traffic sample within 5s")
	}

	logs := make(chan mihomoapi.LogEntry, 16)
	go func() {
		_ = c.StreamLogs(sctx, "info", func(e mihomoapi.LogEntry) { logs <- e })
	}()
	// A request through a dead proxy logs a dial warning.
	time.Sleep(300 * time.Millisecond)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", inst.MixedPort))
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	if resp, err := hc.Get("http://example.invalid/"); err == nil {
		resp.Body.Close()
	}
	select {
	case e := <-logs:
		if e.Type == "" || e.Payload == "" {
			t.Fatalf("empty log entry %+v", e)
		}
	case <-sctx.Done():
		t.Fatal("no log entry within 5s")
	}
}
