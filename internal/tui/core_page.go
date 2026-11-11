package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
	"github.com/Jlan45/Tlash/internal/corebinary"
)

type latestCoreMsg struct {
	release corebinary.Release
	err     error
}

var managedRows = []managedRow{
	{
		label: "混合端口",
		hint:  "HTTP + SOCKS5 代理端口",
		get: func(m appsettings.Managed) (string, bool) {
			if m.MixedPort != nil {
				return strconv.Itoa(*m.MixedPort), true
			}
			return "", false
		},
		set: func(m *appsettings.Managed, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 65535 {
				return fmt.Errorf("端口需要是 0-65535 的数字")
			}
			m.MixedPort = &n
			return nil
		},
		clear: func(m *appsettings.Managed) { m.MixedPort = nil },
	},
	boolRow("允许局域网", func(m *appsettings.Managed) **bool { return &m.AllowLan }),
	stringRow("绑定地址", "允许局域网时监听的地址，* 表示全部", nil, func(m *appsettings.Managed) **string { return &m.BindAddress }),
	boolRow("IPv6", func(m *appsettings.Managed) **bool { return &m.IPv6 }),
	stringRow("日志级别", "", []string{"debug", "info", "warning", "error", "silent"}, func(m *appsettings.Managed) **string { return &m.LogLevel }),
	boolRow("统一延迟", func(m *appsettings.Managed) **bool { return &m.UnifiedDelay }),
	boolRow("TCP 并发", func(m *appsettings.Managed) **bool { return &m.TCPConcurrent }),
	stringRow("进程匹配", "", []string{"off", "strict", "always"}, func(m *appsettings.Managed) **string { return &m.FindProcessMode }),
	{
		label: "TCP 控制接口",
		hint:  "供 Web 面板使用，如 127.0.0.1:9090；留空关闭。公网监听务必配合密钥",
		get: func(m appsettings.Managed) (string, bool) {
			if m.ExternalController == "" {
				return "关闭", true
			}
			return m.ExternalController, true
		},
		set: func(m *appsettings.Managed, v string) error {
			if v == "关闭" {
				v = ""
			}
			m.ExternalController = v
			if v != "" && m.Secret == "" {
				m.Secret = appsettings.RandomSecret()
			}
			return nil
		},
	},
	{
		label: "API 密钥",
		hint:  "TCP 控制接口的 secret；留空则随机生成",
		get: func(m appsettings.Managed) (string, bool) {
			return maskSecret(m.Secret), true
		},
		set: func(m *appsettings.Managed, v string) error {
			if v == "" || v == maskSecret(m.Secret) {
				v = appsettings.RandomSecret()
			}
			m.Secret = v
			return nil
		},
	},
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return "****" + s[len(s)-4:]
}

type corePage struct {
	sess     *session
	cursor   int
	form     *inputForm
	latest   *corebinary.Release
	checking bool
	progress *atomic.Int64
}

func newCorePage(sess *session) *corePage { return &corePage{sess: sess} }

func (p *corePage) title() string        { return "内核" }
func (p *corePage) capturingInput() bool { return p.form != nil }
func (p *corePage) activate() tea.Cmd    { return fetchCoreState(p.sess.ws, true) }

func (p *corePage) hints() []hint {
	if p.form != nil {
		return p.form.hints()
	}
	return []hint{
		{"D", "下载 / 升级内核"},
		{"enter", "修改设置"},
		{"s", "启动 / 停止"},
		{"x", "改用订阅自带值"},
		{"c", "切换稳定版 / 预览版"},
		{"b", "回滚到上一版本"},
		{"i", "导入本地内核文件"},
		{"M", "设置下载镜像"},
		{"A", "开机自启"},
		{"R", "重启内核"},
	}
}

func (p *corePage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case latestCoreMsg:
		p.checking = false
		if msg.err != nil {
			return notifyErr("检查更新失败", msg.err)
		}
		p.latest = &msg.release
		return p.confirmDownload()
	case tea.KeyPressMsg:
		if p.form != nil {
			cmd, done := p.form.update(msg)
			if done {
				p.form = nil
			}
			return cmd
		}
		return p.handleKey(msg)
	}
	return nil
}

func (p *corePage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	ws, sess := p.sess.ws, p.sess
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
	case "down", "j":
		p.cursor = min(p.cursor+1, len(managedRows)-1)
	case "enter":
		return editManaged(p.sess, managedRows[p.cursor], func(f *inputForm) { p.form = f })
	case "x":
		row := managedRows[p.cursor]
		if row.clear == nil {
			return notify(row.label + " 始终由 Tlash 接管")
		}
		return applyManaged(p.sess, row.label+" 改为使用订阅自带值", row.clear)
	case "D":
		if p.checking {
			return nil
		}
		p.checking = true
		return func() tea.Msg {
			r, err := ws.LatestCore(context.Background())
			return latestCoreMsg{release: r, err: err}
		}
	case "c":
		next := appsettings.ChannelAlpha
		if ws.Settings().Core.Channel == appsettings.ChannelAlpha {
			next = appsettings.ChannelStable
		}
		if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Core.Channel = next }); err != nil {
			return notifyErr("保存失败", err)
		}
		p.latest = nil
		return notify("已切换到" + channelLabel(next) + "，按 D 下载")
	case "b":
		if sess.core.prevVersion == "" {
			return notify("没有可回滚的旧版本")
		}
		return askConfirm(fmt.Sprintf("回滚内核到 %s？", sess.core.prevVersion), func() tea.Cmd {
			return sess.job("core-binary", "回滚内核",
				func(ctx context.Context) (string, error) { return "已回滚", ws.RollbackCore(ctx) }, sess.refreshAll())
		})
	case "i":
		p.form = newInputForm("导入本地内核", func(v []string) tea.Cmd {
			return sess.job("core-binary", "导入内核", func(ctx context.Context) (string, error) {
				ver, err := ws.ImportCore(ctx, expandHome(v[0]))
				return "已安装内核 " + ver, err
			}, sess.refreshAll())
		}).addText("文件路径", "", "/path/to/mihomo 或 mihomo-linux-*.gz", "支持未解压的 .gz 发布包")
	case "M":
		p.form = newInputForm("下载镜像", func(v []string) tea.Cmd {
			mirror := v[0]
			if mirror != "" && !strings.HasPrefix(mirror, "http://") && !strings.HasPrefix(mirror, "https://") {
				return notify("镜像地址需要以 http:// 或 https:// 开头")
			}
			if err := ws.UpdateSettings(func(s *appsettings.Settings) { s.Mirror = mirror }); err != nil {
				return notifyErr("保存失败", err)
			}
			p.latest = nil
			return notify("已保存下载镜像")
		}).addText("镜像前缀", ws.Settings().Mirror, "https://ghfast.top/", "拼接在 github.com 链接前；留空直连 GitHub")
	case "s":
		return toggleCore(sess)
	case "R":
		if !sess.core.running {
			return notify("内核未运行")
		}
		return askConfirm("重启 mihomo 内核？\n现有连接会全部断开。", func() tea.Cmd {
			return sess.job("core-service", "重启内核",
				func(ctx context.Context) (string, error) { return "内核已重启", ws.Restart(ctx) }, sess.refreshAll())
		})
	case "A":
		on := !sess.core.autoStart
		svc := ws.Service()
		return sess.job("core-autostart", "设置开机自启", func(ctx context.Context) (string, error) {
			if err := svc.Install(ctx); err != nil {
				return "", err
			}
			if err := svc.SetAutoStart(ctx, on); err != nil {
				return "", err
			}
			return "开机自启已" + map[bool]string{true: "开启", false: "关闭"}[on], nil
		}, fetchCoreState(ws, false))
	}
	return nil
}

func (p *corePage) confirmDownload() tea.Cmd {
	r := *p.latest
	sess, ws := p.sess, p.sess.ws
	prompt := fmt.Sprintf("下载 mihomo %s（%s）？\n%s", r.Version, channelLabel(r.Channel), r.Asset)
	if r.Version == sess.core.version {
		prompt = fmt.Sprintf("已是最新版本 %s，重新下载？", r.Version)
	}
	if sess.core.running {
		prompt += "\n完成后内核会自动重启。"
	}
	return askConfirm(prompt, func() tea.Cmd {
		progress := &atomic.Int64{}
		p.progress = progress
		return sess.job("core-binary", "下载内核", func(ctx context.Context) (string, error) {
			res, err := ws.InstallCore(ctx, r, progress)
			if err != nil {
				return "", err
			}
			if !res.Verified {
				return "已安装 " + res.Version + "（无法获取校验和，未校验）", nil
			}
			return "已安装 " + res.Version + "（sha256 校验通过）", nil
		}, sess.refreshAll())
	})
}

func channelLabel(c appsettings.CoreChannel) string {
	if c == appsettings.ChannelAlpha {
		return "预览版（Alpha）"
	}
	return "稳定版"
}

func (p *corePage) view(w, h int) string {
	if p.form != nil {
		return p.form.view(w)
	}
	s, ws := p.sess, p.sess.ws
	settings := ws.Settings()
	var b strings.Builder
	row := func(label, value string) { b.WriteString("  " + styleDim.Render(padRight(label, 12)) + value + "\n") }

	b.WriteString(styleTitle.Render("内核文件") + "\n")
	switch {
	case !s.core.installed:
		row("当前版本", styleWarn.Render("未安装")+styleDim.Render("  按 D 下载"))
	case s.core.version == "":
		row("当前版本", styleDim.Render("读取中…"))
	default:
		row("当前版本", s.core.version)
	}
	if s.core.prevVersion != "" {
		row("上一版本", s.core.prevVersion+styleDim.Render("  b 回滚"))
	}
	row("更新通道", channelLabel(settings.Core.Channel)+styleDim.Render("  c 切换"))
	mirror := "直连 GitHub"
	if settings.Mirror != "" {
		mirror = settings.Mirror
	}
	row("下载镜像", mirror+styleDim.Render("  M 修改"))
	if _, busy := s.busy["core-binary"]; busy && p.progress != nil {
		row("下载进度", styleWarn.Render(formatBytes(p.progress.Load())))
	} else if p.checking {
		row("最新版本", styleDim.Render("检查中…"))
	}

	b.WriteString("\n" + styleTitle.Render("服务") + "\n")
	row("托管方式", s.core.kind.Label())
	state := styleDim.Render("● 已停止")
	if s.core.running {
		state = styleOK.Render("● 运行中")
	}
	row("状态", state+styleDim.Render("  s 启动/停止 · R 重启"))
	if s.core.kind != "process" {
		row("开机自启", onOff(s.core.autoStart)+styleDim.Render("  A 切换"))
	}
	row("内核日志", s.core.logHint)

	b.WriteString("\n" + styleTitle.Render("接管设置") + styleDim.Render("  覆盖订阅中的同名项 · enter 修改 · x 改用订阅值") + "\n")
	b.WriteString(renderManagedRows(managedRows, settings.Managed, p.cursor, 14))
	return b.String()
}
