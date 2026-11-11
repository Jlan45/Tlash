package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jlan45/Tlash/internal/profile"
)

func TestRunDueUpdatesOnlyDueProfiles(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("proxies:\n  - {name: n, type: socks5, server: 127.0.0.1, port: 1}\n"))
	}))
	defer srv.Close()

	dir, _ := os.MkdirTemp("", "tlt")
	defer os.RemoveAll(dir)
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	content := []byte("proxies: []\n")
	due := profile.Profile{ID: "due", Name: "过期", Kind: profile.KindRemote, URL: srv.URL, UpdateInterval: time.Hour, UpdatedAt: now.Add(-2 * time.Hour)}
	fresh := profile.Profile{ID: "fresh", Name: "新鲜", Kind: profile.KindRemote, URL: srv.URL, UpdateInterval: time.Hour, UpdatedAt: now}
	manual := profile.Profile{ID: "manual", Name: "手动", Kind: profile.KindRemote, URL: srv.URL}
	local := profile.Profile{ID: "local", Name: "本地", Kind: profile.KindLocal, UpdateInterval: time.Hour}
	for _, p := range []profile.Profile{due, fresh, manual, local} {
		if err := w.Profiles.Put(p, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.RunDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("subscription fetched %d times, want 1", hits.Load())
	}
	if got, _ := w.Profiles.Get("due"); !got.UpdatedAt.After(now.Add(-time.Minute)) {
		t.Error("due profile not marked updated")
	}
	if log := strings.Join(w.TaskLogTail(10), "\n"); !strings.Contains(log, "订阅 过期 已更新") {
		t.Errorf("task log = %q", log)
	}
}
