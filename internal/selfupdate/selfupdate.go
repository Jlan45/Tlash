// Package selfupdate replaces the running tlash binary with a newer release.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Jlan45/Tlash/internal/buildinfo"
	"github.com/Jlan45/Tlash/internal/webui"
)

const repo = "Jlan45/Tlash"

// Updater talks to GitHub; the bases are overridable for tests.
type Updater struct {
	Mirror       string // prefixed to github.com download URLs
	APIBase      string
	DownloadBase string
	HTTP         *http.Client
}

func New(mirror string) *Updater {
	return &Updater{Mirror: mirror, APIBase: "https://api.github.com", DownloadBase: "https://github.com", HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// AssetName is the release tarball for this binary's variant and architecture.
func AssetName() (string, error) {
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64", "386", "riscv64", "mips", "mipsle":
	case "arm":
		arch = "armv" + buildinfo.Setting("GOARM", "7")
	default:
		return "", fmt.Errorf("不支持的架构: %s", arch)
	}
	prefix := "tlash"
	if webui.Lite {
		prefix = "tlash-lite"
	}
	return prefix + "-linux-" + arch + ".tar.gz", nil
}

// Latest is the most recently published release tag, pre-releases included. GitHub's list
// isn't ordered by publish time (alpha.9 sorts before alpha.10), so sort it here.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	b, err := u.get(ctx, u.APIBase+"/repos/"+repo+"/releases?per_page=20", 4<<20)
	if err != nil {
		return "", fmt.Errorf("获取最新版本失败（访问不了 api.github.com 时可用 --version 指定）: %w", err)
	}
	var rels []release
	if err := json.Unmarshal(b, &rels); err != nil {
		return "", err
	}
	rels = slices.DeleteFunc(rels, func(r release) bool { return r.Draft || r.Published.IsZero() })
	if len(rels) == 0 {
		return "", errors.New("没有已发布的版本")
	}
	slices.SortFunc(rels, func(a, b release) int { return b.Published.Compare(a.Published) })
	return rels[0].Tag, nil
}

type release struct {
	Tag       string    `json:"tag_name"`
	Draft     bool      `json:"draft"`
	Published time.Time `json:"published_at"`
}

func (u *Updater) mirrored(url string) string {
	if u.Mirror == "" {
		return url
	}
	return strings.TrimRight(u.Mirror, "/") + "/" + url
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tlash")
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("文件过大: %s", url)
	}
	return b, nil
}

// Install downloads tag, verifies it against checksums.txt and replaces target (the running
// binary) atomically. It returns the new binary's `version` output.
func (u *Updater) Install(ctx context.Context, tag, target string) (string, error) {
	asset, err := AssetName()
	if err != nil {
		return "", err
	}
	base := u.mirrored(fmt.Sprintf("%s/%s/releases/download/%s", u.DownloadBase, repo, tag))
	sums, err := u.get(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return "", fmt.Errorf("下载校验文件失败: %w", err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == asset {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("%s 没有 %s", tag, asset)
	}
	tgz, err := u.get(ctx, base+"/"+asset, 200<<20)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	sum := sha256.Sum256(tgz)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("sha256 校验失败：%s，期望 %s", got, want)
	}
	bin, err := extractTlash(tgz)
	if err != nil {
		return "", err
	}
	staged := filepath.Join(filepath.Dir(target), ".tlash.new")
	if err := os.WriteFile(staged, bin, 0o755); err != nil {
		return "", fmt.Errorf("写入失败（需要对 %s 有写权限）: %w", filepath.Dir(target), err)
	}
	out, err := exec.CommandContext(ctx, staged, "version").Output()
	if err != nil {
		os.Remove(staged)
		return "", fmt.Errorf("新版本无法在本机运行: %w", err)
	}
	// Renaming over a running binary is safe on Linux; the running process keeps its inode.
	if err := os.Rename(staged, target); err != nil {
		os.Remove(staged)
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func extractTlash(tgz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, errors.New("发布包里没有 tlash")
		}
		if h.Name == "tlash" {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// Executable is the running binary's real path (the file to replace).
func Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
