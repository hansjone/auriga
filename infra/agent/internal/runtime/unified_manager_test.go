package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type unifiedSource struct{ spec *nodev1.InstanceSpec }

func (s *unifiedSource) GetDesired(string) (*nodev1.InstanceSpec, error) { return s.spec, nil }

type unifiedDriver struct {
	*Fake
	catalog   string
	stopFails bool
	stops     int
}

func (d *unifiedDriver) Endpoint(string) (Endpoint, error) { return Endpoint{URL: d.catalog}, nil }
func (d *unifiedDriver) Stop(ctx context.Context, id string, grace time.Duration) error {
	d.stops++
	if d.stopFails {
		return errors.New("temporary stop failure")
	}
	return d.Fake.Stop(ctx, id, grace)
}

type unifiedFixture struct {
	manager           *UnifiedManager
	source            *unifiedSource
	driver            *unifiedDriver
	now               time.Time
	mono              int64
	payload, manifest []byte
	list              *resourcev1.ResourceRevocationList
	ref               *resourcev1.UnifiedResourceRef
	revPrivate        ed25519.PrivateKey
	releasePrivate    ed25519.PrivateKey
	catalogPrivate    bool
	catalogEmpty      bool
	privateCollision  bool
	override          bool
	root              string
}

func unifiedSign(t *testing.T, document map[string]any, key ed25519.PrivateKey, kid string) {
	t.Helper()
	raw, err := unifiedCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	document["signature"] = map[string]any{"alg": "Ed25519", "kid": kid, "value": base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
}
func skillArchive(t *testing.T, names map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	archive := tar.NewWriter(gzipWriter)
	for name, body := range names {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

const unifiedSkillText = "---\nname: example-skill\ndescription: Public isolated skill fixture.\n---\n\nPublic fixture body.\n"

func newUnifiedFixture(t *testing.T) *unifiedFixture {
	t.Helper()
	f := &unifiedFixture{now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	revPub, revKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.revPrivate = revKey
	f.releasePrivate = key
	f.payload = skillArchive(t, map[string]string{"SKILL.md": unifiedSkillText, "references/notes.txt": "Public reference."})
	manifest := map[string]any{"schemaVersion": 1, "kind": "skill", "name": "example-skill", "version": "1.0.0", "description": "Fixture", "author": map[string]any{"displayName": "Example"}, "license": "MIT", "permissions": map[string]any{"tools": []any{}, "outboundHosts": []any{}, "dataAccess": []any{}}, "runtimes": []any{}, "signature": map[string]any{"mode": "detached", "algorithm": "Ed25519"}, "skill": map[string]any{"entry": "SKILL.md", "contentProfile": "text-only", "dshVersionRange": ">=0.2.0"}, "artifacts": []any{map[string]any{"id": "payload", "role": "payload", "digest": udigest(f.payload), "sizeBytes": len(f.payload), "mediaType": "application/gzip", "os": "any", "arch": "any"}}}
	f.manifest, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	tenant, pkg, version, owner, instance := "019a0000-0000-7000-8000-000000000001", "019a0000-0000-7000-8000-000000000002", "019a0000-0000-7000-8000-000000000004", "019a0000-0000-7000-8000-000000000006", "019a0000-0000-7000-8000-000000000007"
	envelope := map[string]any{"schemaVersion": json.Number("2"), "tenantId": tenant, "packageId": pkg, "versionId": version, "releaseSequence": json.Number("7"), "notAfter": f.now.Add(time.Hour).Format(time.RFC3339Nano), "statement": map[string]any{"purpose": "resource", "kind": "skill", "name": "example-skill", "version": "1.0.0", "manifestDigest": udigest(f.manifest), "artifactDigests": []any{udigest(f.payload)}, "issuedAt": f.now.Add(-time.Minute).Format(time.RFC3339Nano)}}
	unifiedSign(t, envelope, key, "release")
	wire, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	e := new(resourcev1.UnifiedEnvelope)
	if err := protojson.Unmarshal(wire, e); err != nil {
		t.Fatal(err)
	}
	list := map[string]any{"schemaVersion": json.Number("1"), "tenantId": tenant, "revision": json.Number("1"), "issuedAt": f.now.Add(-time.Minute).Format(time.RFC3339Nano), "expiresAt": f.now.Add(time.Hour).Format(time.RFC3339Nano), "revokedKids": []any{}, "revokedArtifacts": []any{}}
	unifiedSign(t, list, revKey, "revocation")
	wire, err = json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	f.list = new(resourcev1.ResourceRevocationList)
	if err := protojson.Unmarshal(wire, f.list); err != nil {
		t.Fatal(err)
	}
	f.ref = &resourcev1.UnifiedResourceRef{InstallationRevision: 1, DesiredState: resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_ENABLED.Enum(), Identity: &resourcev1.UnifiedResourceIdentity{TenantId: tenant, PackageId: pkg, VersionId: version, Kind: "skill", Version: "1.0.0", ManifestDigest: udigest(f.manifest)}, Envelope: e, ReleaseSequence: 7, Enabled: true, ManifestDownloadPath: "/internal/resource/v1/catalog/downloads/019a0000-0000-7000-8000-000000000010/manifest", Artifacts: []*resourcev1.UnifiedArtifactDownload{{ArtifactId: "payload", Digest: udigest(f.payload), SizeBytes: uint64(len(f.payload)), DownloadPath: "/internal/resource/v1/catalog/downloads/019a0000-0000-7000-8000-000000000010/artifacts/payload"}}, Lease: &resourcev1.UnifiedLease{EnvelopeNotAfter: e.NotAfter, RevocationsExpiresAt: f.list.ExpiresAt, ExpiresAt: e.NotAfter, RevocationRevision: 1}}
	f.source = &unifiedSource{spec: &nodev1.InstanceSpec{InstanceId: instance, TenantId: tenant, OwnerId: owner, Generation: 1, ResourceSelectionRevision: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING, UnifiedResources: []*resourcev1.UnifiedResourceRef{f.ref}, ResourceRevocations: []*resourcev1.ResourceRevocationList{f.list}}}
	f.driver = &unifiedDriver{Fake: NewFake()}
	layout := &storage.Layout{InstancesDir: filepath.Join(t.TempDir(), "instances")}
	paths, err := layout.Ensure(instance, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Meta, "instance.token"), []byte("public-test-node-token"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Date", f.now.Format(http.TimeFormat))
		switch r.URL.Path {
		case f.ref.ManifestDownloadPath:
			_, _ = w.Write(f.manifest)
		case f.ref.Artifacts[0].DownloadPath:
			_, _ = w.Write(f.payload)
		default:
			w.WriteHeader(404)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig.Certificates = server.TLS.Certificates
	f.manager = &UnifiedManager{Layout: layout, Driver: f.driver, Specs: f.source, Origin: server.URL, Client: client, BootID: "019a0000-0000-7000-8000-000000000008", Now: func() time.Time { return f.now }, Mono: func() int64 { return f.mono }, Protocol: UnifiedProtocol{Keys: []*resourcev1.ResourceTrustKey{{Kid: "release", Purpose: "release", PublicKey: pub, NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"}, {Kid: "revocation", Purpose: "revocation", PublicKey: revPub, NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"}}}}
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer public-test-node-token" {
			w.WriteHeader(403)
			return
		}
		rows := []map[string]any{}
		if (f.catalogPrivate || f.catalogEmpty) && f.privateCollision {
			for _, preset := range []string{"cordis", "standard", "ptc"} {
				rows = append(rows, map[string]any{"name": "example-skill", "provider": "user", "path": "/home/dsh/.dsh/skills/example-skill/SKILL.md", "content": "Private colliding name.", "preset": preset})
			}
		}
		if !f.catalogPrivate && !f.catalogEmpty {
			for _, preset := range []string{"cordis", "standard", "ptc"} {
				provider := "auriga-shared"
				if f.override && preset == "ptc" {
					provider = "user"
				}
				rows = append(rows, map[string]any{"name": "example-skill", "provider": provider, "path": "/home/dsh/.dsh/auriga/skills-active/current/example-skill/SKILL.md", "content": "Public fixture body.", "preset": preset})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"privateOnly": f.catalogPrivate, "skills": rows})
	}))
	t.Cleanup(catalog.Close)
	f.driver.catalog = catalog.URL
	return f
}

func TestUnifiedManagerActivationObservationAndExpiry(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	f.root = filepath.Dir(filepath.Dir(current))
	if raw, err := os.ReadFile(filepath.Join(current, "example-skill", "SKILL.md")); err != nil || string(raw) != unifiedSkillText {
		t.Fatalf("activation bytes: %v", err)
	}
	if err := f.manager.AuthorizeStart(ctx, f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	reports, guardian, err := f.manager.Observe(ctx, f.source.spec.InstanceId)
	if err != nil || len(reports) != 1 || reports[0].State != "applied" || guardian.State != "active" {
		t.Fatalf("observation: %+v %+v %v", reports, guardian, err)
	}
	f.override = true
	reports, _, err = f.manager.Observe(ctx, f.source.spec.InstanceId)
	if err == nil || len(reports) != 0 {
		t.Fatalf("preset collision counted as applied: %+v %v", reports, err)
	}
	f.override = false
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal("same-generation replay", err)
	}
	f.now = f.now.Add(time.Hour)
	f.mono = 3600000
	err = f.manager.Guard(ctx, f.source.spec)
	if !errors.Is(err, ErrUnifiedPrivateOnly) {
		t.Fatalf("expiry did not isolate: %v", err)
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired view remains: %v", err)
	}
	f.catalogPrivate = true
	if err := f.manager.AuthorizeStart(ctx, f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	reports, guardian, err = f.manager.Observe(ctx, f.source.spec.InstanceId)
	if err != nil || !guardian.NewSessionRequired || reports[0].State != "expired" {
		t.Fatalf("private only: %+v %+v %v", reports, guardian, err)
	}
	state, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil || len(state.Watermarks) != 1 {
		t.Fatalf("high water lost: %v", err)
	}
}

func TestUnifiedManagerStopFailureRetriesAndRestartFailsClosed(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Ensure(ctx, &Spec{InstanceID: f.source.spec.InstanceId, Generation: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Start(ctx, f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	f.manager.BootID = unifiedNonce()
	f.driver.stopFails = true
	if err := f.manager.Guard(ctx, f.source.spec); err == nil {
		t.Fatal("stop failure accepted")
	}
	if _, err := os.Stat(filepath.Join(current, "example-skill", "SKILL.md")); err != nil {
		t.Fatal("removed before stop", err)
	}
	f.driver.stopFails = false
	if !errors.Is(f.manager.Guard(ctx, f.source.spec), ErrUnifiedPrivateOnly) {
		t.Fatal("cleanup did not retry")
	}
	s, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil || s.State != "private_only" {
		t.Fatalf("failed cleanup: %v", err)
	}
	actual, err := f.driver.Inspect(ctx, f.source.spec.InstanceId)
	if err != nil || actual.Running {
		t.Fatal("process still running")
	}
}

func TestUnifiedManagerRemovalTombstoneAndNoRedownload(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	f.ref.DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED.Enum()
	f.ref.InstallationRevision = 2
	ref := proto.Clone(f.ref).(*resourcev1.UnifiedResourceRef)
	ref.Enabled = false
	f.source.spec = proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	f.source.spec.Generation = 2
	f.source.spec.ResourceSelectionRevision = 2
	f.source.spec.UnifiedResources = []*resourcev1.UnifiedResourceRef{ref}
	f.manager.Origin = "https://offline.invalid"
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatal("remove left shared content", err)
	}
	f.catalogEmpty = true
	f.privateCollision = true
	reports, _, err := f.manager.Observe(ctx, f.source.spec.InstanceId)
	if err != nil || reports[0].InstallationRevision != 2 || reports[0].State != "removed" || reports[0].Generation != 2 {
		t.Fatalf("wrong removal acknowledgement: %+v %v", reports, err)
	}
}

func TestUnifiedArchiveRejectsUnsafeAndExecutableContent(t *testing.T) {
	for _, name := range []string{"../SKILL.md", "/SKILL.md", "references/CON.txt", "references/CONIN$.md", "references/CONOUT$.txt", "references/a//b.md", "assets/payload.sh", "assets/archive.tar.gz"} {
		t.Run(name, func(t *testing.T) {
			archive := skillArchive(t, map[string]string{"SKILL.md": unifiedSkillText, name: "unsafe"})
			if err := ExtractUnifiedSkill(archive, filepath.Join(t.TempDir(), "stage")); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	for _, name := range []string{"references/COM¹.txt", "references/LPT².md", "references/COM³.txt"} {
		if _, err := unifiedArchivePath(name); err == nil {
			t.Fatalf("Windows reserved alias admitted: %s", name)
		}
	}
	for _, raw := range []string{"---\na: 1\na: 2\n---\nBody", "---\na: &x test\nb: *x\n---\nBody"} {
		if err := unifiedSkillFrontMatter([]byte(raw)); err == nil {
			t.Fatal("ambiguous YAML accepted")
		}
	}
}

func TestUnifiedManagerDowngradeRetainsHighWaterAndRejectsReplay(t *testing.T) {
	f := newUnifiedFixture(t)
	s, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	key := f.source.spec.TenantId + "/" + f.ref.Identity.PackageId
	s.Watermarks[key] = map[string]any{"tenantId": f.source.spec.TenantId, "packageId": f.ref.Identity.PackageId, "releaseSequence": json.Number("8"), "versionId": "019a0000-0000-7000-8000-000000000009", "verifiedDigestAliases": []any{"sha256:previous"}}
	if err := f.manager.save(root, s); err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.manager.Protocol.Keys = append(f.manager.Protocol.Keys, &resourcev1.ResourceTrustKey{Kid: "downgrade", Purpose: "downgrade", PublicKey: pub, NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"})
	id, err := unifiedProto(f.ref.Identity)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"schemaVersion": json.Number("2"), "purpose": "downgrade", "authorizationId": "019a0000-0000-7000-8000-000000000010", "instanceId": f.source.spec.InstanceId, "identity": id, "fromReleaseSequence": json.Number("8"), "toReleaseSequence": json.Number("7"), "issuedAt": f.now.Add(-time.Minute).Format(time.RFC3339Nano), "expiresAt": f.now.Add(5 * time.Minute).Format(time.RFC3339Nano), "reason": "Public fixture"}
	unifiedSign(t, body, private, "downgrade")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	authorization := new(resourcev1.UnifiedDowngradeAuthorization)
	if err := protojson.Unmarshal(raw, authorization); err != nil {
		t.Fatal(err)
	}
	f.source.spec.UnifiedDowngrades = []*resourcev1.UnifiedDowngradeAuthorization{authorization}
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	s, _, _, err = f.manager.load(f.source.spec.InstanceId)
	if err != nil || un(s.Watermarks[key]["releaseSequence"]) != 8 || len(s.UsedAuthorizations) != 1 {
		t.Fatal("downgrade lost high water", err)
	}
	f.ref.InstallationRevision = 2
	f.source.spec = proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	f.source.spec.Generation = 2
	f.source.spec.ResourceSelectionRevision = 2
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err == nil {
		t.Fatal("used downgrade authorization replayed")
	}
}

func TestUnifiedManagerRecoversCrashAfterQuarantineRename(t *testing.T) {
	f := newUnifiedFixture(t)
	current, err := f.manager.Prepare(context.Background(), f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	s, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	s.PendingIsolation = unifiedNonce()
	s.State = "isolating"
	q := filepath.Join(root, "skills-active", "quarantine", s.PendingIsolation)
	if err := os.MkdirAll(filepath.Dir(q), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(current, q); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.save(root, s); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Guard(context.Background(), f.source.spec); !errors.Is(err, ErrUnifiedPrivateOnly) {
		t.Fatal("isolation recovery failed", err)
	}
	s, _, _, err = f.manager.load(f.source.spec.InstanceId)
	if err != nil || s.PendingIsolation != "" || s.State != "private_only" {
		t.Fatal("uncleared recovery log", err)
	}
}

func TestUnifiedDockerMountMustBeReadOnlyAndOwned(t *testing.T) {
	d := &Docker{DataRoot: t.TempDir()}
	id := "instance"
	source := filepath.Join(d.DataRoot, "instances", id, "resource-state", "skills-active", "current")
	mount := map[string]any{"Source": source, "Destination": "/home/dsh/.dsh/auriga/skills-active/current", "Type": "bind", "RW": false}
	inspection := map[string]any{"Mounts": []any{mount}, "Config": map[string]any{"Entrypoint": []string{"node", renderer.ContainerResourceSupervisor}, "Cmd": []string{renderer.ContainerResourcePolicy, "node", "dsh.mjs"}}, "HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "no"}}}
	if err := d.verifyUnifiedMount(inspection, id); err != nil {
		t.Fatal(err)
	}
	inspection["HostConfig"].(map[string]any)["RestartPolicy"] = map[string]any{"Name": "always"}
	if err := d.verifyUnifiedMount(inspection, id); err == nil {
		t.Fatal("automatic restart bypassed guardian")
	}
	inspection["HostConfig"].(map[string]any)["RestartPolicy"] = map[string]any{"Name": "no"}
	mount["RW"] = true
	if err := d.verifyUnifiedMount(inspection, id); err == nil {
		t.Fatal("writable shared mount accepted")
	}
	mount["RW"] = false
	mount["Source"] = filepath.Join(d.DataRoot, "other")
	if err := d.verifyUnifiedMount(inspection, id); err == nil {
		t.Fatal("unowned shared root accepted")
	}
}
