//go:build !linux

package main

import "fmt"

func nasCollabCommand(_ []string) error { return fmt.Errorf("NAS collab activation requires Linux") }
