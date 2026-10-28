//go:build !unix

package solve

import "time"

// cpuTime is unavailable here; the scale table then shows no CPU time.
func cpuTime() time.Duration { return 0 }
