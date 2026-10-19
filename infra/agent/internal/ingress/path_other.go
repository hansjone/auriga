//go:build !linux

package ingress

import (
	"fmt"
	"os"
)

// OpenBeneath resolves under root then opens, rejecting symlink escapes.
func OpenBeneath(root, rel string, flags int, mode os.FileMode) (*os.File, error) {
	path, err := ResolveUnderRoot(root, rel)
	if err != nil {
		return nil, err
	}
	// Reject if any path component is a symlink pointing outside (ResolveUnderRoot
	// already checked). Open with O_NOFOLLOW where supported via os.OpenFile flags.
	f, err := os.OpenFile(path, flags, mode)
	if err != nil {
		return nil, fmt.Errorf("%s: open: %w", CodePathInvalid, err)
	}
	return f, nil
}
