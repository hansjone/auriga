package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// PoolTaskAdmission is an adapter to the existing persistent budget gate, not a
// second scheduler. One adapter is shared by all relays of the owning Node.
type PoolTaskAdmission struct {
	TenantID string
	Pools    *SharedPools
	Driver   *Docker
	Report   func(*nodev1.SharedDshPoolMemorySample)
	Control  PoolTaskControl
	mu       sync.Mutex
}

// PoolTaskControl is implemented by the actual NodeControl session, never the
// service-JWT Workspace client. Responses are verified at that transport seam.
type PoolTaskControl interface {
	AdmitSharedTask(context.Context, *nodev1.SharedDshSessionBinding, string, *nodev1.SharedDshPoolMemorySample) error
	CheckSharedTask(*nodev1.SharedDshSessionBinding, string) error
	ReleaseSharedTask(context.Context, *nodev1.SharedDshSessionBinding, string) error
}

func placementOf(b *nodev1.SharedDshSessionBinding) *nodev1.SharedDshBinding {
	if b == nil {
		return nil
	}
	return &nodev1.SharedDshBinding{TenantId: b.TenantId, MemberId: b.MemberId, PlacementId: b.PlacementId,
		PhysicalInstanceId: b.PhysicalInstanceId, Mode: b.Mode, Epoch: b.Epoch, Revision: b.Revision}
}

// The bool says this call may own a reservation and must compensate on failure.
// Pre-existing slots, including the same UUID after Node restart, are never adopted.
func (a *PoolTaskAdmission) Admit(ctx context.Context, b *nodev1.SharedDshSessionBinding, task string) (bool, error) {
	if a.Pools == nil || a.Driver == nil || a.Control == nil || b == nil || a.TenantID == "" || b.TenantId != a.TenantID {
		return false, fmt.Errorf("pool admission unavailable")
	}
	sample, err := a.Driver.PoolMemory(ctx, b.PhysicalInstanceId)
	if err != nil {
		return false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if sample.ValidUntil <= time.Now().Unix() || ctx.Err() != nil {
		return false, fmt.Errorf("pool admission sample no longer current")
	}
	active, err := a.Pools.activeTask(placementOf(b), true)
	if err != nil || active != "" {
		return false, fmt.Errorf("member already active or restart cleanup required")
	}
	sample.TenantId = a.TenantID
	if a.Report != nil {
		a.Report(sample)
	}
	err = a.Pools.AdmitTask(ctx, placementOf(b), task, sample.MemoryUsedBytes, sample.MemoryLimitBytes)
	if err != nil {
		// Known budget rejection owns nothing. An ambiguous durable-write error
		// is compensated only against this exact UUID, never a replacement slot.
		active, readErr := a.Pools.activeTask(placementOf(b), false)
		return readErr != nil || active == task, err
	}
	// An unknown W Admit still owns the durable local slot. Exact Release must
	// confirm its tombstone before compensation can clear that slot.
	return true, a.Control.AdmitSharedTask(ctx, b, task, sample)
}

func (a *PoolTaskAdmission) Check(b *nodev1.SharedDshSessionBinding, task string) error {
	if a.Pools == nil || a.Control == nil {
		return fmt.Errorf("pool admission unavailable")
	}
	if err := a.Control.CheckSharedTask(b, task); err != nil {
		return err
	}
	active, err := a.Pools.activeTask(placementOf(b), true)
	if err != nil || active != task {
		return fmt.Errorf("pool task no longer current")
	}
	return nil
}

func (a *PoolTaskAdmission) Complete(b *nodev1.SharedDshSessionBinding, task string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Pools == nil || a.Control == nil {
		return fmt.Errorf("pool control unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Control.ReleaseSharedTask(ctx, b, task); err != nil {
		return err
	}
	active, err := a.Pools.activeTask(placementOf(b), false)
	if err != nil {
		return err
	}
	if active == "" {
		return nil
	}
	if active != task {
		return nil
	}
	return a.Pools.CompleteTask(placementOf(b), task)
}

func (m *SharedPools) activeTask(b *nodev1.SharedDshBinding, available bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validPoolBinding(b) {
		return "", fmt.Errorf("invalid task binding")
	}
	s, _, err := m.load(b.PhysicalInstanceId)
	if err != nil {
		return "", err
	}
	if !available && s.Active[b.MemberId] == "" {
		return "", nil
	}
	if !available && !proto.Equal(s.Bindings[b.MemberId], b) {
		return "", nil
	}
	if !proto.Equal(s.Bindings[b.MemberId], b) || (available && (s.Contaminated || poolStopPending(s) || m.stopping[b.PhysicalInstanceId] > 0 || m.inflight[b.PhysicalInstanceId+"/"+b.MemberId] || memberOperationBlocked(s, b) || proto.Equal(s.Quiesced[b.MemberId], b))) {
		return "", fmt.Errorf("pool binding unavailable")
	}
	return s.Active[b.MemberId], nil
}
