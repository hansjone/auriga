package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

type poolQuotaDouble struct{ fail bool }

func (q *poolQuotaDouble) Ensure(_ context.Context, _ string, b, i int64) error {
	if q.fail || b != 5368709120 || i != 100000 {
		return errors.New("quota unavailable")
	}
	return nil
}

type poolSupervisorDouble struct {
	stopped, deny bool
	stops         int
}

func (s *poolSupervisorDouble) Stop(context.Context, string, time.Duration) error {
	s.stops++
	s.stopped = !s.deny
	return nil
}
func (s *poolSupervisorDouble) ResourceProcessStopped(context.Context, string) (bool, error) {
	return s.stopped, nil
}

type poolSessionDouble struct {
	fail     bool
	discards int
}

func (s *poolSessionDouble) Cancel(context.Context, *nodev1.SharedDshBinding) error {
	if s.fail {
		return errors.New("timeout")
	}
	return nil
}
func (s *poolSessionDouble) Discard(context.Context, *nodev1.SharedDshBinding) error {
	s.discards++
	return nil
}

type poolViewDouble struct{ fail bool }

type blockedPoolSessions struct{ release chan struct{} }

func (s *blockedPoolSessions) Cancel(context.Context, *nodev1.SharedDshBinding) error {
	<-s.release
	return nil
}
func (s *blockedPoolSessions) Discard(context.Context, *nodev1.SharedDshBinding) error { return nil }
func TestSharedPoolCancelDeadlineIgnoresBlockedHost(t *testing.T) {
	m, s, _ := newPoolTest(t)
	host := &blockedPoolSessions{release: make(chan struct{})}
	defer close(host.release)
	m.Sessions = host
	applyPool(t, m, poolIntent("prepare-empty", "p", "alice"))
	s.stopped = false
	start := time.Now()
	r := applyPool(t, m, poolIntent("cooperative-cancel", "cancel", "alice"))
	if elapsed := time.Since(start); elapsed < 29*time.Second || elapsed > 40*time.Second {
		t.Fatal("cancel deadline", elapsed)
	}
	if r.Outcome != "unconfirmed" || !r.ExecutionTreeStopped || s.stops != 1 {
		t.Fatal(r)
	}
	impacts, e := m.GuardianImpacts(poolID("pool"))
	if e != nil || len(impacts) != 1 || impacts[0].Scope != "pool" || impacts[0].Reason != "cancel-timeout" || impacts[0].State != "stopped" {
		t.Fatal(impacts, e)
	}
}

func (v poolViewDouble) Verify(context.Context, string, []*nodev1.SharedDshResourceView) error {
	if v.fail {
		return errors.New("revoked")
	}
	return nil
}
func (v poolViewDouble) Cleanup(context.Context, string) error {
	if v.fail {
		return errors.New("cleanup")
	}
	return nil
}
func poolID(label string) string {
	h := sha256.Sum256([]byte(label))
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
func poolIntent(action, id, member string) *nodev1.SharedDshRuntimeIntent {
	return &nodev1.SharedDshRuntimeIntent{SchemaVersion: 1, OperationId: poolID(id), Binding: &nodev1.SharedDshBinding{TenantId: poolID("tenant"), MemberId: poolID(member), PlacementId: poolID("placement-" + member), PhysicalInstanceId: poolID("pool"), Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1}, Action: action, CancelWaitSeconds: 30, ArchiveRetentionSeconds: 2592000, WorkspacePolicy: "empty", SessionPolicy: "discard"}
}
func newPoolTest(t *testing.T) (*SharedPools, *poolSupervisorDouble, *poolQuotaDouble) {
	t.Helper()
	s := &poolSupervisorDouble{stopped: true}
	q := &poolQuotaDouble{}
	return &SharedPools{Layout: &storage.PoolLayout{Root: t.TempDir(), Quota: q}, Supervisor: s, Sessions: &poolSessionDouble{}, Now: func() time.Time { return time.Unix(1000, 0) }}, s, q
}
func applyPool(t *testing.T, m *SharedPools, i *nodev1.SharedDshRuntimeIntent) *nodev1.SharedDshRuntimeReceipt {
	t.Helper()
	r, e := m.Apply(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestSharedPoolDirectoriesArchiveAndRestart(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newPoolTest(t)
	in := poolIntent("prepare-empty", "prepare", "alice")
	if r := applyPool(t, m, in); !r.WorkspaceEmpty || r.Outcome != "confirmed" {
		t.Fatal(r)
	}
	p, _ := m.Layout.Workspace(poolID("pool"), poolID("tenant"), poolID("alice"), 1)
	if e := os.WriteFile(filepath.Join(p, "user.txt"), []byte("data"), 0600); e != nil {
		t.Fatal(e)
	}
	// A repeated prepare returns the original receipt, never empties user data.
	r := applyPool(t, m, in)
	if !r.WorkspaceEmpty {
		t.Fatal(r)
	}
	archive := poolIntent("archive-reset", "archive", "alice")
	r = applyPool(t, m, archive)
	if r.Outcome != "confirmed" || r.ArchiveExpiresAt != r.ObservedAt+2592000 {
		t.Fatal(r)
	}
	entries, e := os.ReadDir(p)
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
	a, _ := m.Layout.Path(poolID("pool"), "control", "archives", poolID("archive"))
	if _, e = os.Stat(filepath.Join(a, "user.txt")); e != nil {
		t.Fatal(e)
	}
	restarted := &SharedPools{Layout: m.Layout, Supervisor: m.Supervisor, Sessions: m.Sessions, Now: m.Now}
	if !proto.Equal(r, applyPool(t, restarted, archive)) {
		t.Fatal("receipt changed across restart")
	}
	changed := proto.Clone(archive).(*nodev1.SharedDshRuntimeIntent)
	changed.Action = "stop-pool"
	if _, e = restarted.Apply(ctx, changed); e == nil {
		t.Fatal("operation reused")
	}
	restarted.Now = func() time.Time { return time.Unix(r.ArchiveExpiresAt, 0) }
	if e = restarted.SweepArchives(ctx, poolID("pool")); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(a); !os.IsNotExist(e) {
		t.Fatal("archive retained", e)
	}
}
func TestSharedPoolRejectsCrossTenantEpochAndQuota(t *testing.T) {
	m, s, q := newPoolTest(t)
	in := poolIntent("prepare-empty", "p", "alice")
	applyPool(t, m, in)
	bad := poolIntent("prepare-empty", "x", "bob")
	bad.Binding.TenantId = poolID("other")
	if _, e := m.Apply(context.Background(), bad); e == nil {
		t.Fatal("cross tenant")
	}
	for _, value := range []string{"../escape", `a\b`, "/tmp", "a:b"} {
		bad = poolIntent("prepare-empty", "x", "bob")
		bad.Binding.MemberId = value
		if _, e := m.Apply(context.Background(), bad); e == nil {
			t.Fatal(value)
		}
	}
	bad = poolIntent("stop-pool", "old", "alice")
	bad.Binding.Revision = 0
	if _, e := m.Apply(context.Background(), bad); e == nil {
		t.Fatal("revision zero")
	}
	q.fail = true
	if r := applyPool(t, m, poolIntent("prepare-empty", "q", "bob")); r.Outcome != "unconfirmed" {
		t.Fatal(r)
	}
	s.stopped = false
	if r := applyPool(t, m, poolIntent("archive-reset", "archive", "alice")); r.Outcome != "unconfirmed" {
		t.Fatal(r)
	}
}
func poolView(b *nodev1.SharedDshBinding) *nodev1.SharedDshResourceView {
	return &nodev1.SharedDshResourceView{SchemaVersion: 1, Binding: b, ViewId: poolID("view-" + b.MemberId), Source: "platform", AuthorizationSetDigest: "sha256:" + strings.Repeat("a", 64), Resources: []*nodev1.SharedDshResource{{ResourceId: poolID("skill"), LeaseId: poolID("lease"), Digest: "sha256:" + strings.Repeat("b", 64), ExpiresAt: 2000}}}
}
func TestSharedPoolGuardianAffectsAllAndBlocksRecovery(t *testing.T) {
	ctx := context.Background()
	m, s, _ := newPoolTest(t)
	a := poolIntent("prepare-empty", "a", "alice")
	b := poolIntent("prepare-empty", "b", "bob")
	applyPool(t, m, a)
	applyPool(t, m, b)
	views := []*nodev1.SharedDshResourceView{poolView(a.Binding), poolView(b.Binding)}
	if e := m.RecoverPool(ctx, poolID("pool"), poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
	if e := m.AuthorizePoolStart(ctx, poolID("pool"), views, poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
	s.stopped = false
	s.deny = true
	impact, e := m.Trip(ctx, poolID("pool"), poolID("expire"), "resource-expired")
	if e != nil || impact.State != "blocked" || len(impact.Affected) != 2 {
		t.Fatal(impact, e)
	}
	if e = m.AuthorizePoolStart(ctx, poolID("pool"), views, poolViewDouble{}); e == nil {
		t.Fatal("contaminated start")
	}
	if e = m.RecoverPool(ctx, poolID("pool"), poolViewDouble{}); e == nil {
		t.Fatal("unconfirmed recovery")
	}
	s.deny = false
	impact, e = m.Trip(ctx, poolID("pool"), poolID("expire"), "resource-expired")
	if e != nil || impact.State != "stopped" {
		t.Fatal(impact, e)
	}
	if e = m.RecoverPool(ctx, poolID("pool"), poolViewDouble{fail: true}); e == nil {
		t.Fatal("cleanup ignored")
	}
	if e = m.RecoverPool(ctx, poolID("pool"), poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
	if m.Sessions.(*poolSessionDouble).discards != 4 {
		t.Fatal("sessions restored")
	}
	if e = m.AuthorizePoolStart(ctx, poolID("pool"), views, poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
	views[1].Resources[0].ExpiresAt = 1000
	if e = m.AuthorizePoolStart(ctx, poolID("pool"), views, poolViewDouble{}); e == nil {
		t.Fatal("expiry boundary")
	}
}
func TestSharedPoolCancelFailureStopsPool(t *testing.T) {
	m, s, _ := newPoolTest(t)
	m.Sessions = &poolSessionDouble{fail: true}
	applyPool(t, m, poolIntent("prepare-empty", "p", "alice"))
	s.stopped = false
	r := applyPool(t, m, poolIntent("cooperative-cancel", "cancel", "alice"))
	if r.Outcome != "unconfirmed" || !r.ExecutionTreeStopped || s.stops != 1 {
		t.Fatal(r)
	}
}
func TestSharedPoolResourceSelectionMismatch(t *testing.T) {
	a, b := poolIntent("prepare-empty", "a", "alice").Binding, poolIntent("prepare-empty", "b", "bob").Binding
	va, vb := poolView(a), poolView(b)
	for _, mutate := range []func(){func() { vb.Source = "personal" }, func() { vb.AuthorizationSetDigest = "sha256:" + strings.Repeat("c", 64) }, func() { vb.Resources[0].LeaseId = "other" }, func() { vb.Binding.Epoch = 2 }} {
		vb = poolView(proto.Clone(b).(*nodev1.SharedDshBinding))
		mutate()
		if e := CheckPoolViews([]*nodev1.SharedDshBinding{a, b}, []*nodev1.SharedDshResourceView{va, vb}, 1000); e == nil {
			t.Fatal("invalid view accepted")
		}
	}
}

func TestSharedPoolKReceiptOracle(t *testing.T) {
	m, _, _ := newPoolTest(t)
	ctx := context.Background()
	for _, action := range []string{"prepare-empty", "stop-pool", "discard-sessions", "archive-reset", "cooperative-cancel"} {
		in := poolIntent(action, action, "alice")
		r, e := m.Apply(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		binding := map[string]any{"tenantId": in.Binding.TenantId, "memberId": in.Binding.MemberId, "placementId": in.Binding.PlacementId, "physicalInstanceId": in.Binding.PhysicalInstanceId, "mode": "shared", "epoch": in.Binding.Epoch, "revision": in.Binding.Revision}
		intent := map[string]any{"schemaVersion": 1, "operationId": in.OperationId, "binding": binding, "action": action, "cancelWaitSeconds": 30, "archiveRetentionSeconds": 2592000, "workspacePolicy": "empty", "sessionPolicy": "discard"}
		receipt := map[string]any{"schemaVersion": 1, "operationId": r.OperationId, "binding": binding, "receiptId": r.ReceiptId, "action": r.Action, "outcome": r.Outcome, "observedAt": r.ObservedAt, "executionTreeStopped": r.ExecutionTreeStopped, "sessionsDiscarded": r.SessionsDiscarded, "workspaceEmpty": r.WorkspaceEmpty}
		if r.ArchiveId != "" {
			receipt["archiveId"] = r.ArchiveId
			receipt["archiveExpiresAt"] = r.ArchiveExpiresAt
		}
		raw, _ := json.Marshal(map[string]any{"intent": intent, "receipt": receipt})
		cmd := exec.Command("node", "--input-type=module", "-e", `import {confirmReceipt} from '../../../../contracts/shared-dsh/ports.mjs';import{readFileSync}from'node:fs';const x=JSON.parse(readFileSync(0,'utf8'));if(confirmReceipt(x.intent,x.receipt)!=='confirmed')process.exit(2);`)
		cmd.Stdin = bytes.NewReader(raw)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("K oracle %s: %v: %s", action, e, out)
		}
	}
}

func TestSharedPoolAdmissionBudget(t *testing.T) {
	m, _, _ := newPoolTest(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		in := poolIntent("prepare-empty", fmt.Sprintf("op-%d", i), fmt.Sprintf("member-%d", i))
		applyPool(t, m, in)
	}
	if e := m.RecoverPool(ctx, poolID("pool"), poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		in := poolIntent("prepare-empty", fmt.Sprintf("op-%d", i), fmt.Sprintf("member-%d", i))
		e := m.AdmitTask(ctx, in.Binding, poolID(fmt.Sprintf("task-%d", i)), 79, 100)
		if (i < 4) != (e == nil) {
			t.Fatal(i, e)
		}
	}
	in := poolIntent("prepare-empty", "unused", "member-0")
	if e := m.AdmitTask(ctx, in.Binding, poolID("new"), 79, 100); e == nil {
		t.Fatal("two member tasks")
	}
	if e := m.CompleteTask(in.Binding, poolID("late")); e == nil {
		t.Fatal("late completion")
	}
	if e := m.CompleteTask(in.Binding, poolID("task-0")); e != nil {
		t.Fatal(e)
	}
	if e := m.AdmitTask(ctx, in.Binding, poolID("new"), 80, 100); e == nil {
		t.Fatal("80 percent")
	}
}
