package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Login preference never grants permission to erase a company identity source.
// Existing fixed NAS adapter bytes are preserved exactly, under their original
// protected metadata. Explicit no-adapter first installation cannot shadow UAC
// properties/members either; the operator must approve the real source instead.
func sharedPreserveUACSource(base string, approved []byte, teamOnly bool) error {
	var adapter map[string]json.RawMessage
	if sharedUniqueJSON(approved) != nil || json.Unmarshal(approved, &adapter) != nil || adapter == nil {
		return fmt.Errorf("approved company adapter must be a strict JSON object")
	}
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
	if teamOnly || len(adapter) == 0 {
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
