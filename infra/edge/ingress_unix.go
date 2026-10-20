//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"time"
)

func dialLocalIngress(ctx context.Context, u *url.URL) (net.Conn, error) {
	if u.Scheme != "unix" {
		return nil, fmt.Errorf("named pipes require Windows")
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", u.Path)
}

// Best-effort: bind-mounted TLS files are often owned by another uid or on a
// read-only mount; refuse to fail startup when chmod is not allowed.
func restrictTLSFile(path string) error {
	err := os.Chmod(path, 0o600)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission) {
		return nil
	}
	return err
}
