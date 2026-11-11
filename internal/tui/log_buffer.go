package tui

import "strings"

// logBuffer keeps the most recent log lines, dropping the oldest past its capacity.
type logBuffer struct {
	entries []logMsg
	limit   int
	// seq increases on every change so views can cache their wrapped layout.
	seq int
}

func newLogBuffer(limit int) *logBuffer { return &logBuffer{limit: limit} }

func (b *logBuffer) add(e logMsg) {
	b.entries = append(b.entries, e)
	if over := len(b.entries) - b.limit; over > 0 {
		// Trim in chunks so we don't reslice on every append once full.
		drop := max(over, b.limit/10)
		b.entries = append(b.entries[:0:0], b.entries[drop:]...)
	}
	b.seq++
}

func (b *logBuffer) clear() {
	b.entries = nil
	b.seq++
}

// matching returns entries whose type or payload contains filter (case-insensitive).
func (b *logBuffer) matching(filter string) []logMsg {
	if filter == "" {
		return b.entries
	}
	filter = strings.ToLower(filter)
	out := make([]logMsg, 0, len(b.entries)/4)
	for _, e := range b.entries {
		if strings.Contains(strings.ToLower(e.entry.Payload), filter) || strings.Contains(e.entry.Type, filter) {
			out = append(out, e)
		}
	}
	return out
}
