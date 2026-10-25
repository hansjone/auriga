// Package testpg provisions isolated runtime roles for real PostgreSQL tests.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
)

func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AURIGA_AI_TEST_PG_DSN")
	if dsn == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI requires AURIGA_AI_TEST_PG_DSN")
		}
		t.Skip("AURIGA_AI_TEST_PG_DSN absent; real PostgreSQL integration not run")
	}
	return dsn
}
func Open(t *testing.T) *store.Store {
	t.Helper()
	dsn := DSN(t)
	ctx := context.Background()
	m := new(store.Store)
	if err := m.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	role := "ai_test_" + hex.EncodeToString(random[:8])
	password := hex.EncodeToString(random[:])
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN PASSWORD '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP OWNED BY "+quoted)
		_, _ = conn.Exec(ctx, "DROP ROLE "+quoted)
		_ = conn.Close(ctx)
	})
	if _, err = conn.Exec(ctx, "GRANT USAGE ON SCHEMA ai TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA ai TO "+quoted); err != nil {
		t.Fatal(err)
	}
	cfg, err := url.Parse(dsn)
	if err != nil || (cfg.Scheme != "postgres" && cfg.Scheme != "postgresql") {
		t.Fatal(err)
	}
	cfg.User = url.UserPassword(role, password)
	s, err := store.Open(ctx, store.Config{DSN: cfg.String(), MigrationDSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
