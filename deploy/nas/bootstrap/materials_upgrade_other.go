//go:build !linux

package main

import (
	"fmt"
	"io"
)

func nasMaterialsUpgrade(_ []string, _ io.Writer) error {
	return fmt.Errorf("NAS materials upgrade requires Linux")
}
