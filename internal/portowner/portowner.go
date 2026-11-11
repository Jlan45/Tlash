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

// Owner is the process listening on a port.
type Owner struct {
	PID     int
	Comm    string
	Cmdline string // argv joined with spaces
	Unit    string // systemd unit from the cgroup, if any
}

func (o Owner) String() string {
	s := fmt.Sprintf("%s (pid %d)", o.Comm, o.PID)
	if o.Unit != "" {
		s += "，systemd 服务 " + o.Unit
	}
	if o.Cmdline != "" {
		s += "，命令 " + o.Cmdline
	}
	return s
}

// Describe names the listener on port. Empty when it can't tell (another user's process and
// we aren't root).
func Describe(port int) string {
	if o, ok := Find(port); ok {
		return o.String()
	}
	return ""
}

// Find locates the process listening on port via /proc.
func Find(port int) (Owner, bool) {
	inodes := listeningInodes(port)
	if len(inodes) == 0 {
		return Owner{}, false
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
				return ownerOf(dir), true
			}
		}
	}
	return Owner{}, false
}

func ownerOf(dir string) Owner {
	pid, _ := strconv.Atoi(filepath.Base(dir))
	comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
	cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
	cgroup, _ := os.ReadFile(filepath.Join(dir, "cgroup"))
	return Owner{
		PID:     pid,
		Comm:    strings.TrimSpace(string(comm)),
		Cmdline: strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " ")),
		Unit:    unitFromCgroup(string(cgroup)),
	}
}

// unitFromCgroup takes the innermost *.service from e.g.
// "0::/user.slice/user-1000.slice/user@1000.service/app.slice/tlash-mihomo.service".
func unitFromCgroup(cg string) string {
	for _, line := range strings.Split(cg, "\n") {
		parts := strings.Split(line, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.HasSuffix(parts[i], ".service") && !strings.HasPrefix(parts[i], "user@") {
				return parts[i]
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
