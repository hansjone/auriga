//go:build !windows

package main

import "errors"

func windowsDispatch(_ []string) error { return errors.New("Windows入口仅在Windows运行") }
