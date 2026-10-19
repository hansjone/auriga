//go:build windows

package runtime

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

func defaultDockerHost() string {
	return "npipe:////./pipe/docker_engine"
}

func dialNamedPipe(ctx context.Context, path string) (net.Conn, error) {
	// path from TrimPrefix("npipe://") is like "//./pipe/docker_engine"
	name := path
	name = strings.TrimPrefix(name, "//./pipe/")
	name = strings.TrimPrefix(name, `\\.\pipe\`)
	name = strings.TrimPrefix(name, "/pipe/")
	name = strings.TrimPrefix(name, `\pipe\`)
	return winio.DialPipeContext(ctx, `\\.\pipe\`+name)
}
