//go:build windows

package main

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
)

func localTestListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	id, err := randomID(12)
	if err != nil {
		t.Fatal(err)
	}
	name := "auriga-test-" + id
	ln, err := winio.ListenPipe(`\\.\pipe\`+name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;BA)(A;;GA;;;SY)(A;;GA;;;OW)"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, "npipe:////./pipe/" + name
}
