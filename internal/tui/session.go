package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

const (
	statusPollInterval = 2 * time.Second
	trafficHistoryLen  = 240
)

// session is the core state every page reads; only the app model mutates it.
type session struct {
	client    *mihomoapi.Client
	delayTest mihomoapi.DelayTest

	connected bool
	lastErr   error
	version   mihomoapi.Version
	config    mihomoapi.Config
	connCount int

	traffic     mihomoapi.Traffic
	memory      int64
	upHistory   []int64
	downHistory []int64
}

func (s *session) recordTraffic(t mihomoapi.Traffic) {
	s.traffic = t
	s.upHistory = appendCapped(s.upHistory, t.Up, trafficHistoryLen)
	s.downHistory = appendCapped(s.downHistory, t.Down, trafficHistoryLen)
}

func appendCapped(xs []int64, v int64, limit int) []int64 {
	xs = append(xs, v)
	if len(xs) > limit {
		xs = xs[len(xs)-limit:]
	}
	return xs
}

type statusMsg struct {
	version   mihomoapi.Version
	config    mihomoapi.Config
	connCount int
	err       error
}

type statusTickMsg struct{}

func statusTick() tea.Cmd {
	return tea.Tick(statusPollInterval, func(time.Time) tea.Msg { return statusTickMsg{} })
}

func fetchStatus(c *mihomoapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		v, err := c.Version(ctx)
		if err != nil {
			return statusMsg{err: err}
		}
		cfg, err := c.Config(ctx)
		if err != nil {
			return statusMsg{err: err}
		}
		msg := statusMsg{version: v, config: cfg}
		if conns, err := c.Connections(ctx); err == nil {
			msg.connCount = len(conns.Connections)
		}
		return msg
	}
}

// notifyMsg shows a transient message in the status bar.
type notifyMsg struct {
	text  string
	isErr bool
}

func notify(text string) tea.Cmd { return func() tea.Msg { return notifyMsg{text: text} } }

func notifyErr(prefix string, err error) tea.Cmd {
	return func() tea.Msg { return notifyMsg{text: prefix + ": " + err.Error(), isErr: true} }
}

// confirmMsg asks the user to confirm before running onYes.
type confirmMsg struct {
	prompt string
	onYes  tea.Cmd
}

func askConfirm(prompt string, onYes tea.Cmd) tea.Cmd {
	return func() tea.Msg { return confirmMsg{prompt: prompt, onYes: onYes} }
}

// apiCall runs fn in the background and reports failure (or okText on success) in the status bar.
func apiCall(errPrefix, okText string, fn func(ctx context.Context) error, then ...tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		if err := fn(context.Background()); err != nil {
			return notifyMsg{text: errPrefix + ": " + err.Error(), isErr: true}
		}
		cmds := append([]tea.Cmd{}, then...)
		if okText != "" {
			cmds = append(cmds, notify(okText))
		}
		return tea.BatchMsg(cmds)
	}
}
