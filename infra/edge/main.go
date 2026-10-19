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
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("auriga-edge", flag.ContinueOnError)
	flags.SetOutput(logs)
	showVersion := flags.Bool("version", false, "print version")
	configPath := flags.String("config", "", "path to JSON config file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-edge", version)
		return err
	}

	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "edge")

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	signer, err := LoadIdentitySigner(cfg.IdentityKeysFile)
	if err != nil {
		return err
	}

	sessions, err := openSessionStore(cfg, logger)
	if err != nil {
		return err
	}
	routes := NewMemoryRouteStore()
	tickets := NewMemoryTicketStore()

	gw, err := NewGateway(cfg, sessions, routes, tickets, signer, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &Server{Cfg: cfg, Handler: gw.Handler(), Log: logger}
	logger.Info("edge starting", "version", version, "listen", cfg.Listen, "tls", cfg.TLS.Mode)
	return srv.Run(ctx)
}

func openSessionStore(cfg Config, log *slog.Logger) (SessionStore, error) {
	mem := NewMemorySessionStore()
	switch cfg.SessionStore {
	case "", "memory":
		return CachingSessionStore{Primary: mem, Introspect: FailIntrospector{}}, nil
	case "nats":
		natsStore, err := NewNATSSessionStore(cfg.NATSURL, log)
		if err != nil {
			log.Warn("nats session store unavailable; falling back to memory", "error", err.Error())
			return CachingSessionStore{Primary: mem, Introspect: FailIntrospector{}}, nil
		}
		return CachingSessionStore{Primary: natsStore, Introspect: FailIntrospector{}}, nil
	default:
		return nil, fmt.Errorf("unknown sessionStore %q", cfg.SessionStore)
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
