//go:build !linux

package main

import (
	"context"
	"errors"
	"os/exec"
)

func runGateProcess(context.Context, *exec.Cmd, *gateWorkspace, string) error {
	return errors.New("隔离门禁需要Linux")
}

func awaitGateSupervisor(context.Context, string) error {
	return errors.New("隔离门禁需要Linux")
}

func gateNamespaceCleanup([]string) error {
	return errors.New("隔离门禁需要Linux")
}
