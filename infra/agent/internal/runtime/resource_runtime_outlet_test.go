package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func TestSignedResourceRuntimeUplinkReadsReportAfterEngineInspect(t *testing.T) {
	f := newResourceRuntimeFixture(t)
	r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
	f.pendingRestart(t, r, statuses)
	f.validCatalog()
	id := f.required.InstanceId
	if result := resourceRestart(t, r, id); !result.GetOk() {
		t.Fatal(result)
	}
	ready := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
	record := assertRequiredResourceReady(t, ready, f.preparing, f.release.store, f.required)
	r.Close()

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	f.engine.mu.Lock()
	f.engine.beforeInspect = func(containerID string) error {
		if containerID != record.ContainerID {
			return nil
		}
		f.engine.mu.Lock()
		f.engine.beforeInspect = nil
		f.engine.mu.Unlock()
		close(entered)
		<-release
		return nil
	}
	f.engine.mu.Unlock()
	execution := r.CaptureUplinkExecution(id, ready.ObservedGeneration)
	done := make(chan *nodev1.InstanceStatus, 1)
	go func() { done <- r.StatusForUplink(t.Context(), ready, execution) }()
	select {
	case <-entered:
	case got := <-done:
		t.Fatal("uplink skipped the Engine inspection", got)
	case <-time.After(3 * time.Second):
		t.Fatal("Engine inspection did not reach the barrier")
	}
	current := proto.Clone(ready).(*nodev1.InstanceStatus)
	current.UnifiedResourceReports[0].State = "applying"
	if err := f.release.store.PutActual(state.ActualRecord{Status: current}); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case got := <-done:
		if !got.GetReady() || len(got.GetUnifiedResourceReports()) != 0 || !proto.Equal(got.GetUpdatedAt(), ready.GetUpdatedAt()) {
			t.Fatal("the inspected container was READY but its original applied report was stale", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("uplink check did not finish")
	}
}

func TestSignedResourceRuntimeReadOnlyInspectionKeepsProof(t *testing.T) {
	f := newResourceRuntimeFixture(t)
	elapse := f.controlLeaseClock(t, false)
	r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
	f.pendingRestart(t, r, statuses)
	f.validCatalog()
	id := f.required.InstanceId
	if result := resourceRestart(t, r, id); !result.GetOk() {
		t.Fatal(result)
	}
	record := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
	r.Close()
	var withdrawals atomic.Int32
	f.driver.SetEndpointRevoker(func(string) { withdrawals.Add(1) })
	verified, err := runtime.InspectReadOnly(t.Context(), f.preparing, id)
	if err != nil || !verified.StillReady() {
		t.Fatal("read-only inspection did not verify the running endpoint", verified, err)
	}
	elapse()
	if verified.StillReady() || withdrawals.Load() != 0 {
		t.Fatal("late lease check accepted the expired proof or withdrew the endpoint", withdrawals.Load())
	}
	live, err := runtime.InspectReadOnly(t.Context(), f.preparing, id)
	if err != nil || live.Ready || !live.Running || live.ContainerID != record.ContainerID || withdrawals.Load() != 0 {
		t.Fatal("read-only readiness check touched publication", live, err, withdrawals.Load())
	}
	if ep, err := f.preparing.Endpoint(id); ep.URL != "" || err == nil || withdrawals.Load() != 1 {
		t.Fatal("normal Endpoint read did not withdraw the expired proof", ep, err, withdrawals.Load())
	}
}

func TestSignedResourceRuntimeRenewalBetweenHealthAndCatalog(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("old-lease-expired=%t", expire), func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			f.pendingRestart(t, r, statuses)
			f.validCatalog()
			id := f.required.InstanceId
			if result := resourceRestart(t, r, id); !result.GetOk() {
				t.Fatal(result)
			}
			original := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
			r.Close()
			verified, err := runtime.InspectReadOnly(t.Context(), f.preparing, id)
			if err != nil || !verified.StillReady() {
				t.Fatal("read-only inspection did not verify the original endpoint", verified, err)
			}
			renewed := proto.Clone(f.required).(*nodev1.InstanceSpec)
			f.renew(renewed)
			wall, expires := f.manager.Now(), f.required.UnifiedResources[0].GetLease().GetExpiresAt()
			oldDeadline, err := time.Parse(time.RFC3339Nano, expires)
			if err != nil {
				t.Fatal(err)
			}
			var expired atomic.Bool
			f.manager.Now = func() time.Time {
				if expired.Load() {
					return oldDeadline
				}
				return wall
			}
			if err := f.release.store.PutDesired(renewed); err != nil {
				t.Fatal(err)
			}
			before := f.engineCounts()
			health, err := f.preparing.CheckHealth(t.Context(), id)
			if err != nil || !health.Ready || health.Generation != original.Generation {
				t.Fatal("health did not validate the original process", health, err)
			}
			_, guardian, err := f.manager.Status(id)
			if err != nil || !proto.Equal(guardian.GetLease(), renewed.UnifiedResources[0].GetLease()) {
				t.Fatal("health did not store the signed renewal", guardian, err)
			}
			// This is the point after CheckHealth and before health asks
			// the native catalog to confirm the renewed view.
			expired.Store(expire)
			reports, guardian, err := f.preparing.ResourceObservations(t.Context(), id)
			if expire {
				if err == nil || len(reports) != 0 || guardian.GetState() != "active" || f.engineCounts() != before {
					t.Fatalf("expired proof isolated a renewed live view: reports=%v guardian=%v error=%v Engine=%v (was %v)",
						reports, guardian, err, f.engineCounts(), before)
				}
			} else if err != nil || len(reports) != 1 || reports[0].GetState() != "applied" {
				t.Fatal("still-valid endpoint failed catalog observation", reports, guardian, err)
			}
			if err := f.driver.Activation.Recover(t.Context(), id, original.Generation, func(ctx context.Context) error {
				return f.preparing.Start(ctx, id)
			}); err != nil {
				t.Fatal(err)
			}
			if !verified.StillReady() {
				t.Fatal("the renewed proof did not cover the inspected execution")
			}
			record, err := f.release.store.GetExecution(id)
			if err != nil || record == nil || record.ContainerID != original.ContainerID || f.engineCounts() != before {
				t.Fatal("renewed proof changed the live execution", record, err)
			}
			if reports, _, err := f.preparing.ResourceState(id); err != nil || len(reports) != 1 ||
				reports[0].GetObservedGeneration() != uint64(original.Generation) || reports[0].GetState() != "applied" {
				t.Fatal("incorrect report after same-container recovery", reports, err)
			}
		})
	}
}

func TestSignedResourceRuntimeLateReadCannotWithdrawRenewedProof(t *testing.T) {
	for _, operation := range []string{"endpoint", "health"} {
		t.Run(operation, func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			f.pendingRestart(t, r, statuses)
			f.validCatalog()
			id := f.required.InstanceId
			if result := resourceRestart(t, r, id); !result.GetOk() {
				t.Fatal(result)
			}
			old := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
			r.Close()
			renewed := proto.Clone(f.required).(*nodev1.InstanceSpec)
			f.renew(renewed)
			if err := f.release.store.PutDesired(renewed); err != nil {
				t.Fatal(err)
			}
			var withdrawals atomic.Int32
			f.driver.SetEndpointRevoker(func(string) { withdrawals.Add(1) })
			captured, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			f.engine.mu.Lock()
			f.engine.beforeInspect = func(string) error {
				f.engine.mu.Lock()
				f.engine.beforeInspect = nil
				f.engine.mu.Unlock()
				close(captured)
				<-release
				return errors.New("stale read failed")
			}
			f.engine.mu.Unlock()
			done := make(chan error, 1)
			go func() {
				if operation == "health" {
					_, err := f.driver.CheckHealth(t.Context(), id)
					done <- err
				} else {
					_, err := f.driver.Endpoint(id)
					done <- err
				}
			}()
			select {
			case <-captured:
			case <-time.After(5 * time.Second):
				t.Fatal("old proof read did not reach Engine barrier")
			}
			before := f.engineCounts()
			if err := f.driver.Activation.Recover(t.Context(), id, old.Generation, func(ctx context.Context) error {
				return f.preparing.Start(ctx, id)
			}); err != nil {
				t.Fatal(err)
			}
			afterRecover := withdrawals.Load()
			if afterRecover == 0 {
				t.Fatal("recovery did not withdraw the old proof")
			}
			unblock()
			if err := <-done; err == nil {
				t.Fatal("old read unexpectedly succeeded")
			}
			if ep, err := f.driver.Endpoint(id); err != nil || ep.URL == "" || withdrawals.Load() != afterRecover {
				t.Fatal("late read withdrew the new proof", ep, err, afterRecover, withdrawals.Load())
			}
			if f.engineCounts() != before {
				t.Fatal("recovery or late read mutated the executing container", before, f.engineCounts())
			}
		})
	}
}

func (f *resourceRuntimeFixture) engineCounts() [3]int {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	return [3]int{f.engine.creates, len(f.engine.starts), f.engine.stops}
}
