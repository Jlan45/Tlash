// Package buildinfo holds version metadata injected at build time via -ldflags -X.
package buildinfo

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
