package runtimeconfig

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// TunDevice is the TUN interface name Tlash asks mihomo for, so firewall rules can name it.
const TunDevice = "tlash"

// cnRuleSet is the rule provider behind route-exclude-address-set for mainland-China IPs.
const cnRuleSet = "tlash-cn-ip"

const cnRuleSetURL = "https://github.com/MetaCubeX/meta-rules-dat/raw/meta/geo/geoip/cn.mrs"

// applyGateway turns on mihomo's TUN with auto-route/auto-redirect, which is what makes this
// machine a transparent gateway; DNS from LAN clients is served on g.DNSListen.
func applyGateway(root *yaml.Node, g appsettings.Gateway, mirror string) error {
	g = g.WithDefaults()
	tun := map[string]any{
		"enable":                true,
		"device":                TunDevice,
		"stack":                 g.Stack,
		"auto-route":            true,
		"auto-redirect":         true,
		"auto-detect-interface": true,
		"dns-hijack":            []string{"any:53", "tcp://any:53"},
	}
	if len(g.ExcludeAddrs) > 0 {
		tun["route-exclude-address"] = cidrs(g.ExcludeAddrs)
	}
	if macs := deviceMACs(g.Devices); len(macs) > 0 {
		if g.DeviceMode == appsettings.DeviceModeSelected {
			tun["include-mac-address"] = macs
		} else {
			tun["exclude-mac-address"] = macs
		}
	}
	if g.BypassCN {
		tun["route-exclude-address-set"] = []string{cnRuleSet}
		if err := addRuleProvider(root, cnRuleSet, map[string]any{
			"type":     "http",
			"behavior": "ipcidr",
			"format":   "mrs",
			"url":      withMirror(mirror, cnRuleSetURL),
			"path":     "./ruleset/" + cnRuleSet + ".mrs",
			"interval": 86400,
		}); err != nil {
			return err
		}
	}
	var tunNode yaml.Node
	if err := tunNode.Encode(tun); err != nil {
		return err
	}
	setKey(root, "tun", &tunNode)
	setKey(root, "ipv6", boolNode(g.IPv6))

	// dns-hijack hands packets to mihomo's DNS module, so it must be on; LAN clients that use
	// this machine as their DNS server reach it on DNSListen.
	dns := lookup(root, "dns")
	if dns == nil || dns.Kind != yaml.MappingNode {
		dns = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setKey(root, "dns", dns)
	}
	setKey(dns, "enable", boolNode(true))
	setKey(dns, "listen", strNode(g.DNSListen))
	if lookup(dns, "enhanced-mode") == nil {
		setKey(dns, "enhanced-mode", strNode("fake-ip"))
	}
	if n := lookup(dns, "nameserver"); n == nil || len(n.Content) == 0 {
		setKey(dns, "nameserver", listNode(appsettings.DefaultDNS().Nameserver))
	}
	setKey(dns, "ipv6", boolNode(g.IPv6))
	return nil
}

func addRuleProvider(root *yaml.Node, name string, spec map[string]any) error {
	providers := lookup(root, "rule-providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		providers = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setKey(root, "rule-providers", providers)
	}
	var n yaml.Node
	if err := n.Encode(spec); err != nil {
		return err
	}
	setKey(providers, name, &n)
	return nil
}

// cidrs turns bare addresses into host routes (1.2.3.4 → 1.2.3.4/32).
func cidrs(addrs []string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		switch {
		case strings.Contains(a, "/"):
			out = append(out, a)
		case strings.Contains(a, ":"):
			out = append(out, a+"/128")
		default:
			out = append(out, a+"/32")
		}
	}
	return out
}

func deviceMACs(devs []appsettings.Device) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		if d.MAC != "" {
			out = append(out, strings.ToLower(d.MAC))
		}
	}
	return out
}
