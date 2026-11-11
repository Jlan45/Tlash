package mihomoapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoint is where the mihomo external controller listens: a unix socket or a TCP address.
type Endpoint struct {
	UnixSocket string // set for unix sockets
	BaseURL    string // http(s)://host:port for TCP
}

// ParseEndpoint accepts "unix:///path", an absolute socket path, "http(s)://host:port" or "host:port".
func ParseEndpoint(s string) (Endpoint, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Endpoint{}, fmt.Errorf("控制接口地址为空")
	case strings.HasPrefix(s, "unix://"):
		p := strings.TrimPrefix(s, "unix://")
		if p == "" {
			return Endpoint{}, fmt.Errorf("unix socket 路径为空: %q", s)
		}
		return Endpoint{UnixSocket: p}, nil
	case strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./"):
		return Endpoint{UnixSocket: s}, nil
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return Endpoint{}, fmt.Errorf("无效的控制接口地址: %q", s)
		}
		return Endpoint{BaseURL: strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")}, nil
	default:
		if _, _, err := net.SplitHostPort(s); err != nil {
			return Endpoint{}, fmt.Errorf("无效的控制接口地址: %q", s)
		}
		// mihomo allows ":9090", meaning all interfaces; reach it via loopback.
		if strings.HasPrefix(s, ":") {
			s = "127.0.0.1" + s
		}
		return Endpoint{BaseURL: "http://" + s}, nil
	}
}

func (e Endpoint) String() string {
	if e.UnixSocket != "" {
		return "unix://" + e.UnixSocket
	}
	return e.BaseURL
}

func (e Endpoint) baseURL() string {
	if e.UnixSocket != "" {
		return "http://mihomo"
	}
	return e.BaseURL
}

func (e Endpoint) transport() *http.Transport {
	t := &http.Transport{
		Proxy:               nil, // never send controller traffic through HTTP_PROXY
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	if e.UnixSocket != "" {
		sock := e.UnixSocket
		t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
	} else {
		t.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	return t
}
