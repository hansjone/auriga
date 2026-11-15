package main

import (
	"context"
	"errors"
	"sync"
)

// Physical instance modes remain cached until the control plane invalidates them.
// This cache selects the authentication path. Shared member authorization still
// uses a fresh authority Check on every request.
type sharedModeCache struct {
	mu         sync.Mutex
	modes      map[string]bool
	generation uint64
}

func (e *SharedEntry) instanceShared(ctx context.Context, id string) (bool, error) {
	e.modes.mu.Lock()
	mode, known := e.modes.modes[id]
	generation := e.modes.generation
	e.modes.mu.Unlock()
	if known {
		return mode, nil
	}

	mode, err := e.Authority.IsShared(ctx, id)
	if err != nil {
		return false, err
	}
	e.modes.mu.Lock()
	defer e.modes.mu.Unlock()
	// An invalidation during lookup makes this response ineligible for caching
	// or routing. The next request resolves the new physical instance state.
	if generation != e.modes.generation {
		return false, errors.New("common.unavailable")
	}
	if e.modes.modes == nil {
		e.modes.modes = make(map[string]bool)
	}
	e.modes.modes[id] = mode
	return mode, nil
}

// InvalidateInstanceMode must run on every edge replica after authoritative
// state commits and before new routes/node registrations are published for a
// mode change, deletion or ID reuse. A new SharedEntry starts
// with an empty cache. Missing modes fail closed while the authority is down.
func (e *SharedEntry) InvalidateInstanceMode(id string) {
	e.modes.mu.Lock()
	defer e.modes.mu.Unlock()
	delete(e.modes.modes, id)
	e.modes.generation++
}
