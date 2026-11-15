//go:build !linux

package projection

import "os"

// Activation on other platforms is still rejected by the peer and metadata
// gates. Keep the filesystem operation honest rather than pretending to sync.
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
