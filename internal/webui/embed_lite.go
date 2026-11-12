//go:build lite

package webui

import "io/fs"

// Lite builds (for small router flash) carry no dashboard; mihomo downloads it online.
var assets fs.FS = emptyFS{}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
