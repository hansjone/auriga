//go:build !windows

package installation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Umask is process-wide: this test and this package deliberately do not use
// t.Parallel. Exercise the real renderer through the fixed installation facade.
func TestSharedHostPublicOutputRemains0444WithPrivateInstallerUmask(t *testing.T) {
	dir := t.TempDir()
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	in := SharedHostInputs{Model: "synthetic-approved-model", OutputFile: filepath.Join(dir, "host.json"), EnterprisePolicyFile: filepath.Join(dir, "policy.json")}
	for name, target := range map[string]*string{"identity": &in.IdentityKeysFile, "enterprise": &in.EnterpriseKeysFile, "model": &in.ModelKeysFile, "message": &in.MessageKeysFile} {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": name, "kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
		if err != nil {
			t.Fatal(err)
		}
		*target = filepath.Join(dir, name+".json")
		if err := os.WriteFile(*target, raw, 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(in.EnterprisePolicyFile, []byte(`{"allowRawAgentToken":true,"outboundAllowedHosts":["fixture.example.invalid"],"empNoHeader":"X-Fixture-Emp","authValueHeader":"X-Fixture-Auth"}`), 0444); err != nil {
		t.Fatal(err)
	}
	if err := RenderSharedHostConfig(in); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Lstat(in.OutputFile)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0444 {
		t.Fatal("public output was narrowed by private installer umask", err)
	}
	original, err := os.ReadFile(in.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderSharedHostConfig(in); !errors.Is(err, os.ErrExist) {
		t.Fatal("exclusive output contract lost", err)
	}
	again, err := os.ReadFile(in.OutputFile)
	if err != nil || !bytes.Equal(original, again) {
		t.Fatal("existing public output changed")
	}
}
