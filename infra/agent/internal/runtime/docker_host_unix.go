//go:build unix

package runtime

import (
	"context"
	"fmt"
	"net"
)

func defaultDockerHost() string {
	return "unix:///var/run/docker.sock"
}

func dialNamedPipe(_ context.Context, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("docker: named pipes are not supported on unix")
}
