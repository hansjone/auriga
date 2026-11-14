package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// PoolSupervisor lives outside the DSH host. Stopped must confirm the entire
// execution tree, not merely an HTTP health failure or a cooperative ACK.
type PoolSupervisor interface {
	Stop(context.Context, string, time.Duration) error
	ResourceProcessStopped(context.Context, string) (bool, error)
}
type PoolSessions interface {
	Cancel(context.Context, *nodev1.SharedDshBinding) error
	Discard(context.Context, *nodev1.SharedDshBinding) error
}
type poolOperation struct {
	Fingerprint string
	Action      string
	Receipt     *nodev1.SharedDshRuntimeReceipt
	Expires     int64
}
type poolState struct {
	Tenant       string
	Bindings     map[string]*nodev1.SharedDshBinding
	Operations   map[string]*poolOperation
	Active       map[string]string
	Impacts      map[string]*nodev1.SharedDshGuardianImpact
	Contaminated bool
}

// SharedPools is a serialized, durable node executor. One instance must own a
// PoolLayout (the agent's existing exclusive node data lock is required).
// NodeControl dispatch/outbound delivery is deliberately owned by the W wiring.
type SharedPools struct {
	Layout     *storage.PoolLayout
	Supervisor PoolSupervisor
	Sessions   PoolSessions
	Now        func() time.Time
	mu         sync.Mutex
}

func (m *SharedPools) now() int64 {
	if m.Now != nil {
		return m.Now().Unix()
	}
	return time.Now().Unix()
}
func (m *SharedPools) load(pool string) (*poolState, string, error) {
	if m.Layout == nil || !filepath.IsAbs(m.Layout.Root) {
		return nil, "", fmt.Errorf("absolute node-owned pool layout required")
	}
	p, e := m.Layout.Path(pool, "control", "runtime-state")
	if e != nil {
		return nil, "", e
	}
	s := &poolState{Bindings: map[string]*nodev1.SharedDshBinding{}, Operations: map[string]*poolOperation{}}
	raw, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return s, p, nil
	}
	if e != nil {
		return nil, "", e
	}
	if e = json.Unmarshal(raw, s); e != nil {
		return nil, "", e
	}
	if s.Bindings == nil || s.Operations == nil {
		return nil, "", fmt.Errorf("corrupt pool state")
	}
	if s.Active == nil {
		s.Active = map[string]string{}
	}
	if s.Impacts == nil {
		s.Impacts = map[string]*nodev1.SharedDshGuardianImpact{}
	}
	return s, p, nil
}
func savePool(p string, s *poolState) error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".runtime-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return unifiedReplace(f.Name(), p)
}

var poolUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validPoolBinding(b *nodev1.SharedDshBinding) bool {
	return b != nil && len(b.ProtoReflect().GetUnknown()) == 0 && b.Mode == nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED && b.Epoch > 0 && b.Epoch <= 9007199254740991 && b.Revision > 0 && b.Revision <= 9007199254740991 && poolUUID.MatchString(b.TenantId) && poolUUID.MatchString(b.MemberId) && poolUUID.MatchString(b.PlacementId) && poolUUID.MatchString(b.PhysicalInstanceId)
}
func (m *SharedPools) stopped(ctx context.Context, pool string) bool {
	if m.Supervisor == nil {
		return false
	}
	ok, e := m.Supervisor.ResourceProcessStopped(ctx, pool)
	return e == nil && ok
}
func (m *SharedPools) stop(ctx context.Context, pool string) bool {
	if m.Supervisor == nil {
		return false
	}
	// Always inspect after an error: a timed out Engine response may have stopped it.
	_ = m.Supervisor.Stop(ctx, pool, 0)
	return m.stopped(ctx, pool)
}

func (m *SharedPools) Apply(ctx context.Context, in *nodev1.SharedDshRuntimeIntent) (*nodev1.SharedDshRuntimeReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Layout == nil {
		return nil, fmt.Errorf("pool layout missing")
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 || !poolUUID.MatchString(in.OperationId) || in.SchemaVersion != 1 || !validPoolBinding(in.Binding) || in.CancelWaitSeconds != 30 || in.ArchiveRetentionSeconds != 2592000 || in.WorkspacePolicy != "empty" || in.SessionPolicy != "discard" {
		return nil, fmt.Errorf("common.validation_failed")
	}
	in = proto.Clone(in).(*nodev1.SharedDshRuntimeIntent)
	b := in.Binding
	if _, e := m.Layout.Path(b.PhysicalInstanceId, "control", in.OperationId); e != nil {
		return nil, e
	}
	if _, e := m.Layout.Workspace(b.PhysicalInstanceId, b.TenantId, b.MemberId, b.Epoch); e != nil {
		return nil, e
	}
	switch in.Action {
	case "prepare-empty", "archive-reset", "stop-pool", "discard-sessions", "cooperative-cancel":
	default:
		return nil, fmt.Errorf("unknown action")
	}
	s, p, e := m.load(b.PhysicalInstanceId)
	if e != nil {
		return nil, e
	}
	if s.Tenant != "" && s.Tenant != b.TenantId {
		return nil, fmt.Errorf("shared.binding_mismatch")
	}
	wire, _ := proto.MarshalOptions{Deterministic: true}.Marshal(in)
	sum := sha256.Sum256(wire)
	fingerprint := hex.EncodeToString(sum[:])
	if op := s.Operations[in.OperationId]; op != nil {
		if op.Fingerprint != fingerprint {
			return nil, fmt.Errorf("operation fingerprint conflict")
		}
		if op.Receipt != nil && op.Receipt.Outcome == "confirmed" {
			return proto.Clone(op.Receipt).(*nodev1.SharedDshRuntimeReceipt), nil
		}
	}
	prior := s.Bindings[b.MemberId]
	if prior != nil && !proto.Equal(prior, b) {
		if prior.PlacementId != b.PlacementId || prior.Epoch > b.Epoch || prior.Revision >= b.Revision || !m.stopped(ctx, b.PhysicalInstanceId) {
			return nil, fmt.Errorf("stale or conflicting binding")
		}
	}
	if prior == nil && len(s.Bindings) >= 20 {
		return nil, fmt.Errorf("pool registration capacity")
	}
	s.Tenant = b.TenantId
	s.Bindings[b.MemberId] = proto.Clone(b).(*nodev1.SharedDshBinding)
	op := s.Operations[in.OperationId]
	if op == nil {
		op = &poolOperation{Fingerprint: fingerprint, Action: in.Action}
		s.Operations[in.OperationId] = op
	}
	s.Contaminated = true
	// Persist before any side effect; a crash leaves an unconfirmed operation.
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	r := &nodev1.SharedDshRuntimeReceipt{SchemaVersion: 1, OperationId: in.OperationId, Binding: b, ReceiptId: in.OperationId, Action: in.Action, Outcome: "unconfirmed", ObservedAt: m.now()}
	switch in.Action {
	case "stop-pool":
		s.Contaminated = true
		r.ExecutionTreeStopped = m.stop(ctx, b.PhysicalInstanceId)
		if r.ExecutionTreeStopped {
			r.Outcome = "confirmed"
		}
	case "cooperative-cancel":
		if m.Sessions != nil {
			wait, cancel := context.WithTimeout(ctx, 30*time.Second)
			result := make(chan error, 1)
			go func() { result <- m.Sessions.Cancel(wait, proto.Clone(b).(*nodev1.SharedDshBinding)) }()
			select {
			case e = <-result:
			case <-wait.Done():
				e = wait.Err()
			}
			cancel()
			if e == nil {
				r.Outcome = "confirmed"
			} else {
				s.Contaminated = true
				if s.Impacts == nil {
					s.Impacts = map[string]*nodev1.SharedDshGuardianImpact{}
				}
				impact := poolImpact(s, b.PhysicalInstanceId, in.OperationId, "cancel-timeout", m.now())
				s.Impacts[in.OperationId] = impact
				if e = savePool(p, s); e != nil {
					return nil, e
				}
				r.ExecutionTreeStopped = m.stop(ctx, b.PhysicalInstanceId)
				impact.State = "blocked"
				if r.ExecutionTreeStopped {
					impact.State = "stopped"
				}
			}
		}
	case "discard-sessions":
		if m.stopped(ctx, b.PhysicalInstanceId) && m.Sessions != nil && m.Sessions.Discard(ctx, b) == nil {
			r.SessionsDiscarded = true
			r.Outcome = "confirmed"
		}
	case "prepare-empty":
		if m.stopped(ctx, b.PhysicalInstanceId) {
			_, e = m.Layout.Prepare(ctx, b.PhysicalInstanceId, b.TenantId, b.MemberId, b.Epoch)
			if e == nil {
				r.WorkspaceEmpty = true
				r.Outcome = "confirmed"
			}
		}
	case "archive-reset":
		if m.stopped(ctx, b.PhysicalInstanceId) {
			if op.Expires == 0 {
				op.Expires = m.now() + 2592000
				if e = savePool(p, s); e != nil {
					return nil, e
				}
			}
			_, e = m.Layout.ArchiveReset(ctx, b.PhysicalInstanceId, b.TenantId, b.MemberId, b.Epoch, in.OperationId)
			if e == nil {
				r.WorkspaceEmpty = true
				r.ArchiveId = in.OperationId
				r.ArchiveExpiresAt = op.Expires
				r.ObservedAt = op.Expires - 2592000
				r.Outcome = "confirmed"
			}
		}
	}
	op.Receipt = r
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	return proto.Clone(r).(*nodev1.SharedDshRuntimeReceipt), nil
}

// Trip must be called by the out-of-process resource/health observer. Persisting
// contamination precedes Stop; all placements are included, never just the owner.
func (m *SharedPools) Trip(ctx context.Context, pool, operation, reason string) (*nodev1.SharedDshGuardianImpact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Layout == nil || !poolUUID.MatchString(pool) || !poolUUID.MatchString(operation) {
		return nil, fmt.Errorf("invalid guardian binding")
	}
	switch reason {
	case "resource-expired", "resource-revoked", "cancel-timeout", "credential-stop-unconfirmed", "crash":
	default:
		return nil, fmt.Errorf("invalid guardian reason")
	}
	if _, e := m.Layout.Path(pool, "control", operation); e != nil {
		return nil, e
	}
	s, p, e := m.load(pool)
	if e != nil {
		return nil, e
	}
	if len(s.Bindings) == 0 {
		return nil, fmt.Errorf("unknown pool")
	}
	s.Contaminated = true
	r := poolImpact(s, pool, operation, reason, m.now())
	s.Impacts[operation] = r
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	r.State = "blocked"
	if m.stop(ctx, pool) {
		r.State = "stopped"
	}
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	return proto.Clone(r).(*nodev1.SharedDshGuardianImpact), nil
}

func poolImpact(s *poolState, pool, operation, reason string, now int64) *nodev1.SharedDshGuardianImpact {
	r := &nodev1.SharedDshGuardianImpact{SchemaVersion: 1, TenantId: s.Tenant, PhysicalInstanceId: pool, OperationId: operation, Reason: reason, Scope: "pool", State: "stopping", ObservedAt: now}
	for _, b := range s.Bindings {
		r.Affected = append(r.Affected, proto.Clone(b).(*nodev1.SharedDshBinding))
	}
	return r
}

// GuardianImpacts replays durable reports after transport failure/restart.
// The NodeControl consumer deduplicates by pool+operationId, retaining updates.
func (m *SharedPools) GuardianImpacts(pool string) ([]*nodev1.SharedDshGuardianImpact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, _, e := m.load(pool)
	if e != nil {
		return nil, e
	}
	var out []*nodev1.SharedDshGuardianImpact
	for _, r := range s.Impacts {
		out = append(out, proto.Clone(r).(*nodev1.SharedDshGuardianImpact))
	}
	return out, nil
}

func (m *SharedPools) SweepArchives(ctx context.Context, pool string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, p, e := m.load(pool)
	if e != nil {
		return e
	}
	for id, op := range s.Operations {
		if op.Expires > 0 && m.now() >= op.Expires {
			if e = m.Layout.DeleteArchive(pool, id); e != nil {
				return e
			}
			op.Expires = 0
		}
	}
	return savePool(p, s)
}
