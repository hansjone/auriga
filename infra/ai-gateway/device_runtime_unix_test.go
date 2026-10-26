//go:build !windows

package main

import "testing"

func protectDeviceKey(t *testing.T, _ string) { t.Helper() }
