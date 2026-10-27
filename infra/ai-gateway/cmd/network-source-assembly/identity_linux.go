package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

func creationToken(pid int) (string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	tail := strings.LastIndexByte(string(raw), ')')
	if tail < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(raw[tail+1:]))
	if len(fields) < 20 {
		return "", errors.New("missing process start ticks")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	// Linux's monotonic process-creation ticks plus boot identity: never a guessed
	// wall time or a PID alone, and no dependency on a hard-coded clock frequency.
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
