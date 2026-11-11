package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const exitIPService = "https://api.ip.sb/geoip"

type exitIP struct {
	IP           string `json:"ip"`
	Country      string `json:"country"`
	Organization string `json:"organization"`
	err          error
}

func (e exitIP) label() string {
	if e.err != nil {
		return styleBad.Render("失败: " + e.err.Error())
	}
	parts := []string{e.IP}
	if e.Country != "" {
		parts = append(parts, e.Country)
	}
	if e.Organization != "" {
		parts = append(parts, e.Organization)
	}
	return strings.Join(parts, " · ")
}

type exitIPMsg struct{ proxied, direct exitIP }

func lookupExitIP(ctx context.Context, proxy *url.URL) exitIP {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, exitIPService, nil)
	req.Header.Set("User-Agent", "tlash")
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return exitIP{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return exitIP{err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	var r exitIP
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return exitIP{err: err}
	}
	return r
}

// checkExitIP compares the public IP seen directly and through the core's mixed port.
func checkExitIP(mixedPort int) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var proxied exitIP
		if mixedPort > 0 {
			proxied = lookupExitIP(ctx, &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", mixedPort)})
		} else {
			proxied = exitIP{err: fmt.Errorf("内核没有开启 mixed-port")}
		}
		return exitIPMsg{proxied: proxied, direct: lookupExitIP(ctx, nil)}
	}
}
