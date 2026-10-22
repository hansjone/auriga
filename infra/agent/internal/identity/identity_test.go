package identity_test

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
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
}

func TestSaveKeysBeforeCertificateOnFailure(t *testing.T) {
	for _, blocked := range []string{identity.KeyFileName, identity.SealingFileName, identity.CAFileName, identity.CertFileName} {
		t.Run(blocked, func(t *testing.T) {
			ca, err := testutil.NewTestCA()
			if err != nil {
				t.Fatal(err)
			}
			material := func() *identity.Material {
				key, err := identity.GenerateKeyPair()
				if err != nil {
					t.Fatal(err)
				}
				cert, err := ca.IssueNode("n_save", &key.PublicKey, time.Now().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				priv, pub, err := identity.GenerateSealingKey()
				if err != nil {
					t.Fatal(err)
				}
				return &identity.Material{PrivateKey: key, CertificatePEM: cert, CAPEM: ca.PEM, SealingPrivate: priv, SealingPublic: pub}
			}
			old, next := material(), material()
			dir := filepath.Join(t.TempDir(), "agent")
			if err := identity.Save(dir, old); err != nil {
				t.Fatal(err)
			}
			// A directory in place of a file forces rename to fail at a real save boundary.
			target := filepath.Join(dir, blocked)
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := identity.Save(dir, next); err == nil {
				t.Fatal("save should fail")
			}
			if blocked != identity.CertFileName {
				cert, err := os.ReadFile(filepath.Join(dir, identity.CertFileName))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(cert, old.CertificatePEM) {
					t.Fatal("new certificate published before all key writes succeeded")
				}
			}
			if blocked != identity.KeyFileName {
				keyPEM, err := os.ReadFile(filepath.Join(dir, identity.KeyFileName))
				if err != nil {
					t.Fatal(err)
				}
				// The key on disk already matches the next certificate before it can be published.
				certs, err := identity.ParseCertPEM(next.CertificatePEM)
				if err != nil {
					t.Fatal(err)
				}
				pair, err := tls.X509KeyPair(next.CertificatePEM, keyPEM)
				if err != nil {
					t.Fatalf("new key was not written first: %v", err)
				}
				if !bytes.Equal(pair.Certificate[0], certs[0].Raw) {
					t.Fatal("wrong replacement key")
				}
			}
			tmp, err := filepath.Glob(filepath.Join(dir, ".*.tmp.*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(tmp) != 0 {
				t.Fatalf("temporary secrets left behind: %v", tmp)
			}
		})
	}
}
func TestInitializationEvidenceAndUnreadableGuard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	if err := identity.CheckFreshEnrollment(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, identity.TrustGuardFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.ReadTrustGuard(dir); err == nil {
		t.Fatal("directory accepted as guard")
	}
	if err := identity.CheckFreshEnrollment(dir); err == nil {
		t.Fatal("unreadable guard mistaken for absence")
	}
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := identity.CheckFreshEnrollment(filepath.Join(path, "agent")); err == nil {
		t.Fatal("filesystem inspection error mistaken for fresh enrollment")
	}
}
