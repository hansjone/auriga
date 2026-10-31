package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	slog.SetDefault(observability.Logger(logs))
	flags := flag.NewFlagSet("auriga-ai-gateway", flag.ContinueOnError)
	flags.SetOutput(logs)
	teamConfigFile := flags.String("team-config", "", "serve remote Team MCP using a configuration file")
	litellmConfigFile := flags.String("litellm-config", "", "serve authenticated member requests through LiteLLM")
	networkConfigFile := flags.String("network-config", "", "serve network callout and revocation reconciliation using a configuration file")
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	selected := 0
	for _, path := range []string{*teamConfigFile, *networkConfigFile, *litellmConfigFile} {
		if path != "" {
			selected++
		}
	}
	if selected > 1 {
		return fmt.Errorf("choose one runtime per process: --litellm-config, --team-config or --network-config")
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-ai-gateway", version)
		return err
	}
	if *litellmConfigFile != "" {
		return serveLiteLLM(*litellmConfigFile)
	}
	if *teamConfigFile != "" {
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
	return fmt.Errorf("runtime configuration required: --litellm-config, --team-config or --network-config")
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		observability.Logger(os.Stderr).Error("command failed", "error", err)
		os.Exit(1)
	}
}
