package config

import "runtime"

// DetectCapacity fills zero capacity fields from the host. CPU defaults to
// all cores; memory and disk are detected where the platform supports it
// (see capacity_linux.go) and otherwise stay zero, which the scheduler treats
// as "no room" - set them explicitly on such hosts.
func (c Config) DetectCapacity() Capacity {
	out := c.Capacity
	if out.CPUMillis == 0 {
		out.CPUMillis = int64(runtime.NumCPU()) * 1000
	}
	if out.MemoryMB == 0 {
		out.MemoryMB = hostMemoryMB()
	}
	if out.DiskGB == 0 {
		out.DiskGB = hostDiskGB(c.DataRoot)
	}
	return out
}
