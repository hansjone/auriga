//go:build !linux

package main

import (
	"fmt"
	"os"
)

func rotationSupported() bool { return false }
func rotationDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && activationMetadata(info, true)
}
func rotationRuntimeRead(_ string, _ int) ([]byte, error) {
	return nil, fmt.Errorf("rotation runtime requires Linux")
}
func rotationOwnRuntime(_, _ string) error          { return fmt.Errorf("rotation runtime requires Linux") }
func rotationOwnState(_ string) error               { return fmt.Errorf("rotation runtime requires Linux") }
func rotationRuntimeDirectory(_ string, _ int) bool { return false }
func rotationStateLock(_ string) error              { return fmt.Errorf("rotation runtime requires Linux") }
func rotationInputParents(_ string) error           { return nil }
