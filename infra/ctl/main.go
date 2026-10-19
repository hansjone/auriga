package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

var version = "0.0.0-dev"

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "buf.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cannot find repository root; use --data-dir outside the checkout")
		}
		dir = parent
	}
}

func run(ctx context.Context, args []string, out, logs io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "ctl")
	if len(args) > 0 && args[0] == "dev" {
		flags := flag.NewFlagSet("ctl dev", flag.ContinueOnError)
		flags.SetOutput(logs)
		dataDir := flags.String("data-dir", os.Getenv("AURIGA_DEV_DIR"), "development data directory (default: repository .dev)")
		cacheDir := flags.String("cache-dir", os.Getenv("AURIGA_PG_CACHE_DIR"), "PostgreSQL archive cache (default: data-dir/cache)")
		runtimeDir := flags.String("runtime-dir", os.Getenv("AURIGA_PG_RUNTIME_DIR"), "disposable PostgreSQL runtime (default: data-dir/runtime/pg)")
		pgPort := flags.Uint("pg-port", 15433, "PostgreSQL loopback port")
		natsPort := flags.Int("nats-port", 14222, "NATS loopback port")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() > 0 {
			return fmt.Errorf("unexpected arguments: %v", flags.Args())
		}
		if *dataDir == "" {
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			*dataDir = filepath.Join(root, ".dev")
		}
		if *pgPort < 1 || *pgPort > 65535 {
			return errors.New("pg-port must be between 1 and 65535")
		}
		return runDev(ctx, devOptions{DataDir: *dataDir, CacheDir: *cacheDir, RuntimeDir: *runtimeDir, PGPort: uint32(*pgPort), NATSPort: *natsPort}, out, logger)
	}
	flags := flag.NewFlagSet("ctl", flag.ContinueOnError)
	flags.SetOutput(logs)
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unknown command: %s", flags.Arg(0))
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-ctl", version)
		return err
	}
	logger.Info("skeleton initialized", "version", version)
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
