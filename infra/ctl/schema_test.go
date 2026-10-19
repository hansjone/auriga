package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceRolePasswordsPersisted(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateServicePasswords(dir, 15433)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateServicePasswords(dir, 15434)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Roles) != len(serviceSchemas) {
		t.Fatal(len(first.Roles))
	}
	for _, schema := range serviceSchemas {
		if first.Roles[schema].Password != second.Roles[schema].Password {
			t.Fatal("password regenerated")
		}
		if first.Roles[schema].User != roleName(schema) {
			t.Fatal(first.Roles[schema].User)
		}
	}
	if _, err := os.Stat(serviceRolesPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceRolesPath(dir), []byte(`{"roles":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateServicePasswords(dir, 15433); err == nil {
		t.Fatal("corrupt service-roles accepted")
	}
}

func TestServiceRolesAndSchemas(t *testing.T) {
	dir := t.TempDir()
	o := devOptions{DataDir: dir, PGPort: uint32(freePort(t)), NATSPort: freePort(t)}
	c := startForTest(t, o)
	db, err := sql.Open("postgres", c.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	body, err := os.ReadFile(serviceRolesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var file serviceRolesFile
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatal(err)
	}

	for _, schema := range serviceSchemas {
		cred := file.Roles[schema]
		var owner string
		if err := db.QueryRow(
			`SELECT pg_catalog.pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = $1`, schema,
		).Scan(&owner); err != nil || owner != cred.User {
			t.Fatalf("schema %s owner=%q err=%v", schema, owner, err)
		}
		var super, bypass bool
		if err := db.QueryRow(
			`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = $1`, cred.User,
		).Scan(&super, &bypass); err != nil || super || bypass {
			t.Fatalf("role %s elevated super=%v bypass=%v err=%v", cred.User, super, bypass, err)
		}
		roleDB, err := sql.Open("postgres", "postgres://"+cred.User+":"+cred.Password+"@127.0.0.1:"+fmtPort(int(o.PGPort))+"/auriga?sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := roleDB.Exec(`CREATE TABLE probe (id int)`); err != nil {
			_ = roleDB.Close()
			t.Fatalf("role %s cannot create in own schema: %v", cred.User, err)
		}
		other := "iam"
		if schema == "iam" {
			other = "org"
		}
		if _, err := roleDB.Exec(`CREATE TABLE ` + other + `.cross_probe (id int)`); err == nil {
			_ = roleDB.Close()
			t.Fatalf("role %s could write other schema %s", cred.User, other)
		}
		_ = roleDB.Close()
	}

	// Idempotent re-init on restart path.
	services, err := startDev(context.Background(), o, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil {
		_ = services.Close()
		t.Fatal("second startDev acquired lock")
	}
}

func TestEnsureServiceRolesIdempotent(t *testing.T) {
	dir := t.TempDir()
	o := devOptions{DataDir: dir, PGPort: uint32(freePort(t)), NATSPort: freePort(t)}
	c := startForTest(t, o)
	db, err := sql.Open("postgres", c.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := ensureServiceRoles(db, dir, o.PGPort); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "service-roles.json")); err != nil {
		t.Fatal(err)
	}
}
