//go:build !linux

package main

import (
	"fmt"
	"io"
)

func nasRuntimeUpgrade(_ []string, _ io.Writer) error {
	return fmt.Errorf("NAS runtime upgrade requires Linux")
}
