package ingress

import (
	"fmt"
	"os"
	"strings"
)

// RemoveBeneath uses handle-relative removal; never delete the instance root.
func RemoveBeneath(root, rel string) error {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return err
	}
	if clean == "." {
		return fmt.Errorf("%s: cannot delete root", CodePathInvalid)
	}
	if _, err := ResolveUnderRoot(root, clean); err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	// RemoveAll itself is traversal-resistant, including concurrent symlink swaps.
	return r.RemoveAll(clean)
}

func checkedPathError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "path escapes from parent") {
		return fmt.Errorf("%s: %w", CodePathEscape, err)
	}
	return err
}
