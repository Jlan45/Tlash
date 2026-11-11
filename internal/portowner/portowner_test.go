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
