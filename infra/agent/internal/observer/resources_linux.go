//go:build linux

package observer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CgroupCollector reads cgroup v2 (and falls back to coarse /proc) stats.
type CgroupCollector struct {
	// ProcRoot defaults to /proc; override in tests.
	ProcRoot string
}

func (c CgroupCollector) proc() string {
	if c.ProcRoot == "" {
		return "/proc"
	}
	return c.ProcRoot
}

func (c CgroupCollector) CollectNode() (NodeResources, error) {
	var out NodeResources
	// Memory from /proc/meminfo
	f, err := os.Open(filepath.Join(c.proc(), "meminfo"))
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	var totalKB, availKB int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			_, _ = fmt.Sscanf(line, "MemTotal: %d", &totalKB)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			_, _ = fmt.Sscanf(line, "MemAvailable: %d", &availKB)
		}
	}
	if totalKB > 0 {
		out.MemoryMBUsed = (totalKB - availKB) / 1024
	}
	return out, nil
}

func (c CgroupCollector) CollectInstance(t InstanceTarget) (InstanceResources, error) {
	var out InstanceResources
	path := t.CgroupPath
	if path == "" {
		return out, nil
	}
	if b, err := os.ReadFile(filepath.Join(path, "memory.current")); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			out.MemoryMB = n / (1024 * 1024)
		}
	}
	if b, err := os.ReadFile(filepath.Join(path, "cpu.stat")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "usage_usec ") {
				var usec int64
				_, _ = fmt.Sscanf(line, "usage_usec %d", &usec)
				out.CPUMillis = usec / 1000
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(path, "pids.current")); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			out.PIDs = n
		}
	}
	return out, nil
}

// DefaultCollector returns the platform collector.
func DefaultCollector() ResourceCollector { return CgroupCollector{} }
