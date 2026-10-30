package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenewalHealth(t *testing.T) {
	keyDir, dist, out := t.TempDir(), t.TempDir(), t.TempDir()
	if err := Generate(keyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte("panel"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	if err := Publish(filepath.Join(keyDir, "panel-signing.key"), dist, out, "", now); err != nil {
		t.Fatal(err)
	}
	if err := CheckHealth(out, now); err == nil {
		t.Fatal("missing scheduler accepted")
	}
	if err := RecordCheck(out, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckHealth(out, now); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheck(out, fmt.Errorf("signer failed")); err != nil {
		t.Fatal(err)
	}
	if err := CheckHealth(out, now); err == nil || !strings.Contains(err.Error(), "renewal failed") {
		t.Fatalf("failure signal: %v", err)
	}
	if err := RecordCheck(out, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckHealth(out, now.Add(3*time.Hour)); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("scheduler stopped: %v", err)
	}
	future := now.Add(31 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(out, ".panel-renew-checked"), future, future); err != nil {
		t.Fatal(err)
	}
	if err := CheckHealth(out, future); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expiry: %v", err)
	}
}
