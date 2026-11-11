package coreservice

import (
	"context"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/etc/tlash": "/etc/tlash", "/a b": "'/a b'", "it's": `'it'\''s'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSystemdUnitAndCommands(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) > 1 && args[1] == "is-active" {
			return []byte("active\n"), nil
		}
		return nil, nil
	}
	spec := Spec{Name: "tlash-mihomo", CorePath: "/opt/my dir/mihomo", HomeDir: "/etc/tlash/runtime", ConfigPath: "/etc/tlash/runtime/config.yaml"}
	s, err := newSystemd(spec, true, run)
	if err != nil {
		t.Fatal(err)
	}
	unit := s.unit()
	if !strings.Contains(unit, "ExecStart='/opt/my dir/mihomo' -d /etc/tlash/runtime -f /etc/tlash/runtime/config.yaml") ||
		!strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("unit:\n%s", unit)
	}
	s.unitPath = t.TempDir() + "/u.service"
	ctx := context.Background()
	if err := s.Install(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Install(ctx) // unchanged: no second daemon-reload
	if ok, _ := s.Running(ctx); !ok {
		t.Error("Running should parse is-active output")
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user is-active tlash-mihomo"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q", calls)
	}
}

func TestOpenRCScript(t *testing.T) {
	s := &initScript{kind: KindOpenRC, spec: Spec{Name: "tlash-mihomo", CorePath: "/etc/tlash/core/mihomo", HomeDir: "/etc/tlash/runtime", ConfigPath: "/etc/tlash/runtime/config.yaml", LogPath: "/etc/tlash/runtime/mihomo.log"}}
	script := s.script()
	for _, want := range []string{"#!/sbin/openrc-run", `command=/etc/tlash/core/mihomo`, `command_args="-d /etc/tlash/runtime -f /etc/tlash/runtime/config.yaml"`, "supervisor=supervise-daemon"} {
		if !strings.Contains(script, want) {
			t.Errorf("openrc script missing %q:\n%s", want, script)
		}
	}
	s.kind = KindProcd
	if !strings.Contains(s.script(), "procd_set_param command /etc/tlash/core/mihomo -d") {
		t.Errorf("procd script:\n%s", s.script())
	}
}
