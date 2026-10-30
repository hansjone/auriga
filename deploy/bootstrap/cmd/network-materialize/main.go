package main

import (
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/deploy/bootstrap/network"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, output io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(output, "network_materialize_failed category=usage")
		return 2
	}
	raw, err := os.ReadFile(args[1])
	defer clear(raw)
	if err != nil {
		_, _ = fmt.Fprintln(output, "network_materialize_failed category=options_read_failed")
		return 1
	}
	var options network.Options
	if err = json.Unmarshal(raw, &options); err != nil {
		_, _ = fmt.Fprintln(output, "network_materialize_failed category=options_invalid_json")
		return 1
	}
	if err = network.Generate(args[0], options); err != nil {
		_, _ = fmt.Fprintf(output, "network_materialize_failed category=%s\n", network.ErrorCategory(err))
		return 1
	}
	return 0
}
