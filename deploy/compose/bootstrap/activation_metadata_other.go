//go:build !linux

package main

import "os"

func activationMetadata(info os.FileInfo, directory bool) bool {
	return info.Mode()&os.ModeSymlink == 0 && (directory && info.IsDir() || !directory && info.Mode().IsRegular())
}

func activationBaseDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
