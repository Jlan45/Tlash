package workspace

import (
	"context"

	"github.com/Jlan45/Tlash/internal/gatewaysys"
)

// FakeGatewayHost makes gateway operations act on a scratch /proc/sys under procSys, pretend
// to be root and skip the detached guard. For tests in other packages; returns a restore func.
func FakeGatewayHost(procSys, sysctlDir string) (restore func()) {
	return FakeGatewayHostWith(procSys, sysctlDir, false, nil)
}

// FakeGatewayHostWith also chooses OpenWrt mode and the command runner (nil: no-op success).
func FakeGatewayHostWith(procSys, sysctlDir string, openwrt bool, run func(context.Context, string, ...string) ([]byte, error)) (restore func()) {
	if run == nil {
		run = func(context.Context, string, ...string) ([]byte, error) { return []byte("-P FORWARD ACCEPT"), nil }
	}
	oldSys, oldRoot, oldGuard := hostSystem, isRoot, startGuard
	hostSystem = func() gatewaysys.System {
		return gatewaysys.System{
			ProcSys:   procSys,
			SysctlDir: sysctlDir,
			OpenWrt:   openwrt,
			Run:       run,
			LookPath:  func(string) (string, error) { return "/sbin/iptables", nil },
		}
	}
	isRoot = func() bool { return true }
	startGuard = func(*Workspace, string) error { return nil }
	return func() { hostSystem, isRoot, startGuard = oldSys, oldRoot, oldGuard }
}
