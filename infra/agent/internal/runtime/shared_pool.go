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
	Binding     *nodev1.SharedDshBinding
	Action      string
	Receipt     *nodev1.SharedDshRuntimeReceipt
	Expires     int64
	Pending     bool
}
type poolState struct {
	Tenant       string
	Bindings     map[string]*nodev1.SharedDshBinding
	Operations   map[string]*poolOperation
	Active       map[string]string
	Impacts      map[string]*nodev1.SharedDshGuardianImpact
	Contaminated bool
	Fence        uint64
	Quiesced     map[string]*nodev1.SharedDshBinding
	Released     map[string]*nodev1.SharedDshRevocation
	Retiring     map[string]*nodev1.SharedDshBinding
}

// SharedPools durably serializes state, with member execution outside the lock.
// One instance must own a
// PoolLayout (the agent's existing exclusive node data lock is required).
// NodeControl dispatch/outbound delivery is deliberately owned by the W wiring.
type SharedPools struct {
	Layout     *storage.PoolLayout
	Supervisor PoolSupervisor
	Sessions   PoolSessions
	Now        func() time.Time
	mu         sync.Mutex
	inflight   map[string]bool
	stopping   map[string]int
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
	if s.Quiesced == nil {
		s.Quiesced = map[string]*nodev1.SharedDshBinding{}
	}
	if s.Released == nil {
		s.Released = map[string]*nodev1.SharedDshRevocation{}
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
	defer func() { _ = os.Remove(f.Name()) }()
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
	return poolReplace(f.Name(), p)
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
	if m.Layout == nil || in == nil || len(in.ProtoReflect().GetUnknown()) != 0 || !poolUUID.MatchString(in.OperationId) || in.SchemaVersion != 1 || !validPoolBinding(in.Binding) || in.CancelWaitSeconds != 30 || in.ArchiveRetentionSeconds != 2592000 || in.WorkspacePolicy != "empty" || in.SessionPolicy != "discard" {
		return nil, fmt.Errorf("common.validation_failed")
	}
	in = proto.Clone(in).(*nodev1.SharedDshRuntimeIntent)
	b := in.Binding
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
	if rev := s.Released[b.MemberId]; rev != nil && (b.Epoch < rev.Epoch || b.Revision < rev.Revision) {
		return nil, fmt.Errorf("released binding")
	}
	key := b.PhysicalInstanceId + "/" + b.MemberId
	if m.inflight[key] {
		return nil, fmt.Errorf("member operation in progress")
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
	quiet := s.Active[b.MemberId] == "" || proto.Equal(s.Quiesced[b.MemberId], prior)
	if prior != nil && !proto.Equal(prior, b) {
		if prior.PlacementId != b.PlacementId || prior.Epoch > b.Epoch || prior.Revision >= b.Revision || !quiet {
			return nil, fmt.Errorf("stale or conflicting binding")
		}
		delete(s.Quiesced, b.MemberId)
	}
	if prior == nil && len(s.Bindings) >= 20 {
		return nil, fmt.Errorf("pool registration capacity")
	}
	if (in.Action == "discard-sessions" || in.Action == "archive-reset") && !quiet {
		return nil, fmt.Errorf("member cancellation unconfirmed")
	}
	s.Tenant = b.TenantId
	s.Bindings[b.MemberId] = proto.Clone(b).(*nodev1.SharedDshBinding)
	op := s.Operations[in.OperationId]
	if op == nil {
		op = &poolOperation{Fingerprint: fingerprint, Action: in.Action, Binding: b}
		s.Operations[in.OperationId] = op
	}
	op.Pending = true
	if in.Action == "archive-reset" && op.Expires == 0 {
		op.Expires = m.now() + 2592000
	}
	if in.Action == "stop-pool" {
		s.Contaminated = true
		s.Fence++
	}
	fence, expires := s.Fence, op.Expires
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	if m.inflight == nil {
		m.inflight = map[string]bool{}
	}
	m.inflight[key] = true
	// Host calls, quota commands, the 30-second wait and Engine Stop never hold
	// the state mutex. Guardian Trip can preempt them; Fence rejects late ACKs.
	m.mu.Unlock()
	r := m.executeIntent(ctx, in, expires)
	m.mu.Lock()
	delete(m.inflight, key)
	s, p, e = m.load(b.PhysicalInstanceId)
	if e != nil {
		return nil, e
	}
	op = s.Operations[in.OperationId]
	if op == nil || op.Fingerprint != fingerprint {
		return nil, fmt.Errorf("operation superseded")
	}
	current := proto.Equal(s.Bindings[b.MemberId], b) && s.Fence == fence
	if !current {
		r.Outcome = "unconfirmed"
	}
	if current && r.Outcome == "confirmed" && in.Action == "cooperative-cancel" {
		if s.Quiesced == nil {
			s.Quiesced = map[string]*nodev1.SharedDshBinding{}
		}
		s.Quiesced[b.MemberId] = b
		delete(s.Active, b.MemberId)
	}
	op.Pending = false
	op.Receipt = r
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	return proto.Clone(r).(*nodev1.SharedDshRuntimeReceipt), nil
}

func (m *SharedPools) executeIntent(ctx context.Context, in *nodev1.SharedDshRuntimeIntent, expires int64) *nodev1.SharedDshRuntimeReceipt {
	b := in.Binding
	r := &nodev1.SharedDshRuntimeReceipt{SchemaVersion: 1, OperationId: in.OperationId, Binding: b, ReceiptId: in.OperationId, Action: in.Action, Outcome: "unconfirmed", ObservedAt: m.now()}
	switch in.Action {
	case "stop-pool":
		r.ExecutionTreeStopped = m.stop(ctx, b.PhysicalInstanceId)
		if r.ExecutionTreeStopped {
			r.Outcome = "confirmed"
		}
	case "cooperative-cancel":
		if m.Sessions != nil {
			wait, cancel := context.WithTimeout(ctx, 30*time.Second)
			result := make(chan error, 1)
			go func() { result <- m.Sessions.Cancel(wait, proto.Clone(b).(*nodev1.SharedDshBinding)) }()
			var err error
			select {
			case err = <-result:
			case <-wait.Done():
				err = wait.Err()
			}
			cancel()
			if err == nil {
				r.Outcome = "confirmed"
			} else {
				impact, err := m.Trip(ctx, b.PhysicalInstanceId, in.OperationId, "cancel-timeout")
				r.ExecutionTreeStopped = err == nil && impact.State == "stopped"
			}
		}
	case "discard-sessions":
		if m.Sessions != nil && m.Sessions.Discard(ctx, b) == nil {
			r.SessionsDiscarded = true
			r.Outcome = "confirmed"
		}
	case "prepare-empty":
		if _, err := m.Layout.Prepare(ctx, b.PhysicalInstanceId, b.TenantId, b.MemberId, b.Epoch); err == nil {
			r.WorkspaceEmpty = true
			r.Outcome = "confirmed"
		}
	case "archive-reset":
		if _, err := m.Layout.ArchiveReset(ctx, b.PhysicalInstanceId, b.TenantId, b.MemberId, b.Epoch, in.OperationId); err == nil {
			r.WorkspaceEmpty = true
			r.ArchiveId = in.OperationId
			r.ArchiveExpiresAt = expires
			r.ObservedAt = expires - 2592000
			r.Outcome = "confirmed"
		}
	}
	return r
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
	key := pool + "/guardian/" + operation
	if m.inflight[key] {
		return proto.Clone(s.Impacts[operation]).(*nodev1.SharedDshGuardianImpact), nil
	}
	s.Contaminated = true
	s.Fence++
	r := poolImpact(s, pool, operation, reason, m.now())
	s.Impacts[operation] = r
	if e = savePool(p, s); e != nil {
		return nil, e
	}
	if m.inflight == nil {
		m.inflight = map[string]bool{}
	}
	m.inflight[key] = true
	if m.stopping == nil {
		m.stopping = map[string]int{}
	}
	m.stopping[pool]++
	m.mu.Unlock()
	stopped := m.stop(ctx, pool)
	m.mu.Lock()
	delete(m.inflight, key)
	m.stopping[pool]--
	s, p, e = m.load(pool)
	if e != nil {
		return nil, e
	}
	r = s.Impacts[operation]
	if r == nil {
		return nil, fmt.Errorf("guardian impact missing")
	}
	r.State = "blocked"
	if stopped {
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
			if e = m.Layout.DeleteArchive(ctx, pool, id); e != nil {
				return e
			}
			op.Expires = 0
		}
	}
	return savePool(p, s)
}
