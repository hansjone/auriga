package app

import (
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// instanceBridge keeps ingress Registry and observer targets aligned with driver endpoints.
type instanceBridge struct {
	mu       sync.Mutex
	cfg      config.Config
	driver   runtime.Driver
	registry *ingress.MapRegistry
	targets  *observer.SliceTargets
	byID     map[string]observer.InstanceTarget
	// Retained after removal so an older in-flight publisher cannot recreate
	// an endpoint after deletion or reuse of the same instance ID.
	publicationEpoch map[string]uint64
}

func newInstanceBridge(cfg config.Config, driver runtime.Driver, reg *ingress.MapRegistry, targets *observer.SliceTargets) *instanceBridge {
	return &instanceBridge{
		cfg:              cfg,
		driver:           driver,
		registry:         reg,
		targets:          targets,
		byID:             map[string]observer.InstanceTarget{},
		publicationEpoch: map[string]uint64{},
	}
}

func (b *instanceBridge) Sync(st *nodev1.InstanceStatus) {
	if b == nil || st == nil || st.InstanceId == "" {
		return
	}
	id := st.InstanceId
	if storage.ValidateInstanceID(id) != nil {
		return
	}
	switch st.Phase {
	case nodev1.InstancePhase_INSTANCE_PHASE_RUNNING,
		nodev1.InstancePhase_INSTANCE_PHASE_STARTING,
		nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART:
		running := st.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || st.StartedAt != nil
		startedAt := time.Time{}
		if st.StartedAt != nil && st.StartedAt.IsValid() {
			startedAt = st.StartedAt.AsTime()
		}
		b.publish(id, running, startedAt)
	case nodev1.InstancePhase_INSTANCE_PHASE_STOPPED:
		b.mu.Lock()
		b.publicationEpoch[id]++
		b.registry.Delete(id)
		b.byID[id] = observer.InstanceTarget{ID: id, RootDir: filepath.Join(b.cfg.InstancesDir(), id, "home"), Process: observer.ProcessStopped}
		b.flushLocked()
		b.mu.Unlock()
	case nodev1.InstancePhase_INSTANCE_PHASE_ABSENT,
		nodev1.InstancePhase_INSTANCE_PHASE_DELETING,
		nodev1.InstancePhase_INSTANCE_PHASE_FAILED:
		b.remove(id)
	default:
		// preparing / migrating: no upstream yet
	}
}

func (b *instanceBridge) publish(id string, running bool, startedAt time.Time) {
	b.mu.Lock()
	epoch := b.publicationEpoch[id]
	b.mu.Unlock()
	// Endpoint may synchronously revoke and reenter remove. Never hold the
	// bridge mutex across this call; compare its original epoch afterwards.
	ep, err := b.driver.Endpoint(id)
	if err != nil || ep.URL == "" {
		return
	}
	u, err := url.Parse(ep.URL)
	if err != nil || u.Scheme == "" {
		return
	}
	home := filepath.Join(b.cfg.InstancesDir(), id, "home")
	logPath := filepath.Join(b.cfg.InstancesDir(), id, "meta", "instance.log")
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.publicationEpoch[id] != epoch {
		return
	}
	b.registry.Put(&ingress.Instance{
		ID:       id,
		Upstream: u,
		RootDir:  home,
		LogPath:  logPath,
	})
	proc := observer.ProcessStopped
	if running {
		proc = observer.ProcessRunning
	}
	b.byID[id] = observer.InstanceTarget{
		ID:               id,
		BaseURL:          u,
		RootDir:          home,
		Process:          proc,
		ProcessStartedAt: startedAt,
	}
	b.flushLocked()
}

func (b *instanceBridge) remove(id string) {
	b.mu.Lock()
	b.publicationEpoch[id]++
	b.registry.Delete(id)
	delete(b.byID, id)
	b.flushLocked()
	b.mu.Unlock()
}

func (b *instanceBridge) flushLocked() {
	list := make([]observer.InstanceTarget, 0, len(b.byID))
	for _, t := range b.byID {
		list = append(list, t)
	}
	b.targets.Set(list)
}
