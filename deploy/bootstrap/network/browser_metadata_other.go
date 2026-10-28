//go:build !linux

package network

// Unix source ownership is verified during Linux activation, not Windows validation.
func ValidateBrowserSource(_ string, _ bool) error { return nil }
