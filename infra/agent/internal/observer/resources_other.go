//go:build !linux && !windows

package observer

// DefaultCollector returns a no-op collector on unsupported OS.
func DefaultCollector() ResourceCollector { return NopResources{} }
