package runtime

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/hansjone/auriga/infra/agent/internal/observer"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// PoolResourceVerifier bridges the existing signed unified-resource manager.
// Verify must recheck platform signatures/revocations, readonly active root and
// live leases. Cleanup removes that view; neither is a DSH/plugin callback.
type PoolResourceVerifier interface {
	Verify(context.Context, string, []*nodev1.SharedDshResourceView) error
	Cleanup(context.Context, string) error
}

var poolDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// CheckViews takes a fresh W snapshot, not a caller-owned session declaration.
// A missing placement or mismatched authorization set rejects admission.
// Only a verified resource expiry/revocation triggers the pool guardian. Empty platform views are legal; personal sources are not.
func CheckPoolViews(bindings []*nodev1.SharedDshBinding, views []*nodev1.SharedDshResourceView, now int64) error {
	if len(bindings) == 0 || len(bindings) > 20 || len(bindings) != len(views) {
		return fmt.Errorf("%w: incomplete pool resource snapshot", observer.ErrPoolSnapshotRejected)
	}
	seen := map[string]bool{}
	digest := ""
	tenant, pool := bindings[0].GetTenantId(), bindings[0].GetPhysicalInstanceId()
	for i, b := range bindings {
		if !validPoolBinding(b) || b.TenantId != tenant || b.PhysicalInstanceId != pool || seen[b.MemberId] {
			return fmt.Errorf("%w: shared.binding_mismatch", observer.ErrPoolSnapshotRejected)
		}
		seen[b.MemberId] = true
		v := views[i]
		if v == nil || len(v.ProtoReflect().GetUnknown()) != 0 || v.SchemaVersion != 1 || !proto.Equal(v.Binding, b) || v.Source != "platform" || !poolUUID.MatchString(v.ViewId) || !poolDigest.MatchString(v.AuthorizationSetDigest) {
			return fmt.Errorf("%w: invalid platform resource view", observer.ErrPoolSnapshotRejected)
		}
		if digest == "" {
			digest = v.AuthorizationSetDigest
		}
		if digest != v.AuthorizationSetDigest {
			return fmt.Errorf("%w: authorization-mismatch", observer.ErrPoolSnapshotRejected)
		}
		resources := map[string]bool{}
		for _, r := range v.Resources {
			if r == nil || len(r.ProtoReflect().GetUnknown()) != 0 || !poolUUID.MatchString(r.ResourceId) || resources[r.ResourceId] || !poolUUID.MatchString(r.LeaseId) || !poolDigest.MatchString(r.Digest) || r.ExpiresAt < 0 || r.ExpiresAt > 9007199254740991 {
				return fmt.Errorf("%w: expired or invalid resource", observer.ErrPoolSnapshotRejected)
			}
			resources[r.ResourceId] = true
		}
		// Authorization digest alone cannot excuse differing content/leases.
		if i > 0 {
			a := views[0].Resources
			if len(a) != len(v.Resources) {
				return fmt.Errorf("%w: resource set mismatch", observer.ErrPoolSnapshotRejected)
			}
			for j := range a {
				if !proto.Equal(a[j], v.Resources[j]) {
					return fmt.Errorf("%w: resource set mismatch", observer.ErrPoolSnapshotRejected)
				}
			}
		}
	}
	for _, v := range views {
		for _, r := range v.Resources {
			if r.ExpiresAt <= now {
				return observer.ErrPoolResourceExpired
			}
		}
	}
	return nil
}

// PoolHistoryFence retires old execution handles while preserving owned history
// for read-only access. It must not delete sessions as a pool recovery shortcut.
type PoolHistoryFence interface {
	MakeHistoryReadOnly(context.Context, string, []*nodev1.SharedDshBinding) error
}

func (m *SharedPools) RecoverPool(ctx context.Context, pool string, v PoolResourceVerifier) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, _, e := m.load(pool)
	if e != nil {
		return e
	}
	if !s.Contaminated {
		return nil
	}
	if poolStopPending(s) || m.stopping[pool] > 0 {
		return fmt.Errorf("pool stop still in progress")
	}
	history, ok := m.Sessions.(PoolHistoryFence)
	if !ok || v == nil {
		return fmt.Errorf("pool history fence unavailable")
	}
	fence := s.Fence
	var bindings []*nodev1.SharedDshBinding
	for _, b := range s.Bindings {
		bindings = append(bindings, proto.Clone(b).(*nodev1.SharedDshBinding))
	}
	m.mu.Unlock()
	var err error
	if !m.stopped(ctx, pool) {
		err = fmt.Errorf("shared.stop_unconfirmed")
	} else if err = v.Cleanup(ctx, pool); err == nil {
		err = history.MakeHistoryReadOnly(ctx, pool, bindings)
	}
	m.mu.Lock()
	if err != nil {
		return err
	}
	s, p, e := m.load(pool)
	if e != nil {
		return e
	}
	if s.Fence != fence {
		return fmt.Errorf("pool recovery superseded")
	}
	s.Contaminated = false
	s.Active = map[string]string{}
	return savePool(p, s)
}

// RevokeMember consumes K's existing switch/member-disabled revocation after
// W has confirmed member quiescence/archive. It is not a new wire action. An
// early revoke is retried by orchestration after cancellation; no pool stop.
func (m *SharedPools) RevokeMember(ctx context.Context, pool string, r *nodev1.SharedDshRevocation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r == nil || r.SchemaVersion != 1 || len(r.ProtoReflect().GetUnknown()) != 0 || !poolUUID.MatchString(r.TenantId) || !poolUUID.MatchString(r.MemberId) || !poolUUID.MatchString(r.PlacementId) || r.Epoch < 1 || r.Epoch > 9007199254740991 || r.Revision < 1 || r.Revision > 9007199254740991 || r.AuthGeneration < 1 || r.AuthGeneration > 9007199254740991 || r.RevokedAt < 0 || r.RevokedAt > 9007199254740991 || (r.Reason != "switch" && r.Reason != "member-disabled") {
		return fmt.Errorf("invalid member release revocation")
	}
	r = proto.Clone(r).(*nodev1.SharedDshRevocation)
	s, p, e := m.load(pool)
	if e != nil {
		return e
	}
	if s.Tenant != r.TenantId {
		return fmt.Errorf("shared.binding_mismatch")
	}
	if m.inflight[pool+"/"+r.MemberId] {
		return fmt.Errorf("member operation in progress")
	}
	duplicate := false
	if old := s.Released[r.MemberId]; old != nil {
		if proto.Equal(old, r) {
			duplicate = true
		}
		if !duplicate && (r.Epoch < old.Epoch || r.Revision <= old.Revision) {
			return fmt.Errorf("stale member release")
		}
	}
	if !duplicate {
		if s.Retiring[r.MemberId] != nil {
			return fmt.Errorf("prior member directory retirement pending")
		}
		if b := s.Bindings[r.MemberId]; b != nil {
			if b.PlacementId != r.PlacementId || r.Epoch <= b.Epoch || r.Revision <= b.Revision {
				return fmt.Errorf("stale member release")
			}
			if s.Active[r.MemberId] != "" && !proto.Equal(s.Quiesced[r.MemberId], b) {
				return fmt.Errorf("member cancellation unconfirmed")
			}
			workspace, err := m.Layout.Workspace(pool, b.TenantId, b.MemberId, b.Epoch)
			if err != nil {
				return err
			}
			entries, err := os.ReadDir(workspace)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if len(entries) != 0 {
				return fmt.Errorf("member workspace not archived")
			}
			if s.Retiring == nil {
				s.Retiring = map[string]*nodev1.SharedDshBinding{}
			}
			s.Retiring[r.MemberId] = proto.Clone(b).(*nodev1.SharedDshBinding)
		}
		if s.Released == nil {
			s.Released = map[string]*nodev1.SharedDshRevocation{}
		}
		s.Released[r.MemberId] = proto.Clone(r).(*nodev1.SharedDshRevocation)
		delete(s.Bindings, r.MemberId)
		delete(s.Active, r.MemberId)
		delete(s.Quiesced, r.MemberId)
		if e = savePool(p, s); e != nil {
			return e
		}
	}
	retired := s.Retiring[r.MemberId]
	if retired == nil {
		return nil
	}
	key := pool + "/" + r.MemberId
	if m.inflight == nil {
		m.inflight = map[string]bool{}
	}
	m.inflight[key] = true
	m.mu.Unlock()
	err := m.Layout.RetireWorkspace(ctx, pool, retired.TenantId, retired.MemberId, retired.Epoch)
	m.mu.Lock()
	delete(m.inflight, key)
	if err != nil {
		return err
	}
	s, p, e = m.load(pool)
	if e != nil {
		return e
	}
	if proto.Equal(s.Retiring[r.MemberId], retired) {
		delete(s.Retiring, r.MemberId)
	}
	return savePool(p, s)
}

// AdmitTask is an additional node-side budget gate. W remains the authority for
// 20 registrations, pool expansion and member validity. Persisted in-flight
// slots are not released on restart; stopped-tree/session cleanup is required.
func (m *SharedPools) AdmitTask(ctx context.Context, b *nodev1.SharedDshBinding, task string, used, total int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validPoolBinding(b) || !poolUUID.MatchString(task) || total <= 0 || used < 0 || used >= total-total/5 {
		return fmt.Errorf("pool admission closed")
	}
	s, p, e := m.load(b.PhysicalInstanceId)
	if e != nil {
		return e
	}
	if s.Active == nil {
		s.Active = map[string]string{}
	}
	if memberOperationBlocked(s, b) || s.Contaminated || m.inflight[b.PhysicalInstanceId+"/"+b.MemberId] || proto.Equal(s.Quiesced[b.MemberId], b) || !proto.Equal(s.Bindings[b.MemberId], b) {
		return fmt.Errorf("pool binding unavailable")
	}
	if old := s.Active[b.MemberId]; old != "" {
		if old == task {
			return nil
		}
		return fmt.Errorf("member already active")
	}
	if len(s.Active) >= 4 {
		return fmt.Errorf("pool active capacity")
	}
	s.Active[b.MemberId] = task
	return savePool(p, s)
}
func (m *SharedPools) CompleteTask(b *nodev1.SharedDshBinding, task string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validPoolBinding(b) {
		return fmt.Errorf("invalid binding")
	}
	s, p, e := m.load(b.PhysicalInstanceId)
	if e != nil {
		return e
	}
	if !proto.Equal(s.Bindings[b.MemberId], b) || s.Active[b.MemberId] != task {
		return fmt.Errorf("late task completion")
	}
	delete(s.Active, b.MemberId)
	return savePool(p, s)
}

func (m *SharedPools) AuthorizePoolStart(ctx context.Context, pool string, views []*nodev1.SharedDshResourceView, v PoolResourceVerifier) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, _, e := m.load(pool)
	if e != nil {
		return e
	}
	if s.Contaminated || poolStopPending(s) || m.stopping[pool] > 0 || len(s.Bindings) == 0 || v == nil {
		return fmt.Errorf("pool cleanup unconfirmed")
	}
	for _, op := range s.Operations {
		opBinding := op.Binding
		if opBinding == nil && op.Receipt != nil {
			opBinding = op.Receipt.Binding
		}
		if opBinding != nil && !proto.Equal(s.Bindings[opBinding.MemberId], opBinding) {
			continue
		}
		if op.Pending {
			return fmt.Errorf("runtime operation pending")
		}
		if (op.Action == "prepare-empty" || op.Action == "archive-reset") && (op.Receipt == nil || op.Receipt.Outcome != "confirmed") {
			return fmt.Errorf("runtime operation unconfirmed")
		}
	}
	bindings := make([]*nodev1.SharedDshBinding, len(views))
	for i, view := range views {
		if view == nil || view.Binding == nil {
			return fmt.Errorf("missing view binding")
		}
		b := s.Bindings[view.Binding.MemberId]
		if !proto.Equal(b, view.Binding) {
			return fmt.Errorf("shared.binding_mismatch")
		}
		bindings[i] = b
	}
	if len(bindings) != len(s.Bindings) {
		return fmt.Errorf("incomplete resource views")
	}
	if e = CheckPoolViews(bindings, views, m.now()); e != nil {
		return e
	}
	return v.Verify(ctx, pool, views)
}

func poolStopPending(s *poolState) bool {
	for _, impact := range s.Impacts {
		if impact.State == "stopping" {
			return true
		}
	}
	for _, op := range s.Operations {
		if op.Action == "stop-pool" && op.Pending {
			return true
		}
	}
	return false
}

func memberOperationBlocked(s *poolState, b *nodev1.SharedDshBinding) bool {
	for _, op := range s.Operations {
		binding := op.Binding
		if binding == nil && op.Receipt != nil {
			binding = op.Receipt.Binding
		}
		if proto.Equal(binding, b) && (op.Pending || ((op.Action == "prepare-empty" || op.Action == "archive-reset") && (op.Receipt == nil || op.Receipt.Outcome != "confirmed"))) {
			return true
		}
	}
	return false
}
