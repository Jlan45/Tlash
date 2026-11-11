package runtimeconfig

import (
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// applyDNS sets the managed DNS keys inside the profile's dns block, keeping keys Tlash
// doesn't manage (nameserver-policy, fallback-filter, ...).
func applyDNS(root *yaml.Node, d *appsettings.DNS) error {
	dns := lookup(root, "dns")
	if dns == nil || dns.Kind != yaml.MappingNode {
		dns = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setKey(root, "dns", dns)
	}
	setKey(dns, "enable", boolNode(d.Enable))
	setOrDelete(dns, "listen", d.Listen)
	setKey(dns, "enhanced-mode", strNode(d.EnhancedMode))
	setOrDelete(dns, "fake-ip-range", d.FakeIPRange)
	for key, list := range map[string][]string{
		"fake-ip-filter":          d.FakeIPFilter,
		"default-nameserver":      d.DefaultNameserver,
		"nameserver":              d.Nameserver,
		"fallback":                d.Fallback,
		"proxy-server-nameserver": d.ProxyServerNameserver,
	} {
		if len(list) == 0 {
			deleteKey(dns, key)
			continue
		}
		setKey(dns, key, listNode(list))
	}
	return nil
}

func snifferNode(s *appsettings.Sniffer) (*yaml.Node, error) {
	sniff := map[string]any{}
	add := func(proto string, ports []string, override bool) {
		if len(ports) == 0 {
			return
		}
		entry := map[string]any{"ports": portValues(ports)}
		if override {
			entry["override-destination"] = true
		}
		sniff[proto] = entry
	}
	add("HTTP", s.HTTPPorts, true)
	add("TLS", s.TLSPorts, false)
	add("QUIC", s.QUICPorts, false)
	v := map[string]any{
		"enable":               s.Enable,
		"force-dns-mapping":    true,
		"parse-pure-ip":        true,
		"override-destination": s.OverrideDestination,
		"sniff":                sniff,
	}
	if len(s.ForceDomain) > 0 {
		v["force-domain"] = s.ForceDomain
	}
	if len(s.SkipDomain) > 0 {
		v["skip-domain"] = s.SkipDomain
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}

// portValues keeps single ports numeric and ranges ("8080-8880") as strings.
func portValues(ports []string) []any {
	out := make([]any, len(ports))
	for i, p := range ports {
		if n, err := strconv.Atoi(p); err == nil {
			out[i] = n
		} else {
			out[i] = p
		}
	}
	return out
}

func setOrDelete(m *yaml.Node, key, v string) {
	if v == "" {
		deleteKey(m, key)
		return
	}
	setKey(m, key, strNode(v))
}

func listNode(items []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, it := range items {
		n.Content = append(n.Content, strNode(it))
	}
	return n
}
