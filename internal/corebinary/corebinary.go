// Package corebinary downloads, verifies and swaps the mihomo executable.
package corebinary

import (
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
	"strings"
	"sync/atomic"
	"time"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

const (
	repo         = "MetaCubeX/mihomo"
	alphaTag     = "Prerelease-Alpha"
	maxAssetSize = 200 << 20
)

// Paths locates the current core and the one kept for rollback.
type Paths struct {
	Current  string
	Previous string
}

func PathsIn(coreDir string) Paths {
	return Paths{Current: filepath.Join(coreDir, "mihomo"), Previous: filepath.Join(coreDir, "mihomo.prev")}
}

// Version runs `mihomo -v` and returns e.g. "v1.19.34" or "alpha-0455d0d".
func Version(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-v").Output()
	if err != nil {
		return "", fmt.Errorf("无法运行内核 %s: %w", path, err)
	}
	return parseVersionOutput(string(out))
}

func parseVersionOutput(out string) (string, error) {
	// "Mihomo Meta v1.19.34 linux amd64 with go1.26.11 ..."
	f := strings.Fields(out)
	if len(f) < 3 || f[0] != "Mihomo" {
		return "", fmt.Errorf("无法识别的内核版本输出: %q", strings.TrimSpace(firstLine(out)))
	}
	return f[2], nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// AssetArch is the arch part of mihomo's asset names for this machine.
func AssetArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		cpuinfo, _ := os.ReadFile("/proc/cpuinfo")
		return "amd64-" + amd64Level(string(cpuinfo)), nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "armv7", nil // Tlash itself ships only as GOARM=7
	case "386":
		return "386", nil
	}
	return "", fmt.Errorf("不支持的架构: %s", runtime.GOARCH)
}

// amd64Level maps CPU flags to the GOAMD64 microarchitecture level.
func amd64Level(cpuinfo string) string {
	flags := map[string]bool{}
	for _, line := range strings.Split(cpuinfo, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "flags" {
			for _, f := range strings.Fields(v) {
				flags[f] = true
			}
			break
		}
	}
	has := func(fs ...string) bool {
		for _, f := range fs {
			if !flags[f] {
				return false
			}
		}
		return true
	}
	v2 := has("cx16", "lahf_lm", "popcnt", "sse4_1", "sse4_2", "ssse3")
	switch {
	case v2 && has("avx", "avx2", "bmi1", "bmi2", "f16c", "fma", "abm", "movbe", "xsave"):
		return "v3"
	case v2:
		return "v2"
	default:
		return "v1"
	}
}

// Release identifies one downloadable core build.
type Release struct {
	Channel appsettings.CoreChannel
	Version string // "v1.19.34" or "alpha-0455d0d"
	Asset   string
	URL     string
}

type Downloader struct {
	Mirror string
	HTTP   *http.Client
	// APIBase is overridable for tests.
	APIBase      string
	DownloadBase string
}

func NewDownloader(mirror string) *Downloader {
	return &Downloader{
		Mirror:       mirror,
		HTTP:         &http.Client{Timeout: 10 * time.Minute},
		APIBase:      "https://api.github.com",
		DownloadBase: "https://github.com",
	}
}

func (d *Downloader) mirrored(u string) string {
	if d.Mirror == "" {
		return u
	}
	return strings.TrimRight(d.Mirror, "/") + "/" + u
}

func tagFor(ch appsettings.CoreChannel, version string) string {
	if ch == appsettings.ChannelAlpha {
		return alphaTag
	}
	return version
}

// Latest resolves the newest build of a channel via version.txt, which works through download mirrors.
func (d *Downloader) Latest(ctx context.Context, ch appsettings.CoreChannel) (Release, error) {
	path := "releases/latest/download/version.txt"
	if ch == appsettings.ChannelAlpha {
		path = "releases/download/" + alphaTag + "/version.txt"
	}
	b, err := d.get(ctx, d.mirrored(d.DownloadBase+"/"+repo+"/"+path), 1<<10, nil)
	if err != nil {
		return Release{}, fmt.Errorf("获取最新版本失败: %w", err)
	}
	version := strings.TrimSpace(string(b))
	if version == "" || strings.ContainsAny(version, "/ \n") {
		return Release{}, fmt.Errorf("版本号无效: %q", version)
	}
	arch, err := AssetArch()
	if err != nil {
		return Release{}, err
	}
	asset := fmt.Sprintf("mihomo-linux-%s-%s.gz", arch, version)
	return Release{
		Channel: ch,
		Version: version,
		Asset:   asset,
		URL:     d.mirrored(fmt.Sprintf("%s/%s/releases/download/%s/%s", d.DownloadBase, repo, tagFor(ch, version), asset)),
	}, nil
}

// digest asks the GitHub API for the asset's sha256. Empty when unreachable (e.g. only a mirror works).
func (d *Downloader) digest(ctx context.Context, r Release) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	b, err := d.get(ctx, fmt.Sprintf("%s/repos/%s/releases/tags/%s", d.APIBase, repo, tagFor(r.Channel, r.Version)), 8<<20, nil)
	if err != nil {
		return ""
	}
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if json.Unmarshal(b, &rel) != nil {
		return ""
	}
	for _, a := range rel.Assets {
		if a.Name == r.Asset {
			return strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	return ""
}

func (d *Downloader) get(ctx context.Context, u string, limit int64, progress *atomic.Int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tlash")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	var r io.Reader = io.LimitReader(resp.Body, limit+1)
	if progress != nil {
		r = &countingReader{r: r, n: progress}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("文件过大: %s", u)
	}
	return b, nil
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// InstallResult reports what was installed and whether the download was checksum-verified.
type InstallResult struct {
	Version  string
	Verified bool
}

// Download fetches r, verifies it and installs it as the current core, keeping the old one for rollback.
func (d *Downloader) Download(ctx context.Context, r Release, paths Paths, progress *atomic.Int64) (InstallResult, error) {
	want := d.digest(ctx, r)
	gz, err := d.get(ctx, r.URL, maxAssetSize, progress)
	if err != nil {
		return InstallResult{}, fmt.Errorf("下载内核失败: %w", err)
	}
	if want != "" {
		sum := sha256.Sum256(gz)
		if got := hex.EncodeToString(sum[:]); got != want {
			return InstallResult{}, fmt.Errorf("内核校验失败：sha256 %s，期望 %s", got, want)
		}
	}
	version, err := installBinary(ctx, gz, paths)
	return InstallResult{Version: version, Verified: want != ""}, err
}

// ImportFile installs a core from a local file, raw or gzipped.
func ImportFile(ctx context.Context, src string, paths Paths) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	return installBinary(ctx, b, paths)
}

func installBinary(ctx context.Context, data []byte, paths Paths) (string, error) {
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("解压内核失败: %w", err)
		}
		data, err = io.ReadAll(io.LimitReader(zr, maxAssetSize))
		if err != nil {
			return "", fmt.Errorf("解压内核失败: %w", err)
		}
	}
	if !bytes.HasPrefix(data, []byte("\x7fELF")) {
		return "", errors.New("文件不是 Linux 可执行文件")
	}
	staged := paths.Current + ".new"
	if err := appsettings.WriteFileAtomic(staged, data, 0o755); err != nil {
		return "", err
	}
	version, err := Version(ctx, staged)
	if err != nil {
		os.Remove(staged)
		return "", fmt.Errorf("新内核无法在本机运行（架构不匹配？）: %w", err)
	}
	// Renaming over a running binary is safe on Linux; the old process keeps its inode.
	if _, err := os.Stat(paths.Current); err == nil {
		if err := os.Rename(paths.Current, paths.Previous); err != nil {
			os.Remove(staged)
			return "", err
		}
	}
	if err := os.Rename(staged, paths.Current); err != nil {
		return "", err
	}
	return version, nil
}

// Rollback swaps the current core with the previous one.
func Rollback(paths Paths) error {
	if _, err := os.Stat(paths.Previous); err != nil {
		return errors.New("没有可回滚的旧版本内核")
	}
	tmp := paths.Current + ".swap"
	if err := os.Rename(paths.Current, tmp); err != nil {
		return err
	}
	if err := os.Rename(paths.Previous, paths.Current); err != nil {
		_ = os.Rename(tmp, paths.Current)
		return err
	}
	return os.Rename(tmp, paths.Previous)
}
