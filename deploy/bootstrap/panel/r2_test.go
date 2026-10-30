package panel

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckedPublicationBusyDoesNotChangeStatus(t *testing.T) {
	out := t.TempDir()
	if err := RecordCheck(out, nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(out, ".panel-renew-checked"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := PublishChecked("missing", "missing", out, time.Now()); !errors.Is(err, ErrPublicationBusy) {
		t.Fatalf("busy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, ".panel-renew-failed")); !os.IsNotExist(err) {
		t.Fatal("lock contention recorded a failure")
	}
	after, err := os.ReadFile(filepath.Join(out, ".panel-renew-checked"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("busy check changed heartbeat")
	}
}

func TestRenewalExactBoundaryAndClockRollback(t *testing.T) {
	keys, dist, out := t.TempDir(), t.TempDir(), t.TempDir()
	if err := Generate(keys); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte("bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	key := filepath.Join(keys, "panel-signing.key")
	if err := PublishChecked(key, dist, out, now); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishChecked(key, dist, out, now.Add(-time.Second)); err == nil {
		t.Fatal("clock rollback accepted")
	}
	if err := PublishChecked(key, dist, out, now.Add(Lifetime-RenewBefore)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("exact seven day boundary did not renew")
	}
}

func TestBusyPublicationPreservesExistingFailure(t *testing.T) {
	out := t.TempDir()
	if err := RecordCheck(out, errors.New("fixture failure")); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := PublishChecked("missing", "missing", out, time.Now()); !errors.Is(err, ErrPublicationBusy) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, ".panel-renew-failed")); err != nil {
		t.Fatal("busy contender cleared failure:", err)
	}
}
