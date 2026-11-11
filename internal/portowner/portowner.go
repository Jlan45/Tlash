// Package portowner finds which local process is listening on a TCP port (Linux /proc).
package portowner

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Busy reports whether port can't be bound on loopback, i.e. someone already listens there.
func Busy(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

// Describe names the listener on port, e.g. "clash (pid 1234)". Empty when it can't tell
// (another user's process and we aren't root).
func Describe(port int) string {
	inodes := listeningInodes(port)
	if len(inodes) == 0 {
		return ""
	}
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, dir := range procs {
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
				return fmt.Sprintf("%s (pid %s)", strings.TrimSpace(string(comm)), filepath.Base(dir))
			}
		}
	}
	return ""
}

// listeningInodes reads /proc/net/tcp{,6} for LISTEN sockets on port.
func listeningInodes(port int) map[string]bool {
	out := map[string]bool{}
	want := fmt.Sprintf(":%04X", port)
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			// local_address is fields[1] ("0100007F:1ED2"), state fields[3] (0A = LISTEN), inode fields[9].
			if len(fields) > 9 && strings.HasSuffix(fields[1], want) && fields[3] == "0A" {
				out[fields[9]] = true
			}
		}
		file.Close()
	}
	return out
}
