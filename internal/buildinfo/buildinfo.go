// Package buildinfo holds version metadata injected at build time via -ldflags -X.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Setting reads a build setting recorded in the binary (GOARM, GOMIPS, ...), e.g. "7".
func Setting(key, fallback string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range info.Settings {
			if kv.Key == key && kv.Value != "" {
				// GOARM may carry a float ABI suffix such as "7,softfloat".
				v, _, _ := strings.Cut(kv.Value, ",")
				return v
			}
		}
	}
	return fallback
}
