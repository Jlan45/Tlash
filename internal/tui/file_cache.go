package tui

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// fileShowLimit caps how much of a file a tab renders; subscriptions can be megabytes.
const fileShowLimit = 3000

// fileCache re-reads a file only when its size or mtime changes, since tabs render every frame.
type fileCache struct {
	path    string
	mod     time.Time
	size    int64
	content string
	err     error
}

func (c *fileCache) read(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		c.path = ""
		return "", err
	}
	if path == c.path && info.ModTime().Equal(c.mod) && info.Size() == c.size {
		return c.content, c.err
	}
	b, err := os.ReadFile(path)
	c.path, c.mod, c.size, c.err = path, info.ModTime(), info.Size(), err
	c.content = string(b)
	if lines := strings.SplitN(c.content, "\n", fileShowLimit+1); len(lines) > fileShowLimit {
		c.content = strings.Join(lines[:fileShowLimit], "\n") +
			styleDim.Render(fmt.Sprintf("\n… 仅显示前 %d 行，完整文件：%s", fileShowLimit, path))
	}
	return c.content, c.err
}
