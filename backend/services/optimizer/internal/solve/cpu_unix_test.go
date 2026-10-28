//go:build unix

package solve

import (
	"syscall"
	"time"
)

// cpuTime is the CPU time this test process has used so far. Wall time says little on a busy machine
// (several searches run side by side, CI shares cores), so the scale table shows both.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
