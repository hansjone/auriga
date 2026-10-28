package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
)

// SQL fixture only: this exercises the snapshot projection and RLS, not the
// production receipt transaction or the full SEND Control/mutant matrix.
func TestSnapshotRecipientHostRequestComesFromPersistedProof(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint32(listener.Addr().(*net.TCPAddr).Port)
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).Port(port).Username("fixture").Password("fixture").Database("fixture").
		CachePath(filepath.Join(dir, "cache")).RuntimePath(filepath.Join(dir, "runtime")).
		DataPath(filepath.Join(dir, "data")).Locale("C").Encoding("UTF8").
		BinaryRepositoryURL("https://repo.maven.apache.org/maven2").
		StartParameters(map[string]string{"listen_addresses": "127.0.0.1"}).
		StartTimeout(time.Minute).Logger(io.Discard))
	if err = pg.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pg.Stop(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://fixture:fixture@127.0.0.1:%d/fixture?sslmode=disable", port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(ctx, `
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
		CREATE ROLE snapshot_reader;
		GRANT USAGE ON SCHEMA collab TO snapshot_reader;
		GRANT SELECT ON ALL TABLES IN SCHEMA collab TO snapshot_reader;
		ALTER TABLE collab.network_recipient ENABLE ROW LEVEL SECURITY;
		ALTER TABLE collab.network_acceptance_proof ENABLE ROW LEVEL SECURITY;
		CREATE POLICY tenant ON collab.network_recipient USING (tenant_id=current_setting('auriga.tenant_id'));
		CREATE POLICY tenant ON collab.network_acceptance_proof USING (tenant_id=current_setting('auriga.tenant_id'));
		SET ROLE snapshot_reader;
	`)
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
