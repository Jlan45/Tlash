package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const logBufferLimit = 2000

var logLevels = []string{"debug", "info", "warning", "error"}

type logView struct {
	sess    *session
	streams *coreStreams
	level   string
	buf     *logBuffer

	paused  bool
	pending []logMsg
	// scroll is how many wrapped lines the view sits above the bottom; 0 follows new logs.
	scroll int

	filter filterInput

	// Wrapped-line cache, rebuilt when the buffer, filter or width changes.
	cacheKey   string
	cacheLines []string
}

func newLogView(sess *session, streams *coreStreams, level string) *logView {
	return &logView{sess: sess, streams: streams, level: level, buf: newLogBuffer(logBufferLimit), filter: newFilterInput("过滤日志")}
}

// actions are the logs tab's keys while the main panel is focused.
func (p *logView) actions() []action {
	pause := "暂停"
	if p.paused {
		pause = "继续"
	}
	return []action{
		{"space", pause, func() tea.Cmd { p.togglePause(); return nil }},
		{"L", "日志级别：" + p.level, p.cycleLevel},
		{"/", "过滤日志", func() tea.Cmd { return p.filter.start("过滤日志") }},
		{"c", "清空", func() tea.Cmd { p.clear(); return nil }},
		{"w", "导出到文件", p.export},
		{"G", "跳到最新", func() tea.Cmd { p.scroll = 0; return nil }},
	}
}

func (p *logView) capturing() bool { return p.filter.active }

func (p *logView) togglePause() {
	p.paused = !p.paused
	if !p.paused {
		for _, e := range p.pending {
			p.buf.add(e)
		}
		p.pending = nil
	}
}

func (p *logView) cycleLevel() tea.Cmd {
	p.level = nextLogLevel(p.level)
	p.streams.setLogLevel(p.level)
	return notify("日志级别：" + p.level + "（仅影响之后的日志）")
}

func (p *logView) clear() {
	p.buf.clear()
	p.pending = nil
	p.scroll = 0
}

func (p *logView) update(msg tea.Msg) {
	if m, ok := msg.(logMsg); ok {
		if p.paused {
			p.pending = appendCappedLogs(p.pending, m, logBufferLimit)
		} else {
			p.buf.add(m)
		}
	}
}

func appendCappedLogs(xs []logMsg, e logMsg, limit int) []logMsg {
	xs = append(xs, e)
	if len(xs) > limit {
		xs = xs[len(xs)-limit:]
	}
	return xs
}

// key handles scrolling and filter typing; reports whether it consumed the key.
func (p *logView) key(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if p.filter.active {
		p.scroll = 0
		return p.filter.update(msg), true
	}
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
	case "end":
		p.scroll = 0
	default:
		return nil, false
	}
	p.scroll = max(p.scroll, 0)
	return nil, true
}

func nextLogLevel(cur string) string {
	for i, l := range logLevels {
		if l == cur {
			return logLevels[(i+1)%len(logLevels)]
		}
	}
	return "info"
}

func (p *logView) export() tea.Cmd {
	entries := append([]logMsg(nil), p.buf.matching(p.filter.value())...)
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

func (p *logView) wrappedLines(w int) []string {
	key := fmt.Sprintf("%d|%d|%s", p.buf.seq, w, p.filter.value())
	if key == p.cacheKey {
		return p.cacheLines
	}
	const prefixW = 17 // "15:04:05 WARNING "
	indent := strings.Repeat(" ", prefixW)
	bodyW := max(w-prefixW, 10)
	lines := p.cacheLines[:0]
	for _, e := range p.buf.matching(p.filter.value()) {
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

func (p *logView) view(w, h int) string {
	header := styleDim.Render("级别 " + p.level)
	if p.paused {
		header += styleWarn.Render(fmt.Sprintf("  已暂停，%d 条新日志", len(p.pending)))
	}
	header += "\n" + p.filter.line(w)

	lines := p.wrappedLines(w)
	listH := h - 2
	if len(lines) == 0 {
		hintText := "等待日志…"
		if !p.sess.connected {
			hintText = "未连接到内核"
		}
		return header + "\n" + styleDim.Render(hintText)
	}
	p.scroll = min(p.scroll, max(len(lines)-listH, 0))
	end := len(lines) - p.scroll
	start := max(end-listH, 0)
	if p.scroll > 0 {
		header += styleDim.Render(fmt.Sprintf("  ↑ 向上滚动 %d 行，G 回到最新", p.scroll))
	}
	return header + "\n" + strings.Join(lines[start:end], "\n")
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
