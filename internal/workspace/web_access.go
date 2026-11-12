package workspace

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Jlan45/Tlash/internal/runtimeconfig"
	"github.com/Jlan45/Tlash/internal/webui"
)

// WebAccess describes how to reach the web dashboard served on the TCP controller.
type WebAccess struct {
	Dashboard  string // metacubexd / zashboard; empty when none is configured
	Downloaded bool
	// Source is "内置 vX" for the embedded copy, "在线下载" otherwise.
	Source     string
	Controller string // the external-controller listen address
	Secret     string
	// URLs open the dashboard directly; empty when the controller is loopback-only.
	URLs []string
	// MoreURLs counts addresses left out of URLs.
	MoreURLs int
	// LocalURL and Tunnel apply when the controller only listens on loopback.
	LocalURL string
	Tunnel   string
	// Problems explain what must be set up first.
	Problems []string
}

// hostAddrs lists this machine's non-loopback IPv4 addresses, most useful first: the default
// route's interface, then physical NICs and br-lan, then the rest. A var for tests.
var hostAddrs = func() []string {
	defIface := defaultRouteIface()
	type addr struct {
		ip   string
		rank int
	}
	var list []addr
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Name == runtimeconfig.TunDevice || virtualIface(ifc.Name) {
			continue
		}
		rank := 2
		_, physErr := os.Stat(filepath.Join("/sys/class/net", ifc.Name, "device"))
		switch {
		case ifc.Name == defIface:
			rank = 0
		case physErr == nil || ifc.Name == "br-lan":
			rank = 1
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				list = append(list, addr{ipn.IP.String(), rank})
			}
		}
	}
	slices.SortStableFunc(list, func(a, b addr) int { return a.rank - b.rank })
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.ip
	}
	return out
}

// defaultRouteIface reads /proc/net/route for the interface holding 0.0.0.0/0.
func defaultRouteIface() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 2 && f[1] == "00000000" {
			return f[0]
		}
	}
	return ""
}

// virtualIface skips container and VM bridges nobody browses from (OpenWrt's br-lan stays).
func virtualIface(name string) bool {
	for _, p := range []string{"docker", "veth", "virbr", "vnet", "cni", "flannel", "kube"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return strings.HasPrefix(name, "br-") && name != "br-lan"
}

// maxWebURLs keeps the printout short on hosts with many interfaces.
const maxWebURLs = 5

func (w *Workspace) WebAccess() WebAccess {
	m := w.Settings().Managed
	acc := WebAccess{Dashboard: m.Dashboard, Controller: m.ExternalController, Secret: m.Secret}
	if acc.Dashboard == "" {
		acc.Problems = append(acc.Problems, "还没有选择 Web 面板：在 设置 → 资源 → Web 面板 按 w 选择 metacubexd 或 zashboard")
	} else {
		dir := w.DashboardDir(acc.Dashboard)
		_, err := os.Stat(filepath.Join(dir, "index.html"))
		acc.Downloaded = err == nil
		acc.Source = "在线下载"
		if v := webui.InstalledVersion(dir); v != "" {
			acc.Source = "内置 " + v
		} else if _, embedded := webui.Embedded(); !acc.Downloaded && embedded && acc.Dashboard == webui.Name {
			acc.Source = "内置，启动内核时释放"
		}
		if !acc.Downloaded {
			acc.Problems = append(acc.Problems, "面板还没下载：启动内核后会自动下载（访问不了 GitHub 时在 设置 → 资源 开启镜像）")
		}
	}
	if acc.Controller == "" {
		acc.Problems = append(acc.Problems, "TCP 控制接口未开启：在 设置 → 接管 → TCP 控制接口 填 127.0.0.1:9090（仅本机，配合 SSH 隧道）或 0.0.0.0:9090（局域网可访问）")
		return acc
	}
	host, port, err := net.SplitHostPort(acc.Controller)
	if err != nil {
		acc.Problems = append(acc.Problems, "TCP 控制接口地址无效："+acc.Controller)
		return acc
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		acc.LocalURL = "http://127.0.0.1:" + port + "/ui"
		acc.Tunnel = fmt.Sprintf("ssh -N -L %s:127.0.0.1:%s %s@%s", port, port, sshUser(), sshServerAddr())
	case "", "0.0.0.0", "::":
		ips := hostAddrs()
		// The address this SSH session came in on is the one most likely reachable.
		if f := strings.Fields(os.Getenv("SSH_CONNECTION")); len(f) >= 3 {
			ips = append([]string{f[2]}, slices.DeleteFunc(ips, func(ip string) bool { return ip == f[2] })...)
		}
		for i, ip := range ips {
			if i == maxWebURLs {
				acc.MoreURLs = len(ips) - maxWebURLs
				break
			}
			acc.URLs = append(acc.URLs, "http://"+net.JoinHostPort(ip, port)+"/ui")
		}
	default:
		acc.URLs = []string{"http://" + net.JoinHostPort(host, port) + "/ui"}
	}
	return acc
}

func sshUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "root"
}

// sshServerAddr is the address the current SSH session reached this machine on, so the
// printed tunnel command works as-is; falls back to the first local address.
func sshServerAddr() string {
	if f := strings.Fields(os.Getenv("SSH_CONNECTION")); len(f) >= 3 {
		return f[2]
	}
	if addrs := hostAddrs(); len(addrs) > 0 {
		return addrs[0]
	}
	return "<服务器地址>"
}
