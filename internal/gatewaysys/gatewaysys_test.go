package gatewaysys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeIptables is a FORWARD chain with a DROP policy and a rule list.
type fakeIptables struct{ rules map[string]int }

func (f *fakeIptables) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if args[0] == "-S" {
		return []byte("-P FORWARD DROP\n-A FORWARD -j DOCKER-USER\n"), nil
	}
	key := name + " " + strings.Join(args[1:], " ")
	switch args[0] {
	case "-C":
		if f.rules[key] == 0 {
			return nil, errors.New("Bad rule")
		}
	case "-I":
		f.rules[key]++
	case "-D":
		f.rules[key]--
	}
	return nil, nil
}

func testSystem(t *testing.T, ipt *fakeIptables) System {
	dir := t.TempDir()
	for _, k := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		p := filepath.Join(dir, "proc", k)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("0\n"), 0o644)
	}
	return System{
		ProcSys:   filepath.Join(dir, "proc"),
		SysctlDir: filepath.Join(dir, "sysctl.d"),
		Run:       ipt.run,
		LookPath:  func(string) (string, error) { return "/sbin/x", nil },
	}
}

func TestApplyAndRevert(t *testing.T) {
	ipt := &fakeIptables{rules: map[string]int{}}
	s := testSystem(t, ipt)
	ctx := context.Background()
	st, err := s.Apply(ctx, true, "tlash", "0.0.0.0:53")
	if err != nil {
		t.Fatal(err)
	}
	if s.read("net.ipv4.ip_forward") != "1" || s.read("net.ipv6.conf.all.forwarding") != "1" {
		t.Fatal("forwarding not enabled")
	}
	if b, _ := os.ReadFile(st.SysctlFile); !strings.Contains(string(b), "net.ipv4.ip_forward = 1") {
		t.Fatalf("sysctl file: %q", b)
	}
	if len(st.ForwardRules) != 2 || ipt.rules["iptables FORWARD -i tlash -m comment --comment tlash-gateway -j ACCEPT"] != 1 {
		t.Fatalf("rules: %+v %v", st, ipt.rules)
	}
	// Applying again (e.g. at boot) doesn't duplicate rules.
	if err := s.EnsureForward(ctx, st, "tlash"); err != nil {
		t.Fatal(err)
	}
	if ipt.rules["iptables FORWARD -o tlash -m comment --comment tlash-gateway -j ACCEPT"] != 1 {
		t.Fatalf("duplicated rule: %v", ipt.rules)
	}
	if err := s.Revert(ctx, st, "tlash"); err != nil {
		t.Fatal(err)
	}
	if s.read("net.ipv4.ip_forward") != "0" || s.read("net.ipv6.conf.all.forwarding") != "0" {
		t.Fatal("forwarding not restored")
	}
	if _, err := os.Stat(st.SysctlFile); !os.IsNotExist(err) {
		t.Fatal("sysctl file left behind")
	}
	for k, n := range ipt.rules {
		if n != 0 {
			t.Fatalf("rule %q left: %d", k, n)
		}
	}
}

func TestNoRulesWhenForwardAccepts(t *testing.T) {
	s := testSystem(t, &fakeIptables{})
	s.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("-P FORWARD ACCEPT\n"), nil }
	st, err := s.Apply(context.Background(), false, "tlash", "0.0.0.0:53")
	if err != nil || len(st.ForwardRules) != 0 || st.PrevIPv6Forward != "" {
		t.Fatalf("state %+v err %v", st, err)
	}
}

// fakeOpenWrt keeps uci options in a map and records service actions.
type fakeOpenWrt struct {
	opts     map[string][]string
	services []string
}

func (f *fakeOpenWrt) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "uci" {
		f.services = append(f.services, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if args[0] == "-q" {
		args = args[1:]
	}
	switch args[0] {
	case "get":
		v, ok := f.opts[args[1]]
		if !ok {
			return nil, errors.New("not found")
		}
		return []byte(strings.Join(v, " ")), nil
	case "set":
		k, v, _ := strings.Cut(args[1], "=")
		f.opts[k] = []string{v}
	case "add_list":
		k, v, _ := strings.Cut(args[1], "=")
		f.opts[k] = append(f.opts[k], v)
	case "delete":
		for k := range f.opts {
			if k == args[1] || strings.HasPrefix(k, args[1]+".") {
				delete(f.opts, k)
			}
		}
	}
	return nil, nil
}

func TestOpenWrtDnsmasqAndFirewall(t *testing.T) {
	s := testSystem(t, &fakeIptables{})
	ow := &fakeOpenWrt{opts: map[string][]string{"dhcp.@dnsmasq[0].server": {"/lan/", "223.5.5.5"}}}
	s.OpenWrt, s.Run = true, ow.run
	s.LookPath = func(string) (string, error) { return "", errors.New("no iptables on fw4") }
	ctx := context.Background()

	st, err := s.Apply(ctx, false, "tlash", "0.0.0.0:1053")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ow.opts["dhcp.@dnsmasq[0].server"], ","); got != "127.0.0.1#1053" {
		t.Errorf("dnsmasq server = %s", got)
	}
	if ow.opts["dhcp.@dnsmasq[0].noresolv"][0] != "1" || ow.opts["firewall.tlash.device"][0] != "tlash" || ow.opts["firewall.tlash_lan.dest"][0] != "tlash" {
		t.Errorf("uci = %v", ow.opts)
	}
	if !st.FirewallZone || !st.Dnsmasq {
		t.Errorf("state = %+v", st)
	}

	if err := s.Revert(ctx, st, "tlash"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ow.opts["dhcp.@dnsmasq[0].server"], ","); got != "/lan/,223.5.5.5" {
		t.Errorf("dnsmasq servers not restored: %s", got)
	}
	if _, ok := ow.opts["dhcp.@dnsmasq[0].noresolv"]; ok {
		t.Error("noresolv should be removed (it was unset before)")
	}
	if _, ok := ow.opts["firewall.tlash"]; ok {
		t.Error("firewall zone left behind")
	}
	if strings.Count(strings.Join(ow.services, "|"), "dnsmasq restart") != 2 {
		t.Errorf("services = %v", ow.services)
	}
}

// Runs against a real OpenWrt uci (e.g. inside an openwrt/rootfs container): Apply then Revert
// must leave the dhcp and firewall configs exactly as they were.
func TestRealUCIRoundTrip(t *testing.T) {
	if os.Getenv("TLASH_TEST_REAL_UCI") == "" {
		t.Skip("TLASH_TEST_REAL_UCI not set")
	}
	host := Host()
	if !host.OpenWrt {
		t.Skip("not OpenWrt")
	}
	dir := t.TempDir()
	host.ProcSys = filepath.Join(dir, "proc")
	host.SysctlDir = filepath.Join(dir, "sysctl.d")
	for _, k := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		p := filepath.Join(host.ProcSys, k)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("1\n"), 0o644)
	}
	realRun := host.Run
	var services []string
	host.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.HasPrefix(name, "/etc/init.d/") {
			services = append(services, name+" "+strings.Join(args, " "))
			return nil, nil // no procd in the container
		}
		return realRun(ctx, name, args...)
	}
	export := func(cfg string) string {
		out, err := realRun(context.Background(), "uci", "export", cfg)
		if err != nil {
			t.Fatalf("uci export %s: %v", cfg, err)
		}
		// Option order may change when values are re-added; compare content, not order.
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		slices.Sort(lines)
		return strings.Join(lines, "\n")
	}
	beforeDHCP, beforeFW := export("dhcp"), export("firewall")

	ctx := context.Background()
	st, err := host.Apply(ctx, false, "tlash", "127.0.0.1:1053")
	if err != nil {
		t.Fatal(err)
	}
	during := export("dhcp")
	if !strings.Contains(during, "127.0.0.1#1053") || !strings.Contains(export("firewall"), "option name 'tlash'") {
		t.Fatalf("uci not changed:\n%s", during)
	}
	if err := host.Revert(ctx, st, "tlash"); err != nil {
		t.Fatal(err)
	}
	if got := export("dhcp"); got != beforeDHCP {
		t.Errorf("dhcp not restored:\n--- before\n%s\n--- after\n%s", beforeDHCP, got)
	}
	if got := export("firewall"); got != beforeFW {
		t.Errorf("firewall not restored:\n--- before\n%s\n--- after\n%s", beforeFW, got)
	}
	t.Logf("services: %v", services)
}
