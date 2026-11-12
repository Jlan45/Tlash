// Package webui carries the zashboard dashboard inside release binaries, so selecting it
// works without reaching GitHub.
package webui

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Name is the dashboard this package embeds.
const Name = "zashboard"

// stampFile marks a ui directory as extracted by Tlash (and which version).
const stampFile = ".tlash-embedded"

// bundle and version are vars so tests can supply their own.
var bundle, version = load()

func load() ([]byte, string) {
	zipBytes, err := fs.ReadFile(assets, "assets/zashboard.zip")
	if err != nil {
		return nil, ""
	}
	v, _ := fs.ReadFile(assets, "assets/zashboard.version")
	return zipBytes, strings.TrimSpace(string(v))
}

// Embedded reports the embedded dashboard's version; ok is false in builds without it.
func Embedded() (ver string, ok bool) { return version, bundle != nil }

// License is the dashboard's license text (MIT), shipped alongside it.
func License() string {
	b, _ := fs.ReadFile(assets, "assets/LICENSE-zashboard")
	return string(b)
}

// InstalledVersion reads the stamp in dir: the embedded version Tlash extracted there, or ""
// when the directory came from elsewhere (an online update) or doesn't exist.
func InstalledVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, stampFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Extract unpacks the embedded dashboard into dir, replacing its contents atomically.
func Extract(dir string) error {
	if bundle == nil {
		return errors.New("这个版本的 Tlash 没有内置 Web 面板")
	}
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return fmt.Errorf("读取内置面板失败: %w", err)
	}
	staged := dir + ".new"
	_ = os.RemoveAll(staged)
	for _, f := range zr.File {
		// The release zip wraps everything in dist/.
		rel := strings.TrimPrefix(f.Name, "dist/")
		if rel == "" || f.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(staged, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, staged+string(filepath.Separator)) {
			return fmt.Errorf("内置面板包含非法路径: %s", f.Name)
		}
		if err := writeEntry(f, target); err != nil {
			_ = os.RemoveAll(staged)
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(staged, stampFile), []byte(version), 0o644); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	old := dir + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, dir); err != nil {
		return err
	}
	return os.RemoveAll(old)
}

func writeEntry(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 64<<20)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ClearStamp marks dir as no longer Tlash-managed, e.g. after an online update, so a later
// Tlash start won't replace the newer files with the embedded ones.
func ClearStamp(dir string) { _ = os.Remove(filepath.Join(dir, stampFile)) }
