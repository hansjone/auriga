package main

import (
	"log/slog"
	"os"

	"github.com/hansjone/auriga/infra/agent/internal/app"
)

var version = "0.0.0-dev"

func main() {
	app.Version = version
	if err := app.RunCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "agent").Error("command failed", "error", err)
		os.Exit(1)
	}
}
