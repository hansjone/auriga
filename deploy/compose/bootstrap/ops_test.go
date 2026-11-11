package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestOpsKeyPreservedAndMissingOrInvalidRefused(t *testing.T) {
	out := t.TempDir()
	if err := writeComposeOpsKey(out); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(out, "iam-installation", "ops.key")
	first, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(string(first))
	if err != nil || len(decoded) != 32 {
		t.Fatal("invalid generated key")
	}
	if err := writeComposeOpsKey(out); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(key)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("key rotated")
	}
	if err := os.WriteFile(filepath.Join(out, "compose.env"), []byte("AURIGA_OPS_NOTIFICATION_KEY="+string(first)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if err := writeComposeOpsKey(out); err == nil {
		t.Fatal("missing existing key silently rotated")
	}
	if err := os.WriteFile(key, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeComposeOpsKey(out); err == nil {
		t.Fatal("invalid key accepted")
	}
}
