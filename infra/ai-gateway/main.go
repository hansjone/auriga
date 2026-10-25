package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("auriga-ai-gateway", flag.ContinueOnError)
	flags.SetOutput(logs)
	teamConfigFile := flags.String("team-config", "", "serve remote Team MCP using a configuration file")
	networkConfigFile := flags.String("network-config", "", "serve network callout and revocation reconciliation using a configuration file")
	adminConfigFile := flags.String("admin-config", "", "serve AI management API using a configuration file")
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-ai-gateway", version)
		return err
	}
	if *adminConfigFile != "" {
		if *teamConfigFile != "" || *networkConfigFile != "" {
			return fmt.Errorf("choose one runtime per process: --admin-config, --team-config or --network-config")
		}
		return serveAdmin(*adminConfigFile)
	}
	if *teamConfigFile != "" {
		if *networkConfigFile != "" {
			return fmt.Errorf("choose one runtime per process: --team-config or --network-config")
		}
		return serveTeam(*teamConfigFile)
	}
	if *networkConfigFile != "" {
		cfg, err := network.LoadRuntimeConfig(*networkConfigFile)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return network.RunRuntime(ctx, cfg)
	}
	return fmt.Errorf("runtime configuration required: --admin-config, --team-config or --network-config")
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
