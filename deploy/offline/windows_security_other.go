//go:build !windows

package main

func protectTransferRoot(_ string) error { return nil }
