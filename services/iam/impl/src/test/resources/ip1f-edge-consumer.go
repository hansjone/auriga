package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Injected only by the Java IAM integration test, against the unmodified production edge.
func TestIP1fIssuerFileConsumer(t *testing.T) {
	var cfg struct {
		Address, TokenFile, CA, Cert, Key, Tenant, Account, Session, Cookie, CSRF string
		Now                                                                       int64
	}
	name := os.Getenv("IP1F_CONSUMER_CONFIG")
	if name == "" {
		t.Fatal("missing issuer integration configuration")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(name)
	barrier := func(stage int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, fmtStage("stage", stage)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(dir, fmtStage("continue", stage))); err == nil {
				return
			}
			select {
			case <-deadline.C:
				t.Fatalf("issuer barrier %d timed out", stage)
			case <-ticker.C:
			}
		}
	}
	rpc := IAMRPCConfig{Address: cfg.Address, ServerName: "localhost", ServiceTokenFile: cfg.TokenFile, TimeoutMillis: 2000}
	hop := IAMHopConfig{CertFile: cfg.Cert, KeyFile: cfg.Key, CAFile: cfg.CA, ServerName: "localhost"}
	if client, err := newIAMRPC(rpc, hop); err == nil {
		_ = client.Close()
		t.Fatal("missing first token must reject startup")
	}
	barrier(0)
	client, err := newIAMRPC(rpc, hop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	expected := &Session{TenantID: cfg.Tenant, AccountID: cfg.Account, SessionID: cfg.Session, Audience: "admin"}
	verify := func(want error) {
		t.Helper()
		if got := client.Verify(context.Background(), cfg.Cookie, cfg.CSRF, expected, time.Unix(cfg.Now, 0)); !errors.Is(got, want) {
			t.Fatalf("edge RPC outcome=%v want=%v", got, want)
		}
	}
	verify(nil)
	barrier(1)
	verify(nil)
	barrier(2) // Same client, freshly atomically replaced service file.
	verify(nil)
	barrier(3) // Initial JWT is now more than five minutes old.
	verify(errIAMUnauthenticated)
	barrier(4) // Failed reissuance did not extend the second JWT.
	verify(nil)
	barrier(5) // Credentials restored; no edge restart.
	verify(errIAMUnavailable)
	barrier(6) // Lifecycle stop removed the file.
	verify(nil)
	barrier(7) // IAM publisher restart recovered the same consumer.
}

func fmtStage(prefix string, stage int) string { return prefix + "-" + string(rune('0'+stage)) }
