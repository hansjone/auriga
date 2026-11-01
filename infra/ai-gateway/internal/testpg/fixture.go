// Package testpg provisions isolated runtime roles for real PostgreSQL tests.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

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
	cfg, err := url.Parse(dsn)
	if err != nil || (cfg.Scheme != "postgres" && cfg.Scheme != "postgresql") {
		t.Fatal("AURIGA_AI_TEST_PG_DSN must be a PostgreSQL URL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	m := new(store.Store)
	if err := m.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := conn.Close(closeCtx); err != nil {
			t.Errorf("close fixture admin connection: %v", err)
		}
	})
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	role := "ai_test_" + hex.EncodeToString(random[:8])
	password := hex.EncodeToString(random[:])
	quoted := pgx.Identifier{role}.Sanitize()
	if err = catalogTransaction(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN PASSWORD '"+password+"'"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "GRANT USAGE ON SCHEMA ai TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA ai TO "+quoted)
		return err
	}); err != nil {
		t.Fatalf("provision fixture role and ACL: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := catalogTransaction(cleanupCtx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(cleanupCtx, "DROP OWNED BY "+quoted); err != nil {
				return err
			}
			_, err := tx.Exec(cleanupCtx, "DROP ROLE "+quoted)
			return err
		}); err != nil {
			t.Errorf("remove fixture role and ACL: %v", err)
		}
	})
	cfg.User = url.UserPassword(role, password)
	s, err := store.Open(ctx, store.Config{DSN: cfg.String(), MigrationDSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// Distinct from the production migration lock (718245009). Only fixture catalog
// writes serialize: stores, queries and migration startups remain concurrent.
// GRANT and DROP OWNED update the same schema/table ACL catalog tuples even for
// different roles. The shared database lock coordinates separate test binaries.
func catalogTransaction(ctx context.Context, conn *pgx.Conn, fn func(pgx.Tx) error) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	// Acquire in its own statement so the following catalog statements see a
	// fresh READ COMMITTED snapshot after waiting for the previous transaction.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718245010)"); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
