package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Login preference never grants permission to erase a company identity source.
// Existing fixed NAS adapter bytes are preserved exactly, under their original
// protected metadata. Explicit no-adapter first installation cannot shadow UAC
// properties/members either; the operator must approve the real source instead.
func sharedPreserveUACSource(base string, approved []byte, teamOnly bool) error {
	if _, err := os.Lstat(filepath.Join(base, "uac/adapter.json")); err == nil {
		current, err := sharedBaseLeaf(base, "uac/adapter.json")
		if err != nil {
			return err
		}
		defer clear(current)
		if teamOnly || !bytes.Equal(current, approved) {
			return fmt.Errorf("shared installation must preserve the existing approved company adapter")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if teamOnly || bytes.Equal(approved, []byte(sharedTeamOnlyAdapter)) {
		for _, leaf := range []string{"uac/uac.properties", "uac/members.json"} {
			if _, err := os.Lstat(filepath.Join(base, leaf)); err == nil {
				return fmt.Errorf("no-adapter first installation cannot override an existing UAC identity source")
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
