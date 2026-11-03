package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Model an existing running instance and a replacement edge that has lost its
// endpoint. Only the network connect and idempotent container start may mutate
// the Engine; creating/replacing the instance or network is always a test error.
type edgeRecoveryFixture struct {
	driver          *runtime.Docker
	client          *runtime.EngineClient
	mu              sync.Mutex
	failure         string
	attached        bool
	starts          atomic.Int64
	connects        atomic.Int64
	probes          atomic.Int64
	unhealthy       atomic.Bool
	revokes         atomic.Int64
	request         atomic.Value // Last Engine request, for scheduling-guard diagnostics.
	startedAt       string
	pid             int
	healthHook      func()
	appliedAliases  []string
	requiredAliases []string
}

func newEdgeRecoveryFixture(t *testing.T, failure string) *edgeRecoveryFixture {
	t.Helper()
	return newEdgeRecoveryFixtureWithIOGuard(t, failure, time.Second)
}

func newEdgeRecoveryFixtureWithIOGuard(t *testing.T, failure string, ioGuard time.Duration) *edgeRecoveryFixture {
	t.Helper()
	f := &edgeRecoveryFixture{failure: failure, attached: failure == "existing-alias", startedAt: "2026-01-01T01:02:03Z", pid: 42}
	f.request.Store("no Engine request")
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.probes.Add(1)
		body := readyBody
		if f.unhealthy.Load() {
			body = strings.Replace(body, `"ready":true`, `"ready":false`, 1)
		}
		f.mu.Lock()
		hook := f.healthHook
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(health.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.request.Store(r.Method + " " + r.URL.Path)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/networks/auriga-net-recovery-test":
			labels := map[string]string{"auriga.instance": "recovery-test", "auriga.managed": "true"}
			if f.failure == "network-owner" {
				labels["auriga.instance"] = "other"
			}
			members := map[string]any{fixtureContainerID: map[string]string{"Name": "auriga-inst-recovery-test"}}
			if f.attached {
				members["replacement-edge-id"] = map[string]string{"Name": "test-edge"}
			}
			if f.failure == "foreign-member" {
				members["foreign-id"] = map[string]string{"Name": "unrelated"}
			}
			driver := "bridge"
			if f.failure == "network-driver" {
				driver = "host"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "existing-network-id", "Name": "auriga-net-recovery-test", "Driver": driver,
				"Internal": f.failure != "external-network", "Labels": labels, "Containers": members})
		case r.Method == http.MethodGet && r.URL.Path == "/containers/test-edge/json":
			role, mode := "edge", "bridge"
			if f.failure == "edge-owner" {
				role = "unrelated"
			}
			if f.failure == "host-edge" {
				mode = "host"
			}
			networks := map[string]any{}
			if f.attached {
				alias, networkID := f.driver.PlatformHost, "existing-network-id"
				if f.failure == "existing-alias" || f.failure == "connect-alias" {
					alias = "other.example.com"
				}
				if f.failure == "connect-network" {
					networkID = "foreign-network-id"
				}
				aliases := append([]string(nil), f.appliedAliases...)
				if len(aliases) == 0 {
					aliases = []string{f.driver.PlatformHost}
				}
				aliases[0] = alias
				if f.failure == "missing-portal-alias" {
					aliases = slices.DeleteFunc(aliases, func(host string) bool { return host == f.driver.PortalHost })
				}
				networks["auriga-net-recovery-test"] = map[string]any{"NetworkID": networkID, "Aliases": aliases}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "replacement-edge-id", "Config": map[string]any{"Labels": map[string]string{"auriga.role": role}},
				"State": map[string]bool{"Running": f.failure != "stopped-edge"}, "HostConfig": map[string]string{"NetworkMode": mode},
				"NetworkSettings": map[string]any{"Networks": networks}})
		case r.Method == http.MethodGet && (r.URL.Path == "/containers/auriga-inst-recovery-test/json" || r.URL.Path == "/containers/"+fixtureContainerID+"/json"):
			owner := "recovery-test"
			if f.failure == "instance-owner" {
				owner = "other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": fixtureContainerID, "Image": fixtureConfigID, "State": map[string]any{"Running": true, "StartedAt": f.startedAt, "Pid": f.pid},
				"Config":     map[string]any{"Labels": map[string]string{"auriga.managed": "true", "auriga.instance": owner, "auriga.generation": "7"}},
				"Mounts":     []any{map[string]any{"Source": "/data/instances/recovery-test/home", "Destination": "/home/dsh", "RW": true}},
				"HostConfig": map[string]any{"NetworkMode": "auriga-net-recovery-test", "PortBindings": map[string]any{"7860/tcp": []any{map[string]string{"HostPort": "0"}}}},
				"NetworkSettings": map[string]any{"Ports": map[string]any{"7860/tcp": []any{map[string]string{"HostPort": port}}},
					"Networks": map[string]any{"auriga-net-recovery-test": map[string]string{"NetworkID": "existing-network-id", "IPAddress": "192.0.2.2"}}}})
		case r.Method == http.MethodPost && r.URL.Path == "/networks/existing-network-id/connect":
			f.connects.Add(1)
			var body struct {
				Container      string
				EndpointConfig struct{ Aliases []string }
			}
			aliases := []string{"portal.example.com"}
			if f.driver.PortalHost != "" {
				aliases = append(aliases, f.driver.PortalHost)
			}
			if f.requiredAliases != nil {
				aliases = f.requiredAliases
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Container != "replacement-edge-id" ||
				!reflect.DeepEqual(body.EndpointConfig.Aliases, aliases) {
				t.Errorf("wrong recovery edge/alias: %+v %v", body, err)
			}
			if f.failure == "connect-error" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			f.attached = f.failure != "connect-noop"
			f.appliedAliases = append([]string(nil), body.EndpointConfig.Aliases...)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/containers/"+fixtureContainerID+"/start":
			f.starts.Add(1)
			w.WriteHeader(http.StatusNotModified) // Preserve the already running process.
		default:
			t.Errorf("recovery attempted unexpected operation: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	f.client, err = runtime.DialEngine(engine.URL, ioGuard)
	if err != nil {
		t.Fatal(err)
	}
	f.driver = runtime.NewDockerWithClient(f.client, "")
	f.driver.Policy.AllowAnyImage = true
	f.driver.EdgeContainer, f.driver.PlatformHost = "test-edge", "portal.example.com"
	// Install the real trust/Store and original actual record before callers
	// start their recovery deadline. CA/key generation and durable seeding are
	// fixture setup, not part of the bounded Engine recovery being asserted.
	seedDockerRecord(t, f.driver, "recovery-test", 7)
	return f
}

func TestDockerIndependentIdentityAliasesSurviveStartAndAutomaticRecovery(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	// The app's IP access + internal model origin + independent static issuer
	// projection supplies these two managed hosts. Only the necessary unique
	// internal/issuer aliases may be connected, not the whole origin allowlist.
	f.driver.PlatformHost = "auriga-instance.internal"
	f.driver.ManagedHosts = []string{"auriga-instance.internal", "native.example.com"}
	f.requiredAliases = []string{"auriga-instance.internal", "native.example.com"}
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	rec, _, statuses := newMaintenanceReconciler(t, f)
	f.mu.Lock()
	f.attached = false
	f.appliedAliases = nil
	f.mu.Unlock()
	rec.RequestHealth("recovery-test")
	guard := time.NewTimer(5 * time.Second)
	defer guard.Stop()
	for {
		select {
		case st := <-statuses:
			if st.Error != nil {
				t.Fatal(st)
			}
			if st.Ready {
				f.mu.Lock()
				aliases := append([]string(nil), f.appliedAliases...)
				f.mu.Unlock()
				if !reflect.DeepEqual(aliases, []string{"auriga-instance.internal", "native.example.com"}) || f.starts.Load() != 1 || f.connects.Load() != 2 {
					t.Fatalf("independent issuer alias lost or original process restarted: aliases=%v starts=%d connects=%d", aliases, f.starts.Load(), f.connects.Load())
				}
				return
			}
		case <-guard.C:
			t.Fatal("automatic recovery did not restore independent identity aliases")
		}
	}
}

func TestDockerHealthRejectsProcessLifetimeChange(t *testing.T) {
	for _, operation := range []string{"check", "recover"} {
		for _, change := range []string{"started-at", "pid", "both"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				f := newEdgeRecoveryFixture(t, "")
				if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				f.healthHook = func() {
					f.mu.Lock()
					defer f.mu.Unlock()
					if change != "pid" {
						f.startedAt = "2026-01-01T01:02:04Z"
					}
					if change != "started-at" {
						f.pid++
					}
					f.unhealthy.Store(true)
				}
				f.mu.Unlock()
				if operation == "check" {
					st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
					if err == nil || st.Ready {
						t.Fatalf("HTTP proof survived process lifetime change: %+v %v", st, err)
					}
				} else if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err == nil {
					t.Fatal("recovery published old HTTP proof for a new process lifetime")
				}
				if _, err := f.driver.Endpoint("recovery-test"); err == nil || f.starts.Load() != 1 {
					t.Fatal("changed process retained endpoint or recovery restarted it")
				}
			})
		}
	}
}

func TestDockerEndpointRejectsProcessChangeBetweenHealthPolls(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.pid++
	f.mu.Unlock()
	if _, err := f.driver.Endpoint("recovery-test"); err == nil {
		t.Fatal("cached endpoint remained usable for a different process")
	}
	st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
	if err != nil || st.Ready {
		t.Fatalf("a fresh healthy process inherited the old endpoint proof: %+v %v", st, err)
	}
}

type processPublicationStore struct {
	runtime.ReleaseState
	afterSave func()
}

func (s processPublicationStore) PutExecution(record state.ExecutionRecord) error {
	if err := s.ReleaseState.PutExecution(record); err != nil {
		return err
	}
	s.afterSave()
	return nil
}

func TestDockerRecoveryRejectsProcessChangeDuringExecutionSave(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	f.driver.Activation.Store = processPublicationStore{ReleaseState: f.driver.Activation.Store, afterSave: func() {
		f.mu.Lock()
		f.pid++
		f.mu.Unlock()
	}}
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err == nil {
		t.Fatal("recovery published a process proof invalidated during durable save")
	}
	if _, err := f.driver.Endpoint("recovery-test"); err == nil {
		t.Fatal("changed process retained its old endpoint")
	}
}

func TestDockerHealthRejectsUnavailableProcessLifetime(t *testing.T) {
	for _, field := range []string{"started-at", "pid"} {
		t.Run(field, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			if field == "started-at" {
				f.startedAt = ""
			} else {
				f.pid = 0
			}
			f.mu.Unlock()
			st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
			if err == nil || st.Ready {
				t.Fatalf("unavailable process identity was accepted: %+v %v", st, err)
			}
			if _, err := f.driver.Endpoint("recovery-test"); err == nil {
				t.Fatal("unavailable process retained endpoint")
			}
		})
	}
}

func newMaintenanceReconciler(t *testing.T, f *edgeRecoveryFixture) (*reconciler.Reconciler, *nodev1.InstanceSpec, chan *nodev1.InstanceStatus) {
	t.Helper()
	store := dockerFixture(t, f.driver).store
	spec := &nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING, Dsh: dockerFixture(t, f.driver).dsh}
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: "recovery-test", ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true}}); err != nil {
		t.Fatal(err)
	}
	rec := reconciler.New(store, f.driver, nil, 1)
	t.Cleanup(rec.Close)
	statuses := make(chan *nodev1.InstanceStatus, 32)
	rec.Sink = func(st *nodev1.InstanceStatus) { statuses <- st }
	return rec, spec, statuses
}

func TestDockerMaintenanceRejectsControlNetworkLostBeforePublication(t *testing.T) {
	for _, boundary := range []string{"health", "save"} {
		t.Run(boundary, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			rec, _, statuses := newMaintenanceReconciler(t, f)
			loseEdge := func() { f.mu.Lock(); f.attached = false; f.mu.Unlock() }
			f.mu.Lock()
			f.attached = false
			if boundary == "health" {
				f.healthHook = loseEdge
			}
			f.mu.Unlock()
			if boundary == "save" {
				f.driver.Activation.Store = processPublicationStore{ReleaseState: f.driver.Activation.Store, afterSave: loseEdge}
			}
			rec.RequestHealth("recovery-test")
			guard := time.NewTimer(5 * time.Second)
			defer guard.Stop()
			for {
				select {
				case st := <-statuses:
					if st.Ready {
						t.Fatal("automatic recovery emitted READY after losing the trusted edge", st)
					}
					if st.Error != nil {
						if _, err := f.driver.Endpoint("recovery-test"); err == nil || f.starts.Load() != 1 {
							t.Fatal("lost edge retained endpoint or restarted original process")
						}
						return
					}
				case <-guard.C:
					t.Fatal("automatic recovery did not finish final topology verification")
				}
			}
		})
	}
}

func TestDockerReadOnlyMaintenanceCancellationDoesNotPoisonQualification(t *testing.T) {
	for _, operation := range []string{"same-spec", "parent-cancel"} {
		t.Run(operation, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			rec, spec, statuses := newMaintenanceReconciler(t, f)
			record, err := dockerFixture(t, f.driver).store.GetExecution("recovery-test")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rec.StartHealthChecks(ctx, time.Hour)
			entered := make(chan struct{})
			var once sync.Once
			f.mu.Lock()
			f.healthHook = func() {
				if f.probes.Load() >= 3 {
					once.Do(func() { close(entered) })
				}
			}
			f.mu.Unlock()
			f.unhealthy.Store(true)
			rec.RequestHealth("recovery-test")
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("read-only maintenance never entered health wait")
			}
			if f.connects.Load() != 1 || f.starts.Load() != 1 {
				t.Fatal("read-only maintenance unexpectedly wrote to Engine")
			}
			if operation == "same-spec" {
				if err := rec.Apply(spec); err != nil {
					t.Fatal(err)
				}
				f.unhealthy.Store(false)
				guard := time.NewTimer(5 * time.Second)
				defer guard.Stop()
			waitReplay:
				for {
					select {
					case st := <-statuses:
						if st.Ready || st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
							break waitReplay
						}
					case <-guard.C:
						t.Fatal("same signed spec replay did not complete")
					}
				}
			} else {
				cancel()
				rec.Close()
			}
			if err := dockerFixture(t, f.driver).store.CheckReleaseExecution(record.Candidate); err != nil {
				t.Fatalf("read-only cancellation fabricated unresolved execution: %v (connects=%d starts=%d)", err, f.connects.Load(), f.starts.Load())
			}
		})
	}
}

func TestDockerQualificationCancellationBeforeExecutionRemainsPrompt(t *testing.T) {
	for _, operation := range []string{"pre-canceled", "partition-wait"} {
		t.Run(operation, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			store := dockerFixture(t, f.driver).store
			record, err := store.GetExecution("recovery-test")
			if err != nil {
				t.Fatal(err)
			}
			if operation == "partition-wait" {
				held, release := make(chan struct{}), make(chan struct{})
				done := make(chan error, 1)
				go func() {
					done <- store.WithReleaseActivation(t.Context(), record.Candidate, func(context.Context) error { return nil }, func(context.Context) error {
						close(held)
						<-release
						return nil
					})
				}()
				t.Cleanup(func() {
					close(release)
					if err := <-done; err != nil {
						t.Error(err)
					}
				})
				select {
				case <-held:
				case <-time.After(3 * time.Second):
					t.Fatal("fixture did not hold the release partition")
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if operation == "pre-canceled" {
				cancel()
			}
			var invoked atomic.Bool
			result := make(chan error, 1)
			go func() {
				result <- f.driver.Activation.Recover(ctx, "recovery-test", 7, func(context.Context) error { invoked.Store(true); return nil })
			}()
			select {
			case err := <-result:
				if err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) || invoked.Load() {
					t.Fatalf("canceled qualification ran an execution or lost cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled qualification kept waiting for the release lock")
			}
			if err := store.CheckReleaseExecution(record.Candidate); err != nil || f.connects.Load() != 1 || f.starts.Load() != 1 {
				t.Fatalf("canceled preflight wrote or poisoned execution: %v", err)
			}
		})
	}
}

func TestDockerMaintenanceDetectsEdgeReplacementAndPreservesHealthyBridge(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	f.driver.PortalHost = "public.example.com"
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	f.driver.SetEndpointRevoker(func(string) { f.revokes.Add(1) })
	for range 2 {
		st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
		if err != nil || !st.Ready || !st.Running {
			t.Fatalf("healthy maintenance failed: %+v %v", st, err)
		}
	}
	if f.revokes.Load() != 0 || f.connects.Load() != 1 || f.starts.Load() != 1 {
		t.Fatal("healthy polling revoked the bridge or mutated the Engine")
	}
	// The old process is still running and its access token may still be valid.
	// Topology, rather than eventual token expiry, must detect the lost edge now.
	f.mu.Lock()
	f.attached = false
	f.mu.Unlock()
	st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
	if err != nil || st.Ready || !st.Running || f.revokes.Load() == 0 {
		t.Fatalf("replacement edge retained readiness: %+v %v", st, err)
	}
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	st, err = f.driver.CheckHealth(t.Context(), "recovery-test")
	if err != nil || !st.Ready || f.connects.Load() != 2 || f.starts.Load() != 1 {
		t.Fatalf("edge was not restored without restarting original process: %+v %v", st, err)
	}
	f.unhealthy.Store(true)
	st, err = f.driver.CheckHealth(t.Context(), "recovery-test")
	if err != nil || st.Ready {
		t.Fatalf("public health false retained Ready: %+v %v", st, err)
	}
}

func TestDockerMaintenanceRejectsUnsafeNetworksAndMissingSecondAlias(t *testing.T) {
	for _, failure := range []string{"foreign-member", "network-owner", "edge-owner", "missing-portal-alias"} {
		t.Run(failure, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, "")
			f.driver.PortalHost = "public.example.com"
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.failure = failure
			f.mu.Unlock()
			st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
			if err == nil || st.Ready || f.connects.Load() != 1 || f.starts.Load() != 1 {
				t.Fatalf("unsafe maintenance changed network/process or reported Ready: %+v %v", st, err)
			}
		})
	}
}

func TestDockerMaintenanceNaturallyRepairsReplacementEdgeThroughReleaseReconciler(t *testing.T) {
	for _, failure := range []string{"connect-noop", "health"} {
		t.Run(failure, func(t *testing.T) { testDockerNaturalMaintenance(t, failure) })
	}
}

func testDockerNaturalMaintenance(t *testing.T, failure string) {
	f := newEdgeRecoveryFixture(t, "")
	f.driver.PortalHost = "public.example.com"
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	before, err := f.client.ContainerInspect(t.Context(), fixtureContainerID)
	if err != nil {
		t.Fatal(err)
	}
	store := dockerFixture(t, f.driver).store
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: "recovery-test", ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: timestamppb.Now()}}); err != nil {
		t.Fatal(err)
	}
	rec := reconciler.New(store, f.driver, nil, 1)
	rec.StartTimeout = 100 * time.Millisecond
	t.Cleanup(rec.Close)
	statuses := make(chan *nodev1.InstanceStatus, 64)
	rec.Sink = func(st *nodev1.InstanceStatus) { statuses <- st }
	f.driver.SetEndpointRevoker(func(string) { f.revokes.Add(1) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rec.StartHealthChecks(ctx, 20*time.Millisecond)
	f.mu.Lock()
	f.attached, f.failure = false, failure
	if failure == "health" {
		f.failure = ""
		f.unhealthy.Store(true)
	}
	f.mu.Unlock()
	rec.RequestHealth("recovery-test")
	waitStatus := func(ready bool) {
		t.Helper()
		guard := time.NewTimer(10 * time.Second)
		defer guard.Stop()
		for {
			select {
			case st := <-statuses:
				if st.Ready == ready && (ready || st.Error != nil) {
					if st.ObservedGeneration != 7 {
						t.Fatal("maintenance changed execution generation", st)
					}
					return
				}
			case <-guard.C:
				t.Fatal("real release/Engine maintenance status missing")
			}
		}
	}
	waitStatus(false)
	if _, err := f.driver.Endpoint("recovery-test"); err == nil || f.revokes.Load() == 0 {
		t.Fatal("failed recovery retained the old ingress endpoint")
	}
	f.mu.Lock()
	f.failure = ""
	f.unhealthy.Store(false)
	f.mu.Unlock()
	// No new observer hint, restart command or spec change: the independent
	// maintenance loop must recover after the revoked target has disappeared.
	waitStatus(true)
	after, err := f.client.ContainerInspect(t.Context(), fixtureContainerID)
	if err != nil || !reflect.DeepEqual(before, after) || f.starts.Load() != 1 {
		t.Fatalf("natural repair replaced/restarted original instance: starts=%d err=%v", f.starts.Load(), err)
	}
	if _, err := f.driver.Endpoint("recovery-test"); err != nil {
		t.Fatal("natural repair did not republish the verified endpoint", err)
	}
}

func TestDockerStartRestoresReplacementEdgeWithoutRebuildingInstance(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	// The assertions concern completed, idempotent recovery. Independent Start
	// calls must not inherit time consumed by prior calls or test scheduling.
	// Each Start has a separate cancel-and-join scheduling watchdog below.
	ctx := context.Background()
	before, err := f.client.ContainerInspect(ctx, "auriga-inst-recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
			t.Fatal(err)
		}
		if f.connects.Load() != 1 || f.starts.Load() != 1 || f.probes.Load() != int64(attempt+1) {
			t.Fatalf("control bridge not restored idempotently: connects=%d starts=%d probes=%d", f.connects.Load(), f.starts.Load(), f.probes.Load())
		}
		if _, err := f.driver.Endpoint("recovery-test"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.client.ContainerInspect(ctx, "auriga-inst-recovery-test")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("recovery changed instance ID/process/generation/mounts: before=%v after=%v err=%v", before, after, err)
	}
}

func TestDockerStartRejectsUnsafeEdgeRecoveryBeforeStartingOrProbing(t *testing.T) {
	// This is a safety assertion, not a one-second business SLA. The separate
	// guard bounds scheduling/Engine stalls; only completed Start can reject.
	const schedulingGuard = 15 * time.Second
	for failure, expected := range map[string]string{
		"network-owner": "ownership/isolation mismatch", "external-network": "ownership/isolation mismatch", "network-driver": "ownership/isolation mismatch",
		"foreign-member": "unexpected instance network member", "instance-owner": "actual container identity mismatch",
		"edge-owner": "running labelled bridge container", "host-edge": "running labelled bridge container", "stopped-edge": "running labelled bridge container",
		"existing-alias": "existing edge endpoint has wrong platform alias", "connect-error": "403",
		"connect-noop": "edge platform alias was not applied", "connect-alias": "edge platform alias was not applied", "connect-network": "edge platform alias was not applied",
	} {
		t.Run(failure, func(t *testing.T) {
			// Only this negative fixture has a longer fake I/O guard. Leave the
			// positive fixtures' client and three-second contexts unchanged.
			f := newEdgeRecoveryFixtureWithIOGuard(t, failure, 2*schedulingGuard)
			err := completedEdgeRecoveryStart(t, f, schedulingGuard)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unsafe recovery did not reach business rejection: %v", err)
			}
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("unsafe recovery accepted or wrong error: %v; want %q", err, expected)
			}
			if _, err := f.driver.Endpoint("recovery-test"); err == nil || f.starts.Load() != 0 || f.probes.Load() != 0 {
				t.Fatal("unsafe recovery started/probed instance or published endpoint")
			}
			if !strings.HasPrefix(failure, "connect-") && f.connects.Load() != 0 {
				t.Fatal("unsafe pre-existing state was changed")
			}
		})
	}
}

func completedEdgeRecoveryStart(t *testing.T, f *edgeRecoveryFixture, guard time.Duration) error {
	t.Helper()
	// All PKI, Store seeding and fixture setup has completed before this guard.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result error
	go func() {
		defer close(done)
		result = startDocker(t, f.driver, ctx, "recovery-test")
	}()
	// Registered after fixture/Store/TempDir cleanup: cancel and join first,
	// including on failed assertions. No Start may write after resource cleanup.
	join := func() {
		cancel()
		<-done
	}
	t.Cleanup(join)
	defer join()
	timer := time.NewTimer(guard)
	defer timer.Stop()
	select {
	case <-done:
		return result
	case <-timer.C:
		request := f.request.Load()
		join()
		t.Fatalf("recovery scheduling guard expired after %s; last Engine request: %v; Start joined: %v", guard, request, result)
		return nil
	}
}

func TestDockerStartFailedEdgeRecoveryClearsPriorReadyEndpoint(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := startDocker(t, f.driver, ctx, "recovery-test"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.failure = "foreign-member"
	f.mu.Unlock()
	if err := startDocker(t, f.driver, ctx, "recovery-test"); err == nil || !strings.Contains(err.Error(), "unexpected instance network member") {
		t.Fatalf("contaminated retry accepted: %v", err)
	}
	if _, err := f.driver.Endpoint("recovery-test"); err == nil || f.starts.Load() != 1 || f.probes.Load() != 1 || f.connects.Load() != 1 {
		t.Fatal("failed bridge retry retained readiness or touched the instance")
	}
	status, err := f.driver.Inspect(ctx, "recovery-test")
	if err != nil || status.Ready || !status.Running || status.Generation != 7 {
		t.Fatalf("failed bridge recovery published ready or changed instance: %+v %v", status, err)
	}
}
