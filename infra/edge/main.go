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
	accessPath := flags.String("access-config", "", "path to trusted deployment access configuration")
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

	cfg, err := loadConfigWithAccess(*configPath, *accessPath)
	if err != nil {
		return err
	}
	if cfg.NodeTLS.CAFile == "" {
		return fmt.Errorf("nodeTLS is required for instance ingress")
	}

	signer, err := LoadIdentitySigner(cfg.IdentityKeysFile)
	if err != nil {
		return err
	}

	sessions, err := openSessionStore(cfg, logger)
	if err != nil {
		return err
	}
	routesCreds := cfg.RoutesNATSCredsFile
	if routesCreds == "" {
		routesCreds = cfg.NATSCredsFile
	}
	routes, err := NewNATSRouteStore(cfg.NATSURL, routesCreds)
	if err != nil {
		return err
	}
	defer routes.Close()
	tickets := NewMemoryTicketStore()

	gw, err := NewGateway(cfg, sessions, routes, tickets, signer, logger)
	if err != nil {
		return err
	}

	defer gw.Ingress.Close()
	if transport, ok := gw.IAMTransport.(interface{ CloseIdleConnections() }); ok {
		defer transport.CloseIdleConnections()
	}
	for _, component := range gw.APITransports {
		if transport, ok := component.(interface{ CloseIdleConnections() }); ok {
			defer transport.CloseIdleConnections()
		}
	}
	if cfg.Access.Valid() && cfg.IAMRPC.Address != "" {
		rpc, err := newIAMRPC(cfg.IAMRPC, cfg.IAMHop)
		if err != nil {
			return err
		}
		defer func() { _ = rpc.Close() }()
		gw.AdminCSRF = rpc
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
		natsStore, err := NewNATSSessionStore(cfg.NATSURL, cfg.NATSCredsFile, log)
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
