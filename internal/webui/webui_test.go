package webui

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testBundle(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestExtractReplacesAndStamps(t *testing.T) {
	oldB, oldV := bundle, version
	defer func() { bundle, version = oldB, oldV }()
	bundle, version = testBundle(t, map[string]string{"dist/index.html": "<html>", "dist/assets/app.js": "js"}), "v9.9.9"

	dir := filepath.Join(t.TempDir(), "ui", "zashboard")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "stale.js"), []byte("old"), 0o644)
	if err := Extract(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "assets", "app.js")); string(b) != "js" {
		t.Fatal("dist/ prefix not stripped")
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.js")); !os.IsNotExist(err) {
		t.Fatal("old files not replaced")
	}
	if InstalledVersion(dir) != "v9.9.9" {
		t.Fatalf("stamp = %q", InstalledVersion(dir))
	}
	ClearStamp(dir)
	if InstalledVersion(dir) != "" {
		t.Fatal("stamp not cleared")
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	oldB, oldV := bundle, version
	defer func() { bundle, version = oldB, oldV }()
	bundle, version = testBundle(t, map[string]string{"dist/../../evil": "x"}), "v1"
	if err := Extract(filepath.Join(t.TempDir(), "ui")); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestWithoutBundle(t *testing.T) {
	oldB := bundle
	defer func() { bundle = oldB }()
	bundle = nil
	if _, ok := Embedded(); ok {
		t.Fatal("Embedded() true without a bundle")
	}
	if err := Extract(t.TempDir()); err == nil {
		t.Fatal("Extract succeeded without a bundle")
	}
}
