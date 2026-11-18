package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func composeAccessProject(access deploymentconfig.Config) (string, error) {
	edge := access.InstanceControl().EdgeContainer
	if err := validateComposeAccess(access, edge); err != nil {
		return "", fmt.Errorf("sealed Compose project refused")
	}
	return strings.TrimSuffix(edge, "-edge"), nil
}

// Inspect only the original installation seal, so a bounded stop remains
// available while the separate activation is pending or locked. Never derive
// the project from compose.env, shell variables or an incomplete activation.
func sealedComposeBinding(out string) (string, bool, error) {
	receipt, err := collabRead(filepath.Join(out, "iam-installation", "bootstrap.receipt"), 65536, true)
	if err != nil {
		return "", false, fmt.Errorf("sealed Compose project receipt unavailable")
	}
	lines := strings.Split(string(receipt), "\n")
	if len(lines) != 3 || len(lines[0]) != 64 {
		return "", false, fmt.Errorf("sealed Compose project receipt invalid")
	}
	if _, err := hex.DecodeString(lines[0]); err != nil {
		return "", false, fmt.Errorf("sealed Compose project receipt invalid")
	}
	path := filepath.Join(out, "access.json")
	raw, err := collabRead(path, 1<<20, false)
	if err != nil {
		return "", false, fmt.Errorf("sealed Compose access unavailable")
	}
	managedCA, err := hasManagedCA(out)
	if err != nil {
		return "", false, err
	}
	if _, _, reuse, err := existingIdentityMaterials(out, lines[0]); err != nil || !reuse {
		return "", false, fmt.Errorf("sealed Compose project seal invalid")
	}
	after, err := collabRead(path, 1<<20, false)
	if err != nil || !bytes.Equal(raw, after) {
		return "", false, fmt.Errorf("sealed Compose access changed")
	}
	if present, err := hasManagedCA(out); err != nil || present != managedCA {
		return "", false, fmt.Errorf("sealed Compose managed CA selection changed")
	}
	access, err := deploymentconfig.Load(raw)
	if err != nil {
		return "", false, fmt.Errorf("sealed Compose access invalid")
	}
	project, err := composeAccessProject(access)
	return project, managedCA, err
}
