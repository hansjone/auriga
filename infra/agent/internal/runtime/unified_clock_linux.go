package runtime

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

func unifiedHostClock() (string, func() int64) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	boot := strings.TrimSpace(string(raw))
	if err != nil || !uUUID.MatchString(boot) {
		boot = ""
	}
	return boot, func() int64 {
		var stamp unix.Timespec
		if unix.ClockGettime(unix.CLOCK_BOOTTIME, &stamp) != nil {
			return -1
		}
		return stamp.Sec*1000 + stamp.Nsec/1000000
	}
}
