//go:build !windows

package main

import "os"

func renamePortable(source, target string) error { return os.Rename(source, target) }
