//go:build !windows

package main

import (
	"net"
	"path/filepath"
	"testing"
)

func localTestListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ingress.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, "unix://" + path
}
