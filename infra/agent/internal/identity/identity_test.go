package identity_test

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
func TestLiteralParentDirectoryRejectsEveryInitializationLeaf(t *testing.T) {
	parents := []string{"legacy[1]"}
	if runtime.GOOS != "windows" {
		parents = append(parents, "legacy*?[1]")
	}
	for _, parent := range parents {
		for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName, identity.TrustGuardFileName, "state.db"} {
			for _, temp := range []bool{false, true} {
				t.Run(parent+"/"+name+map[bool]string{false: "/formal", true: "/temporary"}[temp], func(t *testing.T) {
					dir := filepath.Join(t.TempDir(), parent, "agent")
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					leaf := name
					if temp {
						leaf = "." + name + ".tmp.crashed"
					}
					path := filepath.Join(dir, leaf)
					want := []byte("historical initialization bytes")
					if err := os.WriteFile(path, want, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := identity.CheckFreshEnrollment(dir); err == nil {
						t.Fatal("literal parent hid initialization evidence")
					}
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatal("freshness probe changed evidence")
					}
				})
			}
		}
	}
}
func TestWindowsCaseAliasesRejectAllInitializationLeaves(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows leaf-name alias matching")
	}
	for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName, identity.TrustGuardFileName, "state.db"} {
		for _, temporary := range []bool{false, true} {
			for _, casing := range []string{"upper", "mixed"} {
				canonical := name
				if temporary {
					canonical = "." + name + ".tmp.crashed"
				}
				leaf := strings.ToUpper(canonical)
				if casing == "mixed" {
					mixed := []byte(canonical)
					for i, ch := range mixed {
						if i%2 == 0 && ch >= 'a' && ch <= 'z' {
							mixed[i] = ch - 'a' + 'A'
						}
					}
					leaf = string(mixed)
				}
				t.Run(canonical+"/"+casing, func(t *testing.T) {
					dir := filepath.Join(t.TempDir(), "ParentCASE[1]", "AgentCASE")
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(dir, leaf)
					want := []byte("historical case alias bytes")
					if err := os.WriteFile(path, want, 0o600); err != nil {
						t.Fatal(err)
					}
					before, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := identity.CheckFreshEnrollment(dir); err == nil {
						t.Fatal("Windows case-aliased historical leaf considered fresh")
					}
					entries, err := os.ReadDir(dir)
					if err != nil || len(entries) != 1 || entries[0].Name() != leaf {
						t.Fatal("gate changed original directory entry")
					}
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatal("gate changed evidence bytes/readability")
					}
					after, err := os.Stat(path)
					if err != nil || after.Mode() != before.Mode() {
						t.Fatal("gate changed evidence mode")
					}
				})
			}
		}
	}
}

func TestLinuxCaseSensitiveInitializationLeafSemantics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux case-sensitive leaf semantics")
	}
	for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName, identity.TrustGuardFileName, "state.db"} {
		for _, temporary := range []bool{false, true} {
			canonical := name
			if temporary {
				canonical = "." + name + ".tmp.crashed"
			}
			t.Run(canonical, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "ParentCASE[1]", "agent")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, strings.ToUpper(canonical))
				want := []byte("distinct Linux uppercase leaf")
				if err := os.WriteFile(path, want, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(filepath.Join(dir, canonical)); !os.IsNotExist(err) {
					t.Skip("fixture requires a case-sensitive Linux directory")
				}
				if err := identity.CheckFreshEnrollment(dir); err != nil {
					t.Fatalf("Windows leaf folding changed distinct Linux names: %v", err)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("gate changed distinct Linux leaf")
				}
			})
		}
	}
}
