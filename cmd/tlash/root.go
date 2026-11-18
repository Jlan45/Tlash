package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/Jlan45/Tlash/internal/workspace"
)

// keptEnv survives the sudo re-exec. sudo resets the environment, but the gateway needs
// SSH_CONNECTION to keep the SSH client out of the TUN, and the editor and terminal
// settings make the TUI behave as the user expects.
var keptEnv = []string{
	"TLASH_DIR", "SSH_CONNECTION", "SSH_CLIENT", "EDITOR", "VISUAL",
	"TERM", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE",
}

// needsRoot reports whether cmd manages the core. Exempt: version/help, `env` (run by every
// login shell through /etc/profile.d, where a sudo prompt would be wrong) and connecting to
// an external core with --controller.
func needsRoot(cmd *cobra.Command, f *rootFlags) bool {
	switch cmd.Name() {
	case "version", "help", "env", "completion", "__complete", "__completeNoDesc":
		return false
	}
	if cmd.Parent() != nil && cmd.Parent().Name() == "completion" {
		return false
	}
	return f.controller == ""
}

// ensureRoot re-runs tlash under sudo when it isn't root. TUN, the gateway and system
// services all need root, so Tlash no longer supports a per-user install.
func ensureRoot(cmd *cobra.Command, f *rootFlags) error {
	if os.Geteuid() == 0 || !needsRoot(cmd, f) {
		return nil
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Tlash 需要 root 权限运行：请切换到 root 后再运行 tlash")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// `sudo env K=V tlash ...` passes variables on every sudo version (--preserve-env=list
	// needs 1.8.21+), and works whatever env_keep the sudoers file sets.
	argv := []string{"sudo", "--"}
	if envBin, err := exec.LookPath("env"); err == nil {
		argv = append(argv, envBin)
		for _, k := range keptEnv {
			if v, ok := os.LookupEnv(k); ok {
				argv = append(argv, k+"="+v)
			}
		}
	}
	argv = append(argv, exe)
	argv = append(argv, os.Args[1:]...)
	fmt.Fprintln(os.Stderr, "Tlash 需要 root 权限，正在通过 sudo 运行…")
	return syscall.Exec(sudo, argv, os.Environ())
}

// offerLegacyMigration asks, once, to move an old per-user install into the root data dir.
func offerLegacyMigration(ctx context.Context, dir string) {
	l, ok := workspace.FindLegacyInstall()
	if !ok || !term.IsTerminal(os.Stdin.Fd()) {
		return
	}
	fmt.Printf("发现用户 %s 以前的 Tlash 数据：%s\n", l.User.Username, l.Dir)
	fmt.Printf("Tlash 现在只以 root 运行。迁移到 %s？\n", dir)
	fmt.Print("会停止旧的内核和定时任务、删除 ~/.local/bin/tlash，复制订阅、覆写、设置和内核；旧数据保留。[Y/n] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
		if err := l.Decline(); err != nil {
			fmt.Fprintln(os.Stderr, "记录失败:", err)
		}
		fmt.Println("已跳过，之后不再询问。")
		return
	}
	if err := workspace.MigrateLegacy(ctx, l, dir); err != nil {
		fmt.Fprintln(os.Stderr, "迁移失败:", err)
		fmt.Print("按回车继续…")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	if ws, err := workspace.Open(dir); err == nil && ws.Settings().Tasks.Enabled {
		if err := ws.SetTasksEnabled(ctx, true); err != nil {
			fmt.Fprintln(os.Stderr, "重新注册定时任务失败:", err)
		}
	}
	fmt.Println("迁移完成。内核已停止，请在界面里按 s 启动；需要开机自启时在 设置 → 内核 中开启。")
	time.Sleep(1500 * time.Millisecond)
}
