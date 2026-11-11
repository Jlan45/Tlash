package tui

import (
	"fmt"
	"strings"
	"time"
)

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n) / unit
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func formatSpeed(bytesPerSec int64) string { return formatBytes(bytesPerSec) + "/s" }

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// sparkline renders the last width samples scaled to the largest one.
func sparkline(samples []int64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(samples) > width {
		samples = samples[len(samples)-width:]
	}
	var peak int64
	for _, s := range samples {
		peak = max(peak, s)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(samples)))
	for _, s := range samples {
		if peak == 0 || s == 0 {
			b.WriteRune(' ')
			continue
		}
		idx := int(float64(s) / float64(peak) * float64(len(sparkLevels)-1))
		b.WriteRune(sparkLevels[idx])
	}
	return b.String()
}
