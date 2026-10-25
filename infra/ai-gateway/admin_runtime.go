package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
)

type adminConfig struct {
	Listen          string         `json:"listen"`
	TLSCertFile     string         `json:"tlsCertFile"`
	TLSKeyFile      string         `json:"tlsKeyFile"`
	TLSClientCAFile string         `json:"tlsClientCAFile"`
	TLSEdgeDNSNames []string       `json:"tlsEdgeDNSNames"`
	Issuer          string         `json:"issuer"`
	JWKSFile        string         `json:"jwksFile"`
	MasterKeyFile   string         `json:"masterKeyFile"`
	Store           store.Config   `json:"store"`
	Org             team.RPCConfig `json:"org"`
}

func loadAdminConfig(path string) (adminConfig, error) {
	var cfg adminConfig
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&cfg); err != nil {
		return cfg, err
	}
	if d.Decode(new(any)) != io.EOF {
		return cfg, errors.New("trailing AI admin configuration")
	}
	if v := os.Getenv("AURIGA_AI_PG_DSN"); v != "" {
		cfg.Store.DSN = v
	}
	if v := os.Getenv("AURIGA_AI_MIGRATION_PG_DSN"); v != "" {
		cfg.Store.MigrationDSN = v
	}
	if v := os.Getenv("AURIGA_AI_MASTER_KEY_FILE"); v != "" {
		cfg.MasterKeyFile = v
	}
	if cfg.Store.DSN == "" {
		return cfg, errors.New("AI PostgreSQL DSN is required (AURIGA_AI_PG_DSN)")
	}
	if cfg.Store.MigrationDSN == "" {
		return cfg, errors.New("AI migration DSN is required (AURIGA_AI_MIGRATION_PG_DSN)")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	if err = validateAdminTLS(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func serveAdmin(path string) error {
	cfg, err := loadAdminConfig(path)
	if err != nil {
		return err
	}
	server, err := newAdminServer(cfg, nil)
	if err != nil {
		return err
	}
	c, err := admin.LoadCipher(cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	auth, err := admin.JWTAuthenticator(cfg.Issuer, cfg.JWKSFile)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()
	db, err := store.Open(startup, cfg.Store)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := team.DialRPC(cfg.Org)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	handler, err := admin.New(db, c, auth, admin.RPCGrantChecker(org.NewMemberServiceClient(conn), org.NewAuthorizationServiceClient(conn)), admin.ValidateEndpointDNS)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server.Handler = handler
	completed := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			completed <- server.ServeTLS(listener, "", "")
		} else {
			completed <- server.Serve(listener)
		}
	}()
	select {
	case err = <-completed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
