package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var fixtureContainerID = strings.Repeat("c", 64)
var fixtureConfig = []byte(fmt.Sprintf(`{"os":%q,"architecture":%q,"config":{},"rootfs":{"type":"layers","diff_ids":[]}}`, goruntime.GOOS, goruntime.GOARCH))
var fixtureConfigID = testDigest(fixtureConfig)
var fixtureManifest = []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, fixtureConfigID, len(fixtureConfig)))
var fixtureImage = "registry.example.com/auriga/dsh@" + testDigest(fixtureManifest)

func testDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type dockerReleaseFixture struct {
	store       *state.Store
	dir, proofs string
	key         ed25519.PrivateKey
	dsh         *nodev1.DshRuntime
}

var releaseFixtures sync.Map

func newDockerReleaseFixture(t *testing.T, d *runtime.Docker, deadline time.Time) *dockerReleaseFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(deadline)}}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueNode("release-test", &pair.PublicKey, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	priv, seal, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	mat := &identity.Material{NodeID: "release-test", PrivateKey: pair, CAPEM: ca.PEM, CertificatePEM: cert, SealingPrivate: priv, SealingPublic: seal}
	binding, err := identity.TrustBinding(mat)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := ring.RootDigest()
	if err != nil {
		t.Fatal(err)
	}
	guard := identity.TrustGuard{Version: 1, Binding: binding, Roots: roots, Trusted: true, Certificate: identity.CertificateDigest(cert)}
	if err := identity.CreateTrustGuard(dir, guard); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.BeginReleaseInitialization(state.NodeMeta{NodeID: mat.NodeID}, guard, ring); err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(dir, mat); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteReleaseInitialization(); err != nil {
		t.Fatal(err)
	}
	proofs := filepath.Join(dir, "runtime-proofs")
	entry := filepath.Join(proofs, testDigest(fixtureManifest)[7:])
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "manifest.json"), fixtureManifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "config.json"), fixtureConfig, 0600); err != nil {
		t.Fatal(err)
	}
	f := &dockerReleaseFixture{store: store, dir: dir, proofs: proofs, key: key}
	f.dsh = &nodev1.DshRuntime{ArtifactId: "dsh-test", Version: "1.0.0", Image: fixtureImage, ReleaseEnvelope: &nodev1.RuntimeReleaseEnvelope{SchemaVersion: 1, Purpose: "dsh_runtime", Name: "dsh", ArtifactId: "dsh-test", DshVersion: "1.0.0", Os: goruntime.GOOS, Arch: goruntime.GOARCH, ImageDigest: testDigest(fixtureManifest), ReleaseSequence: 7, IssuedAt: timestamppb.New(time.Now()), Signature: &nodev1.RuntimeReleaseSignature{Alg: "Ed25519", Kid: "root"}}}
	signRelease(t, f.dsh, key)
	d.Activation = &runtime.ReleaseActivation{Store: store, ProofsDir: proofs, Policy: func() policy.Policy { return d.Policy }}
	releaseFixtures.Store(d, f)
	return f
}

func signRelease(t *testing.T, d *nodev1.DshRuntime, key ed25519.PrivateKey) {
	t.Helper()
	e := d.ReleaseEnvelope
	body, err := json.Marshal(map[string]any{"schemaVersion": e.SchemaVersion, "purpose": e.Purpose, "name": e.Name, "artifactId": e.ArtifactId, "dshVersion": e.DshVersion, "os": e.Os, "arch": e.Arch, "archiveDigest": e.ArchiveDigest, "imageDigest": e.ImageDigest, "releaseSequence": e.ReleaseSequence, "issuedAt": e.IssuedAt.AsTime().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	e.Signature.Value = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, body))
}

func dockerFixture(t *testing.T, d *runtime.Docker) *dockerReleaseFixture {
	t.Helper()
	if f, ok := releaseFixtures.Load(d); ok {
		return f.(*dockerReleaseFixture)
	}
	return newDockerReleaseFixture(t, d, time.Now().Add(time.Hour))
}

func seedDockerRecord(t *testing.T, d *runtime.Docker, id string, generation int64) {
	t.Helper()
	f := dockerFixture(t, d)
	ring, err := f.store.GetReleaseTrust()
	if err != nil {
		t.Fatal(err)
	}
	v, err := releaseverify.Verify(f.dsh, ring, releaseverify.Target{OS: goruntime.GOOS, Arch: goruntime.GOARCH, Driver: "docker", AllowImage: d.Policy.AllowImage})
	if err != nil {
		t.Fatal(err)
	}
	p := v.Partition()
	c := state.ReleaseCandidate{Partition: state.ReleasePartition{Purpose: p.Purpose, Name: p.Name, OS: p.OS, Arch: p.Arch}, Sequence: v.Sequence(), BodyDigest: v.BodyDigest()}
	if err := f.store.WithReleaseExecution(context.Background(), c, func(context.Context) error { return nil }, func(context.Context, *state.ReleaseExecution) error {
		return f.store.PutExecution(state.ExecutionRecord{Version: 1, InstanceID: id, Generation: generation, DSH: f.dsh, Candidate: c, ContainerID: fixtureContainerID, ConfigID: fixtureConfigID, ProofDigest: testDigest(fixtureManifest), Phase: "created"})
	}); err != nil {
		t.Fatal(err)
	}
}
