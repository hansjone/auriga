package state

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func trustIdentity(t *testing.T, dir string) *identity.Material {
	t.Helper()
	ca, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueNode("n_trust", &key.PublicKey, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	return &identity.Material{NodeID: "n_trust", PrivateKey: key, CAPEM: ca.PEM, CertificatePEM: cert, SealingPrivate: priv, SealingPublic: pub}
}

func trustStore(t *testing.T, ring releasetrust.Ring) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agent")
	mat := trustIdentity(t, dir)
	binding, err := identity.TrustBinding(mat)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ring.RootDigest()
	if err != nil {
		t.Fatal(err)
	}
	g := identity.TrustGuard{Version: 1, Binding: binding, Roots: digest, Trusted: true, Certificate: identity.CertificateDigest(mat.CertificatePEM)}
	if err := identity.CreateTrustGuard(dir, g); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.BeginReleaseInitialization(NodeMeta{NodeID: mat.NodeID}, g, ring); err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(dir, mat); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteReleaseInitialization(); err != nil {
		t.Fatal(err)
	}
	return st, dir
}

// These 50 public RT0 inputs drive the production verifier and the real bbolt
// store. Fixture text that cannot be represented by a legal Timestamp is mapped
// to an invalid protobuf Timestamp, which the production implementation rejects.
func TestSharedKeyringScenariosWithStore(t *testing.T) {
	type keyJSON struct{ Kid, PublicKey, NotAfter, EndorsedByKid, Endorsement string }
	var vectors struct{ Keys []keyJSON }
	readJSON(t, "signature-vectors.json", &vectors)
	keys := map[string]*nodev1.ArtifactSigningKey{}
	for _, k := range vectors.Keys {
		pub, _ := base64.RawURLEncoding.DecodeString(k.PublicKey)
		sig, _ := base64.RawURLEncoding.DecodeString(k.Endorsement)
		keys[k.Kid] = &nodev1.ArtifactSigningKey{Kid: k.Kid, PublicKey: pub, NotAfter: fixtureTimestamp(k.NotAfter), EndorsedByKid: k.EndorsedByKid, Endorsement: sig}
	}
	var scenarios struct {
		Cases []struct {
			Name, Now, Expected         string
			Bootstrap                   bool
			Initial, Incoming           []string
			InitialDeadlines, Deadlines map[string]string
			Patches                     []struct {
				Index        int
				Field, Value string
			}
		}
	}
	readJSON(t, "keyring-scenarios.json", &scenarios)
	if len(scenarios.Cases) != 50 {
		t.Fatalf("scenario count %d", len(scenarios.Cases))
	}
	base, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{keys["root"]}, true, time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range scenarios.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			st, _ := trustStore(t, base)
			initial := releasetrust.Ring{Version: 1}
			for _, id := range tc.Initial {
				k := keys[id]
				deadline := k.NotAfter.AsTime()
				if id == "child" {
					deadline = keys["root"].NotAfter.AsTime()
				}
				if s, ok := tc.InitialDeadlines[id]; ok {
					deadline = fixtureTimestamp(s).AsTime()
				}
				source := "endorsement"
				if k.EndorsedByKid == "" {
					source = "bootstrap"
				}
				initial.Records = append(initial.Records, releasetrust.Record{Kid: k.Kid, PublicKey: k.PublicKey, NotAfter: releasetrust.FromTime(k.NotAfter.AsTime()), Parent: k.EndorsedByKid, Endorsement: k.Endorsement, Source: source, Deadline: releasetrust.FromTime(deadline)})
			}
			incoming := make([]*nodev1.ArtifactSigningKey, 0, len(tc.Incoming))
			for _, id := range tc.Incoming {
				incoming = append(incoming, proto.Clone(keys[id]).(*nodev1.ArtifactSigningKey))
			}
			for _, p := range tc.Patches {
				k := incoming[p.Index]
				switch p.Field {
				case "kid":
					k.Kid = p.Value
				case "publicKey":
					k.PublicKey, _ = base64.RawURLEncoding.DecodeString(p.Value)
				case "endorsement":
					k.Endorsement, _ = base64.RawURLEncoding.DecodeString(p.Value)
				case "endorsedByKid":
					k.EndorsedByKid = p.Value
				case "notAfter":
					k.NotAfter = fixtureTimestamp(p.Value)
				default:
					t.Fatalf("unknown fixture patch %s", p.Field)
				}
			}
			now := fixtureTimestamp(tc.Now).AsTime()
			var err error
			if tc.Bootstrap {
				ring, e := releasetrust.Merge(releasetrust.Ring{}, incoming, true, now)
				err = e
				if err == nil {
					freshDir := filepath.Join(t.TempDir(), "new")
					mat := trustIdentity(t, freshDir)
					binding, _ := identity.TrustBinding(mat)
					roots, _ := ring.RootDigest()
					g := identity.TrustGuard{Version: 1, Binding: binding, Roots: roots, Trusted: true, Certificate: identity.CertificateDigest(mat.CertificatePEM)}
					if e := identity.CreateTrustGuard(freshDir, g); e != nil {
						t.Fatal(e)
					}
					fresh, e := Open(freshDir)
					if e != nil {
						t.Fatal(e)
					}
					t.Cleanup(func() { _ = fresh.Close() })
					err = fresh.BeginReleaseInitialization(NodeMeta{NodeID: mat.NodeID}, g, ring)
					if err == nil {
						err = identity.Save(freshDir, mat)
					}
					if err == nil {
						err = fresh.CompleteReleaseInitialization()
					}
					st = fresh
				}
			} else {
				// Direct seeding models RT0's explicitly supplied persisted history,
				// including the intentional incomplete-history rejection scenario.
				err = st.db.Update(func(tx *bbolt.Tx) error {
					r, e := readRelease(tx)
					if e != nil {
						return e
					}
					r.Ring = initial
					if digest, e := initial.RootDigest(); e == nil {
						r.Guard.Roots = digest
						raw, _ := json.Marshal(r.Guard)
						if e := os.WriteFile(filepath.Join(st.agentDir, identity.TrustGuardFileName), raw, 0o600); e != nil {
							return e
						}
					}
					return writeRelease(tx, r)
				})
				if err != nil {
					t.Fatal(err)
				}
				err = st.MergeReleaseKeys(incoming, now)
			}
			if (err == nil) != (tc.Expected == "accept") {
				t.Fatalf("expected %s, got %v", tc.Expected, err)
			}
			if err == nil {
				ring, e := st.GetReleaseTrust()
				if e != nil {
					t.Fatal(e)
				}
				for _, k := range ring.Records {
					if want, ok := tc.Deadlines[k.Kid]; ok && !k.Deadline.Time().Equal(fixtureTimestamp(want).AsTime()) {
						t.Fatalf("%s deadline %s want %s", k.Kid, k.Deadline.Time(), want)
					}
				}
				if len(ring.Records) != len(tc.Deadlines) {
					t.Fatalf("records=%d expected=%d", len(ring.Records), len(tc.Deadlines))
				}
			}
		})
	}
}

var fixtureTimePattern = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$`)

func fixtureTimestamp(s string) *timestamppb.Timestamp {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !fixtureTimePattern.MatchString(s) || t.Year() < 1 {
		return &timestamppb.Timestamp{Seconds: -62135596801}
	}
	return timestamppb.New(t)
}
func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../../..", "contracts", "runtime", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentMergeRestartAndAtomicFailure(t *testing.T) {
	now := time.Now()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &nodev1.ArtifactSigningKey{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(now.Add(time.Hour))}
	ring, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{root}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	st, dir := trustStore(t, ring)
	child := func(id string) *nodev1.ArtifactSigningKey {
		p, _, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		return &nodev1.ArtifactSigningKey{Kid: id, PublicKey: p, NotAfter: timestamppb.New(now.Add(2 * time.Hour)), EndorsedByKid: "root", Endorsement: ed25519.Sign(priv, append([]byte(id), p...))}
	}
	a, b := child("a"), child("b")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, k := range []*nodev1.ArtifactSigningKey{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- st.MergeReleaseKeys([]*nodev1.ArtifactSigningKey{k}, now) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	before, err := st.GetReleaseTrust()
	if err != nil {
		t.Fatal(err)
	}
	bad := child("bad")
	bad.Endorsement[0] ^= 1
	if err := st.MergeReleaseKeys([]*nodev1.ArtifactSigningKey{child("valid"), bad}, now); err == nil {
		t.Fatal("bad batch accepted")
	}
	after, err := st.GetReleaseTrust()
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(before)
	y, _ := json.Marshal(after)
	if string(x) != string(y) {
		t.Fatal("failed batch partially committed")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	after, err = st.GetReleaseTrust()
	if err != nil || len(after.Records) != 3 {
		t.Fatalf("restart lost merged keys: %v", err)
	}
	after.Records[0].PublicKey[0] ^= 1
	if _, err := st.GetReleaseTrust(); err != nil {
		t.Fatal("mutable returned ring altered database")
	}
}

func TestInitializationCrashWindowsFailClosed(t *testing.T) {
	now := time.Now()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &nodev1.ArtifactSigningKey{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(now.Add(time.Hour))}
	ring, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{root}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"guard only", "pending without files", "key only", "key and sealing", "key sealing ca", "complete pending", "different certificate", "active", "lost guard", "changed guard", "lost identity", "changed identity", "damaged database record"} {
		t.Run(stage, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "agent")
			mat := trustIdentity(t, dir)
			binding, err := identity.TrustBinding(mat)
			if err != nil {
				t.Fatal(err)
			}
			digest, _ := ring.RootDigest()
			guard := identity.TrustGuard{Version: 1, Binding: binding, Roots: digest, Trusted: true, Certificate: identity.CertificateDigest(mat.CertificatePEM)}
			if err := identity.CreateTrustGuard(dir, guard); err != nil {
				t.Fatal(err)
			}
			if stage == "guard only" {
				if err := identity.CheckFreshEnrollment(dir); err == nil {
					t.Fatal("guard-only state considered fresh")
				}
				return
			}
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			if err := st.BeginReleaseInitialization(NodeMeta{NodeID: mat.NodeID}, guard, ring); err != nil {
				t.Fatal(err)
			}
			if stage != "pending without files" {
				if err := identity.Save(dir, mat); err != nil {
					t.Fatal(err)
				}
			}
			switch stage {
			case "key only":
				for _, name := range []string{identity.SealingFileName, identity.CAFileName, identity.CertFileName} {
					if err := os.Remove(filepath.Join(dir, name)); err != nil {
						t.Fatal(err)
					}
				}
			case "key and sealing":
				for _, name := range []string{identity.CAFileName, identity.CertFileName} {
					if err := os.Remove(filepath.Join(dir, name)); err != nil {
						t.Fatal(err)
					}
				}
			case "key sealing ca":
				if err := os.Remove(filepath.Join(dir, identity.CertFileName)); err != nil {
					t.Fatal(err)
				}
			case "different certificate":
				ca, err := testutil.NewTestCA()
				if err != nil {
					t.Fatal(err)
				}
				cert, err := ca.IssueNode(mat.NodeID, &mat.PrivateKey.PublicKey, now.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, identity.CertFileName), cert, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.GetReleaseTrust(); err == nil {
				t.Fatal("pending ring usable before identity activation")
			}
			complete := stage == "complete pending" || stage == "active" || stage == "lost guard" || stage == "changed guard" || stage == "lost identity" || stage == "changed identity" || stage == "damaged database record"
			if err := st.CompleteReleaseInitialization(); (err == nil) != complete {
				t.Fatalf("activation at %s: %v", stage, err)
			}
			if !complete {
				if err := identity.CheckFreshEnrollment(dir); err == nil {
					t.Fatal("partial state became fresh")
				}
				return
			}
			switch stage {
			case "lost guard":
				if err := os.Remove(filepath.Join(dir, identity.TrustGuardFileName)); err != nil {
					t.Fatal(err)
				}
			case "changed guard":
				guard.Roots = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				raw, _ := json.Marshal(guard)
				if err := os.WriteFile(filepath.Join(dir, identity.TrustGuardFileName), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "lost identity":
				if err := os.Remove(filepath.Join(dir, identity.SealingFileName)); err != nil {
					t.Fatal(err)
				}
			case "changed identity":
				other := trustIdentity(t, dir)
				if err := identity.Save(dir, other); err != nil {
					t.Fatal(err)
				}
			case "damaged database record":
				if err := st.db.Update(func(tx *bbolt.Tx) error {
					return tx.Bucket(bucketReleaseTrust).Put(keyReleaseTrust, []byte(`{"version":1,"unknown":true}`))
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.GetReleaseTrust()
			if (err == nil) != (stage == "active" || stage == "complete pending") {
				t.Fatalf("restart at %s: %v", stage, err)
			}
		})
	}
}
