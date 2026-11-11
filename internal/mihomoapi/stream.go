package mihomoapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// Without a websocket upgrade, mihomo streams these endpoints as newline-delimited JSON.
func stream[T any](ctx context.Context, c *Client, path string, query url.Values, fn func(T)) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var v T
		if err := dec.Decode(&v); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		fn(v)
	}
}

// StreamTraffic calls fn about once per second until ctx ends or the connection drops.
func (c *Client) StreamTraffic(ctx context.Context, fn func(Traffic)) error {
	return stream(ctx, c, "/traffic", nil, fn)
}

func (c *Client) StreamMemory(ctx context.Context, fn func(Memory)) error {
	return stream(ctx, c, "/memory", nil, fn)
}

// StreamLogs streams logs at level and above (debug, info, warning, error, silent).
func (c *Client) StreamLogs(ctx context.Context, level string, fn func(LogEntry)) error {
	q := url.Values{}
	if level != "" {
		q.Set("level", level)
	}
	return stream(ctx, c, "/logs", q, fn)
}
