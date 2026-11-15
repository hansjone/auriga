//go:build !windows

package runtime

func poolReplace(source, target string) error { return unifiedReplace(source, target) }
