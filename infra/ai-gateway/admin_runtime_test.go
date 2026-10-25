package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminRuntimeConfiguration(t *testing.T) {
	t.Setenv("AURIGA_AI_PG_DSN", "")
	t.Setenv("AURIGA_AI_MIGRATION_PG_DSN", "")
	for _, raw := range []string{`{}`, `{"unknown":true}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "admin.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var out, logs bytes.Buffer
		if err := run([]string{"--admin-config", path}, &out, &logs); err == nil {
			t.Fatal("invalid admin config accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "admin.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAdminConfig(path); err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatal("missing DSN error", err)
	}
	t.Setenv("AURIGA_AI_PG_DSN", "postgres://db.example.com/ai")
	t.Setenv("AURIGA_AI_MIGRATION_PG_DSN", "postgres://db.example.com/ai")
	cfg, err := loadAdminConfig(path)
	if err != nil || cfg.Store.DSN != "postgres://db.example.com/ai" {
		t.Fatal("environment configuration", err)
	}
	var out, logs bytes.Buffer
	if err = run([]string{"--admin-config", path, "--team-config", "unused"}, &out, &logs); err == nil || !strings.Contains(err.Error(), "choose one runtime") {
		t.Fatal("multiple runtimes accepted", err)
	}
	if err = os.WriteFile(path, []byte(`{"listen":"0.0.0.0:8080"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadAdminConfig(path); err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatal("insecure listener accepted", err)
	}
}
