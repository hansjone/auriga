package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("auriga-edge", flag.ContinueOnError)
	flags.SetOutput(logs)
	showVersion := flags.Bool("version", false, "print version")
	configPath := flags.String("config", "", "path to JSON config file")
	accessPath := flags.String("access-config", "", "path to trusted deployment access configuration")
	routeMode := flags.String("instance-routes", "nats", "instance route store: nats or explicit local memory")
	sessionKV := flags.String("session-kv-config", "", "private local Beta PostgreSQL session projection configuration")
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

	var sessions SessionStore
	if *sessionKV != "" {
		sessions, err = openBetaSessionStore(*sessionKV, cfg)
	} else {
		sessions, err = openSessionStore(cfg, logger)
	}
	if err != nil {
		return err
	}
	routes, closeRoutes, err := openInstanceRoutes(cfg, *routeMode)
	if err != nil {
		return err
	}
	defer closeRoutes()
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
		gw.SessionRPC = rpc
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &Server{Cfg: cfg, Handler: gw.Handler(), Log: logger}
	logger.Info("edge starting", "version", version, "listen", cfg.Listen, "tls", cfg.TLS.Mode)
	return srv.Run(ctx)
}

func openInstanceRoutes(cfg Config, mode string) (RouteStore, func(), error) {
	switch mode {
	case "memory":
		// No routes are seeded: instance access continues to fail closed.
		return NewMemoryRouteStore(), func() {}, nil
	case "nats", "":
		creds := cfg.RoutesNATSCredsFile
		if creds == "" {
			creds = cfg.NATSCredsFile
		}
		routes, err := NewNATSRouteStore(cfg.NATSURL, creds)
		if err != nil {
			return nil, nil, err
		}
		return routes, routes.Close, nil
	default:
		return nil, nil, fmt.Errorf("unknown instance route store")
	}
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

// Beta already requires the native PostgreSQL client for installation. Read its actual shared
// projection with a dedicated SELECT-only role, rather than treating edge memory as IAM's KV.
// No positive cache: logout/revocation is visible on the next request without NATS events.
type betaSessionKVConfig struct {
	Psql      string `json:"psql"`
	Port      int    `json:"port"`
	Database  string `json:"database"`
	Username  string `json:"username"`
	PassFile  string `json:"passFile"`
	Directory string `json:"directory"`
	TenantID  string `json:"tenantId"`
}

type betaSessionStore struct {
	config betaSessionKVConfig
	slots  chan struct{}
}

func openBetaSessionStore(file string, cfg Config) (SessionStore, error) {
	invalid := fmt.Errorf("invalid private Beta session projection configuration")
	if !cfg.Access.Valid() || cfg.Access.Mode() != "ip" || cfg.Access.PortalOrigin().Host != "127.0.0.1" || cfg.Access.AdminOrigin().Host != "127.0.0.1" || cfg.SessionStore != "memory" {
		return nil, invalid
	}
	input, err := os.Open(file)
	if err != nil {
		return nil, invalid
	}
	defer func() { _ = input.Close() }()
	var c betaSessionKVConfig
	d := json.NewDecoder(io.LimitReader(input, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.TenantID != cfg.Access.TenantID() || c.Port < 1 || c.Port > 65535 || !regexp.MustCompile(`^auriga_beta_[a-f0-9]{10}$`).MatchString(c.Database) || c.Username != c.Database+"_edgekv" {
		return nil, invalid
	}
	for _, name := range []string{c.PassFile, c.Directory} {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name {
			return nil, invalid
		}
		info, statErr := os.Lstat(name)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || (name == c.Directory && !info.IsDir()) || (name == c.PassFile && !info.Mode().IsRegular()) {
			return nil, invalid
		}
	}
	if c.Psql, err = exec.LookPath(c.Psql); err != nil {
		return nil, invalid
	}
	return &betaSessionStore{config: c, slots: make(chan struct{}, 4)}, nil
}

var betaCookieHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *betaSessionStore) Get(ctx context.Context, hash string) (*Session, error) {
	if !betaCookieHash.MatchString(hash) {
		return nil, ErrSessionNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, errIAMUnavailable
	}
	// The launcher's protected directory supplies Windows ACL inheritance as well as POSIX 0600.
	output, err := os.CreateTemp(s.config.Directory, "projection-*.json")
	if err != nil {
		return nil, errIAMUnavailable
	}
	name := output.Name()
	_ = output.Close()
	defer func() { _ = os.Remove(name) }()
	c := s.config
	command := exec.CommandContext(ctx, c.Psql, "-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", strconv.Itoa(c.Port), "-U", c.Username, "-d", c.Database, "-o", name)
	// syscall's public process attributes differ by OS; only Windows has HideWindow.
	command.SysProcAttr = &syscall.SysProcAttr{}
	if hidden := reflect.ValueOf(command.SysProcAttr).Elem().FieldByName("HideWindow"); hidden.IsValid() && hidden.CanSet() {
		hidden.SetBool(true)
	}
	// Fixed SQL/view/tenant; the request contributes only a validated SHA-256 key, never SQL or argv.
	command.Stdin = strings.NewReader("BEGIN READ ONLY; SET LOCAL auriga.tenant_id='" + c.TenantID + "'; SELECT left((value - 'adminCsrfTokens')::text,65537) FROM edge_session_cache.sessions WHERE tenant_id='" + c.TenantID + "'::uuid AND cookie_hash='" + hash + "' AND expires_at>CURRENT_TIMESTAMP; COMMIT;")
	command.Stdout, command.Stderr = io.Discard, io.Discard // JWT results go directly to -o, never stdout.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(strings.SplitN(entry, "=", 2)[0]), "PG") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "PGPASSFILE="+c.PassFile, "PGCONNECT_TIMEOUT=2", "PGCLIENTENCODING=UTF8", "PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=1500")
	if command.Run() != nil {
		return nil, errIAMUnavailable
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errIAMUnavailable
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errIAMUnavailable
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, ErrSessionNotFound
	}
	projection, err := DecodeSessionJSON(raw)
	if err != nil || projection.TenantID != c.TenantID {
		return nil, errIAMUnavailable
	}
	return projection, nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}
