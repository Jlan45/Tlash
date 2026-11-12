package selfupdate

import (
	"archive/tar"
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
	"strings"
	"testing"
)

func tgzOf(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func fakeGitHub(t *testing.T, asset string, tgz []byte, sum string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/Jlan45/Tlash/releases", func(w http.ResponseWriter, r *http.Request) {
		// Listed out of publish order, like GitHub does.
		fmt.Fprint(w, `[{"tag_name":"v0.1.0-alpha.9","published_at":"2026-11-11T12:47:06Z"},
			{"tag_name":"v0.1.0-alpha.10","published_at":"2026-11-11T12:49:02Z"},
			{"tag_name":"v0.2.0-draft","draft":true}]`)
	})
	mux.HandleFunc("/Jlan45/Tlash/releases/download/v0.1.0-alpha.10/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, asset)
	})
	mux.HandleFunc("/Jlan45/Tlash/releases/download/v0.1.0-alpha.10/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tgz)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestAndInstall(t *testing.T) {
	asset, err := AssetName()
	if err != nil {
		t.Skip(err)
	}
	// The "new release" is a shell script, which is enough to prove replace + verify.
	newBin := []byte("#!/bin/sh\necho tlash v0.1.0-alpha.10\n")
	tgz := tgzOf(t, "tlash", newBin)
	sum := sha256.Sum256(tgz)
	srv := fakeGitHub(t, asset, tgz, hex.EncodeToString(sum[:]))
	u := New("")
	u.APIBase, u.DownloadBase = srv.URL, srv.URL

	tag, err := u.Latest(context.Background())
	if err != nil || tag != "v0.1.0-alpha.10" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
	target := filepath.Join(t.TempDir(), "tlash")
	_ = os.WriteFile(target, []byte("old"), 0o755)
	out, err := u.Install(context.Background(), tag, target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha.10") {
		t.Fatalf("version output = %q", out)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, newBin) {
		t.Fatal("binary not replaced")
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	asset, err := AssetName()
	if err != nil {
		t.Skip(err)
	}
	tgz := tgzOf(t, "tlash", []byte("#!/bin/sh\n"))
	srv := fakeGitHub(t, asset, tgz, strings.Repeat("0", 64))
	u := New("")
	u.APIBase, u.DownloadBase = srv.URL, srv.URL
	target := filepath.Join(t.TempDir(), "tlash")
	_ = os.WriteFile(target, []byte("old"), 0o755)
	if _, err := u.Install(context.Background(), "v0.1.0-alpha.10", target); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatal("binary replaced despite bad checksum")
	}
}
