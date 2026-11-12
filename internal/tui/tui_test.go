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
	"github.com/Jlan45/Tlash/internal/override"
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

// Terminals deliver pastes as one PasteMsg (bracketed paste), not as key presses.
func TestPasteReachesTextFields(t *testing.T) {
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 30))

	var got []string
	app.form = newInputForm("添加订阅", func(v []string) tea.Cmd { got = v; return nil }).
		addText("订阅链接", "", "", "").addText("名称", "", "", "")
	app.Update(tea.PasteMsg{Content: "https://sub.example.com/api?token=abc"})
	press(app, "tab")
	app.Update(tea.PasteMsg{Content: "机场A"})
	press(app, "enter")
	if len(got) != 2 || got[0] != "https://sub.example.com/api?token=abc" || got[1] != "机场A" {
		t.Fatalf("form values = %q", got)
	}

	// The side-list filter and the logs filter take pastes too.
	app.broadcast(proxiesMsg{proxies: sampleProxies()})
	press(app, "2", "/")
	app.Update(tea.PasteMsg{Content: "hk"})
	if v := app.cur().list().filter.value(); v != "hk" {
		t.Fatalf("list filter = %q", v)
	}
	press(app, "enter", "1", "]", "enter", "/")
	app.Update(tea.PasteMsg{Content: "dial"})
	if v := app.status.logs.filter.value(); v != "dial" {
		t.Fatalf("logs filter = %q", v)
	}
}

// Arrowing past a panel's ends moves into the neighbouring panel.
func TestSideCursorCrossesPanels(t *testing.T) {
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 30))
	app.broadcast(proxiesMsg{proxies: sampleProxies()}) // 2 visible groups
	title := func() string { return app.cur().title() }

	press(app, "down") // status is one item: straight into 代理组
	if title() != "代理组" || app.cur().list().cursor != 0 {
		t.Fatalf("after down from status: %s cursor %d", title(), app.cur().list().cursor)
	}
	press(app, "down")
	if title() != "代理组" || app.cur().list().cursor != 1 {
		t.Fatalf("second down should stay in groups: %s cursor %d", title(), app.cur().list().cursor)
	}
	press(app, "down") // past the last group
	if title() != "连接" {
		t.Fatalf("down past last group: %s", title())
	}
	press(app, "up") // empty list: back up to the groups' last row
	if title() != "代理组" || app.cur().list().cursor != 1 {
		t.Fatalf("up into groups: %s cursor %d", title(), app.cur().list().cursor)
	}
	press(app, "k", "k")
	if title() != "状态" {
		t.Fatalf("up past first group: %s", title())
	}
	press(app, "up") // top panel: stays put
	if title() != "状态" {
		t.Fatalf("up at top moved to %s", title())
	}
	press(app, "5", "j") // bottom panel (设置, external mode has one category): stays put
	if title() != "设置" {
		t.Fatalf("down at bottom moved to %s", title())
	}
}

// The rate box stays at the bottom-left whatever is focused.
func TestRateBoxPinnedBottomLeft(t *testing.T) {
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.sess.connected = true
	for _, v := range []int64{0, 1 << 20, 3 << 20, 8 << 20} {
		app.sess.recordTraffic(mihomoapi.Traffic{Down: v, Up: v / 8})
	}
	for _, h := range []int{30, 16, 12} {
		app.Update(teaWindowSize(120, h))
		for _, focus := range []string{"1", "2", "5"} {
			press(app, focus)
			lines := strings.Split(screen(app), "\n")
			for i := range lines {
				lines[i] = ansi.Truncate(lines[i], app.sideW, "") // left column only
			}
			// Bottom border of the rate box sits right above the status bar.
			bottom := lines[len(lines)-2]
			box := strings.Join(lines[max(len(lines)-6, 0):len(lines)-1], "\n")
			if h >= 14 && (!strings.HasPrefix(bottom, "╰") || !strings.Contains(box, "速率") || !strings.Contains(box, "8.0 MB/s")) {
				t.Fatalf("h=%d focus=%s: rate box missing:\n%s", h, focus, strings.Join(lines, "\n"))
			}
			if h < 14 && strings.Contains(box, "速率") {
				t.Fatalf("h=%d: rate box should be hidden on very short terminals", h)
			}
		}
	}
	// Main-panel fullscreen hides it.
	app.Update(teaWindowSize(120, 30))
	app.mode, app.inMain = modeFull, true
	if strings.Contains(screen(app), "速率") {
		t.Fatal("rate box shown in main fullscreen")
	}
}

// Inside the main panel ←/→ switch tabs; esc returns to the side list.
func TestMainArrowsSwitchTabs(t *testing.T) {
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 30))
	press(app, "enter") // status panel: 概览 / 日志
	tab := func() int { return app.tabs[app.focus] }
	press(app, "right")
	if !app.inMain || tab() != 1 {
		t.Fatalf("right: inMain=%v tab=%d", app.inMain, tab())
	}
	press(app, "l") // wraps around
	if tab() != 0 {
		t.Fatalf("l should wrap to 0, got %d", tab())
	}
	press(app, "left")
	if tab() != 1 || !app.inMain {
		t.Fatalf("left: inMain=%v tab=%d", app.inMain, tab())
	}
	press(app, "esc")
	if app.inMain {
		t.Fatal("esc should return to the side list")
	}
	press(app, "left") // on the side, ← does nothing
	if app.inMain || app.cur().title() != "状态" {
		t.Fatalf("side left: inMain=%v panel=%s", app.inMain, app.cur().title())
	}
	press(app, "right") // on the side, → enters the main panel instead of switching panels
	if !app.inMain || app.cur().title() != "状态" {
		t.Fatalf("side right: inMain=%v panel=%s", app.inMain, app.cur().title())
	}
	press(app, "esc", "tab")
	if app.cur().title() != "代理组" {
		t.Fatalf("tab went to %s", app.cur().title())
	}
}

func TestExitProxyFallsBack(t *testing.T) {
	for _, c := range []struct {
		cfg  mihomoapi.Config
		want string
	}{
		{mihomoapi.Config{MixedPort: 7890, Port: 8080}, "http://127.0.0.1:7890"},
		{mihomoapi.Config{Port: 8080, SocksPort: 1080}, "http://127.0.0.1:8080"},
		{mihomoapi.Config{SocksPort: 1080}, "socks5://127.0.0.1:1080"},
	} {
		u, err := exitProxy(c.cfg)
		if err != nil || u.String() != c.want {
			t.Errorf("exitProxy(%+v) = %v, %v; want %s", c.cfg, u, err, c.want)
		}
	}
	if _, err := exitProxy(mihomoapi.Config{}); err == nil {
		t.Error("want error when no inbound port is open")
	}
}

func TestFlagsRenderAsLetters(t *testing.T) {
	if got := flagsToText("赔钱机场-🇸🇬新加坡02 🇭🇰"); got != "赔钱机场-SG新加坡02 HK" {
		t.Errorf("flagsToText = %q", got)
	}
	// Same cell width, so layouts computed on the original names still line up.
	if a, b := ansi.StringWidth("🇸🇬新加坡"), ansi.StringWidth("SG新加坡"); a != b {
		t.Errorf("width changed: %d vs %d", a, b)
	}

	// Node names reach mihomo untouched; only the screen is rewritten.
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.Update(teaWindowSize(120, 30))
	app.sess.connected = true
	app.broadcast(proxiesMsg{proxies: map[string]mihomoapi.Proxy{
		"GLOBAL":  {Name: "GLOBAL", Type: "Selector", All: []string{"选择"}},
		"选择":      {Name: "选择", Type: "Selector", All: []string{"🇸🇬新加坡02"}, Now: "🇸🇬新加坡02"},
		"🇸🇬新加坡02": {Name: "🇸🇬新加坡02", Type: "Vless"},
	}})
	press(app, "2")
	out := screen(app)
	if !strings.Contains(out, "SG新加坡02") || strings.ContainsRune(out, 0x1F1F8) {
		t.Fatalf("flag not converted:\n%s", out)
	}
	if _, node, _ := app.panels[1].(*groupsPanel).currentNode(); node != "🇸🇬新加坡02" {
		t.Fatalf("underlying node name changed: %q", node)
	}
}

// Panel actions must not shadow the shell's navigation keys, or they silently never run
// (or navigation breaks), e.g. "g" is "jump to first row" on the side list.
func TestActionKeysDontClashWithNavigation(t *testing.T) {
	sideReserved := map[string]bool{}
	for _, k := range []string{"up", "down", "j", "k", "g", "G", "home", "end", "pgup", "pgdown", "ctrl+u", "ctrl+d",
		"enter", "esc", "/", "tab", "shift+tab", "left", "right", "h", "l", "q", "?", "x", "+", "_", "[", "]",
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		sideReserved[k] = true
	}
	mainReserved := map[string]bool{"esc": true, "tab": true, "left": true, "right": true, "h": true, "l": true,
		"q": true, "?": true, "x": true, "+": true, "_": true, "[": true, "]": true}

	dir, _ := os.MkdirTemp("", "tlk")
	defer os.RemoveAll(dir)
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{Client: ws.Client(), Workspace: ws, DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.sess.connected = true
	app.broadcast(proxiesMsg{proxies: sampleProxies()})
	app.broadcast(profilesMsg{profiles: []profile.Profile{{ID: "a", Name: "a", Kind: profile.KindRemote}}})
	app.broadcast(overridesMsg{items: []override.Override{{ID: "o", Name: "o"}}})
	app.broadcast(statusMsg{conns: []mihomoapi.Connection{{ID: "c"}}})
	app.broadcast(rulesMsg{rules: []mihomoapi.Rule{{Type: "Match"}}})

	check := func(where string, acts []action, reserved map[string]bool) {
		seen := map[string]bool{}
		for _, a := range acts {
			if reserved[a.key] {
				t.Errorf("%s: action %q (%s) uses a navigation key", where, a.key, a.desc)
			}
			if seen[a.key] {
				t.Errorf("%s: key %q bound twice", where, a.key)
			}
			seen[a.key] = true
		}
	}
	for _, p := range app.panels {
		if sp, ok := p.(*settingsPanel); ok {
			for i, c := range sp.categories {
				sp.ls.cursor = i
				check("设置/"+c.name, sp.actions(), sideReserved)
				check("设置/"+c.name+"（主面板）", sp.mainActions(0), mainReserved)
			}
			continue
		}
		check(p.title(), p.actions(), sideReserved)
		for tab, name := range p.tabs() {
			// Main-panel actions may deliberately override a side action (t tests the node, not the group).
			check(p.title()+"/"+name+"（主面板）", p.mainActions(tab), mainReserved)
		}
	}
}
