package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

type sharedModeAuthority struct {
	sharedTestAuthority
	resolve func(context.Context, string) (bool, error)
}

func (a *sharedModeAuthority) IsShared(ctx context.Context, id string) (bool, error) {
	return a.resolve(ctx, id)
}

func TestSharedModeCacheIndependentOutage(t *testing.T) {
	browser := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	var calls atomic.Int32
	var unavailable, shared atomic.Bool
	authority := &sharedModeAuthority{resolve: func(context.Context, string) (bool, error) {
		calls.Add(1)
		if unavailable.Load() {
			return false, errors.New("common.unavailable")
		}
		return shared.Load(), nil
	}}
	entry := &SharedEntry{Authority: authority}
	browser.g.Shared = entry
	browser.open(t, pathInstanceA)
	if calls.Load() != 1 {
		t.Fatalf("mode lookups after open = %d", calls.Load())
	}
	unavailable.Store(true)
	readIPResponse(t, browser.get(t, instanceMount(pathInstanceA)), http.StatusNoContent, "")
	if calls.Load() != 1 {
		t.Fatal("cached independent instance consulted unavailable authority")
	}
	readIPResponse(t, browser.get(t, instanceMount(pathInstanceB)), http.StatusServiceUnavailable, "common.unavailable")
	entry.InvalidateInstanceMode(pathInstanceA)
	readIPResponse(t, browser.get(t, instanceMount(pathInstanceA)), http.StatusServiceUnavailable, "common.unavailable")
	unavailable.Store(false)
	shared.Store(true)
	// An invalidated independent mode must resolve as shared and require v2 configuration.
	readIPResponse(t, browser.get(t, instanceMount(pathInstanceA)), http.StatusServiceUnavailable, "common.unavailable")
}

func TestSharedModeInvalidationDuringLookup(t *testing.T) {
	started := make(chan struct{})
	resume := make(chan struct{})
	entry := &SharedEntry{Authority: &sharedModeAuthority{resolve: func(context.Context, string) (bool, error) {
		close(started)
		<-resume
		return false, nil
	}}}
	result := make(chan error, 1)
	go func() {
		_, err := entry.instanceShared(t.Context(), pathInstanceA)
		result <- err
	}()
	<-started
	entry.InvalidateInstanceMode(pathInstanceA)
	close(resume)
	if err := <-result; err == nil {
		t.Fatal("lookup crossing invalidation authorized stale mode")
	}
	entry.Authority = &sharedModeAuthority{resolve: func(context.Context, string) (bool, error) { return true, nil }}
	if shared, err := entry.instanceShared(t.Context(), pathInstanceA); err != nil || !shared {
		t.Fatalf("fresh mode = %v, error = %v", shared, err)
	}
}
