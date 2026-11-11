package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const logBufferLimit = 2000

var logLevels = []string{"debug", "info", "warning", "error"}

type logsPage struct {
	sess    *session
	streams *coreStreams
	level   string
	buf     *logBuffer

	paused  bool
	pending []logMsg
	// scroll is how many wrapped lines the view sits above the bottom; 0 follows new logs.
	scroll int

	filter    textinput.Model
	filtering bool

	// Wrapped-line cache, rebuilt when the buffer, filter or width changes.
	cacheKey   string
	cacheLines []string
}

func newLogsPage(sess *session, streams *coreStreams, level string) *logsPage {
	ti := newTextInput("/ ", "过滤日志")
	return &logsPage{sess: sess, streams: streams, level: level, buf: newLogBuffer(logBufferLimit), filter: ti}
}

func (p *logsPage) title() string        { return "日志" }
func (p *logsPage) activate() tea.Cmd    { return nil }
func (p *logsPage) capturingInput() bool { return p.filtering }

func (p *logsPage) hints() []hint {
	pause := "暂停"
	if p.paused {
		pause = "继续"
	}
	return []hint{
		{"space", pause},
		{"l", "日志级别：" + p.level},
		{"/", "过滤"},
		{"c", "清空"},
		{"x", "导出到文件"},
		{"j/k", "滚动"},
		{"G", "跳到最新"},
	}
}

func (p *logsPage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logMsg:
		if p.paused {
			p.pending = appendCappedLogs(p.pending, msg, logBufferLimit)
		} else {
			p.buf.add(msg)
		}
		return nil
	case tea.KeyPressMsg:
		if p.filtering {
			return p.updateFilter(msg)
		}
		return p.handleKey(msg)
	}
	return nil
}

func appendCappedLogs(xs []logMsg, e logMsg, limit int) []logMsg {
	xs = append(xs, e)
	if len(xs) > limit {
		xs = xs[len(xs)-limit:]
	}
	return xs
}

func (p *logsPage) updateFilter(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.filter.SetValue("")
		fallthrough
	case "enter":
		p.filtering = false
		p.filter.Blur()
		p.scroll = 0
		return nil
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	p.scroll = 0
	return cmd
}

func (p *logsPage) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		p.scroll++
	case "down", "j":
		p.scroll--
	case "pgup", "ctrl+u":
		p.scroll += 10
	case "pgdown", "ctrl+d":
		p.scroll -= 10
	case "home", "g":
		p.scroll = len(p.cacheLines)
	case "end", "G":
		p.scroll = 0
	case "space":
		p.paused = !p.paused
		if !p.paused {
			for _, e := range p.pending {
				p.buf.add(e)
			}
			p.pending = nil
		}
	case "l":
		p.level = nextLogLevel(p.level)
		p.streams.setLogLevel(p.level)
		return notify("日志级别：" + p.level + "（仅影响之后的日志）")
	case "/":
		p.filtering = true
		return p.filter.Focus()
	case "esc":
		p.filter.SetValue("")
	case "c":
		p.buf.clear()
		p.pending = nil
		p.scroll = 0
	case "x":
		return p.export()
	}
	p.scroll = max(p.scroll, 0)
	return nil
}

func nextLogLevel(cur string) string {
	for i, l := range logLevels {
		if l == cur {
			return logLevels[(i+1)%len(logLevels)]
		}
	}
	return "info"
}

func (p *logsPage) export() tea.Cmd {
	entries := append([]logMsg(nil), p.buf.matching(p.filter.Value())...)
	return func() tea.Msg {
		name := "tlash-logs-" + time.Now().Format("20060102-150405") + ".log"
		var b strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&b, "%s %-7s %s\n", e.at.Format(time.DateTime), strings.ToUpper(e.entry.Type), e.entry.Payload)
		}
		if err := os.WriteFile(name, []byte(b.String()), 0o600); err != nil {
			return notifyMsg{text: "导出失败: " + err.Error(), isErr: true}
		}
		abs, _ := filepath.Abs(name)
		return notifyMsg{text: fmt.Sprintf("已导出 %d 条日志到 %s", len(entries), abs)}
	}
}

func (p *logsPage) wrappedLines(w int) []string {
	key := fmt.Sprintf("%d|%d|%s", p.buf.seq, w, p.filter.Value())
	if key == p.cacheKey {
		return p.cacheLines
	}
	const prefixW = 17 // "15:04:05 WARNING "
	indent := strings.Repeat(" ", prefixW)
	bodyW := max(w-prefixW, 10)
	lines := p.cacheLines[:0]
	for _, e := range p.buf.matching(p.filter.Value()) {
		prefix := styleDim.Render(e.at.Format("15:04:05")) + " " + levelStyle(e.entry.Type).Render(padRight(levelLabel(e.entry.Type), 8))
		for i, part := range strings.Split(ansi.Hardwrap(e.entry.Payload, bodyW, true), "\n") {
			if i == 0 {
				lines = append(lines, prefix+part)
			} else {
				lines = append(lines, indent+part)
			}
		}
	}
	p.cacheKey, p.cacheLines = key, lines
	return lines
}

func (p *logsPage) view(w, h int) string {
	header := styleTitle.Render("日志") + styleDim.Render("  级别 "+p.level)
	if p.paused {
		header += styleWarn.Render(fmt.Sprintf("  已暂停，%d 条新日志", len(p.pending)))
	}
	switch {
	case p.filtering:
		p.filter.SetWidth(max(w-4, 10))
		header += "\n" + p.filter.View()
	case p.filter.Value() != "":
		header += "\n" + styleAccent.Render("过滤: "+p.filter.Value()) + styleDim.Render("  (esc 清除)")
	default:
		header += "\n"
	}

	lines := p.wrappedLines(w)
	listH := h - 3
	if len(lines) == 0 {
		hintText := "等待日志…"
		if !p.sess.connected {
			hintText = "未连接到内核"
		}
		return header + "\n\n" + styleDim.Render(hintText)
	}
	p.scroll = min(p.scroll, max(len(lines)-listH, 0))
	end := len(lines) - p.scroll
	start := max(end-listH, 0)
	if p.scroll > 0 {
		header += styleDim.Render(fmt.Sprintf("  ↑ 向上滚动 %d 行，G 回到最新", p.scroll))
	}
	return header + "\n\n" + strings.Join(lines[start:end], "\n")
}

func levelLabel(t string) string {
	if t == "warning" {
		return "WARN"
	}
	return strings.ToUpper(t)
}

func levelStyle(t string) lipgloss.Style {
	switch t {
	case "debug":
		return styleDim
	case "warning":
		return styleWarn
	case "error":
		return styleBad
	default:
		return styleAccent
	}
}
