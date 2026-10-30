package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

// SQL fixture only: this exercises the snapshot projection and RLS, not the
// production receipt transaction or the full SEND Control/mutant matrix.
func TestSnapshotRecipientHostRequestComesFromPersistedProof(t *testing.T) {
	// CI must use its provisioned server; DSN fails (rather than skips) when
	// CI=true without AURIGA_AI_TEST_PG_DSN. Local absence is an explicit skip.
	dsn := testpg.DSN(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "snapshot_" + hex.EncodeToString(suffix[:])
	database := pgx.Identifier{name}.Sanitize()
	role := pgx.Identifier{name + "_reader"}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatal(err)
	}
	// Defer before connecting so connection failure still removes our database;
	// the fixture connection/transaction below close first, in reverse order.
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+database); err != nil {
			t.Error(err)
		}
		if _, err := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+role); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA collab;
		CREATE TABLE collab.network_recipient (
		  tenant_id text, id text, delivery_id text, target_local text,
		  transport_status text, state_version bigint
		);
		CREATE TABLE collab.network_acceptance_proof (
		  tenant_id text, recipient_id text, delivery_id text, accepted_by text, proof_json text,
		  PRIMARY KEY (tenant_id, recipient_id, accepted_by)
		);
		INSERT INTO collab.network_recipient VALUES
		  ('tenant-a','session','delivery','1','host_accepted',2),
		  ('tenant-a','system','delivery','0','host_accepted',2),
		  ('tenant-a','missing','delivery','1','host_accepted',2),
		  ('tenant-a','mismatch','delivery','1','host_accepted',2),
		  ('tenant-b','hidden','delivery','1','host_accepted',2);
		INSERT INTO collab.network_acceptance_proof VALUES
		  ('tenant-a','session','delivery','session','{"host_request_id":"session"}'),
		  ('tenant-a','session','delivery','system','{"host_request_id":"wrong-phase"}'),
		  ('tenant-a','system','delivery','system','{"host_request_id":"system"}'),
		  ('tenant-a','system','delivery','session','{"host_request_id":"wrong-phase"}'),
		  ('tenant-b','missing','delivery','session','{"host_request_id":"wrong-tenant"}'),
		  ('tenant-a','missing','other-delivery','session','{"host_request_id":"wrong-delivery"}'),
		  ('tenant-a','mismatch','delivery','session','{"host_request_id":"persisted-wrong-id"}');
		CREATE ROLE %s;
		GRANT USAGE ON SCHEMA collab TO %s;
		GRANT SELECT ON ALL TABLES IN SCHEMA collab TO %s;
		ALTER TABLE collab.network_recipient ENABLE ROW LEVEL SECURITY;
		ALTER TABLE collab.network_acceptance_proof ENABLE ROW LEVEL SECURITY;
		CREATE POLICY tenant ON collab.network_recipient USING (tenant_id=current_setting('auriga.tenant_id'));
		CREATE POLICY tenant ON collab.network_acceptance_proof USING (tenant_id=current_setting('auriga.tenant_id'));
		SET ROLE %s;
	`, role, role, role, role))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('auriga.tenant_id','tenant-a',true)"); err != nil {
		t.Fatal(err)
	}
	query, err := snapshotQuery("collab", "network_recipient")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]any{"session": "session", "system": "system", "missing": nil, "mismatch": "persisted-wrong-id"}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var row map[string]any
		if err = json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		id := row["id"].(string)
		expected, ok := want[id]
		if !ok {
			t.Fatalf("unexpected recipient: %s", id)
		}
		if got, present := row["host_request_id"]; !present || got != expected {
			t.Errorf("%s: persisted host_request_id = %v (present %v), want %v", id, got, present, expected)
		}
		if row["transport_status"] != "host_accepted" || row["state_version"] != float64(2) || row["tenant_id"] != "tenant-a" {
			t.Errorf("recipient columns changed: %s", raw)
		}
		delete(want, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(want) != 0 {
		t.Fatalf("snapshot omitted recipients: %v", want)
	}
}
