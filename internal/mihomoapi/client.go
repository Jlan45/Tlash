// Package mihomoapi is a client for the mihomo external-controller REST API.
package mihomoapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultTimeout = 10 * time.Second

type Client struct {
	endpoint Endpoint
	secret   string
	http     *http.Client
}

func New(endpoint Endpoint, secret string) *Client {
	return &Client{
		endpoint: endpoint,
		secret:   secret,
		// No Client.Timeout: streaming endpoints stay open; unary calls bound themselves via ctx.
		http: &http.Client{Transport: endpoint.transport()},
	}
}

func (c *Client) Endpoint() Endpoint { return c.endpoint }

// APIError is a non-2xx response from mihomo.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("mihomo 返回 HTTP %d", e.Status)
	}
	return fmt.Sprintf("mihomo 返回 HTTP %d: %s", e.Status, e.Message)
}

// IsUnauthorized reports whether err is a rejected secret.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	u := c.endpoint.baseURL() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		msg := string(bytes.TrimSpace(b))
		if json.Unmarshal(b, &m) == nil && m.Message != "" {
			msg = m.Message
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

// call performs a unary request and decodes the JSON response into out (if non-nil).
func (c *Client) call(ctx context.Context, timeout time.Duration, method, path string, query url.Values, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.call(ctx, defaultTimeout, http.MethodGet, "/version", nil, nil, &v)
	return v, err
}

func (c *Client) Config(ctx context.Context) (Config, error) {
	var cfg Config
	err := c.call(ctx, defaultTimeout, http.MethodGet, "/configs", nil, nil, &cfg)
	return cfg, err
}

// PatchConfig changes runtime fields such as {"mode": "global"}.
func (c *Client) PatchConfig(ctx context.Context, fields map[string]any) error {
	return c.call(ctx, defaultTimeout, http.MethodPatch, "/configs", nil, fields, nil)
}

// SetMode switches the outbound mode: rule, global or direct.
func (c *Client) SetMode(ctx context.Context, mode string) error {
	return c.PatchConfig(ctx, map[string]any{"mode": mode})
}

// Restart restarts the core in place with its current config.
func (c *Client) Restart(ctx context.Context) error {
	return c.call(ctx, defaultTimeout, http.MethodPost, "/restart", nil, map[string]any{"path": "", "payload": ""}, nil)
}

func (c *Client) Proxies(ctx context.Context) (map[string]Proxy, error) {
	var resp struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	err := c.call(ctx, defaultTimeout, http.MethodGet, "/proxies", nil, nil, &resp)
	return resp.Proxies, err
}

// SelectProxy sets the selection of a group. On URLTest/Fallback groups this pins the choice.
func (c *Client) SelectProxy(ctx context.Context, group, proxy string) error {
	return c.call(ctx, defaultTimeout, http.MethodPut, "/proxies/"+url.PathEscape(group), nil, map[string]string{"name": proxy}, nil)
}

// UnfixProxy clears a pinned selection on URLTest/Fallback groups.
func (c *Client) UnfixProxy(ctx context.Context, group string) error {
	return c.call(ctx, defaultTimeout, http.MethodDelete, "/proxies/"+url.PathEscape(group), nil, nil, nil)
}

// DelayTest is the target and limit used for latency tests.
type DelayTest struct {
	URL     string
	Timeout time.Duration
}

func (t DelayTest) query() url.Values {
	q := url.Values{}
	q.Set("url", t.URL)
	q.Set("timeout", strconv.FormatInt(t.Timeout.Milliseconds(), 10))
	return q
}

// ProxyDelay tests one proxy. Timeouts surface as an *APIError from mihomo.
func (c *Client) ProxyDelay(ctx context.Context, name string, t DelayTest) (int, error) {
	var resp struct {
		Delay int `json:"delay"`
	}
	err := c.call(ctx, t.Timeout+5*time.Second, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay", t.query(), nil, &resp)
	return resp.Delay, err
}

// GroupDelay tests every member of a group; members that failed are absent from the result.
func (c *Client) GroupDelay(ctx context.Context, group string, t DelayTest) (map[string]int, error) {
	out := map[string]int{}
	err := c.call(ctx, t.Timeout+5*time.Second, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay", t.query(), nil, &out)
	return out, err
}

func (c *Client) Connections(ctx context.Context) (ConnectionsSnapshot, error) {
	var s ConnectionsSnapshot
	err := c.call(ctx, defaultTimeout, http.MethodGet, "/connections", nil, nil, &s)
	return s, err
}

func (c *Client) CloseAllConnections(ctx context.Context) error {
	return c.call(ctx, defaultTimeout, http.MethodDelete, "/connections", nil, nil, nil)
}
