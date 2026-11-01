package main

import (
	"os"
	"strings"
	"testing"
)

func TestMaterialsUpgradePublicWiring(t *testing.T) {
	for file, needles := range map[string][]string{
		"../scripts/materials-upgrade.sh": {"materials-upgrade", "stage", "commit", "rollback"},
		"../scripts/lib.sh":               {"materials-upgrades", "materials-upgrade", "bifrost.yml"},
		"../scripts/installer-entry.sh":   {"materials-stage", "materials-commit", "materials-rollback"},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Error(err)
			continue
		}
		for _, s := range needles {
			if !strings.Contains(string(b), s) {
				t.Errorf("%s missing %s", file, s)
			}
		}
	}
}
