package materials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// EnsureLiteLLMSecrets never rotates a key or repairs a partial installation.
// The caller owns this private directory independently of the network receipt.
func EnsureLiteLLMSecrets(dir string, fresh ...bool) error {
	names := []string{"master", "salt", "team", "pg_password"}
	info, err := os.Lstat(dir)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid LiteLLM secrets directory")
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		if len(entries) == 0 && len(fresh) == 1 && fresh[0] {
			goto generate
		}
		for _, name := range names {
			info, e := os.Lstat(filepath.Join(dir, name))
			if e != nil || !info.Mode().IsRegular() {
				return errors.New("incomplete LiteLLM installation; restore original secrets")
			}
			b, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil || len(b) < 64 || len(b) > 68 || strings.ContainsAny(string(b), " \r\n\t") {
				return errors.New("invalid LiteLLM secret")
			}
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
generate:
	for _, name := range names {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return err
		}
		value := hex.EncodeToString(b)
		clear(b)
		if name != "pg_password" {
			value = "sk-" + value
		}
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(value)
		closed := f.Close()
		if e != nil {
			return e
		}
		if closed != nil {
			return closed
		}
	}
	return nil
}
