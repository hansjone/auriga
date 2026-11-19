//go:build !linux

package sharednode

import (
	"context"
	"errors"
)

func (r *Relay) Start(context.Context, string, int) (func(), error) {
	return nil, errors.New("shared Host socket requires Linux peer credentials")
}
