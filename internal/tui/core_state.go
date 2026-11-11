package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/coreservice"
	"github.com/Jlan45/Tlash/internal/workspace"
)

// coreState is what the workspace knows about the local core: binary and service.
type coreState struct {
	known       bool
	installed   bool
	running     bool
	autoStart   bool
	kind        coreservice.Kind
	logHint     string
	version     string
	prevVersion string
}

type coreStateMsg struct {
	state        coreState
	withVersions bool
}

// fetchCoreState polls the service; versions (an exec each) only when asked.
func fetchCoreState(ws *workspace.Workspace, withVersions bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		svc := ws.Service()
		st := coreState{known: true, installed: ws.CoreInstalled(), kind: svc.Kind(), logHint: svc.LogHint()}
		st.running, _ = svc.Running(ctx)
		st.autoStart, _ = svc.AutoStart(ctx)
		if withVersions && st.installed {
			st.version, st.prevVersion = ws.CoreVersion(ctx)
		}
		return coreStateMsg{state: st, withVersions: withVersions}
	}
}

func (s *session) applyCoreState(msg coreStateMsg) {
	prev := s.core
	s.core = msg.state
	if !msg.withVersions {
		s.core.version, s.core.prevVersion = prev.version, prev.prevVersion
	}
}

type jobDoneMsg struct{ key string }

// job runs fn in the background, showing text in the status bar until it finishes.
// okText (if non-empty) or the error is reported when done; then runs afterwards.
func (s *session) job(key, text string, fn func(ctx context.Context) (okText string, err error), then ...tea.Cmd) tea.Cmd {
	if _, busy := s.busy[key]; busy {
		return notify(s.busy[key] + "，请稍候")
	}
	s.busy[key] = text
	return func() tea.Msg {
		okText, err := fn(context.Background())
		cmds := []tea.Cmd{func() tea.Msg { return jobDoneMsg{key: key} }}
		if err != nil {
			if follow := s.followUp(err); follow != nil {
				cmds = append(cmds, follow)
			} else {
				cmds = append(cmds, notifyErr(text+"失败", err))
			}
		} else if okText != "" {
			cmds = append(cmds, notify(okText))
		}
		return tea.BatchMsg(append(cmds, then...))
	}
}

// refreshAll re-polls everything after an action changed core state.
func (s *session) refreshAll() tea.Cmd {
	cmds := []tea.Cmd{fetchStatus(s.client)}
	if s.ws != nil {
		cmds = append(cmds, fetchCoreState(s.ws, true))
	}
	return tea.Batch(cmds...)
}

// followUp turns errors the user can fix in one step into a confirm prompt.
func (s *session) followUp(err error) tea.Cmd {
	var stale *workspace.StaleCoreError
	if s.ws == nil || !errors.As(err, &stale) {
		return nil
	}
	ws, pid := s.ws, stale.PID
	return askConfirm(fmt.Sprintf("端口被本数据目录残留的旧内核占用（pid %d），\n它不受当前托管方式管理。结束它并重新启动内核？", pid), func() tea.Cmd {
		return s.job("core-service", "清理旧内核并启动", func(ctx context.Context) (string, error) {
			if err := ws.StopStaleCore(pid); err != nil {
				return "", err
			}
			if running, _ := ws.Service().Running(ctx); running {
				return "已清理旧内核并重启", ws.Restart(ctx)
			}
			return "已清理旧内核并启动", ws.Start(ctx)
		}, s.refreshAll())
	})
}
