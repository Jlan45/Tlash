package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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

// Every page must render inside the frame at any size without panicking.
func TestRenderSizes(t *testing.T) {
	ep, _ := mihomoapi.ParseEndpoint("127.0.0.1:1")
	app := New(Options{Client: mihomoapi.New(ep, ""), DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	pp := app.pages[1].(*proxiesPage)
	pp.update(proxiesMsg{proxies: sampleProxies()})
	app.sess.connected = true
	for _, size := range [][2]int{{40, 10}, {80, 24}, {200, 60}, {30, 5}} {
		app.Update(teaWindowSize(size[0], size[1]))
		for i := range app.pages {
			app.active = i
			out := app.render()
			lines := strings.Split(out, "\n")
			if size[0] >= 40 && len(lines) != size[1] {
				t.Errorf("%dx%d page %d: %d lines", size[0], size[1], i, len(lines))
			}
			for _, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d page %d: line width %d > %d: %q", size[0], size[1], i, w, size[0], ansi.Strip(l))
				}
			}
		}
	}
}

// Workspace pages (profiles, core, setup checklist) also fit every size.
func TestRenderSizesWithWorkspace(t *testing.T) {
	dir, err := os.MkdirTemp("", "tlr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{Client: ws.Client(), Workspace: ws, DelayTest: mihomoapi.DelayTest{URL: "http://x", Timeout: time.Second}})
	defer app.Close()
	app.sess.core = coreState{known: true, installed: true, version: "v1.19.34", kind: "systemd"}
	pp := app.pages[2].(*profilesPage)
	pp.update(profilesMsg{profiles: []profile.Profile{{
		ID: "a", Name: "一个名字非常非常非常非常长的机场订阅", Kind: profile.KindRemote, URL: "https://sub.example.com/x?token=1",
		Usage: &profile.Usage{Upload: 1 << 30, Download: 5 << 30, Total: 100 << 30, Expire: time.Now().Add(72 * time.Hour)},
	}}})
	for _, size := range [][2]int{{40, 10}, {80, 24}, {160, 50}} {
		app.Update(teaWindowSize(size[0], size[1]))
		for i := range app.pages {
			app.active = i
			for _, l := range strings.Split(app.render(), "\n") {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d page %s: width %d: %q", size[0], size[1], app.pages[i].title(), w, ansi.Strip(l))
				}
			}
		}
		// A form open on the profiles page must fit too.
		app.active = 2
		pp.form = pp.addRemoteForm()
		for _, l := range strings.Split(app.render(), "\n") {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d form: width %d", size[0], size[1], w)
			}
		}
		pp.form = nil
	}
}
