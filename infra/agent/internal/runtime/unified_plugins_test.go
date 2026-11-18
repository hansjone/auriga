package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func newUnifiedPluginFixture(t *testing.T) *unifiedFixture {
	t.Helper()
	f := newUnifiedFixture(t)
	manifest, err := DecodeUnifiedJSON(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	delete(manifest, "skill")
	manifest["kind"] = "plugin"
	manifest["plugin"] = map[string]any{"packageName": "@example/catalog-probe", "dshVersionRange": ">=0.2.0"}
	f.payload = skillArchive(t, map[string]string{"package/package.json": `{"name":"@example/catalog-probe","version":"1.0.0","dsh":{"bundle":{"patch":"cordis.patch.yml"}}}`, "package/cordis.patch.yml": "[]"})
	artifact := um(ua(manifest["artifacts"])[0])
	artifact["digest"], artifact["sizeBytes"] = udigest(f.payload), len(f.payload)
	f.manifest, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.ref.Identity.Kind, f.ref.Identity.ManifestDigest = "plugin", udigest(f.manifest)
	f.ref.Artifacts[0].Digest, f.ref.Artifacts[0].SizeBytes = udigest(f.payload), uint64(len(f.payload))
	envelope, err := unifiedProto(f.ref.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	delete(envelope, "signature")
	statement := um(envelope["statement"])
	statement["kind"], statement["manifestDigest"], statement["artifactDigests"] = "plugin", udigest(f.manifest), []any{udigest(f.payload)}
	unifiedSign(t, envelope, f.releasePrivate, "release")
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	f.ref.Envelope = new(resourcev1.UnifiedEnvelope)
	if err := protojson.Unmarshal(raw, f.ref.Envelope); err != nil {
		t.Fatal(err)
	}
	return f
}

func NewUnifiedRequiredPluginFixture(t *testing.T) (*UnifiedManager, *nodev1.InstanceSpec) {
	t.Helper()
	f := newUnifiedPluginFixture(t)
	f.ref.InstallationMode = "required"
	f.source.spec.AllowPersonalPlugins = new(bool)
	return f.manager, f.source.spec
}

func TestUnifiedPluginNativeGovernanceDoesNotIsolate(t *testing.T) {
	for _, mode := range []string{"required", "default", "optional"} {
		for _, phase := range []string{"native", "cleanup"} {
			t.Run(mode+"/"+phase, func(t *testing.T) {
				f := newUnifiedPluginFixture(t)
				f.ref.InstallationMode = mode
				f.source.spec.AllowPersonalPlugins = new(bool)
				if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
					t.Fatal(err)
				}
				s, _, _, err := f.manager.load(f.source.spec.InstanceId)
				if err != nil {
					t.Fatal(err)
				}
				policy, err := json.Marshal(f.manager.sessionPolicy(s))
				if err != nil {
					t.Fatal(err)
				}
				helper, err := filepath.Abs("../renderer/resource_plugins.mjs")
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("node", "testdata/unified-plugin-governance.mjs", helper, string(policy), phase, base64.StdEncoding.EncodeToString(f.payload))
				output, err := cmd.Output()
				if err != nil {
					var execution *exec.ExitError
					if errors.As(err, &execution) {
						t.Fatalf("native governance: %v %s", err, execution.Stderr)
					}
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(output) }))
				t.Cleanup(server.Close)
				f.driver.catalog = server.URL
				stops := f.driver.stops
				reports, _, err := f.manager.Observe(context.Background(), f.source.spec.InstanceId)
				if err != nil || len(reports) != 1 || reports[0].State != "applied" {
					t.Fatalf("guarded operation changed observation: %+v %v", reports, err)
				}
				s, _, _, err = f.manager.load(f.source.spec.InstanceId)
				if err != nil || s.State != "active" || f.driver.stops != stops {
					t.Fatal("guarded native operation isolated or stopped DSH", err)
				}
			})
		}
	}
}

func TestUnifiedPluginAndSupervisorNodeContracts(t *testing.T) {
	cmd := exec.Command("node", "--test", "../renderer/resource_plugins.test.mjs", "../renderer/resource_supervisor.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("supervisor cleanup: %v %s", err, output)
	}
}

func TestUnifiedPluginUsesNPMNameAndRealObservation(t *testing.T) {
	f := newUnifiedPluginFixture(t)
	f.source.spec.AllowPersonalPlugins = new(bool)
	f.ref.InstallationMode = "required"
	current, err := f.manager.Prepare(context.Background(), f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatal("plugin was unpacked into the skill root", err)
	}
	paths, err := f.manager.Layout.For(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(paths.Meta, "catalog-plugins", strings.TrimPrefix(f.ref.Artifacts[0].Digest, "sha256:")+".tgz"))
	if err != nil || udigest(payload) != udigest(f.payload) {
		t.Fatal("verified tgz not staged", err)
	}
	s, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	policy := f.manager.sessionPolicy(s)
	if policy["allowPersonalPlugins"] != false || len(s.ManagedPluginNames) != 1 || s.ManagedPluginNames[0] != "@example/catalog-probe" {
		t.Fatalf("bad public plan: %+v", policy)
	}
	reports, _, err := f.manager.Status(f.source.spec.InstanceId)
	if err != nil || reports != nil {
		t.Fatal("prepare fabricated applied evidence", err)
	}
	var loaded atomic.Bool
	loaded.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer public-test-node-token" {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"privateOnly": false, "skills": []any{}, "plugins": []any{map[string]any{"name": "@example/catalog-probe", "version": "1.0.0", "enabled": true, "installed": true, "loaded": loaded.Load()}}})
	}))
	t.Cleanup(server.Close)
	f.driver.catalog = server.URL
	reports, _, err = f.manager.Observe(context.Background(), f.source.spec.InstanceId)
	if err != nil || len(reports) != 1 || reports[0].State != "applied" {
		t.Fatalf("plugin not observed: %+v %v", reports, err)
	}
	loaded.Store(false)
	if _, _, err = f.manager.Observe(context.Background(), f.source.spec.InstanceId); err == nil {
		t.Fatal("unloaded plugin counted applied")
	}
	s, _, _, err = f.manager.load(f.source.spec.InstanceId)
	if err != nil || s.State != "private_only" {
		t.Fatal("failed observation did not isolate", err)
	}
}

func TestUnifiedRevocationDowngradeFromRefBindsOwnerAndRetainsOneUse(t *testing.T) {
	f := newUnifiedPluginFixture(t)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.manager.Protocol.Keys = append(f.manager.Protocol.Keys, &resourcev1.ResourceTrustKey{Kid: "downgrade", Purpose: "downgrade", PublicKey: pub, NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"})
	identity, err := unifiedProto(f.ref.Identity)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"schemaVersion": json.Number("2"), "purpose": "downgrade", "authorizationId": "019a0000-0000-7000-8000-000000000010", "instanceId": f.source.spec.InstanceId, "ownerId": f.source.spec.OwnerId, "identity": identity, "fromReleaseSequence": json.Number("8"), "toReleaseSequence": json.Number("7"), "issuedAt": f.now.Format(time.RFC3339Nano), "expiresAt": f.now.Add(10 * time.Minute).Format(time.RFC3339Nano), "reason": "revocation"}
	unifiedSign(t, body, key, "downgrade")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	auth := new(resourcev1.UnifiedDowngradeAuthorization)
	if err := protojson.Unmarshal(raw, auth); err != nil {
		t.Fatal(err)
	}
	f.ref.DowngradeAuthorization = auth
	s := &unifiedNodeState{Watermarks: map[string]map[string]any{f.source.spec.TenantId + "/" + f.ref.Identity.PackageId: {"releaseSequence": json.Number("8")}}}
	if grant, err := f.manager.authorizeDowngrade(f.source.spec, s, f.ref, 1, unifiedInstalled{}); err != nil || grant != auth.AuthorizationId {
		t.Fatal(grant, err)
	}
	original := f.source.spec.OwnerId
	f.source.spec.OwnerId = "019a0000-0000-7000-8000-000000000012"
	if _, err := f.manager.authorizeDowngrade(f.source.spec, s, f.ref, 1, unifiedInstalled{}); err == nil {
		t.Fatal("another member reused authorization")
	}
	f.source.spec.OwnerId = original
	s.UsedAuthorizations = []string{auth.AuthorizationId}
	if _, err := f.manager.authorizeDowngrade(f.source.spec, s, f.ref, 1, unifiedInstalled{}); err == nil {
		t.Fatal("single-use authorization replayed")
	}
}

func TestUnifiedPluginGuardianExpiresAndRemembersCleanup(t *testing.T) {
	f := newUnifiedPluginFixture(t)
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	f.mono = 3600000
	if err := f.manager.Guard(context.Background(), f.source.spec); !errors.Is(err, ErrUnifiedPrivateOnly) {
		t.Fatal(err)
	}
	s, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	policy := f.manager.sessionPolicy(s)
	if !s.newSessionRequired() || len(s.ManagedPluginNames) != 1 || policy["leaseExpiresAt"] != "" {
		t.Fatalf("missing restart cleanup: %+v", policy)
	}
	if _, err := unifiedObservePlugin(s, s.Installations[0], f.ref, []unifiedPluginObservation{{Name: s.ManagedPluginNames[0], Enabled: true, Loaded: true}}); err == nil {
		t.Fatal("active revoked plugin accepted")
	}
	if state, err := unifiedObservePlugin(s, s.Installations[0], f.ref, nil); err != nil || state != "expired" {
		t.Fatal(state, err)
	}
}

func TestUnifiedPluginRejectsHistoricalUnselectedActivation(t *testing.T) {
	f := newUnifiedPluginFixture(t)
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	s, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	s.ManagedPluginNames = append(s.ManagedPluginNames, "obsolete-plugin")
	if err := f.manager.save(root, s); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"privateOnly": false, "skills": []any{}, "plugins": []any{
			map[string]any{"name": "@example/catalog-probe", "version": "1.0.0", "enabled": true, "installed": true, "loaded": true},
			map[string]any{"name": "obsolete-plugin", "version": "1.0.0", "enabled": true, "installed": true, "loaded": true},
		}})
	}))
	t.Cleanup(server.Close)
	f.driver.catalog = server.URL
	if _, _, err := f.manager.Observe(context.Background(), f.source.spec.InstanceId); err == nil {
		t.Fatal("unselected historical plugin accepted")
	}
	s, _, _, err = f.manager.load(f.source.spec.InstanceId)
	if err != nil || s.State != "private_only" {
		t.Fatal("unselected activation did not isolate", err)
	}
}
