package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const requiredPluginCatalog = `{"privateOnly":false,"skills":[],"plugins":[{"name":"@example/catalog-probe","version":"1.0.0","enabled":true,"installed":true,"loaded":true}]}`
const resourceFixtureToken = "signed-runtime-resource-fixture"

type resourceRuntimeFixture struct {
	engine     *releaseEngine
	driver     *runtime.Docker
	release    *dockerReleaseFixture
	manager    *runtime.UnifiedManager
	preparing  *runtime.Preparing
	plain      *nodev1.InstanceSpec
	required   *nodev1.InstanceSpec
	renew      func(*nodev1.InstanceSpec)
	sealingKey [32]byte
}

func newResourceRuntimeFixture(t *testing.T) *resourceRuntimeFixture {
	t.Helper()
	e, d, release := newReleaseEngine(t)
	manager, required, renew := runtime.NewUnifiedRequiredPluginFixture(t)
	required.Dsh = proto.Clone(release.dsh).(*nodev1.DshRuntime)
	required.Profile = &nodev1.Profile{ListenPort: 7860}
	required.Generation, required.ResourceSelectionRevision = 2, 4
	required.UnifiedResources[0].InstallationRevision = 3
	plain := proto.Clone(required).(*nodev1.InstanceSpec)
	plain.Generation, plain.ResourceSelectionRevision = 1, 0
	plain.UnifiedResources = nil
	manager.Driver, manager.Specs = d, release.store
	d.DataRoot = filepath.Dir(manager.Layout.InstancesDir)
	prep := &runtime.Preparing{
		Inner: d, Unified: manager, Specs: release.store, Executions: release.store,
		Layout: manager.Layout, UIDs: storage.NewUIDTable(release.dir),
		Artifacts: artifacts.New(filepath.Join(release.dir, "artifacts"), d.Policy), Policy: d.Policy, Access: testAccess(t),
	}
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	prep.SealingPrivate = priv
	for _, spec := range []*nodev1.InstanceSpec{plain, required} {
		token, err := renderer.SealForTest(pub, spec.InstanceId, "instance_token", spec.Generation, []byte(resourceFixtureToken))
		if err != nil {
			t.Fatal(err)
		}
		spec.Secrets = &nodev1.Secrets{InstanceToken: token}
	}
	return &resourceRuntimeFixture{e, d, release, manager, prep, plain, required, renew, pub}
}

func (f *resourceRuntimeFixture) pendingRestart(t *testing.T, r *reconciler.Reconciler, statuses <-chan *nodev1.InstanceStatus) *state.ExecutionRecord {
	t.Helper()
	if err := r.Apply(f.plain); err != nil {
		t.Fatal(err)
	}
	plain := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
	if !plain.Ready || plain.ObservedGeneration != 1 || len(plain.UnifiedResourceReports) != 0 {
		t.Fatal(plain)
	}
	record, err := f.release.store.GetExecution(f.plain.InstanceId)
	if err != nil || record == nil {
		t.Fatal(record, err)
	}
	if err := r.Apply(f.required); err != nil {
		t.Fatal(err)
	}
	pending := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
	if !pending.Ready || pending.ObservedGeneration != 1 || len(pending.UnifiedResourceReports) != 0 {
		t.Fatal(pending)
	}
	return record
}

func startResourceReconciler(t *testing.T, store *state.Store, prep runtime.Driver) (*reconciler.Reconciler, <-chan *nodev1.InstanceStatus) {
	t.Helper()
	r := reconciler.New(store, prep, nil, 1)
	ch := make(chan *nodev1.InstanceStatus, 32)
	r.Sink = func(status *nodev1.InstanceStatus) { ch <- status }
	t.Cleanup(r.Close)
	return r, ch
}

func awaitResourceRuntime(t *testing.T, statuses <-chan *nodev1.InstanceStatus, phase nodev1.InstancePhase) *nodev1.InstanceStatus {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case st := <-statuses:
			if st.Phase == phase {
				return st
			}
			if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
				t.Fatalf("runtime did not reach %s: %v", phase, st)
			}
		case <-timeout.C:
			t.Fatalf("runtime did not reach %s", phase)
		}
	}
}

func resourceRestart(t *testing.T, r *reconciler.Reconciler, id string) *nodev1.CommandResult {
	return resourceRestartAs(t, r, id, "restart-for-required-plugin")
}

func resourceRestartAs(t *testing.T, r *reconciler.Reconciler, id, command string) *nodev1.CommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	return r.Command(ctx, &nodev1.Command{CommandId: command, InstanceId: id,
		Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{Grace: durationpb.New(time.Second), Reason: "user"}}})
}

// Check the real public driver entry points while the catalog is outstanding
// and again after it is verified but before the bbolt callback returns.
func unpublishedResourceStart(ctx context.Context, store *state.Store, prep *runtime.Preparing, id string) error {
	record, err := store.GetExecution(id)
	if err != nil || record == nil || record.Generation != 2 || record.Phase != "started" {
		return fmt.Errorf("actual startup record not durable: %v (%v)", record, err)
	}
	if err := store.CheckReleaseExecution(record.Candidate); !errors.Is(err, state.ErrReleaseUnresolved) {
		return fmt.Errorf("own startup intent not visible in bbolt: %v", err)
	}
	if got, err := prep.Inspect(ctx, id); !errors.Is(err, state.ErrReleaseUnresolved) || got.Ready {
		return fmt.Errorf("public Inspect qualified an in-flight start: %+v (%v)", got, err)
	}
	if ep, err := prep.Endpoint(id); !errors.Is(err, state.ErrReleaseUnresolved) || ep.URL != "" {
		return fmt.Errorf("public Endpoint qualified an in-flight start: %+v (%v)", ep, err)
	}
	status, err := store.GetActual(id)
	if err != nil || status.Status.GetReady() || len(status.Status.GetUnifiedResourceReports()) != 0 {
		return fmt.Errorf("startup already published READY/applied: %v (%v)", status.Status, err)
	}
	return nil
}

type resourceIntentFixtureStore struct {
	*state.Store
	beforeCommit func(context.Context) error
	failStarted  bool
}

func (s *resourceIntentFixtureStore) PutExecution(r state.ExecutionRecord) error {
	if s.failStarted && r.Generation == 2 && r.Phase == "started" {
		return errors.New("injected started record write failure")
	}
	return s.Store.PutExecution(r)
}

func (s *resourceIntentFixtureStore) WithReleaseExecution(ctx context.Context, c state.ReleaseCandidate, authorize func(context.Context) error, activate func(context.Context, *state.ReleaseExecution) error, committed ...func() error) error {
	return s.Store.WithReleaseExecution(ctx, c, authorize, func(ctx context.Context, execution *state.ReleaseExecution) error {
		err := activate(ctx, execution)
		if err == nil && s.beforeCommit != nil {
			err = s.beforeCommit(ctx)
		}
		return err
	}, committed...)
}

func (f *resourceRuntimeFixture) setCatalog(handler http.HandlerFunc) {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	f.engine.catalog = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+resourceFixtureToken {
			http.Error(w, "missing fixture authorization", http.StatusForbidden)
			return
		}
		handler(w, r)
	}
}

func assertRequiredResourceReady(t *testing.T, status *nodev1.InstanceStatus, prep *runtime.Preparing, store *state.Store, spec *nodev1.InstanceSpec) *state.ExecutionRecord {
	t.Helper()
	record, err := store.GetExecution(spec.InstanceId)
	if err != nil || record == nil || record.Generation != spec.Generation || record.Phase != "started" {
		t.Fatal(record, err)
	}
	if err := store.CheckReleaseExecution(record.Candidate); err != nil {
		t.Fatal("status published before intent cleared", err)
	}
	if actual, err := prep.Inspect(t.Context(), spec.InstanceId); err != nil || !actual.Ready || actual.Generation != spec.Generation {
		t.Fatal(actual, err)
	}
	got := status.GetUnifiedResourceReports()
	guardian := status.GetUnifiedGuardian()
	ref := spec.UnifiedResources[0]
	if !status.Ready || status.ObservedGeneration != spec.Generation || guardian.GetState() != "active" ||
		guardian.GetGeneration() != uint64(spec.Generation) || guardian.GetSelectionRevision() != spec.ResourceSelectionRevision ||
		len(got) != 1 || got[0].State != "applied" || got[0].ReportSource != "node" || !proto.Equal(got[0].Identity, ref.Identity) ||
		got[0].Generation != uint64(spec.Generation) || got[0].ObservedGeneration != uint64(spec.Generation) ||
		got[0].SelectionRevision != spec.ResourceSelectionRevision || got[0].InstallationRevision != ref.GetInstallationRevision() {
		t.Fatal(status)
	}
	return record
}

func TestSignedResourceRuntimeRestartAndAgentRecover(t *testing.T) {
	f := newResourceRuntimeFixture(t)
	r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
	old := f.pendingRestart(t, r, statuses)
	id := f.required.InstanceId
	configure := func(d *runtime.Docker, prep *runtime.Preparing, store *state.Store) *atomic.Int32 {
		var catalogs atomic.Int32
		d.Activation.Store = &resourceIntentFixtureStore{Store: store, beforeCommit: func(ctx context.Context) error {
			if err := unpublishedResourceStart(ctx, store, prep, id); err != nil {
				return err
			}
			reports, guardian, err := prep.ResourceState(id)
			if err != nil || len(reports) != 0 || guardian.GetState() != "active" {
				return fmt.Errorf("cached applied leaked before commit: %v (%v)", reports, err)
			}
			return nil
		}}
		f.setCatalog(func(w http.ResponseWriter, r *http.Request) {
			catalogs.Add(1)
			if err := unpublishedResourceStart(r.Context(), store, prep, id); err != nil {
				t.Error(err)
				http.Error(w, "invalid publication ordering", http.StatusConflict)
				return
			}
			_, _ = io.WriteString(w, requiredPluginCatalog)
		})
		return &catalogs
	}
	calls := configure(f.driver, f.preparing, f.release.store)
	if result := resourceRestart(t, r, id); !result.GetOk() {
		t.Fatal(result)
	}
	first := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), f.preparing, f.release.store, f.required)
	if first.ContainerID == old.ContainerID || first.Candidate != old.Candidate || calls.Load() != 1 {
		t.Fatal("authorized restart did not qualify the same candidate on a replacement", calls.Load())
	}
	r.Close()
	if err := f.release.store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := state.Open(f.release.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	fresh := runtime.NewDockerWithClient(f.driver.Client, f.driver.DataRoot)
	fresh.Policy = f.driver.Policy
	fresh.Activation = &runtime.ReleaseActivation{Store: reopened, ProofsDir: f.release.proofs, Policy: func() policy.Policy { return fresh.Policy }}
	manager := runtime.NewUnifiedManager(f.manager.Layout, fresh, reopened, t.TempDir())
	manager.Client, manager.Origin, manager.Protocol = f.manager.Client, f.manager.Origin, f.manager.Protocol
	manager.Now, manager.Mono, manager.BootID = f.manager.Now, f.manager.Mono, f.manager.BootID
	prep := &runtime.Preparing{Inner: fresh, Unified: manager, Specs: reopened}
	calls = configure(fresh, prep, reopened)
	f.engine.mu.Lock()
	creates, starts, stops := f.engine.creates, len(f.engine.starts), f.engine.stops
	f.engine.mu.Unlock()
	r, statuses = startResourceReconciler(t, reopened, prep)
	if err := r.ReconcileAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered := assertRequiredResourceReady(t, awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING), prep, reopened, f.required)
	f.engine.mu.Lock()
	unchanged := creates == f.engine.creates && starts == len(f.engine.starts) && stops == f.engine.stops
	f.engine.mu.Unlock()
	if recovered.ContainerID != first.ContainerID || !unchanged || calls.Load() != 1 {
		t.Fatal("same-generation Agent restart failed to observe the original live process", calls.Load())
	}
}

func TestSignedResourceRuntimeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, failure, guardian string
		unknown                 bool
	}{
		{"definite-stop", "native plugin", "private_only", false},
		{"uncertain-stop", "stop response is uncertain", "blocked", true},
		{"prior-intent", state.ErrReleaseUnresolved.Error(), "", true},
		{"process-replacement", "endpoint process changed", "private_only", false},
		{"started-write", "injected started record write failure", "active", false},
		{"catalog-timeout", "context deadline exceeded", "private_only", false},
		{"post-observation-abort", "injected callback failure after resource verification", "active", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceRuntimeFixture(t)
			r, statuses := startResourceReconciler(t, f.release.store, f.preparing)
			if tc.name == "catalog-timeout" {
				r.StartTimeout = 2 * time.Second
			}
			old := f.pendingRestart(t, r, statuses)
			id := f.required.InstanceId
			var catalogs atomic.Int32
			f.setCatalog(func(w http.ResponseWriter, req *http.Request) {
				catalogs.Add(1)
				if err := unpublishedResourceStart(req.Context(), f.release.store, f.preparing, id); err != nil {
					t.Error(err)
				}
				f.engine.mu.Lock()
				f.engine.stopUnknown = tc.name == "uncertain-stop"
				if tc.name == "process-replacement" {
					for _, body := range f.engine.containers {
						body["replaced-process"] = true
					}
				}
				f.engine.mu.Unlock()
				switch tc.name {
				case "catalog-timeout":
					<-req.Context().Done()
				case "process-replacement", "post-observation-abort":
					_, _ = io.WriteString(w, requiredPluginCatalog)
				default:
					_, _ = io.WriteString(w, strings.Replace(requiredPluginCatalog, `"loaded":true`, `"loaded":false`, 1))
				}
			})
			switch tc.name {
			case "prior-intent":
				err := f.release.store.WithReleaseExecution(t.Context(), old.Candidate, func(context.Context) error { return nil }, func(_ context.Context, execution *state.ReleaseExecution) error {
					finish, err := execution.BeginAction()
					if err == nil {
						finish(false)
					}
					return err
				})
				if !errors.Is(err, state.ErrReleaseUnresolved) {
					t.Fatalf("could not persist unresolved intent: %v", err)
				}
			case "started-write":
				f.driver.Activation.Store = &resourceIntentFixtureStore{Store: f.release.store, failStarted: true}
			case "post-observation-abort":
				f.driver.Activation.Store = &resourceIntentFixtureStore{Store: f.release.store, beforeCommit: func(context.Context) error {
					reports, _, err := f.manager.Status(id)
					if err != nil || len(reports) != 1 || reports[0].GetState() != "applied" {
						t.Errorf("resource receipt was not persisted before transaction exit: %v (%v)", reports, err)
					}
					return errors.New(tc.failure)
				}}
			}
			if result := resourceRestart(t, r, id); result.GetOk() {
				t.Fatal("unsafe restart succeeded", result)
			}
			failed := awaitResourceRuntime(t, statuses, nodev1.InstancePhase_INSTANCE_PHASE_FAILED)
			if failed.Ready || len(failed.UnifiedResourceReports) != 0 || !strings.Contains(failed.GetError().GetMessage(), tc.failure) {
				t.Fatal(failed)
			}
			if tc.guardian != "" && failed.GetUnifiedGuardian().GetState() != tc.guardian {
				t.Fatal(failed)
			}
			if ep, err := f.preparing.Endpoint(id); err == nil || ep.URL != "" {
				t.Fatal("failed resource activation exposed endpoint", ep, err)
			}
			if reports, _, err := f.preparing.ResourceState(id); err != nil || len(reports) != 0 {
				t.Fatal("failed resource activation exposed cached applied", reports, err)
			}
			r.Close()
			f.engine.mu.Lock()
			creates, starts, stops := f.engine.creates, len(f.engine.starts), f.engine.stops
			f.engine.mu.Unlock()
			switch tc.name {
			case "prior-intent":
				// The restart command is authorized to stop the old container,
				// but the already persisted intent prevents new Engine actions.
				if creates != 1 || starts != 1 || stops != 1 || catalogs.Load() != 0 {
					t.Fatal("pre-existing intent allowed a rebuild", creates, starts, stops, catalogs.Load())
				}
			case "started-write":
				if creates != 2 || starts != 2 || catalogs.Load() != 0 {
					t.Fatal("resource probe ran without a durable started record", creates, starts, catalogs.Load())
				}
			case "post-observation-abort":
				if creates != 2 || starts != 2 || catalogs.Load() != 1 {
					t.Fatal("failed transaction did not reach resource observation", creates, starts, catalogs.Load())
				}
			default:
				if creates != 2 || starts != 2 || stops < 3 || catalogs.Load() != 1 {
					t.Fatal("failure did not reach isolation after plugin health", creates, starts, stops, catalogs.Load())
				}
				if stopped, err := f.driver.ResourceProcessStopped(t.Context(), id); err != nil || !stopped {
					t.Fatal("isolation/fallback did not stop the process", stopped, err)
				}
			}
			if err := f.release.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := state.Open(f.release.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			err = reopened.CheckReleaseExecution(old.Candidate)
			if (tc.unknown && !errors.Is(err, state.ErrReleaseUnresolved)) || (!tc.unknown && err != nil) {
				t.Fatalf("unexpected persisted intent after reopen: %v", err)
			}
		})
	}
}
