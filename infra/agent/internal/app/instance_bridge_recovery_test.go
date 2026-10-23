package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type stepWakeTargets struct {
	targets *observer.SliceTargets
	step    atomic.Bool
	entered chan struct{}
	advance chan struct{}
	done    chan struct{}
}

func (s *stepWakeTargets) Targets() []observer.InstanceTarget {
	if s.step.Load() {
		select {
		case s.entered <- struct{}{}:
		case <-s.done:
			return nil
		}
		select {
		case <-s.advance:
		case <-s.done:
			return nil
		}
	}
	return s.targets.Targets()
}

type wakeLifecycleReporter struct {
	wakes  chan *nodev1.WakeSchedule
	health chan struct{}
	usage  chan struct{}
}

func (r wakeLifecycleReporter) ReportWake(w *nodev1.WakeSchedule) { r.wakes <- w }
func (r wakeLifecycleReporter) ReportReady(string, bool) {
	select {
	case r.health <- struct{}{}:
	default:
	}
}
func (r wakeLifecycleReporter) ReportUsage(*nodev1.NodeUsage) {
	select {
	case r.usage <- struct{}{}:
	default:
	}
}

func TestBridgeRevocationReaddConsumesUnchangedDueWake(t *testing.T) {
	f := newRecoveryFixture(t)
	f.healthy.Store(true)
	d := f.driver.Driver.(*runtime.Docker)
	const id = "recovery-test"
	recoverRunning := func() {
		t.Helper()
		if err := d.Activation.Recover(context.Background(), id, 1, func(ctx context.Context) error { return d.Start(ctx, id) }); err != nil {
			t.Fatal(err)
		}
		f.bridge.Sync(&nodev1.InstanceStatus{InstanceId: id, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true})
	}
	root := filepath.Join(f.bridge.cfg.InstancesDir(), id, "home")
	if err := os.MkdirAll(filepath.Join(root, "ops-cron"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	due := now.Add(time.Minute)
	path := filepath.Join(root, "ops-cron", "store.json")
	body := fmt.Sprintf(`{"jobs":[{"enabled":true,"nextRunAt":%d,"schedule":{"kind":"at","at":%q}}]}`, due.UnixMilli(), due.Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := &wakeClock{}
	clock.nanos.Store(now.UnixNano())
	source := &stepWakeTargets{targets: f.targets, entered: make(chan struct{}), advance: make(chan struct{}), done: make(chan struct{})}
	reporter := wakeLifecycleReporter{wakes: make(chan *nodev1.WakeSchedule, 8), health: make(chan struct{}, 1), usage: make(chan struct{}, 1)}
	obs := observer.New(observer.Config{Source: source, Reporter: reporter, Collector: observer.NopResources{}, Clock: clock, WatchPoll: time.Millisecond, HealthEvery: time.Hour, UsageEvery: time.Hour})
	recoverRunning()
	obs.Start(context.Background())
	t.Cleanup(func() { close(source.done); obs.Stop() })
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal(what)
		}
	}
	wait(reporter.health, "initial health loop")
	wait(reporter.usage, "initial usage loop")
	select {
	case first := <-reporter.wakes:
		if first.NextWakeAt == nil || !first.NextWakeAt.AsTime().Equal(due) {
			t.Fatal(first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial wake missing")
	}
	if _, ok := f.registry.Lookup(id); !ok {
		t.Fatal("initial ingress absent")
	}
	// Initial health/usage scans are finished; only the watch loop calls this
	// source until their hour-long intervals elapse. Each barrier starts the
	// next refresh only after the previous refresh has fully completed.
	source.step.Store(true)
	wait(source.entered, "watch did not reach first barrier")
	f.inspectError.Store(true)
	if _, err := d.Endpoint(id); err == nil {
		t.Fatal("unproven actual object kept its endpoint")
	}
	if _, ok := f.registry.Lookup(id); ok || len(f.targets.Targets()) != 0 {
		t.Fatal("failed actual proof retained ingress or observer target")
	}
	f.inspectError.Store(false)
	// refreshWakes samples the clock before reading Targets. Advance it before
	// releasing the empty pass so the next pass samples the due timestamp.
	clock.nanos.Store(due.UnixNano())
	source.advance <- struct{}{}
	wait(source.entered, "empty-target refresh did not finish")
	recoverRunning()
	if _, ok := f.registry.Lookup(id); !ok {
		t.Fatal("qualified recovery did not restore ingress")
	}
	source.advance <- struct{}{}
	wait(source.entered, "readded-target refresh did not finish")
	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("test changed schedule mtime", err)
	}
	select {
	case wake := <-reporter.wakes:
		if wake.NextWakeAt != nil {
			t.Fatal("due wake not consumed", wake)
		}
	default:
		t.Fatal("qualified recovery restored ingress but lost unchanged due wake")
	}
}

type countedRecoveryDriver struct {
	runtime.Driver
	ensures    atomic.Int64
	cancelNext atomic.Pointer[recoveryStartCancellation]
}

type recoveryStartCancellation struct {
	started chan context.CancelFunc
}

func (d *countedRecoveryDriver) Ensure(ctx context.Context, spec *runtime.Spec, rendered *runtime.Rendered) error {
	d.ensures.Add(1)
	return d.Driver.Ensure(ctx, spec, rendered)
}

func (d *countedRecoveryDriver) Start(ctx context.Context, id string) error {
	if control := d.cancelNext.Swap(nil); control != nil {
		child, cancel := context.WithCancel(ctx)
		defer cancel()
		control.started <- cancel
		return d.Driver.Start(child, id)
	}
	return d.Driver.Start(ctx, id)
}

type recoveryFixture struct {
	rec                   *reconciler.Reconciler
	bridge                *instanceBridge
	registry              *ingress.MapRegistry
	targets               *observer.SliceTargets
	driver                *countedRecoveryDriver
	statuses              chan *nodev1.InstanceStatus
	terminal              chan *nodev1.InstanceStatus
	probed                chan bool
	stage                 atomic.Value
	healthy, inspectError atomic.Bool
	starts, probes        atomic.Int64
	startedAt             *timestamppb.Timestamp
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	return newRecoveryFixtureWithPort(t, "7860/tcp")
}

func newRecoveryFixtureWithPort(t *testing.T, actualPort string) *recoveryFixture {
	t.Helper()
	f := &recoveryFixture{statuses: make(chan *nodev1.InstanceStatus, 32), terminal: make(chan *nodev1.InstanceStatus, 32),
		probed: make(chan bool, 32), startedAt: timestamppb.New(time.Now().Add(-time.Hour))}
	f.stage.Store("fixture setup")
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.stage.Store("health probe")
		f.probes.Add(1)
		ready := f.healthy.Load()
		select {
		case f.probed <- ready:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "ready": ready, "protocol": 1, "pluginVersion": "test"})
	}))
	t.Cleanup(health.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.stage.Store("Engine " + r.Method + " " + r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/containers/"+fixtureContainerID+"/json":
			if f.inspectError.Load() {
				http.Error(w, "inspection unavailable", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": fixtureContainerID, "Image": fixtureConfigID, "State": map[string]any{"Running": true},
				"Config":          map[string]any{"Labels": map[string]string{"auriga.generation": "1", "auriga.instance": "recovery-test", "auriga.managed": "true"}},
				"HostConfig":      map[string]any{"PortBindings": map[string]any{actualPort: []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": "0"}}}},
				"NetworkSettings": map[string]any{"Ports": map[string]any{actualPort: []any{map[string]string{"HostPort": port}}}}})
		case r.Method == http.MethodPost && r.URL.Path == "/containers/"+fixtureContainerID+"/start":
			f.starts.Add(1)
			w.WriteHeader(http.StatusNotModified) // Existing user process is still running.
		default:
			t.Errorf("recovery unnecessarily changed runtime: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	client, err := runtime.DialEngine(engine.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")
	d.Policy.AllowAnyImage = true
	f.driver = &countedRecoveryDriver{Driver: d}
	fixture := newDockerReleaseFixture(t, d, time.Now().Add(time.Hour))
	store := fixture.store
	seedDockerRecord(t, d, "recovery-test", 1)
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: "recovery-test", ObservedGeneration: 1,
		Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: f.startedAt}}); err != nil {
		t.Fatal(err)
	}
	f.registry, f.targets = ingress.NewMapRegistry(), &observer.SliceTargets{}
	f.bridge = newInstanceBridge(config.Config{DataRoot: t.TempDir()}, f.driver, f.registry, f.targets)
	d.SetEndpointRevoker(f.bridge.remove)
	f.rec = reconciler.New(store, f.driver, nil, 1)
	f.rec.Activation = d.Activation
	t.Cleanup(f.rec.Close)
	// Failure tests cancel only after observing a real unhealthy probe. Keep the
	// production Start budget independent of scheduler/fixture setup latency.
	f.rec.Sink = func(status *nodev1.InstanceStatus) {
		f.stage.Store("bridge sync " + status.Phase.String())
		f.bridge.Sync(status)
		f.statuses <- status
		f.stage.Store("status queued " + status.Phase.String())
		if status.Ready || status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			f.terminal <- status
		}
	}
	f.stage.Store("fixture ready")
	return f
}

func (f *recoveryFixture) next(t *testing.T) *nodev1.InstanceStatus {
	t.Helper()
	return awaitRecovery(t, f, f.statuses, "recovery status")
}

func awaitRecovery[T any](t *testing.T, f *recoveryFixture, events <-chan T, what string) T {
	t.Helper()
	// This is a test-wide deadlock watchdog, not a claimed per-step runtime SLA.
	remaining := time.Minute
	if deadline, ok := t.Deadline(); ok {
		remaining = time.Until(deadline.Add(-2 * time.Second))
	}
	watchdog := time.NewTimer(remaining)
	defer watchdog.Stop()
	select {
	case event := <-events:
		return event
	case <-watchdog.C:
		stack := make([]byte, 1<<20)
		stack = stack[:goruntime.Stack(stack, true)]
		t.Logf("stage=%v queued=%d starts=%d probes=%d ensures=%d\n%s", f.stage.Load(), len(f.statuses), f.starts.Load(), f.probes.Load(), f.driver.ensures.Load(), stack)
		actual := make(chan string, 1)
		go func() {
			rec, err := f.rec.Store.GetActual("recovery-test")
			actual <- fmt.Sprintf("%v error=%v", rec.Status, err)
		}()
		select {
		case snapshot := <-actual:
			t.Log("persisted actual:", snapshot)
		case <-time.After(100 * time.Millisecond):
			t.Log("persisted actual read also blocked")
		}
		t.Fatalf("%s missing before global test watchdog", what)
		var zero T
		return zero
	}
}

func (f *recoveryFixture) waitTerminal(t *testing.T) {
	t.Helper()
	_ = awaitRecovery(t, f, f.terminal, "terminal sink notification")
}

func (f *recoveryFixture) armHealthCancellation(t *testing.T) *recoveryStartCancellation {
	t.Helper()
	control := &recoveryStartCancellation{started: make(chan context.CancelFunc, 1)}
	if !f.driver.cancelNext.CompareAndSwap(nil, control) {
		t.Fatal("a recovery cancellation is already armed")
	}
	return control
}

func (f *recoveryFixture) cancelAfterUnhealthyProbe(t *testing.T, control *recoveryStartCancellation) {
	t.Helper()
	cancel := awaitRecovery(t, f, control.started, "Start context")
	if ready := awaitRecovery(t, f, f.probed, "real unhealthy probe"); ready {
		t.Fatal("failure fixture was already healthy")
	}
	cancel()
}

func TestAgentRestartRestoresHealthAndBridgeWithoutRebuildingContainer(t *testing.T) {
	f := newRecoveryFixture(t)
	f.rec.StartTimeout = 2 * time.Second
	initial, err := f.rec.Inventory()
	if err != nil || len(initial) != 1 || initial[0].Ready {
		t.Fatalf("first inventory reused persisted ready: %v %v", initial, err)
	}
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := f.next(t)
	if first.Ready || first.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING {
		t.Fatalf("re-emitted old ready without live health: %v", first)
	}
	f.healthy.Store(true)
	final := f.next(t)
	if !final.Ready || final.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || !final.StartedAt.AsTime().Equal(f.startedAt.AsTime()) {
		t.Fatalf("bad recovered status: %v", final)
	}
	if _, ok := f.registry.Lookup("recovery-test"); !ok || len(f.targets.Targets()) != 1 {
		t.Fatal("healthy recovery has no ingress/observer")
	}
	if f.driver.ensures.Load() != 0 || f.starts.Load() != 1 || f.probes.Load() == 0 {
		t.Fatal("did not recover the existing process with real bounded health")
	}
	verified, err := f.rec.Inventory()
	if err != nil || len(verified) != 1 || !verified[0].Ready {
		t.Fatal("healthy inventory did not become ready")
	}
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.next(t).Ready || f.starts.Load() != 1 || f.driver.ensures.Load() != 0 {
		t.Fatal("healthy idempotent reconciliation restarted process")
	}
}

func TestRecoveryInspectionOrHealthFailureDoesNotPublishReadyOrRebuildOnRetry(t *testing.T) {
	for _, failure := range []string{"inspect", "health"} {
		t.Run(failure, func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.inspectError.Store(failure == "inspect")
			var control *recoveryStartCancellation
			if failure == "health" {
				control = f.armHealthCancellation(t)
			}
			if err := f.rec.ReconcileAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if control != nil {
				f.cancelAfterUnhealthyProbe(t, control)
			}
			f.waitTerminal(t)
			for {
				status := f.next(t)
				if status.Ready {
					t.Fatal("failure reused persisted ready")
				}
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					break
				}
			}
			if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 || f.driver.ensures.Load() != 0 {
				t.Fatal("failed recovery left ingress or rebuilt process")
			}
			f.inspectError.Store(false)
			f.healthy.Store(true)
			f.rec.StartTimeout = time.Second
			if err := f.rec.ReconcileAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			for {
				status := f.next(t)
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING && status.Ready {
					if !status.StartedAt.AsTime().Equal(f.startedAt.AsTime()) {
						t.Fatal("retry invented a new process start time")
					}
					break
				}
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					t.Fatal("healthy retry failed")
				}
			}
			if f.driver.ensures.Load() != 0 {
				t.Fatal("retry rebuilt still-running user process")
			}
		})
	}
}

func TestOldGenerationPendingRecoveryRetainsDecisionAcrossFailure(t *testing.T) {
	f := newRecoveryFixtureWithPort(t, "8888/tcp")
	if err := f.rec.Store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 2, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING,
		Profile: &nodev1.Profile{ListenPort: 9999}}); err != nil {
		t.Fatal(err)
	}
	previous, err := f.rec.Store.GetActual("recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	previous.Status.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
	reasons := []nodev1.RestartReason{nodev1.RestartReason_RESTART_REASON_DSH_VERSION, nodev1.RestartReason_RESTART_REASON_RESOURCES}
	previous.Status.RestartReasons = reasons
	if err := f.rec.Store.PutActual(previous); err != nil {
		t.Fatal(err)
	}
	control := f.armHealthCancellation(t)
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || status.Ready {
		t.Fatal("unhealthy pending recovery did not start", status)
	}
	f.cancelAfterUnhealthyProbe(t, control)
	f.waitTerminal(t)
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_FAILED || status.Ready ||
		status.ObservedGeneration != 1 || !slices.Equal(status.RestartReasons, reasons) {
		t.Fatal("unhealthy recovery lost the pending decision", status)
	}
	f.healthy.Store(true)
	f.rec.StartTimeout = time.Second
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waitTerminal(t)
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || status.Ready {
		t.Fatal("missing pending recovery start")
	}
	status := f.next(t)
	if status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || !status.Ready || status.ObservedGeneration != 1 ||
		!slices.Equal(status.RestartReasons, reasons) || !status.StartedAt.AsTime().Equal(f.startedAt.AsTime()) || f.driver.ensures.Load() != 0 {
		t.Fatalf("pending decision lost or process rebuilt: %v", status)
	}
	starts := f.starts.Load()
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waitTerminal(t)
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || !status.Ready || status.ObservedGeneration != 1 || f.starts.Load() != starts {
		t.Fatalf("healthy old-generation pending recovery was not idempotent: %v", status)
	}
}

func TestRuntimeObservationFailureWithdrawsAlreadyPublishedBridge(t *testing.T) {
	f := newRecoveryFixture(t)
	f.healthy.Store(true)
	f.rec.StartTimeout = time.Second
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		status := f.next(t)
		if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			t.Fatal(status)
		}
		if status.Ready {
			break
		}
	}
	if _, ok := f.registry.Lookup("recovery-test"); !ok {
		t.Fatal("initial ingress missing")
	}
	f.inspectError.Store(true)
	if _, err := f.driver.Endpoint("recovery-test"); err == nil {
		t.Fatal("failed actual object still has endpoint")
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 {
		t.Fatal("observation failure left previously published ingress/observer")
	}
	if f.driver.ensures.Load() != 0 {
		t.Fatal("observation rebuilt process")
	}
	// Endpoint failure synchronously reenters bridge.remove. Publishing must
	// never hold the bridge mutex across that call.
	joined := make(chan struct{})
	go func() {
		f.bridge.Sync(&nodev1.InstanceStatus{InstanceId: "recovery-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true})
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("Endpoint revocation reentered a held bridge mutex")
	}
}

type capturedEndpointDriver struct {
	runtime.Driver
	captured chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (d *capturedEndpointDriver) Endpoint(id string) (runtime.Endpoint, error) {
	ep, err := d.Driver.Endpoint(id)
	if err == nil {
		d.once.Do(func() { close(d.captured); <-d.release })
	}
	return ep, err
}

func TestRevocationFencesAlreadyCapturedEndpointPublication(t *testing.T) {
	f := newRecoveryFixture(t)
	f.healthy.Store(true)
	f.rec.StartTimeout = time.Second
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		st := f.next(t)
		if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			t.Fatal(st)
		}
		if st.Ready {
			break
		}
	}
	barrier := &capturedEndpointDriver{Driver: f.driver, captured: make(chan struct{}), release: make(chan struct{})}
	f.bridge.driver = barrier
	done := make(chan struct{})
	go func() {
		f.bridge.Sync(&nodev1.InstanceStatus{InstanceId: "recovery-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: f.startedAt})
		close(done)
	}()
	defer func() {
		select {
		case <-barrier.release:
		default:
			close(barrier.release)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	select {
	case <-barrier.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("did not capture verified old Endpoint")
	}
	f.inspectError.Store(true)
	if _, err := f.driver.Endpoint("recovery-test"); err == nil {
		t.Fatal("actual Engine inspection error did not revoke")
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok {
		t.Fatal("revocation did not finish before stale publisher resumed")
	}
	close(barrier.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stale publication did not join")
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 {
		t.Fatal("captured stale Endpoint resurrected ingress/observer after completed revocation")
	}
}

func TestStoppedStatusFencesAlreadyCapturedEndpointPublication(t *testing.T) {
	f := newRecoveryFixture(t)
	f.healthy.Store(true)
	f.rec.StartTimeout = time.Second
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		st := f.next(t)
		if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			t.Fatal(st)
		}
		if st.Ready {
			break
		}
	}
	barrier := &capturedEndpointDriver{Driver: f.driver, captured: make(chan struct{}), release: make(chan struct{})}
	f.bridge.driver = barrier
	done := make(chan struct{})
	go func() {
		f.bridge.Sync(&nodev1.InstanceStatus{InstanceId: "recovery-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: f.startedAt})
		close(done)
	}()
	defer func() {
		select {
		case <-barrier.release:
		default:
			close(barrier.release)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	select {
	case <-barrier.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("did not capture old Endpoint")
	}
	f.bridge.Sync(&nodev1.InstanceStatus{InstanceId: "recovery-test", Phase: nodev1.InstancePhase_INSTANCE_PHASE_STOPPED})
	close(barrier.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stale publisher did not join")
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok {
		t.Fatal("stopped ingress was recreated")
	}
	targets := f.targets.Targets()
	if len(targets) != 1 || targets[0].Process != observer.ProcessStopped || targets[0].BaseURL != nil {
		t.Fatal("stopped observer target was replaced by stale running publication")
	}
}
