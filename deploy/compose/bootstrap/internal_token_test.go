package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallationCredentialIsRandomPrivateAndStable(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, out := range []string{a, b} {
		if err := writeInternalTokenInstallation(out); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(a, "iam-installation", "edge.secret")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile(filepath.Join(b, "iam-installation", "edge.secret"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 43 || bytes.Equal(first, other) {
		t.Fatal("installation secrets must be independently random")
	}
	if err := writeInternalTokenInstallation(a); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if !bytes.Equal(first, again) {
		t.Fatal("rerun rotated an existing installation")
	}
	properties, _ := os.ReadFile(filepath.Join(a, "iam-installation", "internal.properties"))
	hash := sha256.Sum256(first)
	if string(properties) != "auriga.iam.internal-secret-hashes.edge="+hex.EncodeToString(hash[:])+"\nauriga.iam.edge-token-export.installation-owner="+fmt.Sprint(os.Getuid())+"\n" || bytes.Contains(properties, first) {
		t.Fatal("hash configuration is not the issuer's existing authentication format")
	}
	if runtime.GOOS != "windows" {
		for name, mode := range map[string]os.FileMode{"iam-installation": 0o700, "iam-installation/edge.secret": 0o600, "iam-installation/internal.properties": 0o600} {
			info, err := os.Lstat(filepath.Join(a, name))
			if err != nil || info.Mode().Perm() != mode {
				t.Fatal("installation permissions differ")
			}
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeInternalTokenInstallation(a); err == nil {
			t.Fatal("publicly readable secret accepted")
		}
	}
}

func TestInstallationRejectsMalformedOrMismatchedExistingMaterial(t *testing.T) {
	for _, name := range []string{"edge.secret", "internal.properties"} {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			if err := writeInternalTokenInstallation(out); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "iam-installation", name), []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeInternalTokenInstallation(out); err == nil {
				t.Fatal("invalid installation silently replaced")
			}
		})
	}
}
