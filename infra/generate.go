//go:build ignore

// Run from infra: go run generate.go. No globally installed generators required.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Remove only generated packages, preserving gen/go.mod and gen/go.sum.
	if err := os.RemoveAll(filepath.Join("gen", "auriga")); err != nil {
		panic(err)
	}
	cmd := exec.Command("go", "run", "github.com/bufbuild/buf/cmd/buf@v1.59.0", "generate", "--template", "buf.gen.yaml")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
}
