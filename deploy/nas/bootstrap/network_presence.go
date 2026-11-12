package main

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

// Select the pre-presence manifest for existing sources only. CheckReceipt still
// verifies its exact hash, so deleting leaves from a new installation is rejected.
func nasCompatibleNetworkMaterials(base string, set materials.MaterialSet) materials.MaterialSet {
	if _, err := os.Lstat(filepath.Join(base, "iam-installation/bootstrap.receipt")); err != nil {
		return set
	}
	leaves := []string{"network/edge-presence.pem", "network/edge-presence.key", "network/edge-presence.password"}
	for _, name := range leaves {
		if _, err := os.Lstat(filepath.Join(base, name)); !os.IsNotExist(err) {
			return set
		}
	}
	set.PublicFiles = slices.DeleteFunc(slices.Clone(set.PublicFiles), func(name string) bool { return slices.Contains(leaves, name) })
	return set
}
