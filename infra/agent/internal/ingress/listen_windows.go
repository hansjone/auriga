//go:build windows

package ingress

import (
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// ListenLocal opens a Windows named pipe. path may be `\\.\pipe\name` or just `name`.
// gid is unused on Windows; the pipe ACL grants Administrators, SYSTEM and the owner.
func ListenLocal(path string, _ int) (net.Listener, error) {
	pipe := path
	if !strings.HasPrefix(strings.ToLower(path), `\\.\pipe\`) {
		pipe = `\\.\pipe\` + strings.TrimPrefix(path, `\`)
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;BA)(A;;GA;;;SY)(A;;GA;;;OW)",
		MessageMode:        false,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	})
	if err != nil {
		return nil, fmt.Errorf("listen pipe %s: %w", pipe, err)
	}
	return ln, nil
}
