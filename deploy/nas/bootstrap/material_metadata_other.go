//go:build !linux

package main

import "fmt"

type nasMaterialGuard struct{}

func openNASMaterialGuard(string) (*nasMaterialGuard, error) {
	return nil, fmt.Errorf("NAS material activation requires Linux metadata enforcement")
}

func (*nasMaterialGuard) Close() {}
func (*nasMaterialGuard) Validate(bool) error {
	return fmt.Errorf("NAS material activation requires Linux metadata enforcement")
}
