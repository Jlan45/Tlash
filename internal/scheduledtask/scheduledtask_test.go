package scheduledtask

import (
	"strings"
	"testing"
)

func TestUnitsAndCronLine(t *testing.T) {
	job := Job{Name: "tlash-tasks", TlashBin: "/usr/local/bin/tlash", DataDir: "/etc/my tlash"}
	st := &systemdTimer{job: job}
	service, timer := st.units()
	if !strings.Contains(service, "ExecStart=/usr/local/bin/tlash --dir '/etc/my tlash' task run-due") {
		t.Errorf("service:\n%s", service)
	}
	if !strings.Contains(timer, "OnUnitActiveSec=15min") || !strings.Contains(timer, "WantedBy=timers.target") {
		t.Errorf("timer:\n%s", timer)
	}
	c := &cron{job: job}
	line := c.line()
	if !strings.HasPrefix(line, "*/15 * * * * /usr/local/bin/tlash --dir '/etc/my tlash' task run-due") || !strings.HasSuffix(line, c.marker()) {
		t.Errorf("cron line = %q", line)
	}
	// Only our own line (for this data dir) is removed.
	kept := c.without([]string{"0 * * * * other", line, "*/15 * * * * x # tlash-tasks:/other/dir"})
	if len(kept) != 2 {
		t.Errorf("without = %q", kept)
	}
}
