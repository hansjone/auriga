package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
)

func TestUnifiedRenewalExtendsRunningDeadlineWithoutResettingCAS(t *testing.T) {
	f := newUnifiedFixture(t)
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	stops := f.driver.stops
	f.now = f.now.Add(10 * time.Minute)
	f.mono = 600000
	end := f.now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	envelope, err := unifiedProto(f.ref.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	delete(envelope, "signature")
	envelope["notAfter"] = end
	unifiedSign(t, envelope, f.releasePrivate, "release")
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(raw, f.ref.Envelope); err != nil {
		t.Fatal(err)
	}
	list, err := unifiedProto(f.list)
	if err != nil {
		t.Fatal(err)
	}
	delete(list, "signature")
	list["revision"] = json.Number("2")
	list["expiresAt"] = end
	unifiedSign(t, list, f.revPrivate, "revocation")
	raw, err = json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(raw, f.list); err != nil {
		t.Fatal(err)
	}
	f.ref.Lease.ExpiresAt = end
	f.ref.Lease.EnvelopeNotAfter = end
	f.ref.Lease.RevocationsExpiresAt = end
	f.ref.Lease.RevocationRevision = 2
	f.source.spec.Generation++
	f.ref.ManifestDownloadPath = "/internal/resource/v1/catalog/downloads/renewed/manifest"
	f.ref.Artifacts[0].DownloadPath = "/internal/resource/v1/catalog/downloads/renewed/artifacts/payload"
	if err := f.manager.Guard(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	s, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	if f.driver.stops != stops || s.LeaseExpiresAt != end || s.Installations[0].InstallationRevision != 1 || s.Generation != 2 || s.SelectionRevision != 1 || len(s.Watermarks) != 1 {
		t.Fatalf("renewal changed running installation: %+v stops=%d", s, f.driver.stops-stops)
	}
	paths, err := f.manager.Layout.For(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		SchemaVersion      int    `json:"schemaVersion"`
		NewSessionRequired bool   `json:"newSessionRequired"`
		LeaseExpiresAt     string `json:"leaseExpiresAt"`
		TrustedTime        string `json:"trustedTime"`
	}
	if err := unifiedRead(filepath.Join(paths.Meta, "resource-session-policy.json"), &policy); err != nil || policy.LeaseExpiresAt != end {
		t.Fatal("supervisor renewal not published", err)
	}
	receipts, err := filepath.Glob(filepath.Join(root, "installations", "*", "*", "*", "skill", "*", "*", "1.json"))
	if err != nil || len(receipts) != 1 {
		t.Fatal("installation receipt missing", err)
	}
}

func TestUnifiedCorruptLedgerStopsExistingProcess(t *testing.T) {
	f := newUnifiedFixture(t)
	if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	_, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	before := f.driver.stops
	if err := os.WriteFile(filepath.Join(root, "node-state.json"), []byte(`{"schemaVersion":1,"schemaVersion":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Guard(context.Background(), f.source.spec); err == nil || f.driver.stops <= before {
		t.Fatal("invalid state did not stop process", err)
	}
}

func TestUnifiedTombstoneCollectionRetainsInstallationCAS(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal(err)
	}
	f.state = "REMOVED"
	f.revision = 2
	f.ref.Enabled = false
	f.source.spec.Generation = 2
	f.source.spec.ResourceSelectionRevision = 2
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal(err)
	}
	f.source.spec.Generation = 3
	f.source.spec.UnifiedResources = nil
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal(err)
	}
	f.state = "ENABLED"
	f.revision = 1
	f.ref.Enabled = true
	f.source.spec.Generation = 4
	f.source.spec.ResourceSelectionRevision = 3
	f.source.spec.UnifiedResources = append(f.source.spec.UnifiedResources, f.ref)
	// Private fallback may keep the instance usable, but cannot reactivate the
	// collected grant or erase its installation revision high-water mark.
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal("private fallback", err)
	}
	s, _, current, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 || s.State != "private_only" {
		t.Fatal("collected revision reactivated", err)
	}
	key := f.source.spec.TenantId + "/" + f.source.spec.OwnerId + "/" + f.ref.Identity.PackageId
	if s.InstallationRevisions[key] != 2 {
		t.Fatal("installation CAS lost")
	}
}

func TestUnifiedIdentitySwitchIsolatesSharedAndPreservesPrivate(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := f.manager.Layout.For(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(paths.Home, ".dsh", "skills", "private-probe", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(private), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("private skill"), 0600); err != nil {
		t.Fatal(err)
	}
	f.source.spec.OwnerId = "019a0000-0000-7000-8000-000000000099"
	f.source.spec.Generation++
	if err := f.manager.Guard(ctx, f.source.spec); err == nil {
		t.Fatal("identity switch kept old view")
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatal("old identity shared view remains", err)
	}
	if raw, err := os.ReadFile(private); err != nil || string(raw) != "private skill" {
		t.Fatal("private skill modified", err)
	}
	if _, err := f.manager.Prepare(ctx, f.source.spec); err != nil {
		t.Fatal("fresh owner activation", err)
	}
	s, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil || s.Owner != f.source.spec.OwnerId || s.State != "active" {
		t.Fatal("new owner not bound", err)
	}
}

func TestUnifiedExistingPrivateExecutionNeedsNoNewLedger(t *testing.T) {
	f := newUnifiedFixture(t)
	f.source.spec.UnifiedResources = nil
	if err := f.manager.AuthorizeStart(context.Background(), f.source.spec.InstanceId); err != nil {
		t.Fatal("ordinary runtime recovery regressed", err)
	}
}
