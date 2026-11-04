//go:build !linux

package main

import (
	"fmt"
	"io"
)

func nasRotationCommand(_ []string, _ io.Writer) error {
	return fmt.Errorf("NAS key rotation requires Linux installer root")
}
