//go:build !linux

package projection

import (
	"context"
	"fmt"
)

func (r *Reader) Listen(context.Context) error {
	return fmt.Errorf("local projection socket requires Linux SO_PEERCRED")
}
