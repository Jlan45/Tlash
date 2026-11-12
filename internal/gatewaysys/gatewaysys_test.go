package gatewaysys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	st, err := s.Apply(ctx, true, "tlash")
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
	st, err := s.Apply(context.Background(), false, "tlash")
	if err != nil || len(st.ForwardRules) != 0 || st.PrevIPv6Forward != "" {
		t.Fatalf("state %+v err %v", st, err)
	}
}
