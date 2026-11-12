package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/buildinfo"
	"github.com/Jlan45/Tlash/internal/mihomoapi"
	"github.com/Jlan45/Tlash/internal/profile"
	"github.com/Jlan45/Tlash/internal/selfupdate"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// openManaged opens the workspace for commands that manage the core; they don't apply to an
// external core attached with --controller.
func openManaged(f *rootFlags) (*workspace.Workspace, error) {
	if f.controller != "" {
		return nil, errors.New("连接外部内核（--controller）时不能使用这个命令")
	}
	return workspace.Open(f.dir)
}

// apiClient is the controller to talk to: the external one, or the workspace's socket.
func apiClient(f *rootFlags) (*mihomoapi.Client, *workspace.Workspace, error) {
	if f.controller != "" {
		ep, err := mihomoapi.ParseEndpoint(f.controller)
		if err != nil {
			return nil, nil, err
		}
		return mihomoapi.New(ep, f.secret), nil, nil
	}
	ws, err := workspace.Open(f.dir)
	if err != nil {
		return nil, nil, err
	}
	return ws.Client(), ws, nil
}

func pad(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func opsCommands(f *rootFlags) []*cobra.Command {
	daily := func(c *cobra.Command) *cobra.Command { c.GroupID = groupDaily; return c }
	maint := func(c *cobra.Command) *cobra.Command { c.GroupID = groupMaint; return c }
	return []*cobra.Command{
		daily(statusCmd(f)),
		daily(startCmd(f)),
		daily(&cobra.Command{
			Use: "stop", Short: "停止内核", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ws, err := openManaged(f)
				if err != nil {
					return err
				}
				if err := ws.Stop(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "内核已停止")
				return nil
			},
		}),
		daily(&cobra.Command{
			Use: "restart", Short: "重启内核（会重新生成配置）", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ws, err := openManaged(f)
				if err != nil {
					return err
				}
				if err := ws.Restart(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "内核已重启")
				return nil
			},
		}),
		daily(&cobra.Command{
			Use: "mode rule|global|direct", Short: "切换出站模式", Args: cobra.ExactArgs(1),
			ValidArgs: []string{"rule", "global", "direct"},
			RunE: func(cmd *cobra.Command, args []string) error {
				mode := args[0]
				if mode != "rule" && mode != "global" && mode != "direct" {
					return fmt.Errorf("模式只能是 rule / global / direct")
				}
				c, _, err := apiClient(f)
				if err != nil {
					return err
				}
				if err := c.SetMode(cmd.Context(), mode); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "出站模式："+mode)
				return nil
			},
		}),
		daily(subCmd(f)),
		maint(coreCmd(f)),
		maint(updateCmd(f)),
		maint(uninstallCmd(f)),
	}
}

func statusCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "查看内核、订阅、模式、网关和速率", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ws, err := apiClient(f)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			out := cmd.OutOrStdout()
			row := func(k, v string) { fmt.Fprintf(out, "%s%s\n", pad(k, 8), v) }

			ver, verErr := c.Version(ctx)
			state := "● 运行中  " + ver.Version
			if verErr != nil {
				state = "○ 未运行"
				if ws == nil {
					state = "○ 无法连接 " + c.Endpoint().String() + "：" + verErr.Error()
				}
			}
			if ws != nil {
				svc := ws.Service()
				auto, _ := svc.AutoStart(ctx)
				state += fmt.Sprintf("（%s，开机自启 %s）", svc.Kind().Label(), onOffText(auto))
				if verErr != nil && !ws.CoreInstalled() {
					state = "○ 未安装内核"
				}
			}
			row("内核", state)
			if ws != nil {
				row("订阅", profileSummary(ws))
			}
			if verErr != nil {
				return nil
			}
			if cfg, err := c.Config(ctx); err == nil {
				ports := []string{}
				for _, p := range []struct {
					name string
					port int
				}{{"mixed", cfg.MixedPort}, {"http", cfg.Port}, {"socks", cfg.SocksPort}} {
					if p.port > 0 {
						ports = append(ports, fmt.Sprintf("%s %d", p.name, p.port))
					}
				}
				row("模式", fmt.Sprintf("%s    端口 %s", cfg.Mode, strings.Join(ports, " · ")))
			}
			if ws != nil {
				g := ws.Settings().Gateway
				gw := "未开启"
				if g.Enabled {
					gw = "已开启"
					if _, pending := ws.PendingGateway(); pending {
						gw += "（等待确认）"
					}
				}
				row("网关", gw)
			}
			// One traffic sample (mihomo pushes one per second).
			sample := make(chan mihomoapi.Traffic, 1)
			sctx, scancel := context.WithTimeout(ctx, 2*time.Second)
			defer scancel()
			go func() {
				_ = c.StreamTraffic(sctx, func(t mihomoapi.Traffic) {
					select {
					case sample <- t:
					default:
					}
					scancel()
				})
			}()
			select {
			case t := <-sample:
				row("速率", fmt.Sprintf("↓ %s/s  ↑ %s/s    累计 ↓ %s  ↑ %s", bytesText(t.Down), bytesText(t.Up), bytesText(t.DownTotal), bytesText(t.UpTotal)))
			case <-sctx.Done():
			}
			if conns, err := c.Connections(ctx); err == nil {
				row("连接", fmt.Sprint(len(conns.Connections)))
			}
			return nil
		},
	}
}

func startCmd(f *rootFlags) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use: "start", Short: "启动内核", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openManaged(f)
			if err != nil {
				return err
			}
			err = ws.Start(cmd.Context())
			var stale *workspace.StaleCoreError
			if errors.As(err, &stale) {
				if !force {
					return fmt.Errorf("%w\n加 --force 结束这个旧内核（pid %d）后启动", err, stale.PID)
				}
				if err := ws.StopStaleCore(stale.PID); err != nil {
					return err
				}
				err = ws.Start(cmd.Context())
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "内核已启动")
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "端口被本数据目录残留的旧内核占用时，结束它再启动")
	return c
}

func profileSummary(ws *workspace.Workspace) string {
	id := ws.Settings().ActiveProfile
	if id == "" {
		return "未选择（直连模式运行）"
	}
	p, err := ws.Profiles.Get(id)
	if err != nil {
		return "未选择"
	}
	return p.Name + usageText(p)
}

func usageText(p profile.Profile) string {
	u := p.Usage
	if u == nil {
		return ""
	}
	s := "  已用 " + bytesText(u.Used())
	if u.Total > 0 {
		s += fmt.Sprintf(" / %s（%.0f%%）", bytesText(u.Total), float64(u.Used())/float64(u.Total)*100)
	}
	if !u.Expire.IsZero() {
		s += fmt.Sprintf(" · %s 到期", u.Expire.Format("2006-01-02"))
	}
	return s
}

func subCmd(f *rootFlags) *cobra.Command {
	sub := &cobra.Command{Use: "sub", Short: "订阅：list / update / use / add"}
	find := func(ws *workspace.Workspace, key string) (profile.Profile, error) {
		ps, err := ws.Profiles.List()
		if err != nil {
			return profile.Profile{}, err
		}
		for _, p := range ps {
			if p.Name == key || p.ID == key {
				return p, nil
			}
		}
		return profile.Profile{}, fmt.Errorf("没有名为 %q 的订阅（tlash sub list 查看）", key)
	}
	var addName, addUA string
	var addProxy bool
	add := &cobra.Command{
		Use: "add 链接", Short: "添加订阅链接（第一个订阅会自动启用）", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openManaged(f)
			if err != nil {
				return err
			}
			p, err := ws.AddRemote(cmd.Context(), workspace.NewRemote{URL: args[0], Name: addName, UserAgent: addUA, UseProxy: addProxy})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已添加 %s%s\n", p.Name, usageText(p))
			return nil
		},
	}
	add.Flags().StringVar(&addName, "name", "", "名称（默认用订阅提供的名称）")
	add.Flags().StringVar(&addUA, "ua", "", "User-Agent（默认 "+profile.DefaultUserAgent+"）")
	add.Flags().BoolVar(&addProxy, "proxy", false, "通过运行中的内核下载")
	sub.AddCommand(
		&cobra.Command{
			Use: "list", Short: "列出订阅", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ws, err := openManaged(f)
				if err != nil {
					return err
				}
				ps, err := ws.Profiles.List()
				if err != nil {
					return err
				}
				if len(ps) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "还没有订阅：tlash sub add <链接>")
					return nil
				}
				active := ws.Settings().ActiveProfile
				for _, p := range ps {
					mark := "  "
					if p.ID == active {
						mark = "● "
					}
					kind := "订阅"
					if p.Kind == profile.KindLocal {
						kind = "本地"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s%s %s  更新于 %s%s\n", mark, pad(p.Name, 20), kind, p.UpdatedAt.Local().Format("01-02 15:04"), usageText(p))
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "update [名称...]", Short: "更新订阅；不带名称时更新全部",
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := openManaged(f)
				if err != nil {
					return err
				}
				var targets []profile.Profile
				if len(args) == 0 {
					ps, err := ws.Profiles.List()
					if err != nil {
						return err
					}
					for _, p := range ps {
						if p.Kind == profile.KindRemote {
							targets = append(targets, p)
						}
					}
				}
				for _, a := range args {
					p, err := find(ws, a)
					if err != nil {
						return err
					}
					targets = append(targets, p)
				}
				if len(targets) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "没有可在线更新的订阅（本地配置不能在线更新）")
					return nil
				}
				failed := 0
				for _, p := range targets {
					np, err := ws.UpdateProfile(cmd.Context(), p.ID)
					if err != nil {
						failed++
						fmt.Fprintf(cmd.OutOrStdout(), "✗ %s：%v\n", p.Name, err)
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "✓ %s%s\n", np.Name, usageText(np))
				}
				if failed > 0 {
					return fmt.Errorf("%d 个订阅更新失败", failed)
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "use 名称", Short: "启用订阅（运行中会热重载）", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := openManaged(f)
				if err != nil {
					return err
				}
				p, err := find(ws, args[0])
				if err != nil {
					return err
				}
				if err := ws.Activate(cmd.Context(), p.ID); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "已启用", p.Name)
				return nil
			},
		},
		add,
	)
	return sub
}

func coreCmd(f *rootFlags) *cobra.Command {
	core := &cobra.Command{Use: "core", Short: "内核：upgrade"}
	var channel string
	var force bool
	upgrade := &cobra.Command{
		Use: "upgrade", Short: "下载最新内核（运行中会自动重启）", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openManaged(f)
			if err != nil {
				return err
			}
			if channel != "" {
				ch := appsettings.CoreChannel(channel)
				if ch != appsettings.ChannelStable && ch != appsettings.ChannelAlpha {
					return fmt.Errorf("--channel 只能是 stable / alpha")
				}
				if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Core.Channel = ch }); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			r, err := ws.LatestCore(ctx)
			if err != nil {
				return err
			}
			cur, _ := ws.CoreVersion(ctx)
			out := cmd.OutOrStdout()
			if cur == r.Version && !force {
				fmt.Fprintf(out, "已是最新：%s（--force 重新下载）\n", cur)
				return nil
			}
			fmt.Fprintf(out, "下载 %s …\n", r.Asset)
			var progress atomic.Int64
			res, err := ws.InstallCore(ctx, r, &progress)
			if err != nil {
				return err
			}
			verified := "sha256 校验通过"
			if !res.Verified {
				verified = "无法获取校验和，未校验"
			}
			fmt.Fprintf(out, "已安装 %s（%s，%s）\n", res.Version, bytesText(progress.Load()), verified)
			return nil
		},
	}
	upgrade.Flags().StringVar(&channel, "channel", "", "更新通道 stable / alpha（会保存为默认）")
	upgrade.Flags().BoolVar(&force, "force", false, "已是最新也重新下载")
	core.AddCommand(upgrade)
	return core
}

func updateCmd(f *rootFlags) *cobra.Command {
	var version string
	var force bool
	c := &cobra.Command{
		Use: "update", Short: "升级 Tlash 自身（保持标准版 / 精简版）", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mirror := ""
			if ws, err := workspace.Open(f.dir); err == nil {
				mirror = ws.Settings().Mirror
			}
			u := selfupdate.New(mirror)
			ctx := cmd.Context()
			tag := version
			if tag == "" {
				var err error
				if tag, err = u.Latest(ctx); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if tag == buildinfo.Version && !force {
				fmt.Fprintf(out, "已是最新：%s（--force 重新安装）\n", tag)
				return nil
			}
			target, err := selfupdate.Executable()
			if err != nil {
				return err
			}
			asset, _ := selfupdate.AssetName()
			fmt.Fprintf(out, "下载 %s %s …\n", tag, asset)
			v, err := u.Install(ctx, tag, target)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "已升级：%s\n", v)
			return nil
		},
	}
	c.Flags().StringVar(&version, "version", "", "指定版本，如 v0.1.0-alpha.14（访问不了 api.github.com 时使用）")
	c.Flags().BoolVar(&force, "force", false, "已是最新也重新安装")
	return c
}

func uninstallCmd(f *rootFlags) *cobra.Command {
	var purge, yes bool
	c := &cobra.Command{
		Use: "uninstall", Short: "卸载 Tlash：还原网关、移除服务和定时任务；--purge 同时删除数据", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openManaged(f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !yes {
				what := "卸载 Tlash（保留数据目录 " + ws.Dir + "）"
				if purge {
					what = "卸载 Tlash 并删除数据目录 " + ws.Dir
				}
				if !confirm(cmd.InOrStdin(), out, what+"？[y/N] ") {
					return errors.New("已取消")
				}
			}
			if err := ws.Uninstall(cmd.Context(), purge); err != nil {
				return err
			}
			if bin, err := selfupdate.Executable(); err == nil {
				if err := os.Remove(bin); err != nil {
					fmt.Fprintf(out, "无法删除 %s：%v\n", bin, err)
				}
			}
			fmt.Fprintln(out, "Tlash 已卸载")
			return nil
		},
	}
	c.Flags().BoolVar(&purge, "purge", false, "同时删除数据目录（设置、订阅、内核）")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "不询问，直接卸载")
	return c
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是":
		return true
	}
	return false
}

func onOffText(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / unit
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
