//go:build !linux

package projection

import "os"

// The projection listener itself rejects non-Linux platforms. Portable parsing
// tests do not claim SO_PEERCRED or POSIX mode enforcement on Windows.
func privateInfo(os.FileInfo, bool) error { return nil }
