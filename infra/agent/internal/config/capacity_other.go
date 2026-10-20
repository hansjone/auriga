//go:build !linux

package config

func hostMemoryMB() int64 { return 0 }

func hostDiskGB(string) int64 { return 0 }
