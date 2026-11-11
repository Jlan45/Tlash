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
)

// Probed in order when --controller and TLASH_CONTROLLER are both unset.
var defaultControllers = []string{
	"/etc/tlash/runtime/mihomo.sock",
	"127.0.0.1:9090",
}

type rootFlags struct {
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
	fl.StringVar(&f.controller, "controller", os.Getenv("TLASH_CONTROLLER"),
		"mihomo 控制接口：unix socket 路径、unix:///path、host:port 或 http://host:port（环境变量 TLASH_CONTROLLER）")
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

func runTUI(ctx context.Context, f *rootFlags) error {
	ep, err := resolveController(ctx, f)
	if err != nil {
		return err
	}
	app := tui.New(tui.Options{
		Client:    mihomoapi.New(ep, f.secret),
		DelayTest: mihomoapi.DelayTest{URL: f.testURL, Timeout: f.testTimeout},
		LogLevel:  f.logLevel,
	})
	defer app.Close()
	_, err = tea.NewProgram(app, tea.WithContext(ctx)).Run()
	return err
}

// resolveController uses the explicit address, else the first default that answers.
func resolveController(ctx context.Context, f *rootFlags) (mihomoapi.Endpoint, error) {
	if f.controller != "" {
		return mihomoapi.ParseEndpoint(f.controller)
	}
	for _, addr := range defaultControllers {
		ep, err := mihomoapi.ParseEndpoint(addr)
		if err != nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err = mihomoapi.New(ep, f.secret).Version(probeCtx)
		cancel()
		// A secret rejection still proves the controller is there.
		if err == nil || mihomoapi.IsUnauthorized(err) {
			return ep, nil
		}
	}
	// Nothing answered: open the UI on the last default so it keeps retrying.
	return mihomoapi.ParseEndpoint(defaultControllers[len(defaultControllers)-1])
}
