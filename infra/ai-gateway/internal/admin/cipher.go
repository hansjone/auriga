package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Binding is authenticated in full, including the independent ciphertext version.
type Binding struct {
	Tenant, Provider, Credential string
	Version                      int
}
type Cipher struct {
	aead           cipher.AEAD
	fingerprintKey []byte
}

// LoadCipher accepts exactly 32 raw bytes from a deployment-protected regular file.
// Windows deployments must grant file access only to the service identity/administrators.
func LoadCipher(path string) (*Cipher, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("AI master key requires a protected regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open AI master key file")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || checkKeyFile(f) != nil {
		return nil, errors.New("AI master key requires a protected regular file")
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, errors.New("AI master key file must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("auriga/ai/admin/idempotency/v1"))
	clear(key)
	return &Cipher{aead: aead, fingerprintKey: mac.Sum(nil)}, nil
}
func (c *Cipher) Encrypt(secret []byte, b Binding) ([]byte, error) {
	aad, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, secret, aad), nil
}
func (c *Cipher) Decrypt(value []byte, b Binding) ([]byte, error) {
	if len(value) < c.aead.NonceSize()+c.aead.Overhead() {
		return nil, errors.New("invalid encrypted AI credential")
	}
	aad, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return c.aead.Open(nil, value[:c.aead.NonceSize()], value[c.aead.NonceSize():], aad)
}
func (c *Cipher) fingerprint(value []byte) []byte {
	mac := hmac.New(sha256.New, c.fingerprintKey)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}
