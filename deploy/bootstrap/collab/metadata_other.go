//go:build !linux

package collab

import "os"

func sourceMetadata(_ os.FileInfo, _ bool) bool { return true }
