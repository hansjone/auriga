package admin

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	protectTestKey(t, path)
	c, err := LoadCipher(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCipherAAD(t *testing.T) {
	c := testCipher(t)
	aad := Binding{"tenant", "provider", "credential", 1}
	ciphertext, err := c.Encrypt([]byte("test-value"), aad)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(ciphertext, aad)
	if err != nil || string(plain) != "test-value" {
		t.Fatal(err)
	}
	if _, err = testCipher(t).Decrypt(ciphertext, aad); err == nil {
		t.Fatal("wrong master key accepted")
	}
	for _, other := range []Binding{{"other", "provider", "credential", 1}, {"tenant", "other", "credential", 1}, {"tenant", "provider", "other", 1}, {"tenant", "provider", "credential", 2}} {
		if _, err = c.Decrypt(ciphertext, other); err == nil {
			t.Fatal("modified AAD accepted")
		}
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = c.Decrypt(ciphertext, aad); err == nil {
		t.Fatal("modified ciphertext accepted")
	}
}
func TestMissingKey(t *testing.T) {
	if _, err := LoadCipher(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing key accepted")
	}
}
