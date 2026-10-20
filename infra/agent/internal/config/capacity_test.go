package config

import (
	"runtime"
	"testing"
)

func TestDetectCapacityKeepsExplicitValuesAndDefaultsCPU(t *testing.T) {
	c := Config{Capacity: Capacity{MemoryMB: 2048, DiskGB: 50, MaxInstances: 2}}
	got := c.DetectCapacity()
	if got.CPUMillis != int64(runtime.NumCPU())*1000 {
		t.Fatalf("cpu = %d", got.CPUMillis)
	}
	if got.MemoryMB != 2048 || got.DiskGB != 50 || got.MaxInstances != 2 {
		t.Fatalf("explicit values overwritten: %+v", got)
	}
}

func TestCapacityFromEnv(t *testing.T) {
	t.Setenv("AURIGA_AGENT_CAPACITY_CPU_MILLIS", "1500")
	t.Setenv("AURIGA_AGENT_CAPACITY_MEMORY_MB", "4096")
	t.Setenv("AURIGA_AGENT_CAPACITY_DISK_GB", "20")
	t.Setenv("AURIGA_AGENT_CAPACITY_MAX_INSTANCES", "5")
	c := Default()
	applyEnv(&c)
	if c.Capacity != (Capacity{CPUMillis: 1500, MemoryMB: 4096, DiskGB: 20, MaxInstances: 5}) {
		t.Fatalf("capacity = %+v", c.Capacity)
	}
}
