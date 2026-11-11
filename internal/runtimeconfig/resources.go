package runtimeconfig

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

const geoRelease = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/"

// DashboardURLs are the zip archives mihomo's /upgrade/ui downloads.
var DashboardURLs = map[string]string{
	"metacubexd": "https://github.com/MetaCubeX/metacubexd/archive/refs/heads/gh-pages.zip",
	"zashboard":  "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip",
}

func withMirror(mirror, u string) string {
	if mirror == "" {
		return u
	}
	return strings.TrimRight(mirror, "/") + "/" + u
}

func applyResources(root *yaml.Node, m appsettings.Managed) error {
	if m.ResourceMirror != "" {
		var n yaml.Node
		if err := n.Encode(map[string]string{
			"geoip":   withMirror(m.ResourceMirror, geoRelease+"geoip.dat"),
			"geosite": withMirror(m.ResourceMirror, geoRelease+"geosite.dat"),
			"mmdb":    withMirror(m.ResourceMirror, geoRelease+"country.mmdb"),
			"asn":     withMirror(m.ResourceMirror, geoRelease+"GeoLite2-ASN.mmdb"),
		}); err != nil {
			return err
		}
		setKey(root, "geox-url", &n)
	}
	if u, ok := DashboardURLs[m.Dashboard]; ok {
		setKey(root, "external-ui", strNode("ui"))
		setKey(root, "external-ui-name", strNode(m.Dashboard))
		setKey(root, "external-ui-url", strNode(withMirror(m.ResourceMirror, u)))
	}
	return nil
}
