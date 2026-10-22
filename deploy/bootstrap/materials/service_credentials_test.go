package materials

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallationCredentialIsRandomPrivateAndStable(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, out := range []string{a, b} {
		if err := EnsureServiceCredentials(out); err != nil {
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
	if err := EnsureServiceCredentials(a); err != nil {
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
		if err := EnsureServiceCredentials(a); err == nil {
			t.Fatal("publicly readable secret accepted")
		}
	}
}

func TestInstallationRejectsMalformedOrMismatchedExistingMaterial(t *testing.T) {
	for _, name := range []string{"edge.secret", "internal.properties"} {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			if err := EnsureServiceCredentials(out); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "iam-installation", name), []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := EnsureServiceCredentials(out); err == nil {
				t.Fatal("invalid installation silently replaced")
			}
		})
	}
}

func TestInstallationRejectsNoncanonicalCredentialAndExtraProperties(t *testing.T) {
	for _, kind := range []string{"noncanonical-secret", "extra-properties", "wrong-installer"} {
		t.Run(kind, func(t *testing.T) {
			out := t.TempDir()
			if err := EnsureServiceCredentials(out); err != nil {
				t.Fatal(err)
			}
			name := "internal.properties"
			if kind == "noncanonical-secret" {
				name = "edge.secret"
			}
			path := filepath.Join(out, "iam-installation", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "noncanonical-secret":
				raw = []byte(strings.Repeat("A", 42) + "B")
			case "extra-properties":
				raw = append(raw, []byte("extra=value\n")...)
			case "wrong-installer":
				raw = []byte(strings.Replace(string(raw), "installation-owner="+fmt.Sprint(os.Getuid()), "installation-owner="+fmt.Sprint(os.Getuid()+1), 1))
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := EnsureServiceCredentials(out); err == nil {
				t.Fatal("noncanonical or unexpected installation content accepted")
			}
		})
	}
}
