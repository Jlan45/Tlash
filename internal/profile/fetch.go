package profile

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	fetchTimeout   = 60 * time.Second
	maxProfileSize = 32 << 20
)

// FetchResult is a downloaded subscription and what its response headers told us.
type FetchResult struct {
	Content []byte
	// Name and UpdateInterval are zero when the provider didn't send them.
	Name           string
	UpdateInterval time.Duration
	Usage          *Usage
}

// Fetch downloads a subscription. proxyURL, when set, routes the request through the core.
func Fetch(ctx context.Context, rawURL, userAgent string, proxyURL *url.URL) (FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("订阅链接无效: %w", err)
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	req.Header.Set("User-Agent", userAgent)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("下载订阅失败: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchResult{}, fmt.Errorf("下载订阅失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProfileSize+1))
	if err != nil {
		return FetchResult{}, fmt.Errorf("下载订阅失败: %w", err)
	}
	if len(body) > maxProfileSize {
		return FetchResult{}, errors.New("订阅内容超过 32 MB")
	}
	if err := Validate(body); err != nil {
		return FetchResult{}, err
	}
	res := FetchResult{Content: body, Name: nameFromHeaders(resp.Header)}
	if u, ok := ParseUserinfo(resp.Header.Get("subscription-userinfo")); ok {
		res.Usage = &u
	}
	if h, err := strconv.ParseFloat(strings.TrimSpace(resp.Header.Get("profile-update-interval")), 64); err == nil && h > 0 {
		res.UpdateInterval = time.Duration(h * float64(time.Hour))
	}
	return res, nil
}

// Subscription URLs embed access tokens, so errors must not echo them.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Validate checks content is a Clash/mihomo YAML config with at least some outbounds.
func Validate(content []byte) error {
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil || doc == nil {
		if looksLikeNodeList(content) {
			return errors.New("订阅返回的是节点链接列表而不是 Clash 配置，请使用 Clash 格式的订阅链接或订阅转换")
		}
		return errors.New("订阅内容不是有效的 YAML 配置")
	}
	_, hasProxies := doc["proxies"]
	_, hasProviders := doc["proxy-providers"]
	if !hasProxies && !hasProviders {
		return errors.New("配置中没有 proxies 或 proxy-providers")
	}
	return nil
}

func looksLikeNodeList(content []byte) bool {
	s := strings.TrimSpace(string(content))
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		s = string(decoded)
	}
	for _, scheme := range []string{"ss://", "ssr://", "vmess://", "vless://", "trojan://", "hysteria2://", "hy2://", "tuic://"} {
		if strings.Contains(s, scheme) {
			return true
		}
	}
	return false
}

// ParseUserinfo parses "upload=1; download=2; total=3; expire=1700000000".
func ParseUserinfo(h string) (Usage, bool) {
	if strings.TrimSpace(h) == "" {
		return Usage{}, false
	}
	var u Usage
	found := false
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		found = true
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			u.Upload = int64(n)
		case "download":
			u.Download = int64(n)
		case "total":
			u.Total = int64(n)
		case "expire":
			if n > 0 {
				u.Expire = time.Unix(int64(n), 0)
			}
		}
	}
	return u, found
}

// nameFromHeaders reads profile-title (optionally "base64:...") or the content-disposition filename.
func nameFromHeaders(h http.Header) string {
	if t := strings.TrimSpace(h.Get("profile-title")); t != "" {
		if enc, ok := strings.CutPrefix(t, "base64:"); ok {
			if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
				return strings.TrimSpace(string(b))
			}
		}
		return t
	}
	if _, params, err := mime.ParseMediaType(h.Get("content-disposition")); err == nil {
		name := params["filename"] // mime decodes filename* into filename
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		return strings.TrimSpace(name)
	}
	return ""
}

// NameFromURL is a fallback display name: the host of the subscription URL.
func NameFromURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "订阅"
}
