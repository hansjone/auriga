// Command render-shared-profile creates public immutable bootstrap fixtures;
// it does not start a Host, container, service or Loader.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
)

func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: render-shared-profile <approved-profile.yml> <fixture-output-directory>")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	output, err := filepath.Abs(os.Args[2])
	if err != nil {
		return err
	}
	digest, err := renderer.RenderPool(raw, nil, "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000001", output, 7860)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, digest)
	return err
}
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
