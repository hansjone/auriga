package runtimeimport

import "os/exec"

// The public import command rejects Windows before a child is started.
func setProcessGroup(_ *exec.Cmd) {}
