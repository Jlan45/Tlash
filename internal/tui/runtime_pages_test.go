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

func TestRuntimePagesAgainstRealMihomo(t *testing.T) {
	inst := mihomotest.Start(t)
	ctx := context.Background()
	// Route everything direct so the tunnel succeeds.
	if err := inst.Client.SelectProxy(ctx, "🐟 漏网之鱼", "DIRECT"); err != nil {
		t.Fatal(err)
	}
	target, closeConn := holdConnection(t, inst.MixedPort)
	defer closeConn()

	app := New(Options{Client: inst.Client, DelayTest: mihomoapi.DelayTest{URL: "http://127.0.0.1:1/", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(140, 30))
	run(app, fetchStatus(inst.Client))

	// Connections: the tunnel is listed and can be closed from the page.
	press(app, pageIndex(t, app, "连接"))
	out := screen(app)
	if !strings.Contains(out, target) || !strings.Contains(out, "DIRECT") {
		t.Fatalf("connection not listed:\n%s", out)
	}
	t.Logf("connections page:\n%s", out)
	press(app, "enter")
	if out = screen(app); !strings.Contains(out, "连接详情") || !strings.Contains(out, "链路") {
		t.Fatalf("detail view:\n%s", out)
	}
	press(app, "esc", "x")
	snap, _ := inst.Client.Connections(ctx)
	if len(snap.Connections) != 0 {
		t.Fatalf("connection not closed: %+v", snap.Connections)
	}

	// Rules: listed with their target; space disables temporarily.
	press(app, pageIndex(t, app, "规则"))
	if out = screen(app); !strings.Contains(out, "Match") || !strings.Contains(out, "🐟 漏网之鱼") {
		t.Fatalf("rules page:\n%s", out)
	}
	press(app, "space")
	rules, _ := inst.Client.Rules(ctx)
	if rules[0].Extra == nil || !rules[0].Extra.Disabled {
		t.Fatalf("rule not disabled: %+v", rules[0])
	}
	if out = screen(app); !strings.Contains(out, "已停用") {
		t.Fatalf("disabled rule not shown:\n%s", out)
	}

	// Network tools work in external mode: a DNS query renders a result or an error.
	press(app, pageIndex(t, app, "网络"), "d")
	typeText(app, "localhost")
	press(app, "enter", "enter")
	if out = screen(app); !strings.Contains(out, "localhost A") {
		t.Fatalf("dns query result missing:\n%s", out)
	}
}
