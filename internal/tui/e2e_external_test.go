package tui

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/mihomotest"
)

// holdConnection opens a long-lived CONNECT tunnel through the core to a local listener.
func holdConnection(t *testing.T, mixedPort int) (target string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = bufio.NewReader(c).ReadString(0) }()
		}
	}()
	target = ln.Addr().String()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", mixedPort))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT failed: %q %v", status, err)
	}
	return target, func() { conn.Close(); ln.Close() }
}

// External mode against a real core, driven only by key presses.
func TestExternalCoreKeyboardFlow(t *testing.T) {
	inst := mihomotest.Start(t)
	ctx := context.Background()
	app := New(Options{Client: inst.Client, DelayTest: mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: 2 * time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 32))
	run(app, fetchStatus(inst.Client))
	run(app, app.Init())

	out := screen(app)
	if !strings.Contains(out, "[1]-状态") || !strings.Contains(out, "● 运行中") || !strings.Contains(out, "[2]-代理组") {
		t.Fatalf("initial screen:\n%s", out)
	}

	// Status panel: m cycles the outbound mode.
	press(app, "m")
	if app.sess.config.Mode != "global" {
		t.Fatalf("mode = %q after m", app.sess.config.Mode)
	}
	press(app, "m", "m")
	if app.sess.config.Mode != "rule" {
		t.Fatalf("mode = %q after three m", app.sess.config.Mode)
	}

	// Groups: the main panel lists the selected group's nodes; enter, move, enter selects.
	press(app, panelKey(t, app, "代理组"))
	out = screen(app)
	t.Logf("groups panel focused:\n%s", out)
	for _, want := range []string{"🚀 节点选择", "自动选择", "香港 01", "Selector"} {
		if !strings.Contains(out, want) {
			t.Fatalf("groups screen missing %q:\n%s", want, out)
		}
	}
	press(app, "enter", "down", "enter")
	proxies, _ := inst.Client.Proxies(ctx)
	if now := proxies["🚀 节点选择"].Now; now != "香港 01" {
		t.Fatalf("selected %q, want 香港 01\n%s", now, screen(app))
	}
	press(app, "t")
	if out = screen(app); !strings.Contains(out, "超时") {
		t.Fatalf("no timeout after node test:\n%s", out)
	}

	// Filter from the side list narrows groups and nodes.
	press(app, "esc", "/", "日", "enter")
	out = screen(app)
	if strings.Contains(out, "美国 01") || !strings.Contains(out, "日本 01") || len(app.cur().rows(40)) != 2 {
		t.Fatalf("filter not applied:\n%s", out)
	}
	press(app, "esc")

	// x lists every action of the focused panel.
	press(app, "x")
	if out = screen(app); !strings.Contains(out, "代理组 · 可用操作") || !strings.Contains(out, "整组测速") {
		t.Fatalf("menu:\n%s", out)
	}
	press(app, "esc")

	// Connections: a live tunnel is listed and closed with d.
	if err := inst.Client.SelectProxy(ctx, "🐟 漏网之鱼", "DIRECT"); err != nil {
		t.Fatal(err)
	}
	target, closeConn := holdConnection(t, inst.MixedPort)
	defer closeConn()
	run(app, fetchStatus(inst.Client))
	press(app, panelKey(t, app, "连接"))
	if out = screen(app); !strings.Contains(out, target) || !strings.Contains(out, "链路") {
		t.Fatalf("connection not shown:\n%s", out)
	}
	t.Logf("connections panel:\n%s", out)
	press(app, "d")
	if snap, _ := inst.Client.Connections(ctx); len(snap.Connections) != 0 {
		t.Fatalf("connection not closed: %+v", snap.Connections)
	}

	// Rules: space disables temporarily.
	press(app, panelKey(t, app, "规则"))
	if out = screen(app); !strings.Contains(out, "Match") {
		t.Fatalf("rules:\n%s", out)
	}
	press(app, "space")
	rules, _ := inst.Client.Rules(ctx)
	if rules[0].Extra == nil || !rules[0].Extra.Disabled {
		t.Fatalf("rule not disabled: %+v", rules[0])
	}
	if out = screen(app); !strings.Contains(out, "已临时停用") {
		t.Fatalf("disabled state not shown:\n%s", out)
	}

	// Settings → tools: DNS query via a popup form.
	press(app, panelKey(t, app, "设置"), "enter", "enter")
	typeText(app, "localhost")
	press(app, "enter", "enter")
	if out = screen(app); !strings.Contains(out, "localhost A") {
		t.Fatalf("dns result missing:\n%s", out)
	}

	// Logs tab with a filter that must not quit on q.
	press(app, "1", "]", "enter")
	app.Update(logMsg{at: time.Now(), entry: mihomoapi.LogEntry{Type: "warning", Payload: "[TCP] dial quick failed"}})
	press(app, "/", "q", "u", "i", "c", "k", "enter")
	if out = screen(app); !strings.Contains(out, "WARN") || !strings.Contains(out, "dial quick failed") || !strings.Contains(out, "过滤: quick") {
		t.Fatalf("logs tab:\n%s", out)
	}
	t.Logf("logs tab:\n%s", out)
}
