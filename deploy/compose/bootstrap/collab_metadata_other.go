//go:build !linux

package main

import "os"

// Windows only validates configuration; private activation remains Linux-only.
func collabSourceMetadata(_ os.FileInfo, _ bool) bool { return true }
func validateCollabSource(_ string) bool              { return true }
