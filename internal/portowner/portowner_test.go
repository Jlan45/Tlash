package portowner

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestBusyAndDescribe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if !Busy(port) {
		t.Fatal("listening port not reported busy")
	}
	// Our own process holds it, so /proc lookup must find us.
	if d := Describe(port); !strings.Contains(d, "pid "+itoa(os.Getpid())) {
		t.Errorf("Describe = %q", d)
	}
	ln.Close()
	if Busy(port) {
		t.Fatal("closed port still busy")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestUnitFromCgroup(t *testing.T) {
	for in, want := range map[string]string{
		"0::/system.slice/tlash-mihomo.service\n":                                         "tlash-mihomo.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/tlash-mihomo.service": "tlash-mihomo.service",
		"0::/user.slice/user-1000.slice/session-3.scope":                                  "",
	} {
		if got := unitFromCgroup(in); got != want {
			t.Errorf("unitFromCgroup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindReportsCmdline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	o, ok := Find(ln.Addr().(*net.TCPAddr).Port)
	if !ok || o.PID != os.Getpid() || o.Cmdline == "" {
		t.Fatalf("Find = %+v, %v", o, ok)
	}
}
