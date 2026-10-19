//go:build windows

package identity

// On Windows, Go's chmod bits are limited; files were created with 0600 where possible.
func restrictFile(path string) error { return nil }

func restrictDir(path string) error { return nil }
