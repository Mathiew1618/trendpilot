//go:build unix

package usage

import "syscall"

var lowered bool

// lowerPriority sets nice 10 so the OS always schedules OBS, games and
// encoders ahead of TrendPilot.
func lowerPriority() {
	lowered = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10) == nil
}
