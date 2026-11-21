package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Called from the already explicitly scheduled root-only NAS metadata case.
// This is not a new Fatal test accidentally selected by non-root all-package CI.
func testSharedReplayPrivateSourceMetadata(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	edge, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Wipe()
	public, err := edge.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("1", 64)
	if err := stageSharedReplaySource(root, source, public); err != nil {
		t.Fatal(err)
	}
	before := nasSnapshot(t, filepath.Join(root, "source"))
	if err := stageSharedReplaySource(root, source, public); err != nil || nasSnapshot(t, filepath.Join(root, "source")) != before {
		t.Fatal("installer source retry changed credentials", err)
	}
	receipt, err := readSharedReplaySource(root, source, public)
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(root, "source/installer.seed")
	option, err := nats.NkeyOptionFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	settings := nats.GetDefaultOptions()
	if err := option(&settings); err != nil || settings.Nkey != receipt.Public {
		t.Fatal("fresh file callback identity differs", err)
	}
	nonce := []byte("synthetic-replay-installer-file-roundtrip")
	signature, err := settings.SignatureCB(nonce)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := nkeys.FromPublicKey(receipt.Public)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Wipe()
	if err := verifier.Verify(nonce, signature); err != nil {
		t.Fatal("producer Wipe damaged the actual stored installer seed", err)
	}
	if _, err := readSharedReplaySource(root, strings.Repeat("2", 64), public); err == nil {
		t.Fatal("different source receipt accepted")
	}
	if err := os.Chmod(seed, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := readSharedReplaySource(root, source, public); err == nil {
		t.Fatal("broad private installer seed accepted")
	}
	if err := os.Chmod(seed, 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "source/unknown")
	if err := os.WriteFile(other, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSharedReplaySource(root, source, public); err == nil {
		t.Fatal("unknown installer source leaf accepted")
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := sharedReplayWriteState(root, sharedReplayActivation{Version: 1, Source: source, Final: strings.Repeat("3", 64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeReadLeaf(filepath.Join(root, "activation.receipt"), 0600, 65536); err != nil {
		t.Fatal(err)
	}
	if _, err := readSharedReplaySource(root, source, public); err != nil {
		t.Fatal(err)
	}
}
