//go:build linux

package main

import (
	"os"
	"path/filepath"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

// This exact historical byte manifest is verification-only. No old generator,
// credentials or services are restored. The original receipt remains immutable.
func legacyNASMaterialSet(https bool) materials.MaterialSet {
	set := nasMaterialSet(https)
	var files []string
	for _, name := range set.PublicFiles {
		switch name {
		case "bifrost-runtime.json":
			files = append(files, "litellm-runtime.json", "litellm-public.env")
		case "bifrost/admin_password":
			files = append(files, "litellm/master", "litellm/salt", "litellm/team", "litellm/pg_password")
		case "bifrost/member_password", "bifrost/encryption_key", "bifrost/pg_password", "bifrost/pg_migration_password":
		default:
			files = append(files, name)
		}
	}
	set.PublicFiles = files
	for i, name := range set.ManagedDirectories {
		if name == "bifrost" {
			set.ManagedDirectories[i] = "litellm"
		}
	}
	return set
}

func nasBaseMaterialSet(base string, https bool) materials.MaterialSet {
	if _, err := os.Lstat(filepath.Join(base, "litellm-runtime.json")); err == nil {
		return nasCompatibleNetworkMaterials(base, legacyNASMaterialSet(https))
	}
	return nasCompatibleNetworkMaterials(base, nasMaterialSet(https))
}
