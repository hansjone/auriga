package bifrostbackend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

func TestReviewExistingDatabaseProvisioningIsIdempotent(t *testing.T) {
	dsn := testpg.DSN(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var occupied bool
	if e = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='bifrost') OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname IN ('auriga_bifrost','auriga_bifrost_owner'))`).Scan(&occupied); e != nil || occupied {
		t.Fatal("provisioning test requires an isolated PostgreSQL cluster")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		connection, e := pgx.Connect(cleanup, dsn)
		if e != nil {
			t.Error(e)
			return
		}
		defer func() { _ = connection.Close(context.Background()) }()
		if _, e = connection.Exec(cleanup, `DROP DATABASE IF EXISTS bifrost WITH (FORCE)`); e != nil {
			t.Error(e)
		}
		if _, e = connection.Exec(cleanup, `DROP ROLE IF EXISTS auriga_bifrost; DROP ROLE IF EXISTS auriga_bifrost_owner`); e != nil {
			t.Error(e)
		}
	})
	parsed, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	secretDir := t.TempDir()
	newPassword := func() string {
		var value [32]byte
		if _, err := rand.Read(value[:]); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(value[:])
	}
	runtimePassword := newPassword()
	ownerPassword := newPassword()
	if e = os.WriteFile(filepath.Join(secretDir, "bifrost_pg_password"), []byte(runtimePassword), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(secretDir, "bifrost_pg_migration_password"), []byte(ownerPassword), 0600); e != nil {
		t.Fatal(e)
	}
	script, e := filepath.Abs("../../../../deploy/nas/bifrost/postgres-init.sh")
	if e != nil {
		t.Fatal(e)
	}
	user := parsed.User.Username()
	run := func(wantError ...bool) {
		cmd := exec.CommandContext(ctx, "sh", filepath.ToSlash(script))
		cmd.Env = append(os.Environ(), "BIFROST_SECRET_DIR="+filepath.ToSlash(secretDir), "POSTGRES_USER="+user, "POSTGRES_DB="+strings.TrimPrefix(parsed.Path, "/"), "PGHOST="+parsed.Hostname(), "PGPORT="+parsed.Port())
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		e = cmd.Run()
		if len(wantError) > 0 {
			if e == nil {
				t.Fatal("unsafe existing role was accepted")
			}
			return
		}
		if e != nil {
			t.Fatalf("database upgrade helper failed: %v: %s", e, strings.ReplaceAll(strings.ReplaceAll(output.String(), runtimePassword, "[redacted]"), ownerPassword, "[redacted]"))
		}
		if bytes.Contains(output.Bytes(), []byte(runtimePassword)) || bytes.Contains(output.Bytes(), []byte(ownerPassword)) {
			t.Fatal("upgrade exposed a password")
		}
	}
	run()
	cfg := *parsed
	cfg.Path = "/bifrost"
	cfg.User = url.UserPassword("auriga_bifrost_owner", ownerPassword)
	migration := new(store.Store)
	if e = migration.Migrate(ctx, cfg.String()); e != nil {
		t.Fatal(e)
	}
	owner, e := pgx.Connect(ctx, cfg.String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = owner.Close(context.Background()) }()
	if _, e = owner.Exec(ctx, `CREATE TABLE ai.upgrade_sentinel(id int PRIMARY KEY); INSERT INTO ai.upgrade_sentinel VALUES(1)`); e != nil {
		t.Fatal(e)
	}
	run()
	var count int
	if e = owner.QueryRow(ctx, `SELECT count(*) FROM ai.upgrade_sentinel`).Scan(&count); e != nil || count != 1 {
		t.Fatal("upgrade destroyed existing data")
	}
	cfg.User = url.UserPassword("auriga_bifrost", runtimePassword)
	db, e := store.Open(ctx, store.Config{DSN: cfg.String(), MigrationDSN: func() string { cfg.User = url.UserPassword("auriga_bifrost_owner", ownerPassword); return cfg.String() }()})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	runtimeConnection := *parsed
	runtimeConnection.Path = "/bifrost"
	runtimeConnection.User = url.UserPassword("auriga_bifrost", runtimePassword)
	r, e := pgx.Connect(ctx, runtimeConnection.String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Close(context.Background()) }()
	var canForge bool
	if e = r.QueryRow(ctx, `SELECT has_table_privilege(current_user,'ai.schema_migration','INSERT,UPDATE,DELETE')`).Scan(&canForge); e != nil || canForge {
		t.Fatal("runtime can forge migration history")
	}
	if _, e = admin.Exec(ctx, "ALTER ROLE auriga_bifrost CREATEDB"); e != nil {
		t.Fatal(e)
	}
	run(true)
	if _, e = admin.Exec(ctx, "ALTER ROLE auriga_bifrost NOCREATEDB"); e != nil {
		t.Fatal(e)
	}

}
