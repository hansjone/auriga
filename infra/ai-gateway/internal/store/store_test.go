package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

func TestMissingDSN(t *testing.T) {
	_, err := store.Open(context.Background(), store.Config{})
	if err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("missing DSN: %v", err)
	}
}
func TestPostgresMigrationAndIsolation(t *testing.T) {
	s := testpg.Open(t)
	ctx := context.Background()
	dsn := testpg.DSN(t)
	if err := s.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var count int
	var versions []string
	wantVersions := []string{"0001_admin.sql", "0010_budget.sql"}
	if err = conn.QueryRow(ctx, "SELECT count(*),array_agg(name ORDER BY name) FROM ai.schema_migration").Scan(&count, &versions); err != nil || count != len(wantVersions) || !slices.Equal(versions, wantVersions) {
		t.Fatalf("migration count %d, versions %v, want %v: %v", count, versions, wantVersions, err)
	}
	tenant := "00000000-0000-7000-8000-000000000001"
	other := "00000000-0000-7000-8000-000000000002"
	rollback := errors.New("rollback fixture")
	err = s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES ($1,$2,'{}',1)", tenant, tenant); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ai.provider WHERE tenant_id=$1", tenant).Scan(&n); e != nil || n != 1 {
			t.Fatalf("same tenant %d: %v", n, e)
		}
		if _, e := tx.Exec(ctx, "SELECT set_config('auriga.tenant_id',$1,true)", other); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ai.provider WHERE tenant_id=$1", tenant).Scan(&n); e != nil || n != 0 {
			t.Fatalf("RLS leaked %d: %v", n, e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	err = s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		e := tx.QueryRow(ctx, "SELECT count(*) FROM ai.provider WHERE tenant_id=$1", tenant).Scan(&n)
		if e == nil && n != 0 {
			t.Fatal("rollback persisted")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES ($1,$2,'{}',1)", other, other)
		return e
	}); err == nil {
		t.Fatal("cross tenant write accepted")
	}
}
