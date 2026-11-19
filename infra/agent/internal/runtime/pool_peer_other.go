//go:build !linux

package runtime

import (
	"context"
	"fmt"
)

func (d *Docker) VerifyPoolPeer(context.Context, string, int, string) (string, error) {
	return "", fmt.Errorf("shared pool peer proof requires Linux")
}
