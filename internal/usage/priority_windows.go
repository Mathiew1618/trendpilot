//go:build windows

package usage

import "golang.org/x/sys/windows"

var lowered bool

// lowerPriority sets BELOW_NORMAL so Windows always schedules OBS, games and
// encoders ahead of TrendPilot.
func lowerPriority() {
	lowered = windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS) == nil
}
