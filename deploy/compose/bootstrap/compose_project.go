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
func sealedComposeProject(out string) (string, error) {
	receipt, err := collabRead(filepath.Join(out, "iam-installation", "bootstrap.receipt"), 65536, true)
	if err != nil {
		return "", fmt.Errorf("sealed Compose project receipt unavailable")
	}
	lines := strings.Split(string(receipt), "\n")
	if len(lines) != 3 || len(lines[0]) != 64 {
		return "", fmt.Errorf("sealed Compose project receipt invalid")
	}
	if _, err := hex.DecodeString(lines[0]); err != nil {
		return "", fmt.Errorf("sealed Compose project receipt invalid")
	}
	path := filepath.Join(out, "access.json")
	raw, err := collabRead(path, 1<<20, false)
	if err != nil {
		return "", fmt.Errorf("sealed Compose access unavailable")
	}
	if _, _, reuse, err := existingIdentityMaterials(out, lines[0]); err != nil || !reuse {
		return "", fmt.Errorf("sealed Compose project seal invalid")
	}
	after, err := collabRead(path, 1<<20, false)
	if err != nil || !bytes.Equal(raw, after) {
		return "", fmt.Errorf("sealed Compose access changed")
	}
	access, err := deploymentconfig.Load(raw)
	if err != nil {
		return "", fmt.Errorf("sealed Compose access invalid")
	}
	return composeAccessProject(access)
}
