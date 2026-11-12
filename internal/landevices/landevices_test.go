package landevices

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMergesARPAndLeases(t *testing.T) {
	dir := t.TempDir()
	arp := filepath.Join(dir, "arp")
	_ = os.WriteFile(arp, []byte(`IP address       HW type     Flags       HW address            Mask     Device
192.168.1.20     0x1         0x2         AA:BB:CC:00:00:01     *        br-lan
192.168.1.30     0x1         0x0         00:00:00:00:00:00     *        br-lan
192.168.1.40     0x1         0x2         aa:bb:cc:00:00:02     *        br-lan
`), 0o644)
	leases := filepath.Join(dir, "dhcp.leases")
	_ = os.WriteFile(leases, []byte(`1762000000 aa:bb:cc:00:00:01 192.168.1.20 iPhone 01:aa
1762000000 aa:bb:cc:00:00:03 192.168.1.50 * 01:bb
`), 0o644)
	got := List(Sources{ARP: arp, Leases: []string{leases, filepath.Join(dir, "missing")}})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].MAC != "aa:bb:cc:00:00:01" || got[0].Name != "iPhone" || got[0].Iface != "br-lan" {
		t.Errorf("merged device = %+v", got[0])
	}
	if got[2].IP != "192.168.1.50" || got[2].Name != "" {
		t.Errorf("lease-only device = %+v", got[2])
	}
}
