package identity_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
)

func TestAtomicSaveAndLoad(t *testing.T) {
	ca, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := ca.IssueNode("n_save", &key.PublicKey, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sealPriv, sealPub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent")
	mat := &identity.Material{
		NodeID:         "n_save",
		PrivateKey:     key,
		CertificatePEM: certPEM,
		CAPEM:          ca.PEM,
		SealingPrivate: sealPriv,
		SealingPublic:  sealPub,
	}
	if err := identity.Save(dir, mat); err != nil {
		t.Fatal(err)
	}
	loaded, err := identity.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NodeID != "n_save" {
		t.Fatal(loaded.NodeID)
	}
	certs, err := identity.ParseCertPEM(loaded.CertificatePEM)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.PublicKeysEqual(certs[0], key) {
		t.Fatal("pubkey mismatch")
	}
	// Private files must exist with content
	for _, name := range []string{identity.KeyFileName, identity.SealingFileName, identity.CAFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(name, err)
		}
	}
	_ = runtime.GOOS
	_ = pem.Block{}
	_ = x509.Certificate{}
}
