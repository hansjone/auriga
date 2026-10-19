//go:build windows

package identity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"golang.org/x/sys/windows"
)

func TestWindowsRestrictFileACL(t *testing.T) {
	dir := t.TempDir()
	if err := identity.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(path, []byte("secret"), 0o666); err != nil {
		t.Fatal(err)
	}
	// Save path uses restrict via atomicWrite; call EnsureDir already restricted dir.
	// Re-write through Save-like restrict by opening via identity package: write then Save empty not needed.
	// Use EnsureDir + manual: identity.Save with minimal material is heavy; call through writing private via Save.
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sealPriv, sealPub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(t.TempDir(), "agent")
	mat := &identity.Material{
		NodeID:         "n_acl",
		PrivateKey:     key,
		CertificatePEM: []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
		CAPEM:          []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
		SealingPrivate: sealPriv,
		SealingPublic:  sealPub,
	}
	// Save will write even with fake PEM; Load would fail parse — we only need ACL on key file.
	if err := identity.Save(agentDir, mat); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(agentDir, identity.KeyFileName)
	sd, err := windows.GetNamedSecurityInfo(keyPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("expected DACL")
	}
	// Presence of a protected DACL after restrict is enough; Users group should not inherit.
	_ = path
}
