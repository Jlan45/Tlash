// Package landevices lists devices seen on the LAN from the ARP table and DHCP leases.
package landevices

import (
	"bufio"
	"os"
	"slices"
	"strings"
)

type Device struct {
	MAC   string
	IP    string
	Name  string
	Iface string
}

// Sources are read in order; later lease files only add names and missing devices.
type Sources struct {
	ARP    string
	Leases []string
}

func HostSources() Sources {
	return Sources{
		ARP:    "/proc/net/arp",
		Leases: []string{"/tmp/dhcp.leases", "/var/lib/misc/dnsmasq.leases", "/var/lib/dnsmasq/dnsmasq.leases"},
	}
}

// List merges ARP entries and DHCP leases by MAC, sorted by IP.
func List(src Sources) []Device {
	byMAC := map[string]*Device{}
	add := func(d Device) {
		d.MAC = strings.ToLower(d.MAC)
		if d.MAC == "" || d.MAC == "00:00:00:00:00:00" {
			return
		}
		if cur, ok := byMAC[d.MAC]; ok {
			if cur.Name == "" {
				cur.Name = d.Name
			}
			if cur.IP == "" {
				cur.IP = d.IP
			}
			if cur.Iface == "" {
				cur.Iface = d.Iface
			}
			return
		}
		byMAC[d.MAC] = &d
	}
	readLines(src.ARP, func(f []string) {
		// IP  HWtype  Flags  HWaddress  Mask  Device; flags 0x0 is an incomplete entry.
		if len(f) >= 6 && f[2] != "0x0" && strings.Count(f[3], ":") == 5 {
			add(Device{IP: f[0], MAC: f[3], Iface: f[5]})
		}
	})
	for _, lf := range src.Leases {
		readLines(lf, func(f []string) {
			// expiry  MAC  IP  hostname  clientid (dnsmasq format)
			if len(f) >= 4 && strings.Count(f[1], ":") == 5 {
				name := f[3]
				if name == "*" {
					name = ""
				}
				add(Device{MAC: f[1], IP: f[2], Name: name})
			}
		})
	}
	out := make([]Device, 0, len(byMAC))
	for _, d := range byMAC {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b Device) int { return strings.Compare(a.IP+a.MAC, b.IP+b.MAC) })
	return out
}

func readLines(path string, fn func([]string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fn(strings.Fields(sc.Text()))
	}
}
