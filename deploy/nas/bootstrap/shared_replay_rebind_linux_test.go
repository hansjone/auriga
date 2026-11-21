package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

func TestSharedReplayBindingIgnoresOnlyRotatableFields(t *testing.T) {
	base := sharedInstallationReceipt{Version: 1, Base: strings.Repeat("a", 64), Tenant: "t", Node: "n", CA: strings.Repeat("b", 64), Adapter: strings.Repeat("c", 64), Profile: strings.Repeat("d", 64), InitialRuntime: "r", Model: "gpt-6.1-sol", Files: strings.Repeat("e", 64)}
	want, err := sharedReplayBinding(&base)
	if err != nil {
		t.Fatal(err)
	}
	rotated := base
	rotated.Model, rotated.Profile, rotated.Files = "auto", strings.Repeat("f", 64), strings.Repeat("0", 64)
	if got, _ := sharedReplayBinding(&rotated); got != want {
		t.Fatal("model/profile/file rotation must keep the replay binding")
	}
	for name, mutate := range map[string]func(*sharedInstallationReceipt){
		"node":      func(r *sharedInstallationReceipt) { r.Node = "other" },
		"adapter":   func(r *sharedInstallationReceipt) { r.Adapter = strings.Repeat("9", 64) },
		"base":      func(r *sharedInstallationReceipt) { r.Base = strings.Repeat("8", 64) },
		"dedicated": func(r *sharedInstallationReceipt) { r.DedicatedProfile = strings.Repeat("7", 64) },
		"tenant":    func(r *sharedInstallationReceipt) { r.Tenant = "other" },
		"teamOnly":  func(r *sharedInstallationReceipt) { r.TeamOnly = true },
	} {
		changed := base
		mutate(&changed)
		if got, _ := sharedReplayBinding(&changed); got == want {
			t.Fatalf("identity field %s must change the replay binding", name)
		}
	}
	if _, err := sharedReplayBinding(nil); err == nil {
		t.Fatal("nil receipt accepted")
	}
}

func TestSharedReplayRebindMovesOnlyTheBinding(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("replay receipts are root-owned private leaves")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := user.Seed()
	public, _ := user.PublicKey()
	previous, current := strings.Repeat("1", 64), strings.Repeat("2", 64)
	created := time.Date(2026, 11, 21, 1, 2, 3, 0, time.UTC)
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	put := func(path string, value any) {
		raw, ok := value.([]byte)
		if !ok {
			raw, _ = json.Marshal(value)
		}
		if err := materialsCreate(path, raw); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(source, "installer.seed"), seed)
	put(filepath.Join(source, "installer.nkey"), []byte(public))
	put(filepath.Join(source, "source.receipt"), sharedReplaySourceReceipt{Version: 1, Source: previous, Public: public})
	put(filepath.Join(root, "bucket.receipt"), sharedReplayBucketReceipt{Version: 1, Source: previous, Created: created})
	put(filepath.Join(root, "activation.receipt"), sharedReplayActivation{Version: 1, Source: previous, Final: strings.Repeat("3", 64), Open: true})
	seedBefore, _ := os.ReadFile(filepath.Join(source, "installer.seed"))

	if err := rebindSharedReplaySource(root, current, "UEDGE", new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	var s sharedReplaySourceReceipt
	var b sharedReplayBucketReceipt
	var a sharedReplayActivation
	for path, into := range map[string]any{filepath.Join(source, "source.receipt"): &s, filepath.Join(root, "bucket.receipt"): &b, filepath.Join(root, "activation.receipt"): &a} {
		raw, err := runtimeReadLeaf(path, 0600, 65536)
		if err != nil || json.Unmarshal(raw, into) != nil {
			t.Fatal(path, err)
		}
	}
	if s.Source != current || s.Public != public || b.Source != current || !b.Created.Equal(created) || a.Source != current || !a.Open || a.Final != strings.Repeat("3", 64) {
		t.Fatalf("rebind changed more than the binding: %+v %+v %+v", s, b, a)
	}
	if seedAfter, _ := os.ReadFile(filepath.Join(source, "installer.seed")); !bytes.Equal(seedBefore, seedAfter) {
		t.Fatal("installer key rewritten")
	}
	out := new(bytes.Buffer)
	if err := rebindSharedReplaySource(root, current, "UEDGE", out); err != nil || !strings.Contains(out.String(), "current") {
		t.Fatal("rebind must be idempotent", err, out.String())
	}
	// A receipt bound to an unrelated installation is never adopted.
	put(filepath.Join(root, ".x"), []byte("x"))
	if err := rebindSharedReplaySource(root, strings.Repeat("4", 64), "UEDGE", new(bytes.Buffer)); err == nil {
		t.Fatal("unknown replay material accepted")
	}
	if err := os.Remove(filepath.Join(root, ".x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "bucket.receipt")); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(root, "bucket.receipt"), sharedReplayBucketReceipt{Version: 1, Source: strings.Repeat("5", 64), Created: created})
	if err := rebindSharedReplaySource(root, strings.Repeat("4", 64), "UEDGE", new(bytes.Buffer)); err == nil {
		t.Fatal("bucket receipt of another binding adopted")
	}
}
