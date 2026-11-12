// Command tlash is a terminal UI for managing mihomo on servers.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/Jlan45/Tlash/internal/buildinfo"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/tui"
	"github.com/Jlan45/Tlash/internal/workspace"
)

type rootFlags struct {
	dir         string
	controller  string
	secret      string
	testURL     string
	testTimeout time.Duration
	logLevel    string
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	f := &rootFlags{}
	cmd := &cobra.Command{
		Use:          "tlash",
		Short:        "服务器上的 mihomo 终端管理器",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE:         func(cmd *cobra.Command, _ []string) error { return runTUI(cmd.Context(), f) },
	}
	fl := cmd.PersistentFlags()
	fl.StringVar(&f.dir, "dir", envOr("TLASH_DIR", workspace.DefaultDir()), "Tlash 数据目录（环境变量 TLASH_DIR）")
	fl.StringVar(&f.controller, "controller", os.Getenv("TLASH_CONTROLLER"),
		"连接一个不由 Tlash 管理的 mihomo：unix socket 路径、unix:///path、host:port 或 http://host:port（环境变量 TLASH_CONTROLLER）")
	fl.StringVar(&f.secret, "secret", os.Getenv("TLASH_SECRET"), "控制接口 secret（环境变量 TLASH_SECRET）")
	cmd.Flags().StringVar(&f.testURL, "test-url", "https://www.gstatic.com/generate_204", "测速地址（代理组自带 url 时优先使用组的）")
	cmd.Flags().DurationVar(&f.testTimeout, "test-timeout", 5*time.Second, "测速超时")
	cmd.Flags().StringVar(&f.logLevel, "log-level", "info", "日志页初始级别：debug / info / warning / error")

	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "显示版本",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "tlash %s (%s, %s) %s/%s\n",
				buildinfo.Version, buildinfo.Commit, buildinfo.Date, runtime.GOOS, runtime.GOARCH)
		},
	})
	cmd.AddCommand(newTaskCmd(f), newEnvCmd(f), newGatewayCmd(f), &cobra.Command{
		Use:    "prestart",
		Short:  "内核启动前的准备：把内核恢复到内存、恢复网关系统设置（服务启动钩子）",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return prestart(cmd.Context(), f.dir) },
	})
	return cmd
}

func newTaskCmd(f *rootFlags) *cobra.Command {
	task := &cobra.Command{Use: "task", Short: "定时任务"}
	task.AddCommand(&cobra.Command{
		Use:   "run-due",
		Short: "执行到期的任务（订阅更新、GEO 更新），由 systemd timer 或 cron 调用",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := workspace.Open(f.dir)
			if err != nil {
				return err
			}
			return ws.RunDue(cmd.Context(), time.Now())
		},
	})
	return task
}

func newGatewayCmd(f *rootFlags) *cobra.Command {
	gw := &cobra.Command{Use: "gateway", Short: "网关（透明代理）"}
	open := func() (*workspace.Workspace, error) { return workspace.Open(f.dir) }
	var token string
	guard := &cobra.Command{
		Use:    "guard",
		Short:  "开启网关后等待确认，超时自动回滚（由 tlash 自动启动）",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := open()
			if err != nil {
				return err
			}
			return ws.RunGatewayGuard(cmd.Context(), token)
		},
	}
	guard.Flags().StringVar(&token, "token", "", "")
	gw.AddCommand(guard,
		&cobra.Command{
			Use:    "prepare",
			Short:  "旧名称，等同 tlash prestart（v0.1.0-alpha.11 生成的服务文件仍在调用）",
			Hidden: true,
			Args:   cobra.NoArgs,
			RunE:   func(cmd *cobra.Command, _ []string) error { return prestart(cmd.Context(), f.dir) },
		},
		&cobra.Command{
			Use:   "check",
			Short: "检查本机能否作为网关（环境检查）",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ws, err := open()
				if err != nil {
					return err
				}
				failed := false
				for _, c := range ws.GatewayPreflight(cmd.Context()) {
					mark := map[workspace.CheckLevel]string{workspace.CheckOK: "✓", workspace.CheckWarn: "!", workspace.CheckFail: "✗"}[c.Level]
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s\n", mark, c.Name, c.Detail)
					failed = failed || c.Level == workspace.CheckFail
				}
				if failed {
					return fmt.Errorf("环境检查未通过")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "off",
			Short: "关闭网关并还原系统设置（网关导致无法使用界面时救急用）",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ws, err := open()
				if err != nil {
					return err
				}
				if _, pending := ws.PendingGateway(); pending {
					err = ws.RollbackGateway(cmd.Context())
				} else {
					err = ws.DisableGateway(cmd.Context())
				}
				if err == nil {
					fmt.Fprintln(cmd.OutOrStdout(), "网关已关闭")
				}
				return err
			},
		},
	)
	return gw
}

func prestart(ctx context.Context, dir string) error {
	ws, err := workspace.Open(dir)
	if err != nil {
		return err
	}
	if err := ws.EnsureCore(ctx); err != nil {
		return err
	}
	return ws.PrepareGateway(ctx)
}

func newEnvCmd(f *rootFlags) *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "env",
		Short: `输出代理环境变量，用法：eval "$(tlash env)"`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := workspace.Open(f.dir)
			if err != nil {
				return err
			}
			out, err := ws.ProxyEnv(unset)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "输出取消代理变量的命令")
	return cmd
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runTUI(ctx context.Context, f *rootFlags) error {
	opts := tui.Options{
		DelayTest: mihomoapi.DelayTest{URL: f.testURL, Timeout: f.testTimeout},
		LogLevel:  f.logLevel,
	}
	if f.controller != "" {
		// External core: monitoring and switching only; Tlash doesn't own its config or process.
		ep, err := mihomoapi.ParseEndpoint(f.controller)
		if err != nil {
			return err
		}
		opts.Client = mihomoapi.New(ep, f.secret)
	} else {
		ws, err := workspace.Open(f.dir)
		if err != nil {
			return fmt.Errorf("打开数据目录 %s 失败: %w", f.dir, err)
		}
		opts.Workspace = ws
		opts.Client = ws.Client()
	}
	app := tui.New(opts)
	defer app.Close()
	_, err := tea.NewProgram(app, tea.WithContext(ctx)).Run()
	return err
}
