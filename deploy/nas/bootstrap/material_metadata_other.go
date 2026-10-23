//go:build !linux

package main

import (
	"fmt"
	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

type nasMaterialGuard struct{}

func openNASMaterialGuard(string, ...materials.MaterialSet) (*nasMaterialGuard, error) {
	return nil, fmt.Errorf("NAS material activation requires Linux metadata enforcement")
}

func (*nasMaterialGuard) Close() {}
func (*nasMaterialGuard) pinRuntimeProof(string) error {
	return fmt.Errorf("NAS material activation requires Linux metadata enforcement")
}
func (*nasMaterialGuard) Validate(bool) error {
	return fmt.Errorf("NAS material activation requires Linux metadata enforcement")
}
