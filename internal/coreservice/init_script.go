package coreservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// initScript covers OpenRC and OpenWrt procd, both driven by a script in /etc/init.d.
type initScript struct {
	spec Spec
	kind Kind
	run  runner
	dir  string
}

func (s *initScript) Kind() Kind      { return s.kind }
func (s *initScript) path() string    { return filepath.Join(s.dir, s.spec.Name) }
func (s *initScript) Installed() bool { return fileExists(s.path()) }

func (s *initScript) script() string {
	if s.kind == KindProcd {
		return fmt.Sprintf(`#!/bin/sh /etc/rc.common
# Managed by Tlash; changes are overwritten.
START=99
STOP=10
USE_PROCD=1

start_service() {
	%s
	procd_open_instance
	procd_set_param command %s
	procd_set_param respawn 3600 3 0
	procd_set_param limits nofile="1048576 1048576"
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
`, preStartShell(s.spec), s.spec.commandLine())
	}
	args := make([]string, 0, 4)
	for _, a := range s.spec.args() {
		args = append(args, shellQuote(a))
	}
	return fmt.Sprintf(`#!/sbin/openrc-run
# Managed by Tlash; changes are overwritten.
name="mihomo (Tlash)"
command=%s
command_args="%s"
supervisor=supervise-daemon
respawn_delay=3
output_log=%s
error_log=%s
rc_ulimit="-n 1048576"

depend() {
	need net
	after firewall
}

start_pre() {
	%s
}
`, shellQuote(s.spec.CorePath), strings.ReplaceAll(strings.Join(args, " "), `"`, `\"`), shellQuote(s.spec.LogPath), shellQuote(s.spec.LogPath), preStartShell(s.spec))
}

func (s *initScript) Install(ctx context.Context) error {
	_, err := writeIfChanged(s.path(), s.script(), 0o755)
	return err
}

func (s *initScript) Uninstall(ctx context.Context) error {
	if !s.Installed() {
		return nil
	}
	_ = s.Stop(ctx)
	_ = s.SetAutoStart(ctx, false)
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *initScript) action(ctx context.Context, verb string) error {
	if s.kind == KindOpenRC {
		_, err := s.run(ctx, "rc-service", s.spec.Name, verb)
		return err
	}
	_, err := s.run(ctx, s.path(), verb)
	return err
}

func (s *initScript) Start(ctx context.Context) error   { return s.action(ctx, "start") }
func (s *initScript) Stop(ctx context.Context) error    { return s.action(ctx, "stop") }
func (s *initScript) Restart(ctx context.Context) error { return s.action(ctx, "restart") }

func (s *initScript) Running(ctx context.Context) (bool, error) {
	if !s.Installed() {
		return false, nil
	}
	if s.kind == KindOpenRC {
		_, err := s.run(ctx, "rc-service", s.spec.Name, "status")
		return err == nil, nil
	}
	out, _ := s.run(ctx, s.path(), "status")
	return strings.TrimSpace(string(out)) == "running", nil
}

func (s *initScript) AutoStart(ctx context.Context) (bool, error) {
	if !s.Installed() {
		return false, nil
	}
	if s.kind == KindOpenRC {
		out, err := s.run(ctx, "rc-update", "show", "default")
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == s.spec.Name {
				return true, nil
			}
		}
		return false, nil
	}
	_, err := s.run(ctx, s.path(), "enabled")
	return err == nil, nil
}

func (s *initScript) SetAutoStart(ctx context.Context, on bool) error {
	if s.kind == KindOpenRC {
		verb := "del"
		if on {
			verb = "add"
		}
		_, err := s.run(ctx, "rc-update", verb, s.spec.Name, "default")
		return err
	}
	verb := "disable"
	if on {
		verb = "enable"
	}
	_, err := s.run(ctx, s.path(), verb)
	return err
}

func (s *initScript) LogHint() string {
	if s.kind == KindProcd {
		return "logread -e mihomo"
	}
	return s.spec.LogPath
}

// preStartShell is the PreStart command for init scripts, never failing the start.
func preStartShell(spec Spec) string {
	if len(spec.PreStart) == 0 {
		return "true"
	}
	return spec.preStartLine() + " || true"
}
