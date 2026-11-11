package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleConfig = "proxies:\n  - {name: a, type: socks5, server: 1.1.1.1, port: 1}\n"

func TestParseUserinfo(t *testing.T) {
	u, ok := ParseUserinfo("upload=1024; download=2048; total=10737418240; expire=1767225600")
	if !ok || u.Used() != 3072 || u.Total != 10737418240 || u.Expire.Unix() != 1767225600 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
	if _, ok := ParseUserinfo(""); ok {
		t.Error("empty header should not parse")
	}
}

func TestValidate(t *testing.T) {
	if err := Validate([]byte(sampleConfig)); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	if err := Validate([]byte("rules: []\n")); err == nil {
		t.Error("config without proxies accepted")
	}
	err := Validate([]byte("dHJvamFuOi8vcGFzc0BleGFtcGxlLmNvbTo0NDM="))
	if err == nil || !strings.Contains(err.Error(), "节点链接列表") {
		t.Errorf("base64 node list: %v", err)
	}
}

func TestFetchReadsHeadersAndUA(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=3")
		w.Header().Set("profile-update-interval", "12")
		w.Header().Set("content-disposition", `attachment; filename*=UTF-8''%E6%9C%BA%E5%9C%BA.yaml`)
		_, _ = w.Write([]byte(sampleConfig))
	}))
	defer srv.Close()
	res, err := Fetch(context.Background(), srv.URL+"/sub?token=secret", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != DefaultUserAgent || res.Name != "机场" || res.UpdateInterval != 12*time.Hour || res.Usage.Total != 3 {
		t.Errorf("ua=%q res=%+v", gotUA, res)
	}
}

func TestFetchErrorHidesToken(t *testing.T) {
	_, err := Fetch(context.Background(), "http://127.0.0.1:1/sub?token=TOPSECRET", "", nil)
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("error leaks token or is nil: %v", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	p := Profile{ID: NewID(), Name: "a", Kind: KindLocal}
	if err := s.Put(p, []byte(sampleConfig)); err != nil {
		t.Fatal(err)
	}
	p.Name = "b"
	if err := s.Put(p, nil); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.List()
	if len(ps) != 1 || ps[0].Name != "b" {
		t.Fatalf("list = %+v", ps)
	}
	if b, _ := s.ReadContent(p.ID); string(b) != sampleConfig {
		t.Error("content changed by metadata update")
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.List(); len(ps) != 0 {
		t.Fatal("not deleted")
	}
}
