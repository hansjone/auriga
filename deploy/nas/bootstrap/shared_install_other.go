//go:build !linux

package main

import (
	"fmt"
	"io"
)

func nasSharedRuntime(_ []string, _ io.Writer) error {
	return fmt.Errorf("NAS shared installation requires Linux")
}
