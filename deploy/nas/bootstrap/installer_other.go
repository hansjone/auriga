//go:build !linux

package main

import "fmt"

func runAsInstaller(_ string, _ []byte, _, _ string, _ int, _, _ string, _ int64, _ runtimeInputs, validateOnly bool) error {
	if validateOnly {
		return nil
	}
	return fmt.Errorf("NAS material activation requires Linux; validation alone is supported here")
}
