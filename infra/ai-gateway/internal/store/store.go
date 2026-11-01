// Package store owns the ai schema and tenant-scoped PostgreSQL transactions.
package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Config struct {
	DSN            string `json:"dsn"`
	MigrationDSN   string `json:"migrationDsn"`
	MaxConnections int32  `json:"maxConnections"`
}
type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, c Config) (*Store, error) {
	if strings.TrimSpace(c.DSN) == "" {
		return nil, errors.New("AI PostgreSQL DSN is required (AURIGA_AI_PG_DSN)")
	}
	if strings.TrimSpace(c.MigrationDSN) == "" {
		return nil, errors.New("AI migration DSN is required (AURIGA_AI_MIGRATION_PG_DSN)")
	}
	s := &Store{}
	if err := s.Migrate(ctx, c.MigrationDSN); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, errors.New("invalid AI PostgreSQL DSN")
	}
	cfg.MaxConns = 8
	if c.MaxConnections > 0 {
		cfg.MaxConns = c.MaxConnections
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "10000"
	s.pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("cannot create AI PostgreSQL pool")
	}
	if err = s.pool.Ping(ctx); err != nil {
		s.Close()
		return nil, errors.New("cannot connect to AI PostgreSQL")
	}
	var privileged bool
	if err = s.pool.QueryRow(ctx, "SELECT rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb FROM pg_roles WHERE rolname=current_user").Scan(&privileged); err != nil || privileged {
		s.Close()
		return nil, errors.New("AI runtime PostgreSQL role must not be superuser, BYPASSRLS, CREATEROLE or CREATEDB")
	}
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='ai' AND pg_has_role(current_user,c.relowner,'MEMBER'))`).Scan(&privileged); err != nil || privileged {
		s.Close()
		return nil, errors.New("AI runtime PostgreSQL role must not own or inherit ownership of AI tables")
	}
	return s, nil
}
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Migrate serializes concurrent startups and rejects edited, already-applied SQL.
func (s *Store) Migrate(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid AI migration DSN")
	}
	cfg.ConnectTimeout = 5 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return errors.New("cannot connect to AI migration PostgreSQL")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("cannot start AI migration")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(718245009); CREATE SCHEMA IF NOT EXISTS ai; CREATE TABLE IF NOT EXISTS ai.schema_migration (name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return errors.New("cannot initialize AI migrations")
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sql, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(sql)
		checksum := hex.EncodeToString(sum[:])
		var old string
		err = tx.QueryRow(ctx, "SELECT checksum FROM ai.schema_migration WHERE name=$1", entry.Name()).Scan(&old)
		if err == nil {
			if old != checksum {
				return fmt.Errorf("AI migration checksum mismatch: %s", entry.Name())
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("cannot read AI migrations")
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("AI migration failed: %s", entry.Name())
		}
		if _, err = tx.Exec(ctx, "INSERT INTO ai.schema_migration(name,checksum) VALUES ($1,$2)", entry.Name(), checksum); err != nil {
			return errors.New("cannot record AI migration")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("cannot commit AI migrations")
	}
	return nil
}

// Transaction sets a transaction-local tenant; pooled connections cannot retain it.
func (s *Store) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('auriga.tenant_id',$1,true)", tenant); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
