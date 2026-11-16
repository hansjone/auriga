//go:build !linux

package main

import "os/exec"

func setGateProcessGroup(_ *exec.Cmd)            {}
func stopGateProcessGroup(cmd *exec.Cmd, _ bool) { _ = cmd.Process.Kill() }
