package coreservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type systemd struct {
	spec     Spec
	unitPath string
	run      runner
}

func newSystemd(spec Spec, run runner) (*systemd, error) {
	return &systemd{
		spec:     spec,
		unitPath: filepath.Join("/etc/systemd/system", spec.Name+".service"),
		run:      run,
	}, nil
}

func (s *systemd) Kind() Kind { return KindSystemd }

func (s *systemd) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	return s.run(ctx, "systemctl", args...)
}

func (s *systemd) unit() string {
	pre := ""
	if len(s.spec.PreStart) > 0 {
		pre = "ExecStartPre=-" + s.spec.preStartLine() + "\n"
	}
	return fmt.Sprintf(`# Managed by Tlash; changes are overwritten.
[Unit]
Description=mihomo (managed by Tlash)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%sExecStart=%s
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, pre, s.spec.commandLine())
}

func (s *systemd) Install(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.unitPath), 0o755); err != nil {
		return err
	}
	changed, err := writeIfChanged(s.unitPath, s.unit(), 0o644)
	if err != nil {
		return err
	}
	if changed {
		_, err = s.systemctl(ctx, "daemon-reload")
	}
	return err
}

func (s *systemd) Uninstall(ctx context.Context) error {
	if !s.Installed() {
		return nil
	}
	_, _ = s.systemctl(ctx, "disable", "--now", s.spec.Name)
	if err := os.Remove(s.unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := s.systemctl(ctx, "daemon-reload")
	return err
}

func (s *systemd) Installed() bool { return fileExists(s.unitPath) }

func (s *systemd) Start(ctx context.Context) error {
	_, err := s.systemctl(ctx, "start", s.spec.Name)
	return err
}

func (s *systemd) Stop(ctx context.Context) error {
	_, err := s.systemctl(ctx, "stop", s.spec.Name)
	return err
}

func (s *systemd) Restart(ctx context.Context) error {
	_, err := s.systemctl(ctx, "restart", s.spec.Name)
	return err
}

func (s *systemd) Running(ctx context.Context) (bool, error) {
	if !s.Installed() {
		return false, nil
	}
	// is-active exits non-zero for every state but active, so read the output instead.
	out, _ := s.systemctl(ctx, "is-active", s.spec.Name)
	return strings.TrimSpace(firstLine(string(out))) == "active", nil
}

func (s *systemd) AutoStart(ctx context.Context) (bool, error) {
	if !s.Installed() {
		return false, nil
	}
	out, _ := s.systemctl(ctx, "is-enabled", s.spec.Name)
	return strings.TrimSpace(firstLine(string(out))) == "enabled", nil
}

func (s *systemd) SetAutoStart(ctx context.Context, on bool) error {
	verb := "disable"
	if on {
		verb = "enable"
	}
	_, err := s.systemctl(ctx, verb, s.spec.Name)
	return err
}

func (s *systemd) LogHint() string { return "journalctl -u " + s.spec.Name }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
