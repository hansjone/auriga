package runtime

import (
	"context"
	"fmt"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type poolSignedSource struct {
	f     *unifiedFixture
	views map[string]*nodev1.SharedDshResourceView
	bad   bool
}

func (s *poolSignedSource) Snapshot(_ context.Context, b *nodev1.SharedDshBinding) (*workspacev1.GetSharedDshResourceSnapshotResponse, error) {
	return &workspacev1.GetSharedDshResourceSnapshotResponse{View: s.views[b.PlacementId], Resources: []*resourcev1.UnifiedResourceRef{s.f.ref}, Revocations: s.f.list}, nil
}
func (s *poolSignedSource) Artifact(_ context.Context, b *nodev1.SharedDshBinding, lease, artifact string, _ int64) ([]byte, time.Time, error) {
	view := s.views[b.PlacementId]
	if view == nil || view.Resources[0].LeaseId != lease {
		return nil, time.Time{}, fmt.Errorf("lease mismatch")
	}
	if s.bad {
		return []byte("tampered"), s.f.now, nil
	}
	if artifact == "" {
		return s.f.manifest, s.f.now, nil
	}
	return s.f.payload, s.f.now, nil
}
func poolSignedFixture(t *testing.T) (*PoolMaterializer, *poolSignedSource, []*nodev1.SharedDshResourceView) {
	f := newUnifiedFixture(t)
	source := &poolSignedSource{f: f, views: map[string]*nodev1.SharedDshResourceView{}}
	m := &PoolMaterializer{Layout: &storage.PoolLayout{Root: filepath.Join(t.TempDir(), "pools")}, Source: source, Unified: f.manager}
	views := []*nodev1.SharedDshResourceView{}
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		b := &nodev1.SharedDshBinding{TenantId: f.ref.Identity.TenantId, MemberId: id, PlacementId: id, PhysicalInstanceId: f.source.spec.InstanceId, Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1}
		view := &nodev1.SharedDshResourceView{SchemaVersion: 1, Binding: b, ViewId: id, Source: "platform", AuthorizationSetDigest: "sha256:" + strings.Repeat("a", 64), Resources: []*nodev1.SharedDshResource{{ResourceId: f.ref.Identity.PackageId, Digest: f.ref.Identity.ManifestDigest, LeaseId: id, ExpiresAt: f.now.Add(time.Hour).Unix()}}}
		source.views[id] = view
		views = append(views, view)
	}
	return m, source, views
}
func TestPoolMaterializerSignedMembersRenewAndDetectMutation(t *testing.T) {
	m, s, views := poolSignedFixture(t)
	pool := views[0].Binding.PhysicalInstanceId
	ctx := context.Background()
	bindings := []*nodev1.SharedDshBinding{views[0].Binding, views[1].Binding}
	if e := CheckPoolViews(bindings, views, s.f.now.Unix()); e != nil {
		t.Fatal("distinct per-member real lease IDs rejected", e)
	}
	if e := m.Verify(ctx, pool, views); e != nil {
		t.Fatal(e)
	}
	root, active, _ := m.paths(pool)
	skill := filepath.Join(active, "example-skill", "SKILL.md")
	raw, e := os.ReadFile(skill)
	if e != nil || string(raw) != unifiedSkillText {
		t.Fatalf("signed bytes not materialized: %v", e)
	}
	s.f.now = s.f.now.Add(time.Minute)
	if e = m.Verify(ctx, pool, views); e != nil {
		t.Fatal("unchanged lease verification", e)
	}
	if e = os.WriteFile(skill, []byte("modified"), 0644); e != nil {
		t.Fatal(e)
	}
	if m.Verify(ctx, pool, views) == nil {
		t.Fatal("changed mounted bytes accepted")
	}
	if e = m.Cleanup(ctx, pool); e != nil {
		t.Fatal(e)
	}
	var state poolResourceState
	if e = unifiedRead(filepath.Join(root, "state.json"), &state); e != nil || len(state.Watermarks) != 1 {
		t.Fatal("cleanup lost high water", e)
	}
	entries, e := os.ReadDir(active)
	if e != nil || len(entries) != 0 {
		t.Fatal("cleanup left an active view", e)
	}
}
func TestPoolMaterializerRejectsWrongLeaseDigestSignatureAndExpiredView(t *testing.T) {
	for _, failure := range []string{"binding", "digest", "signature", "lease", "expired", "payload"} {
		t.Run(failure, func(t *testing.T) {
			m, s, views := poolSignedFixture(t)
			pool := views[0].Binding.PhysicalInstanceId
			switch failure {
			case "binding":
				wrong := proto.Clone(views[0]).(*nodev1.SharedDshResourceView)
				wrong.Binding.Revision++
				s.views[views[0].Binding.PlacementId] = wrong
			case "digest":
				views[0].Resources[0].Digest = "sha256:bad"
			case "signature":
				s.f.ref.Envelope.NotAfter = s.f.now.Add(2 * time.Hour).Format(time.RFC3339Nano)
			case "lease":
				views[0].Resources[0].ExpiresAt++
			case "expired":
				s.f.now = s.f.now.Add(2 * time.Hour)
			case "payload":
				s.bad = true
			}
			if m.Verify(context.Background(), pool, views) == nil {
				t.Fatal("invalid signed pool material accepted")
			}
		})
	}
}
