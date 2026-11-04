package materials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBifrostSecretsPreserved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bifrost")
	if err := EnsureBifrostSecrets(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "encryption_key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureBifrostSecrets(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "encryption_key"))
	if string(before) != string(after) {
		t.Fatal("salt rotated")
	}
	if err = os.Remove(filepath.Join(dir, "encryption_key")); err != nil {
		t.Fatal(err)
	}
	if err = EnsureBifrostSecrets(dir); err == nil {
		t.Fatal("partial installation silently repaired")
	}
}
