//go:build !lite

package webui

import "embed"

// Standard builds embed whatever scripts/fetch-webui.sh put in assets (only the README when
// it hasn't run, in which case the dashboard is downloaded online instead).
//
//go:embed assets
var assets embed.FS

// Lite reports whether this is a lite build (selects the matching asset when self-updating).
const Lite = false
