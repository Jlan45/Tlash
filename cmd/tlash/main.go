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
