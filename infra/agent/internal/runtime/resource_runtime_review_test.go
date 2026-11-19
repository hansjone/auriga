package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestSignedResourceRuntimeCommitRechecksVerifiedStart(t *testing.T) {
	for _, tc := range []string{"process", "original-proof", "resource-lease"} {
		t.Run(tc, func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			old := f.pendingRestart(t, r, statuses)
			f.validCatalog()
			id := f.required.InstanceId
			expected := "endpoint process changed"
			switch tc {
			case "original-proof":
				expected = "manifest.json"
			case "resource-lease":
				expected = "lease_expired"
			}
			checked := false
			f.driver.Activation.Store = &resourceIntentFixtureStore{Store: f.release.store, beforeCommit: func(ctx context.Context) error {
				if err := unpublishedResourceStart(ctx, f.release.store, f.preparing, id); err != nil {
					return err
				}
				reports, _, err := f.manager.Status(id)
				if err != nil || len(reports) != 1 || reports[0].GetState() != "applied" {
					return fmt.Errorf("last resource verification did not finish: %v (%v)", reports, err)
				}
				checked = true
				switch tc {
				case "original-proof":
					digest := strings.TrimPrefix(old.ProofDigest, "sha256:")
					return os.Remove(filepath.Join(f.release.proofs, digest, "manifest.json"))
				case "resource-lease":
					later := f.manager.Now().Add(2 * time.Hour)
					f.manager.Now = func() time.Time { return later }
				default:
					f.replaceProcess()
				}
				return nil
			}}
			result := resourceRestart(t, r, id)
			if !checked || result.GetOk() {
				t.Fatal("restart succeeded across commit gap", checked, result)
			}
			assertResourceNotPublished(t, statuses, f.preparing, id, expected)
			if err := f.release.store.CheckReleaseExecution(old.Candidate); err != nil {
				t.Fatal("definite commit was lost", err)
			}
		})
	}
}

func TestSignedResourceRuntimeBridgeLostDuringOrAfterCommit(t *testing.T) {
	for _, duringCommit := range []bool{true, false} {
		t.Run(fmt.Sprintf("during-commit=%t", duringCommit), func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			release := dockerFixture(t, f.driver)
			record, err := release.store.GetExecution("recovery-test")
			if err != nil || record == nil {
				t.Fatal(record, err)
			}
			detach := func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.attached = false
			}
			checked := false
			f.driver.Activation.Store = &resourceIntentFixtureStore{Store: release.store, beforeCommit: func(context.Context) error {
				if err := release.store.CheckReleaseExecution(record.Candidate); !errors.Is(err, state.ErrReleaseUnresolved) {
					return fmt.Errorf("expected live startup intent: %v", err)
				}
				f.mu.Lock()
				attached := f.attached
				f.mu.Unlock()
				if !attached || f.probes.Load() != 1 {
					return errors.New("healthy bridge proof not established before commit")
				}
				checked = true
				if duringCommit {
					detach()
				}
				return nil
			}}
			err = f.driver.Activation.Recover(t.Context(), record.InstanceID, record.Generation, func(ctx context.Context) error {
				return f.driver.Start(ctx, record.InstanceID)
			})
			if !checked {
				t.Fatal("startup never reached commit", err)
			}
			if duringCommit {
				if err == nil || !strings.Contains(err.Error(), "trusted edge disappeared") {
					t.Fatal("commit published a vanished bridge", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.driver.Endpoint(record.InstanceID); err != nil {
					t.Fatal(err)
				}
				detach()
			}
			if ep, err := f.driver.Endpoint(record.InstanceID); err == nil || ep.URL != "" {
				t.Fatal("endpoint ignored vanished bridge", ep, err)
			}
			if err := release.store.CheckReleaseExecution(record.Candidate); err != nil {
				t.Fatal("bridge loss rewrote definite commit", err)
			}
		})
	}
}

type revokeResourceReadyDriver struct {
	*runtime.Preparing
	afterApplied func()
}

func (d *revokeResourceReadyDriver) ResourceState(id string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	reports, guardian, err := d.Preparing.ResourceState(id)
	if d.afterApplied != nil && len(reports) == 1 && reports[0].State == "applied" {
		d.afterApplied()
		d.afterApplied = nil
	}
	return reports, guardian, err
}

func TestSignedResourceRuntimeChecksEndpointBeforeReadyAndRestart(t *testing.T) {
	for _, afterReady := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-ready=%t", afterReady), func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			driver := &revokeResourceReadyDriver{Preparing: f.preparing}
			r, statuses := startResourceReconciler(t, f.release.store, driver)
			f.pendingRestart(t, r, statuses)
			f.validCatalog()
			if afterReady {
				emit := r.Sink
				r.Sink = func(status *nodev1.InstanceStatus) {
					if status.GetReady() && status.GetObservedGeneration() == f.required.Generation {
						f.replaceProcess()
					}
					emit(status)
				}
			} else {
				driver.afterApplied = f.replaceProcess
			}
			id := f.required.InstanceId
			if result := resourceRestart(t, r, id); result.GetOk() {
				t.Fatal("restart succeeded without a valid endpoint", result)
			}
			if afterReady {
				// This first status was valid when persisted. The final command
				// check must notice that its proof subsequently stopped being valid.
				awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
			}
			assertResourceNotPublished(t, statuses, f.preparing, id, "committed runtime endpoint unavailable")
		})
	}
}

func TestSignedResourceRuntimeLeaseDuringCommittedInspect(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expire=%t", expire), func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			old := f.pendingRestart(t, r, statuses)
			f.validCatalog()
			id := f.required.InstanceId
			elapse := f.controlLeaseClock(t, false)
			entered := make(chan error, 1)
			resume := make(chan struct{})
			var unblocked sync.Once
			unblock := func() { unblocked.Do(func() { close(resume) }) }
			defer unblock()
			var stopsBeforeCommit int
			f.driver.Activation.Store = &resourceIntentFixtureStore{Store: f.release.store, beforeCommit: func(ctx context.Context) error {
				if err := unpublishedResourceStart(ctx, f.release.store, f.preparing, id); err != nil {
					return err
				}
				actual, err := f.release.store.GetExecution(id)
				if err != nil || actual == nil {
					return fmt.Errorf("missing started execution before commit: %v", err)
				}
				f.engine.mu.Lock()
				stopsBeforeCommit = f.engine.stops
				f.engine.beforeInspect = func(container string) error {
					if container != actual.ContainerID {
						return nil
					}
					f.engine.mu.Lock()
					f.engine.beforeInspect = nil
					f.engine.mu.Unlock()
					entered <- f.release.store.CheckReleaseExecution(actual.Candidate)
					<-resume
					return nil
				}
				f.engine.mu.Unlock()
				return nil
			}}
			done := make(chan *nodev1.CommandResult, 1)
			go func() { done <- resourceRestart(t, r, id) }()
			select {
			case err := <-entered:
				if err != nil {
					t.Fatalf("blocked inspect preceded the durable intent commit: %v", err)
				}
			case result := <-done:
				t.Fatal("restart finished without the postcommit Engine read", result)
			case <-time.After(5 * time.Second):
				t.Fatal("postcommit Engine read did not start")
			}
			if expire {
				elapse()
			}
			unblock()
			result := <-done
			if result.GetOk() == expire {
				t.Fatalf("restart after committed inspect (expire=%t): %v", expire, result)
			}
			if expire {
				assertResourceNotPublished(t, statuses, f.preparing, id, "lease_expired")
			} else {
				assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
			}
			if err := f.release.store.CheckReleaseExecution(old.Candidate); err != nil {
				t.Fatal("lease check changed the definite commit", err)
			}
			f.engine.mu.Lock()
			stops := f.engine.stops
			f.engine.mu.Unlock()
			if stops != stopsBeforeCommit {
				t.Fatal("postcommit lease check issued an Engine stop", stopsBeforeCommit, stops)
			}
		})
	}
}

func TestSignedResourceRuntimeLeaseBeforeReadyAndRestart(t *testing.T) {
	for _, afterReady := range []bool{false, true} {
		for _, expire := range []bool{false, true} {
			t.Run(fmt.Sprintf("after-ready=%t/expire=%t", afterReady, expire), func(t *testing.T) {
				f := newResourceRuntimeFixture(t)
				driver := &revokeResourceReadyDriver{Preparing: f.preparing}
				r, statuses := startResourceReconciler(t, f.release.store, driver)
				f.pendingRestart(t, r, statuses)
				f.validCatalog()
				elapse := f.controlLeaseClock(t, true)
				hookReached := false
				hook := func() {
					hookReached = true
					if expire {
						elapse()
					}
				}
				published := false
				emit := r.Sink
				r.Sink = func(status *nodev1.InstanceStatus) {
					if status.GetObservedGeneration() == f.required.Generation && status.GetReady() {
						published = true
						if afterReady {
							hook()
						}
					}
					emit(status)
				}
				if !afterReady {
					driver.afterApplied = hook
				}
				id := f.required.InstanceId
				result := resourceRestart(t, r, id)
				if !hookReached || result.GetOk() == expire || published != (afterReady || !expire) {
					t.Fatalf("expired endpoint at readiness gate (hook=%t, published=%t): %v", hookReached, published, result)
				}
				if expire {
					assertResourceNotPublished(t, statuses, f.preparing, id, "committed runtime endpoint unavailable")
				} else {
					assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
				}
			})
		}
	}
}

// Advance exactly to the signed expiry on one clock, holding the other fixed.
func (f *resourceRuntimeFixture) controlLeaseClock(t *testing.T, monotonic bool) func() {
	t.Helper()
	expires, err := time.Parse(time.RFC3339Nano, f.required.UnifiedResources[0].GetLease().GetExpiresAt())
	if err != nil {
		t.Fatal(err)
	}
	wall, mono := f.manager.Now(), f.manager.Mono()
	var expired atomic.Bool
	if monotonic {
		deadline := mono + expires.Sub(wall).Milliseconds()
		f.manager.Mono = func() int64 {
			if expired.Load() {
				return deadline
			}
			return mono
		}
	} else {
		f.manager.Now = func() time.Time {
			if expired.Load() {
				return expires
			}
			return wall
		}
	}
	return func() { expired.Store(true) }
}

func (f *resourceRuntimeFixture) validCatalog() {
	f.setCatalog(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, requiredPluginCatalog)
	})
}

func (f *resourceRuntimeFixture) replaceProcess() {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	for _, body := range f.engine.containers {
		body["replaced-process"] = true
	}
}

func assertResourceNotPublished(t *testing.T, statuses <-chan *nodev1.InstanceStatus, prep *runtime.Preparing, id, message string) {
	t.Helper()
	failed := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_FAILED)
	if failed.Ready || len(failed.UnifiedResourceReports) != 0 || !strings.Contains(failed.GetError().GetMessage(), message) {
		t.Fatal(failed)
	}
	if ep, err := prep.Endpoint(id); err == nil || ep.URL != "" {
		t.Fatal("failed startup exposed endpoint", ep, err)
	}
	if reports, _, err := prep.ResourceState(id); err != nil || len(reports) != 0 {
		t.Fatal("failed startup exposed applied", reports, err)
	}
}

func TestSignedResourceRuntimeRecoverAfterAuthenticatedRenewal(t *testing.T) {
	for _, action := range []string{"authorized-restart", "different-selection", "old-process-stopped"} {
		t.Run(action, func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			f.pendingRestart(t, r, statuses)
			f.validCatalog()
			id := f.required.InstanceId
			if result := resourceRestart(t, r, id); !result.GetOk() {
				t.Fatal(result)
			}
			original := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
			counts := func() [3]int {
				f.engine.mu.Lock()
				defer f.engine.mu.Unlock()
				return [3]int{f.engine.creates, len(f.engine.starts), f.engine.stops}
			}
			before := counts()

			renewed := proto.Clone(f.required).(*nodev1.InstanceSpec)
			f.renew(renewed)
			token, err := renderer.SealForTest(f.sealingKey, id, "instance_token", renewed.Generation, []byte(resourceFixtureToken))
			if err != nil {
				t.Fatal(err)
			}
			renewed.Secrets.InstanceToken = token
			if err := r.Apply(renewed); err != nil {
				t.Fatal(err)
			}
			checkRenewed := func(prep *runtime.Preparing, store *state.Store, statuses <-chan *nodev1.InstanceStatus) {
				t.Helper()
				pending := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
				actual := assertRequiredResourceReady(t, pending, prep, store, f.required)
				desired, err := store.GetDesired(id)
				if !proto.Equal(desired, renewed) || err != nil || actual.ContainerID != original.ContainerID || counts() != before ||
					!proto.Equal(pending.GetUnifiedGuardian().GetLease(), renewed.UnifiedResources[0].GetLease()) {
					t.Fatal("renewal did not preserve the actual execution and report its refreshed lease", pending, actual, err)
				}
			}
			checkRenewed(f.preparing, f.release.store, statuses)
			r.Close()
			if err := f.release.store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := state.Open(f.release.dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			fresh := runtime.NewDockerWithClient(f.driver.Client, f.driver.DataRoot)
			fresh.Policy = f.driver.Policy
			fresh.Activation = &runtime.ReleaseActivation{Store: store, ProofsDir: f.release.proofs, Policy: func() policy.Policy { return fresh.Policy }}
			manager := runtime.NewUnifiedManager(f.manager.Layout, fresh, store, t.TempDir())
			manager.Client, manager.Origin, manager.Protocol = f.manager.Client, f.manager.Origin, f.manager.Protocol
			manager.Now, manager.Mono, manager.BootID = f.manager.Now, f.manager.Mono, f.manager.BootID
			prep := &runtime.Preparing{
				Inner: fresh, Unified: manager, Specs: store, Executions: store,
				Layout: f.preparing.Layout, UIDs: f.preparing.UIDs, Artifacts: f.preparing.Artifacts,
				Policy: f.preparing.Policy, Access: f.preparing.Access, SealingPrivate: f.preparing.SealingPrivate,
			}
			r, statuses = startResourceReconciler(t, store, prep)
			if err := r.ReconcileAll(t.Context()); err != nil {
				t.Fatal(err)
			}
			checkRenewed(prep, store, statuses)

			switch action {
			case "authorized-restart":
				if result := resourceRestartAs(t, r, id, "restart-after-renewal"); !result.GetOk() {
					t.Fatal(result)
				}
				record := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), prep, store, renewed)
				if record.ContainerID == original.ContainerID || record.Candidate != original.Candidate {
					t.Fatal("authorized restart did not rebuild the renewed view", record)
				}
			case "different-selection":
				changed := proto.Clone(renewed).(*nodev1.InstanceSpec)
				changed.Generation++
				changed.ResourceSelectionRevision++
				changed.UnifiedResources[0].InstallationRevision++
				if err := r.Apply(changed); err != nil {
					t.Fatal(err)
				}
				assertResourceNotPublished(t, statuses, prep, id, "runtime health recovery failed")
				_, guardian, err := prep.ResourceState(id)
				if err != nil || guardian.GetState() != "private_only" || counts()[1] != before[1] {
					t.Fatal("different selection reused the cross-generation permission", guardian, err)
				}
			case "old-process-stopped":
				if err := prep.Stop(t.Context(), id, 0); err != nil {
					t.Fatal(err)
				}
				err := fresh.Activation.Recover(t.Context(), id, original.Generation, func(ctx context.Context) error {
					return prep.Start(ctx, id)
				})
				if err == nil || !strings.Contains(err.Error(), "cannot start a different stopped generation") || counts()[1] != before[1] {
					t.Fatal("renewal restarted an old stopped container", err)
				}
				if _, err := prep.Endpoint(id); err == nil {
					t.Fatal("stopped generation exposed an endpoint")
				}
			}
		})
	}
}
