package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/bifrost"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedhttp"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
)

type sharedModelConfig struct {
	Listen          string            `json:"listen"`
	TenantID        string            `json:"tenantId"`
	AllowedNodeIDs  []string          `json:"allowedNodeIds"`
	ModelPublicKeys map[string]string `json:"modelPublicKeys"`
	sharedhttp.TLSFiles
}

type sharedModelRuntime struct {
	server   *http.Server
	listener net.Listener
	provider *bifrost.SharedRuntime
	database *store.Store
}

func loadModelPublicKeys(files map[string]string) (map[string]ed25519.PublicKey, error) {
	if len(files) == 0 || len(files) > 32 {
		return nil, errors.New("shared model public keys are required")
	}
	keys := make(map[string]ed25519.PublicKey, len(files))
	for kid, path := range files {
		if kid == "" || len(kid) > 128 || strings.ContainsAny(kid, " \t\r\n") || !filepath.IsAbs(path) {
			return nil, errors.New("invalid shared model public key configuration")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errors.New("shared model public key unavailable")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 8193))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(raw) > 8192 {
			return nil, errors.New("invalid shared model public key")
		}
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN PUBLIC KEY-----")) {
			return nil, errors.New("invalid shared model public key")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("invalid shared model public key")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid shared model public key")
		}
		public, ok := parsed.(ed25519.PublicKey)
		if !ok || len(public) != ed25519.PublicKeySize {
			return nil, errors.New("shared model public key must be Ed25519")
		}
		keys[kid] = bytes.Clone(public)
	}
	return keys, nil
}

// prepareSharedModel uses the SAME authenticated IAM client as the existing
// member bridge. Only IAM holds signing keys and raw enterprise credentials.
func prepareSharedModel(ctx context.Context, c *sharedModelConfig, iamClient iam.SharedTaskServiceClient, origin, passwordFile string, policy bifrost.MemberPolicy) (*sharedModelRuntime, error) {
	if c == nil {
		return nil, nil
	}
	tenant, err := uuid.Parse(c.TenantID)
	if err != nil || tenant == uuid.Nil || c.TenantID != tenant.String() {
		return nil, errors.New("invalid shared model deployment tenant")
	}
	if _, _, err = net.SplitHostPort(c.Listen); err != nil {
		return nil, errors.New("invalid shared model listen address")
	}
	keys, err := loadModelPublicKeys(c.ModelPublicKeys)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := sharedhttp.TLSConfig(c.TLSFiles, c.AllowedNodeIDs)
	if err != nil {
		return nil, err
	}
	gate, err := sharedauth.NewRPCGate(iamClient, keys, time.Now)
	if err != nil {
		return nil, errors.New("invalid shared model online authority")
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	database, err := store.Open(startup, store.Config{DSN: os.Getenv("AURIGA_AI_PG_DSN"), MigrationDSN: os.Getenv("AURIGA_AI_MIGRATION_PG_DSN")})
	if err != nil {
		return nil, err
	}
	provider, err := bifrost.NewSharedRuntime(gate, database, origin, passwordFile, policy)
	if err != nil {
		database.Close()
		return nil, err
	}
	handler, err := sharedhttp.NewHandler(c.TenantID, c.AllowedNodeIDs, provider)
	if err != nil {
		provider.Close()
		database.Close()
		return nil, errors.New("invalid shared model handler configuration")
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		provider.Close()
		database.Close()
		return nil, errors.New("shared model listener unavailable")
	}
	server := &http.Server{Handler: handler, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: sharedmodel.MaxCallDuration + 45*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
	return &sharedModelRuntime{server: server, listener: listener, provider: provider, database: database}, nil
}

func (r *sharedModelRuntime) Close() {
	_ = r.listener.Close()
	r.provider.Close()
	r.database.Close()
}
