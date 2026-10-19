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

func defaultDataDir() (string, error) {
	if v := os.Getenv("AURIGA_DEV_DIR"); v != "" {
		return v, nil
	}
	root, err := repositoryRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".dev"), nil
}

type sharedFlags struct {
	dataDir, cacheDir, runtimeDir *string
	pgPort                        *uint
	natsPort                      *int
}

func registerSharedFlags(flags *flag.FlagSet) sharedFlags {
	return sharedFlags{
		dataDir:    flags.String("data-dir", os.Getenv("AURIGA_DEV_DIR"), "development data directory (default: repository .dev)"),
		cacheDir:   flags.String("cache-dir", os.Getenv("AURIGA_PG_CACHE_DIR"), "PostgreSQL archive cache (default: data-dir/cache)"),
		runtimeDir: flags.String("runtime-dir", os.Getenv("AURIGA_PG_RUNTIME_DIR"), "disposable PostgreSQL runtime (default: data-dir/runtime/pg)"),
		pgPort:     flags.Uint("pg-port", 15433, "PostgreSQL loopback port"),
		natsPort:   flags.Int("nats-port", 14222, "NATS loopback port"),
	}
}

func (s sharedFlags) options() (devOptions, error) {
	dataDir := *s.dataDir
	if dataDir == "" {
		var err error
		dataDir, err = defaultDataDir()
		if err != nil {
			return devOptions{}, err
		}
	}
	if *s.pgPort < 1 || *s.pgPort > 65535 {
		return devOptions{}, errors.New("pg-port must be between 1 and 65535")
	}
	return devOptions{
		DataDir:    dataDir,
		CacheDir:   *s.cacheDir,
		RuntimeDir: *s.runtimeDir,
		PGPort:     uint32(*s.pgPort),
		NATSPort:   *s.natsPort,
	}, nil
}

func parseDataDirOnly(flags *flag.FlagSet, args []string) (string, error) {
	dataDir := flags.String("data-dir", os.Getenv("AURIGA_DEV_DIR"), "development data directory (default: repository .dev)")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() > 0 {
		return "", fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *dataDir == "" {
		return defaultDataDir()
	}
	return *dataDir, nil
}

func run(ctx context.Context, args []string, out, logs io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "ctl")
	if len(args) == 0 {
		logger.Info("skeleton initialized", "version", version)
		return nil
	}
	switch args[0] {
	case "dev", "serve", "start":
		flags := flag.NewFlagSet("ctl "+args[0], flag.ContinueOnError)
		flags.SetOutput(logs)
		shared := registerSharedFlags(flags)
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() > 0 {
			return fmt.Errorf("unexpected arguments: %v", flags.Args())
		}
		opts, err := shared.options()
		if err != nil {
			return err
		}
		switch args[0] {
		case "dev":
			return runDev(ctx, opts, out, logger)
		case "serve":
			return runServe(ctx, opts, out, logs)
		case "start":
			return runStart(ctx, opts, out, logs)
		}
	case "stop":
		flags := flag.NewFlagSet("ctl stop", flag.ContinueOnError)
		flags.SetOutput(logs)
		dataDir, err := parseDataDirOnly(flags, args[1:])
		if err != nil {
			return err
		}
		return runStop(ctx, dataDir, out)
	case "status":
		flags := flag.NewFlagSet("ctl status", flag.ContinueOnError)
		flags.SetOutput(logs)
		dataDir, err := parseDataDirOnly(flags, args[1:])
		if err != nil {
			return err
		}
		return runStatus(dataDir, out)
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
		if isStatusStopped(err) {
			os.Exit(statusExitStopped)
		}
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
