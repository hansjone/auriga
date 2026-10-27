package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A CLI boundary unit test, not a PostgreSQL or production IAM substitute.
func TestMain(m *testing.M) {
	if source := os.Getenv("AURIGA_BETA_PSQL_UNIT_OUTPUT"); source != "" {
		query, err := io.ReadAll(os.Stdin)
		if err != nil || !strings.Contains(string(query), "BEGIN READ ONLY;") || !strings.Contains(string(query), "FROM edge_session_cache.sessions") || os.Getenv("PGPASSWORD") != "" || !strings.Contains(os.Getenv("PGOPTIONS"), "default_transaction_read_only=on") {
			os.Exit(9)
		}
		if os.Getenv("AURIGA_BETA_PSQL_UNIT_FAIL") == "1" {
			os.Exit(7)
		}
		var output string
		for i, arg := range os.Args {
			if strings.Contains(arg, "unit-secret-token") {
				os.Exit(10)
			}
			if arg == "-o" && i+1 < len(os.Args) {
				output = os.Args[i+1]
			}
		}
		data, err := os.ReadFile(source)
		if err != nil || output == "" {
			os.Exit(11)
		}
		if os.WriteFile(output, data, 0o600) != nil {
			os.Exit(12)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBetaProjectionCLIUsesPrivateFilesAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pass := filepath.Join(dir, "reader.pgpass")
	if err := os.WriteFile(pass, []byte("unit credential file"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "unit-response.json")
	t.Setenv("AURIGA_BETA_PSQL_UNIT_OUTPUT", source)
	t.Setenv("PGPASSWORD", "must-not-be-forwarded")
	g, _, memory := accessFixture(t, false, nil)
	backend, _ := browserRPCFixture(t, g, memory)
	row := putBrowserSession(g, backend, "unit-cookie", "admin")
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "config.json")
	c := betaSessionKVConfig{Psql: executable, Port: 5432, Database: "auriga_beta_0123456789", Username: "auriga_beta_0123456789_edgekv", PassFile: pass, Directory: dir, TenantID: g.Cfg.Access.TenantID()}
	encodedConfig, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, encodedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openBetaSessionStore(file, g.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name            string
		data            []byte
		missing, failed bool
	}{
		{name: "active", data: encoded},
		{name: "revoked", data: nil, missing: true},
		{name: "malformed", data: []byte("unit-secret-token malformed private JSON"), failed: true},
		{name: "oversize", data: bytes.Repeat([]byte("x"), 65537), failed: true},
	} {
		t.Run(entry.name, func(t *testing.T) {
			if err := os.WriteFile(source, entry.data, 0o600); err != nil {
				t.Fatal(err)
			}
			session, err := store.Get(context.Background(), HashCookie("unit-cookie"))
			if entry.missing {
				if !errors.Is(err, ErrSessionNotFound) {
					t.Fatal("empty authoritative projection did not miss")
				}
			} else if entry.failed {
				if !errors.Is(err, errIAMUnavailable) {
					t.Fatal("invalid authoritative projection did not fail closed")
				}
			} else if err != nil || session.SessionID != row.SessionID || session.CSRFHash != row.CSRFHash {
				t.Fatal("real projection fields were not preserved")
			}
			paths, err := filepath.Glob(filepath.Join(dir, "projection-*.json"))
			if err != nil || len(paths) != 0 {
				t.Fatal("private result files not cleaned")
			}
		})
	}
	t.Setenv("AURIGA_BETA_PSQL_UNIT_FAIL", "1")
	if _, err := store.Get(context.Background(), HashCookie("unit-cookie")); !errors.Is(err, errIAMUnavailable) {
		t.Fatal("CLI error accepted")
	}
	if _, err := store.Get(context.Background(), "invalid' query"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("untrusted lookup key accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := store.Get(ctx, HashCookie("unit-cookie")); !errors.Is(err, errIAMUnavailable) || time.Since(start) > time.Second {
		t.Fatal("cancelled projection read did not terminate")
	}
	c.Username = "wrong_role"
	encodedConfig, _ = json.Marshal(c)
	if err := os.WriteFile(file, encodedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openBetaSessionStore(file, g.Cfg); err == nil {
		t.Fatal("unrestricted database role accepted")
	}
}
