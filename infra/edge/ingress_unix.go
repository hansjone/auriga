//go:build !windows

package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"
)

func dialLocalIngress(ctx context.Context, u *url.URL) (net.Conn, error) {
	if u.Scheme != "unix" {
		return nil, fmt.Errorf("named pipes require Windows")
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", u.Path)
}

func restrictTLSFile(path string) error { return os.Chmod(path, 0o600) }
