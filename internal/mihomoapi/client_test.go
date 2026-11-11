package mihomoapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in       string
		wantUnix string
		wantURL  string
		wantErr  bool
	}{
		{in: "unix:///run/mihomo.sock", wantUnix: "/run/mihomo.sock"},
		{in: "/etc/tlash/runtime/mihomo.sock", wantUnix: "/etc/tlash/runtime/mihomo.sock"},
		{in: "127.0.0.1:9090", wantURL: "http://127.0.0.1:9090"},
		{in: ":9090", wantURL: "http://127.0.0.1:9090"},
		{in: "http://10.0.0.1:9090/", wantURL: "http://10.0.0.1:9090"},
		{in: "https://example.com:443/api", wantURL: "https://example.com:443/api"},
		{in: "", wantErr: true},
		{in: "unix://", wantErr: true},
		{in: "localhost", wantErr: true},
	}
	for _, c := range cases {
		ep, err := ParseEndpoint(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseEndpoint(%q) = %+v, want error", c.in, ep)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseEndpoint(%q) error: %v", c.in, err)
			continue
		}
		if ep.UnixSocket != c.wantUnix || ep.BaseURL != c.wantURL {
			t.Errorf("ParseEndpoint(%q) = %+v, want unix=%q url=%q", c.in, ep, c.wantUnix, c.wantURL)
		}
	}
}

func newTCPClient(t *testing.T, h http.Handler, secret string) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ep, err := ParseEndpoint(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return New(ep, secret)
}

func TestSecretAndErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"meta":true,"version":"v1.19.34"}`))
	})
	mux.HandleFunc("GET /proxies/{name}/delay", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"message":"Timeout"}`))
	})

	v, err := newTCPClient(t, mux, "s3cret").Version(context.Background())
	if err != nil || v.Version != "v1.19.34" || !v.Meta {
		t.Fatalf("Version() = %+v, %v", v, err)
	}

	_, err = newTCPClient(t, mux, "wrong").Version(context.Background())
	if !IsUnauthorized(err) {
		t.Fatalf("want unauthorized, got %v", err)
	}

	_, err = newTCPClient(t, mux, "").ProxyDelay(context.Background(), "a b", DelayTest{URL: "http://x", Timeout: time.Second})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusGatewayTimeout || apiErr.Message != "Timeout" {
		t.Fatalf("want 504 Timeout APIError, got %v", err)
	}
}

func TestSelectProxyEscapesNameAndSendsBody(t *testing.T) {
	var gotPath, gotName string
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /proxies/{group}", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.PathValue("group")
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotName = body.Name
		w.WriteHeader(http.StatusNoContent)
	})
	err := newTCPClient(t, mux, "").SelectProxy(context.Background(), "🚀 节点/选择", "香港 01")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "🚀 节点/选择" || gotName != "香港 01" {
		t.Fatalf("got group=%q name=%q", gotPath, gotName)
	}
}

func TestStreamOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "m.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /traffic", func(w http.ResponseWriter, r *http.Request) {
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, `{"up":%d,"down":%d,"upTotal":0,"downTotal":0}`+"\n", i, i*10)
			w.(http.Flusher).Flush()
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c := New(Endpoint{UnixSocket: sock}, "")
	var got []Traffic
	err = c.StreamTraffic(context.Background(), func(tr Traffic) { got = append(got, tr) })
	if len(got) != 3 || got[2].Down != 30 {
		t.Fatalf("got %+v", got)
	}
	// The server ending the stream is reported, so callers know to reconnect.
	if err == nil {
		t.Fatal("want error when stream ends")
	}
}
