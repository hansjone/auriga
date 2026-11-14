package runtime

import (
	"context"
	"fmt"
	"regexp"

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
// A missing placement, mismatched authorization set or expired resource blocks
// the WHOLE pool. Empty platform views are legal; personal sources are not.
func CheckPoolViews(bindings []*nodev1.SharedDshBinding, views []*nodev1.SharedDshResourceView, now int64) error {
	if len(bindings) == 0 || len(bindings) > 20 || len(bindings) != len(views) {
		return fmt.Errorf("incomplete pool resource snapshot")
	}
	seen := map[string]bool{}
	digest := ""
	tenant, pool := bindings[0].GetTenantId(), bindings[0].GetPhysicalInstanceId()
	for i, b := range bindings {
		if !validPoolBinding(b) || b.TenantId != tenant || b.PhysicalInstanceId != pool || seen[b.MemberId] {
			return fmt.Errorf("shared.binding_mismatch")
		}
		seen[b.MemberId] = true
		v := views[i]
		if v == nil || len(v.ProtoReflect().GetUnknown()) != 0 || v.SchemaVersion != 1 || !proto.Equal(v.Binding, b) || v.Source != "platform" || !poolUUID.MatchString(v.ViewId) || !poolDigest.MatchString(v.AuthorizationSetDigest) {
			return fmt.Errorf("invalid platform resource view")
		}
		if digest == "" {
			digest = v.AuthorizationSetDigest
		}
		if digest != v.AuthorizationSetDigest {
			return fmt.Errorf("authorization-mismatch")
		}
		resources := map[string]bool{}
		for _, r := range v.Resources {
			if r == nil || len(r.ProtoReflect().GetUnknown()) != 0 || !poolUUID.MatchString(r.ResourceId) || resources[r.ResourceId] || !poolUUID.MatchString(r.LeaseId) || !poolDigest.MatchString(r.Digest) || r.ExpiresAt <= now || r.ExpiresAt > 9007199254740991 {
				return fmt.Errorf("expired or invalid resource")
			}
			resources[r.ResourceId] = true
		}
		// Authorization digest alone cannot excuse differing content/leases.
		if i > 0 {
			a := views[0].Resources
			if len(a) != len(v.Resources) {
				return fmt.Errorf("resource set mismatch")
			}
			for j := range a {
				if !proto.Equal(a[j], v.Resources[j]) {
					return fmt.Errorf("resource set mismatch")
				}
			}
		}
	}
	return nil
}

// RecoverPool requires external stop proof, view cleanup and all member session
// discards. It never resumes a contaminated session. A fresh start must still
// call CheckPoolViews and verifier.Verify with authoritative W data.
func (m *SharedPools) RecoverPool(ctx context.Context, pool string, v PoolResourceVerifier) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, p, e := m.load(pool)
	if e != nil {
		return e
	}
	if !s.Contaminated {
		return nil
	}
	if !m.stopped(ctx, pool) || v == nil || m.Sessions == nil {
		return fmt.Errorf("shared.stop_unconfirmed")
	}
	if e = v.Cleanup(ctx, pool); e != nil {
		return e
	}
	for _, b := range s.Bindings {
		if e = m.Sessions.Discard(ctx, b); e != nil {
			return e
		}
	}
	s.Contaminated = false
	s.Active = map[string]string{}
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
	if s.Contaminated || !proto.Equal(s.Bindings[b.MemberId], b) {
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
	if s.Contaminated || len(s.Bindings) == 0 || v == nil {
		return fmt.Errorf("pool cleanup unconfirmed")
	}
	for _, op := range s.Operations {
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
