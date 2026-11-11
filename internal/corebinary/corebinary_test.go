package corebinary

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

func TestAMD64Level(t *testing.T) {
	v2 := "flags : fpu cx16 lahf_lm popcnt sse4_1 sse4_2 ssse3"
	v3 := v2 + " avx avx2 bmi1 bmi2 f16c fma abm movbe xsave"
	for in, want := range map[string]string{"flags : fpu sse2": "v1", v2: "v2", v3: "v3", "": "v1"} {
		if got := amd64Level(in); got != want {
			t.Errorf("amd64Level(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseVersionOutput(t *testing.T) {
	v, err := parseVersionOutput("Mihomo Meta alpha-0455d0d linux amd64 with go1.26\nUse tags: with_gvisor\n")
	if err != nil || v != "alpha-0455d0d" {
		t.Fatalf("got %q, %v", v, err)
	}
	if _, err := parseVersionOutput("garbage"); err == nil {
		t.Fatal("want error")
	}
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// Serves a fake GitHub for both the download host and the API.
func fakeGitHub(t *testing.T, version string, asset []byte, digest string) *httptest.Server {
	arch, err := AssetArch()
	if err != nil {
		t.Skip(err)
	}
	name := fmt.Sprintf("mihomo-linux-%s-%s.gz", arch, version)
	mux := http.NewServeMux()
	mux.HandleFunc("/MetaCubeX/mihomo/releases/latest/download/version.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, version)
	})
	mux.HandleFunc("/MetaCubeX/mihomo/releases/download/"+version+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(asset)
	})
	mux.HandleFunc("/repos/MetaCubeX/mihomo/releases/tags/"+version, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"assets":[{"name":%q,"digest":"sha256:%s"}]}`, name, digest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func realCore(t *testing.T) []byte {
	bin := os.Getenv("TLASH_MIHOMO_BIN")
	if bin == "" {
		t.Skip("TLASH_MIHOMO_BIN not set")
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDownloadVerifiesAndKeepsPrevious(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	asset := gz(realCore(t))
	sum := sha256.Sum256(asset)
	srv := fakeGitHub(t, "v1.19.34", asset, hex.EncodeToString(sum[:]))
	d := NewDownloader("")
	d.APIBase, d.DownloadBase = srv.URL, srv.URL

	ctx := context.Background()
	rel, err := d.Latest(ctx, appsettings.ChannelStable)
	if err != nil || rel.Version != "v1.19.34" {
		t.Fatalf("Latest = %+v, %v", rel, err)
	}
	paths := PathsIn(t.TempDir())
	var progress atomic.Int64
	res, err := d.Download(ctx, rel, paths, &progress)
	if err != nil || !res.Verified || res.Version == "" || progress.Load() != int64(len(asset)) {
		t.Fatalf("Download = %+v, %v (progress %d)", res, err, progress.Load())
	}
	// Second install keeps the first as the rollback target.
	if _, err := d.Download(ctx, rel, paths, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Previous); err != nil {
		t.Fatal("previous core not kept")
	}
	if err := Rollback(paths); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadRejectsBadChecksum(t *testing.T) {
	srv := fakeGitHub(t, "v1.0.0", gz([]byte("\x7fELF junk")), strings.Repeat("0", 64))
	d := NewDownloader("")
	d.APIBase, d.DownloadBase = srv.URL, srv.URL
	rel, err := d.Latest(context.Background(), appsettings.ChannelStable)
	if err != nil {
		t.Fatal(err)
	}
	paths := PathsIn(t.TempDir())
	if _, err := d.Download(context.Background(), rel, paths, nil); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if _, err := os.Stat(paths.Current); err == nil {
		t.Fatal("bad core was installed")
	}
}

func TestImportRejectsNonELF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	_ = os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	if _, err := ImportFile(context.Background(), p, PathsIn(t.TempDir())); err == nil {
		t.Fatal("want error")
	}
}

func TestMirrorPrefix(t *testing.T) {
	d := NewDownloader("https://ghfast.top/")
	if got := d.mirrored("https://github.com/a"); got != "https://ghfast.top/https://github.com/a" {
		t.Errorf("mirrored = %s", got)
	}
}
