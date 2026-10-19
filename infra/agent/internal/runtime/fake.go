package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is an in-memory driver for A1 tests (no real processes/containers).
type Fake struct {
	mu sync.Mutex

	instances map[string]*fakeInst

	FailNextEnsure bool
	FailNextStart  bool
	FailNextStop   bool
}

type fakeInst struct {
	spec    Spec
	running bool
	ready   bool
	err     string
}

// NewFake returns an empty fake driver.
func NewFake() *Fake {
	return &Fake{instances: make(map[string]*fakeInst)}
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Capabilities() Caps {
	return Caps{NetworkIsolation: true, DiskQuota: true, ReadOnlyRoot: true}
}

func (f *Fake) Ensure(_ context.Context, s *Spec, _ *Rendered) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNextEnsure {
		f.FailNextEnsure = false
		return fmt.Errorf("fake ensure failed")
	}
	inst, ok := f.instances[s.InstanceID]
	if !ok {
		inst = &fakeInst{}
		f.instances[s.InstanceID] = inst
	}
	inst.spec = *s
	inst.err = ""
	return nil
}

func (f *Fake) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNextStart {
		f.FailNextStart = false
		return fmt.Errorf("fake start failed")
	}
	inst, ok := f.instances[id]
	if !ok {
		return fmt.Errorf("instance %s not ensured", id)
	}
	inst.running = true
	inst.ready = true
	inst.err = ""
	return nil
}

func (f *Fake) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNextStop {
		f.FailNextStop = false
		return fmt.Errorf("fake stop failed")
	}
	inst, ok := f.instances[id]
	if !ok {
		return nil
	}
	inst.running = false
	inst.ready = false
	return nil
}

func (f *Fake) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.instances, id)
	return nil
}

func (f *Fake) Inspect(_ context.Context, id string) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst, ok := f.instances[id]
	if !ok {
		return Status{InstanceID: id}, nil
	}
	return Status{
		InstanceID: id,
		Running:    inst.running,
		Ready:      inst.ready,
		Generation: inst.spec.Generation,
		Error:      inst.err,
	}, nil
}

func (f *Fake) Stats(_ context.Context, id string) (Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst, ok := f.instances[id]
	if !ok || !inst.running {
		return Usage{}, nil
	}
	return Usage{CPUMillis: 10, MemoryMB: 64, DiskMB: 100, PIDs: 1}, nil
}

func (f *Fake) Endpoint(id string) (Endpoint, error) {
	return Endpoint{URL: "http://127.0.0.1:0/" + id}, nil
}

func (f *Fake) List(_ context.Context) ([]Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Status, 0, len(f.instances))
	for id, inst := range f.instances {
		out = append(out, Status{
			InstanceID: id,
			Running:    inst.running,
			Ready:      inst.ready,
			Generation: inst.spec.Generation,
			Error:      inst.err,
		})
	}
	return out, nil
}
