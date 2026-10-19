package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("auriga-agent", flag.ContinueOnError)
	flags.SetOutput(logs)
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-agent", version)
		return err
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
	logger.Info("skeleton initialized", "version", version)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
