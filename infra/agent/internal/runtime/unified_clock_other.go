//go:build !linux && !windows

package runtime

func unifiedHostClock() (string, func() int64) { return "", func() int64 { return -1 } }
