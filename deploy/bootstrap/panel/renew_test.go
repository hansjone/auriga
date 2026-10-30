package panel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomaticVersionAndRenewal(t *testing.T) {
	keyDir, dist, out := t.TempDir(), t.TempDir(), t.TempDir()
	if err := Generate(keyDir); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keyDir, "panel-signing.key")
	write := func(code string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ([]byte, manifest) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return raw, m
	}
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	write("legacy")
	if err := Publish(key, dist, out, "0.0.0", now); err != nil {
		t.Fatal(err)
	}
	legacy, _ := read()
	write("upgrade")
	if err := Publish(key, dist, out, "", now.Add(time.Hour)); err != nil {
		t.Fatal("legacy upgrade:", err)
	}
	raw, first := read()
	if first.Version != "0.0.1" {
		t.Fatalf("version = %s", first.Version)
	}
	if err := Publish(key, dist, out, "", now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := read()
	if !bytes.Equal(raw, unchanged) {
		t.Fatal("renewed before threshold")
	}
	if err := Publish(key, dist, out, "", now.Add(24*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, renewed := read()
	if renewed.Version != first.Version || renewed.Entry != first.Entry || renewed.ExpiresAt == first.ExpiresAt {
		t.Fatal("renewal changed version/content or did not extend lifetime")
	}
	for _, code := range []string{"third", "fourth"} {
		write(code)
		if err := Publish(key, dist, out, "", now.Add(25*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{"legacy", "upgrade", "third"} {
		write(code)
		_, previous := read()
		if err := Publish(key, dist, out, "", now.Add(26*24*time.Hour)); err != nil {
			t.Fatal("higher version republication:", err)
		}
		_, next := read()
		if next.Version == previous.Version {
			t.Fatal("returning to old bytes reused version")
		}
	}
	before, _ := read()
	write("fourth")
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), legacy, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Publish(key, dist, out, "", now.Add(27*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("replayed signed manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), before, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Publish(key, dist, out, "", now.Add(100*24*time.Hour)); err != nil {
		t.Fatal("expired recovery:", err)
	}
}

func TestPublicationOutputLock(t *testing.T) {
	keyDir, dist, out := t.TempDir(), t.TempDir(), t.TempDir()
	if err := Generate(keyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte("bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	if err := Publish(filepath.Join(keyDir, "panel-signing.key"), dist, out, "", time.Now()); err == nil {
		t.Fatal("concurrent signer acquired output")
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("locked publication changed manifest")
	}
	unlock()
	unlock = nil
	if err := Publish(filepath.Join(keyDir, "panel-signing.key"), dist, out, "", time.Now()); err != nil {
		t.Fatal("released lock persisted:", err)
	}
}

func TestAutomaticHistoryTampering(t *testing.T) {
	keyDir, dist, out := t.TempDir(), t.TempDir(), t.TempDir()
	if err := Generate(keyDir); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keyDir, "panel-signing.key")
	now := time.Now()
	for _, code := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Publish(key, dist, out, "", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, ".panel-history", "0.0.1.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Publish(key, dist, out, "", now.Add(time.Hour)); err == nil {
		t.Fatal("tampered version history accepted")
	}
}
