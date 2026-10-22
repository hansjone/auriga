//go:build !linux

package main

import "fmt"

func runAsInstaller(string, []byte, string, string, int, string, string) error {
	return fmt.Errorf("NAS material activation requires Linux; validation alone is supported here")
}
