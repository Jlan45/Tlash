package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/profile"
	"github.com/Jlan45/Tlash/internal/workspace"
)

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 150 << 20: "150 MB", 3 << 30: "3.0 GB"}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSparkline(t *testing.T) {
	if got := sparkline([]int64{0, 1, 2, 4, 8}, 5); got != " ▁▂▄█" {
		t.Errorf("sparkline = %q", got)
	}
	// Short history is right-aligned; long history keeps the newest samples.
	if got := sparkline([]int64{8}, 3); got != "  █" {
		t.Errorf("sparkline short = %q", got)
	}
	if got := sparkline([]int64{8, 8, 1, 1}, 2); got != "██" {
		t.Errorf("sparkline long = %q", got)
	}
}

func sampleProxies() map[string]mihomoapi.Proxy {
	hist := func(d int) []mihomoapi.DelayHistory { return []mihomoapi.DelayHistory{{Delay: d}} }
	return map[string]mihomoapi.Proxy{
		"GLOBAL": {Name: "GLOBAL", Type: "Selector", All: []string{"DIRECT", "B 组", "A 组", "hk", "jp", "us"}},
		"A 组":    {Name: "A 组", Type: "Selector", All: []string{"us", "hk", "jp", "sg"}, Now: "hk"},
		"B 组":    {Name: "B 组", Type: "URLTest", All: []string{"hk"}, Now: "hk"},
		"hidden": {Name: "hidden", Type: "Selector", All: []string{"hk"}, Hidden: true},
		"hk":     {Name: "hk", Type: "Shadowsocks", History: hist(120)},
		"jp":     {Name: "jp", Type: "Vmess", History: hist(0)},
		"us":     {Name: "us", Type: "Trojan", History: hist(40)},
		"sg":     {Name: "sg", Type: "Trojan"},
		"DIRECT": {Name: "DIRECT", Type: "Direct"},
	}
}

func TestOrderedGroupsFollowsGlobal(t *testing.T) {
	var names []string
	for _, g := range orderedGroups(sampleProxies(), false) {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "B 组,A 组" {
		t.Errorf("groups = %s", got)
	}
	if n := len(orderedGroups(sampleProxies(), true)); n != 3 {
		t.Errorf("with GLOBAL: %d groups", n)
	}
}

func TestSortedMembers(t *testing.T) {
	ps := sampleProxies()
	g := ps["A 组"]
	check := func(sort proxySort, filter, want string) {
		t.Helper()
		if got := strings.Join(sortedMembers(g, ps, sort, filter), ","); got != want {
			t.Errorf("sort=%v filter=%q: %s, want %s", sort, filter, got, want)
		}
	}
	check(sortConfigOrder, "", "us,hk,jp,sg")
	check(sortByName, "", "hk,jp,sg,us")
	check(sortByDelay, "", "us,hk,jp,sg") // 40, 120, timeout, untested
	check(sortConfigOrder, "S", "us,sg")
}

func TestLogBufferTrims(t *testing.T) {
	b := newLogBuffer(100)
	for i := range 250 {
		b.add(logMsg{entry: mihomoapi.LogEntry{Type: "info", Payload: fmt.Sprint(i)}})
	}
	if len(b.entries) > 100 || b.entries[len(b.entries)-1].entry.Payload != "249" {
		t.Fatalf("len=%d last=%v", len(b.entries), b.entries[len(b.entries)-1])
	}
	b.add(logMsg{entry: mihomoapi.LogEntry{Type: "warning", Payload: "Dial ERROR"}})
	if got := b.matching("dial error"); len(got) != 1 {
		t.Fatalf("matching = %d", len(got))
	}
}

func TestFitBlockIsExact(t *testing.T) {
	out := fitBlock("香港节点很长很长很长\nb", 8, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 8 {
			t.Errorf("width %d for %q", w, l)
		}
	}
}

// assertFits checks the screen is exactly height lines, none wider than width.
func assertFits(t *testing.T, app *App, label string) {
	t.Helper()
	lines := strings.Split(app.render(), "\n")
	if len(lines) != app.height {
		t.Errorf("%s: %d lines, want %d", label, len(lines), app.height)
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > app.width {
			t.Errorf("%s: line width %d > %d: %q", label, w, app.width, ansi.Strip(l))
			return
		}
	}
}

// Every panel, tab, focus and screen mode must fit the terminal, popups included.
func TestLayoutFitsEverywhere(t *testing.T) {
	dir, err := os.MkdirTemp("", "tlr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	for _, external := range []bool{false, true} {
		opts := Options{Client: ws.Client(), Workspace: ws, DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}}
		if external {
			opts = Options{Client: mihomoapi.New(ep, ""), DelayTest: opts.DelayTest}
		}
		app := New(opts)
		app.sess.connected = true
		app.sess.core = coreState{known: true, installed: true, running: true, version: "v1.19.34", kind: "systemd"}
		app.sess.conns = []mihomoapi.Connection{{ID: "c1", Metadata: mihomoapi.ConnectionMetadata{Host: "一个很长很长的域名.example.com", DestinationPort: "443"}, Chains: []string{"香港 01", "🚀 节点选择"}}}
		app.broadcast(statusMsg{conns: app.sess.conns})
		app.broadcast(proxiesMsg{proxies: sampleProxies()})
		if !external {
			app.broadcast(profilesMsg{profiles: []profile.Profile{{
				ID: "a", Name: "一个名字非常非常非常非常长的机场订阅", Kind: profile.KindRemote, URL: "https://sub.example.com/x?token=1",
				Usage: &profile.Usage{Upload: 1 << 30, Download: 5 << 30, Total: 100 << 30, Expire: time.Now().Add(72 * time.Hour)},
			}}})
		}
		for _, size := range [][2]int{{40, 12}, {79, 24}, {100, 30}, {200, 60}} {
			app.Update(teaWindowSize(size[0], size[1]))
			for mode := range 3 {
				app.mode = screenMode(mode)
				for i, p := range app.panels {
					app.focus = i
					for tab := range p.tabs() {
						app.tabs[i] = tab
						for _, inMain := range []bool{false, true} {
							app.inMain = inMain
							assertFits(t, app, fmt.Sprintf("ext=%v %dx%d mode=%d %s/%s main=%v", external, size[0], size[1], mode, p.title(), p.tabs()[tab], inMain))
						}
					}
				}
			}
			app.mode, app.focus, app.inMain = modeNormal, 0, false
			app.help = true
			assertFits(t, app, fmt.Sprintf("help %dx%d", size[0], size[1]))
			app.help = false
			app.menu = &actionMenu{title: "菜单", items: app.contextActions()}
			assertFits(t, app, fmt.Sprintf("menu %dx%d", size[0], size[1]))
			app.menu = nil
			app.form = newInputForm("添加订阅", func([]string) tea.Cmd { return nil }).addText("订阅链接", "", "https://...", "需要 Clash 格式")
			assertFits(t, app, fmt.Sprintf("form %dx%d", size[0], size[1]))
			app.form = nil
		}
		app.Close()
	}
}

// Unfocused panels shrink so the focused one gets the room.
func TestSideHeightsFavorFocus(t *testing.T) {
	app := &App{panels: make([]panel, 7), focus: 2}
	hs := app.sideHeights(23, []int{2, 5, 20, 3, 40, 100, 8}, false)
	total := 0
	for _, h := range hs {
		total += h
	}
	if total != 23 || hs[2] < 5 {
		t.Errorf("heights = %v (total %d)", hs, total)
	}
	if hs := app.sideHeights(60, []int{2, 5, 20, 3, 40, 100, 8}, false); hs[0] != 4 || hs[1] != 5 {
		t.Errorf("roomy heights = %v", hs)
	}
}
