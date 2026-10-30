package materials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiteLLMSecretsPreserved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "litellm")
	if err := EnsureLiteLLMSecrets(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "salt"))
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureLiteLLMSecrets(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "salt"))
	if string(before) != string(after) {
		t.Fatal("salt rotated")
	}
	if err = os.Remove(filepath.Join(dir, "salt")); err != nil {
		t.Fatal(err)
	}
	if err = EnsureLiteLLMSecrets(dir); err == nil {
		t.Fatal("partial installation silently repaired")
	}
}
