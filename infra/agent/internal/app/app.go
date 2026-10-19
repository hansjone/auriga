// Package app wires auriga-agent commands (join / run).
package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/conn"
	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// Version is set by main.
var Version = "0.0.0-dev"

// RunCLI parses args and executes join / run / --version.
func RunCLI(args []string, out, logs io.Writer) error {
	if len(args) == 0 {
		logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
		logger.Info("agent ready", "version", Version, "hint", "use join or run")
		return nil
	}
	if args[0] == "--version" || args[0] == "-version" {
		_, err := fmt.Fprintln(out, "auriga-agent", Version)
		return err
	}

	switch args[0] {
	case "join":
		return runJoin(args[1:], logs)
	case "run":
		return runAgent(args[1:], logs)
	default:
		return fmt.Errorf("unknown command %q (want join|run|--version)", args[0])
	}
}

func runJoin(args []string, logs io.Writer) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(logs)
	server := fs.String("server", "", "platform URL (https://host:port)")
	token := fs.String("token", "", "one-time enrollment token (prefer --token-file or env)")
	tokenFile := fs.String("token-file", "", "read token from file")
	caSHA := fs.String("ca-sha256", "", "platform CA SPKI SHA-256 (hex)")
	configPath := fs.String("config", "", "optional JSON config path")
	dataRoot := fs.String("data-root", "", "override data root")
	force := fs.Bool("force", false, "replace existing enrollment")
	allowInsecure := fs.Bool("allow-insecure", false, "allow grpc:// http:// (local dev only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *dataRoot != "" {
		cfg.DataRoot = *dataRoot
	}
	if *server != "" {
		cfg.Server = *server
	}
	cfg.AgentVersion = Version
	if *allowInsecure {
		cfg.AllowInsecure = true
	}
	tok, err := enroll.ReadToken(*token, *tokenFile)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
	if err := ensureLayout(cfg); err != nil {
		return err
	}
	_, err = enroll.Join(context.Background(), enroll.Options{
		Server:        cfg.Server,
		Token:         tok,
		CAFingerprint: *caSHA,
		DataRoot:      cfg.DataRoot,
		AgentDir:      cfg.AgentDir(),
		AgentVersion:  cfg.AgentVersion,
		Logger:        logger,
		AllowInsecure: cfg.AllowInsecure,
		Force:         *force,
	})
	return err
}

func runAgent(args []string, logs io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(logs)
	configPath := fs.String("config", "", "optional JSON config path")
	dataRoot := fs.String("data-root", "", "override data root")
	allowInsecure := fs.Bool("allow-insecure", false, "allow grpc:// http:// (local dev only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *dataRoot != "" {
		cfg.DataRoot = *dataRoot
	}
	if *allowInsecure {
		cfg.AllowInsecure = true
	}
	cfg.AgentVersion = Version
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
	if err := ensureLayout(cfg); err != nil {
		return err
	}

	store, err := state.Open(cfg.AgentDir())
	if err != nil {
		return err
	}
	defer store.Close()

	driver := runtime.NewFake()
	rec := reconciler.New(store, driver, logger, cfg.MaxConcurrentPrepare)
	defer rec.Close()

	sess := conn.New(conn.Config{
		AgentDir:      cfg.AgentDir(),
		AgentVersion:  cfg.AgentVersion,
		ControlURL:    cfg.ControlEndpoint,
		Drivers:       []nodev1.RuntimeKind{nodev1.RuntimeKind_RUNTIME_KIND_DOCKER},
		Logger:        logger,
		Reconciler:    rec,
		Store:         store,
		AllowInsecure: cfg.AllowInsecure,
	})
	rec.Sink = sess.StatusSink()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "version", Version, "data_root", cfg.DataRoot)
	return sess.Run(ctx)
}

func ensureLayout(cfg config.Config) error {
	for _, dir := range []string{cfg.AgentDir(), cfg.InstancesDir(), cfg.ArtifactsDir(), cfg.BackupsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	_ = os.WriteFile(filepath.Join(cfg.AgentDir(), ".layout"), []byte("auriga-agent\n"), 0o644)
	return nil
}
