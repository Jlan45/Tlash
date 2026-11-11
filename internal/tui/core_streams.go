package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

const streamRetryDelay = 2 * time.Second

type trafficMsg mihomoapi.Traffic
type memoryMsg mihomoapi.Memory

type logMsg struct {
	at    time.Time
	entry mihomoapi.LogEntry
}

// coreStreams keeps the long-lived mihomo streams open, reconnecting after failures,
// and funnels their events into a single channel the Bubble Tea loop drains.
type coreStreams struct {
	client     *mihomoapi.Client
	events     chan tea.Msg
	ctx        context.Context
	cancel     context.CancelFunc
	cancelLogs context.CancelFunc
}

func newCoreStreams(client *mihomoapi.Client) *coreStreams {
	ctx, cancel := context.WithCancel(context.Background())
	return &coreStreams{client: client, events: make(chan tea.Msg, 512), ctx: ctx, cancel: cancel}
}

func (s *coreStreams) start(logLevel string) {
	go s.run(s.ctx, func(ctx context.Context) error {
		return s.client.StreamTraffic(ctx, func(t mihomoapi.Traffic) { s.emit(ctx, trafficMsg(t)) })
	})
	go s.run(s.ctx, func(ctx context.Context) error {
		return s.client.StreamMemory(ctx, func(m mihomoapi.Memory) { s.emit(ctx, memoryMsg(m)) })
	})
	s.setLogLevel(logLevel)
}

// setLogLevel reopens the log stream at a new level.
func (s *coreStreams) setLogLevel(level string) {
	if s.cancelLogs != nil {
		s.cancelLogs()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancelLogs = cancel
	go s.run(ctx, func(ctx context.Context) error {
		return s.client.StreamLogs(ctx, level, func(e mihomoapi.LogEntry) {
			s.emit(ctx, logMsg{at: time.Now(), entry: e})
		})
	})
}

func (s *coreStreams) stop() { s.cancel() }

func (s *coreStreams) run(ctx context.Context, open func(context.Context) error) {
	for ctx.Err() == nil {
		_ = open(ctx) // connectivity errors already surface through status polling
		select {
		case <-ctx.Done():
		case <-time.After(streamRetryDelay):
		}
	}
}

func (s *coreStreams) emit(ctx context.Context, msg tea.Msg) {
	select {
	case s.events <- msg:
	case <-ctx.Done():
	}
}

// next waits for one stream event; the app re-arms it after every event.
func (s *coreStreams) next() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-s.events:
			return streamEventMsg{msg}
		case <-s.ctx.Done():
			return nil
		}
	}
}

type streamEventMsg struct{ inner tea.Msg }
