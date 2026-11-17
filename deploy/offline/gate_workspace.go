package main

import (
	"errors"
	"os"
)

// The supervisor owns these paths even if the isolated child is force-killed.
type gateWorkspace struct {
	root    string
	runtime string
}

func newGateWorkspace() (*gateWorkspace, error) {
	root, err := os.MkdirTemp("", "auriga-gate-engine-")
	if err != nil {
		return nil, err
	}
	runtime, err := os.MkdirTemp("/run", "auriga-gate-")
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(root))
	}
	return &gateWorkspace{root: root, runtime: runtime}, nil
}

func (w *gateWorkspace) close() error {
	return errors.Join(os.RemoveAll(w.runtime), os.RemoveAll(w.root))
}
