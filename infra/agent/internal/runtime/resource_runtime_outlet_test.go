package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

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
