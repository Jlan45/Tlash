package tui

import (
	"strings"
)

var eighths = []rune(" ▁▂▃▄▅▆▇█")

// trafficChart draws down and up speed as bars h rows tall and w cells wide (axis included).
// Upload, usually smaller, is drawn over download in its own color.
func trafficChart(down, up []int64, w, h int) []string {
	const axisW = 9 // "4.0 MB/s ┤"
	cols := max(w-axisW-1, 1)
	tail := func(xs []int64) []int64 {
		if len(xs) > cols {
			return xs[len(xs)-cols:]
		}
		return xs
	}
	down, up = tail(down), tail(up)
	var peak int64 = 1
	for _, xs := range [][]int64{down, up} {
		for _, v := range xs {
			peak = max(peak, v)
		}
	}
	// Height of each column in eighths of a row, right-aligned to the newest sample.
	heights := func(xs []int64) []int {
		out := make([]int, cols)
		off := cols - len(xs)
		for i, v := range xs {
			if v > 0 {
				out[off+i] = max(int(float64(v)/float64(peak)*float64(h*8)+0.5), 1)
			}
		}
		return out
	}
	dh, uh := heights(down), heights(up)

	lines := make([]string, h)
	for row := 0; row < h; row++ {
		level := h - 1 - row // 0 is the bottom row
		label := ""
		switch row {
		case 0:
			label = formatSpeed(peak)
		case h / 2:
			if h >= 4 {
				label = formatSpeed(peak / 2)
			}
		case h - 1:
			label = "0"
		}
		var b strings.Builder
		b.WriteString(styleDim.Render(padLeft(label, axisW-1) + " │"))
		for c := 0; c < cols; c++ {
			cell := func(eh int) int { return max(0, min(eh-level*8, 8)) }
			switch u, d := cell(uh[c]), cell(dh[c]); {
			case u > 0:
				b.WriteString(styleAccent.Render(string(eighths[u])))
			case d > 0:
				b.WriteString(styleOK.Render(string(eighths[d])))
			default:
				b.WriteByte(' ')
			}
		}
		lines[row] = b.String()
	}
	return lines
}
