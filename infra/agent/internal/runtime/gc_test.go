package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// captureDriver records the last spec handed to the inner driver.
type captureDriver struct {
	*runtime.Fake
	mu   sync.Mutex
	last runtime.Spec
}

func (c *captureDriver) Ensure(ctx context.Context, s *runtime.Spec, r *runtime.Rendered) error {
	c.mu.Lock()
	c.last = *s
	c.mu.Unlock()
	return c.Fake.Ensure(ctx, s, r)
}

// listingSpecs also implements ListDesiredIDs like state.Store.
type listingSpecs struct {
	mu    sync.Mutex
	specs map[string]*nodev1.InstanceSpec
}

func (l *listingSpecs) GetDesired(id string) (*nodev1.InstanceSpec, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.specs[id], nil
}

func (l *listingSpecs) ListDesiredIDs() ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var ids []string
	for id := range l.specs {
		ids = append(ids, id)
	}
	return ids, nil
}

func (l *listingSpecs) remove(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.specs, id)
}

type gcFixture struct {
	prep  *runtime.Preparing
	art   *artifacts.Store
	specs *listingSpecs
	inner *captureDriver
	pub   [32]byte
	srv   *httptest.Server
	clock *manualClock
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func newGCFixture(t *testing.T, handler http.Handler) *gcFixture {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pol := policy.Default()
	pol.AllowAnyArtifact = true
	art := artifacts.New(filepath.Join(root, "artifacts"), pol)
	art.Client = srv.Client()
	clock := &manualClock{now: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	art.Now = clock.Now
	specs := &listingSpecs{specs: map[string]*nodev1.InstanceSpec{}}
	inner := &captureDriver{Fake: runtime.NewFake()}
	prep := &runtime.Preparing{
		Inner:          inner,
		Specs:          specs,
		Artifacts:      art,
		Layout:         &storage.Layout{InstancesDir: filepath.Join(root, "instances")},
		UIDs:           storage.NewUIDTable(filepath.Join(root, "agent")),
		Policy:         policy.Default(),
		SealingPrivate: priv,
		PlatformURL:    "https://edge.example.com",
		Chown:          func(string, int, int) error { return nil },
	}
	return &gcFixture{prep: prep, art: art, specs: specs, inner: inner, pub: pub, srv: srv, clock: clock}
}

func (f *gcFixture) put(t *testing.T, id string, gen int64, plugins map[string]string, env map[string]string) {
	t.Helper()
	sealed, err := renderer.SealForTest(f.pub, id, "instance_token", gen, []byte("tok"))
	if err != nil {
		t.Fatal(err)
	}
	spec := &nodev1.InstanceSpec{
		InstanceId: id,
		Generation: gen,
		OwnerId:    "u1",
		TenantId:   "t1",
		Dsh:        &nodev1.DshRuntime{Image: "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32)},
		Resources:  &nodev1.Resources{CpuMillis: 1000, MemoryMb: 512, Pids: 128},
		Profile:    &nodev1.Profile{ListenPort: 7860, Env: env},
		Secrets:    &nodev1.Secrets{InstanceToken: sealed},
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
	}
	for path, dig := range plugins {
		spec.Plugins = append(spec.Plugins, &nodev1.PluginRef{
			Name: "p" + strings.TrimPrefix(path, "/"), Version: "1.0.0", Url: f.srv.URL + path, Digest: dig, Enabled: true,
		})
	}
	f.specs.mu.Lock()
	f.specs.specs[id] = spec
	f.specs.mu.Unlock()
}

func (f *gcFixture) ensure(t *testing.T, id string) {
	t.Helper()
	if err := f.prep.Ensure(context.Background(), &runtime.Spec{InstanceID: id}, &runtime.Rendered{}); err != nil {
		t.Fatal(err)
	}
}

func (f *gcFixture) cached(t *testing.T, digest string) bool {
	t.Helper()
	p := filepath.Join(f.art.Root, "sha256", strings.TrimPrefix(digest, "sha256:"))
	_, err := os.Stat(p)
	return err == nil
}

// manualTicker hands the test the tick channel.
func manualTicker(ch chan time.Time) func(time.Duration) (<-chan time.Time, func()) {
	return func(time.Duration) (<-chan time.Time, func()) { return ch, func() {} }
}

func bodiesHandler(bodies map[string][]byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
}

func TestPreparingAppliesEnvAllowlist(t *testing.T) {
	f := newGCFixture(t, bodiesHandler(nil))
	f.put(t, "envi", 1, nil, map[string]string{"TZ": "UTC", "FOO": "bar"})
	f.ensure(t, "envi")
	f.inner.mu.Lock()
	env := f.inner.last.Env
	f.inner.mu.Unlock()
	if env["TZ"] != "UTC" {
		t.Fatalf("declared allowlisted key missing: %v", env)
	}
	if _, ok := env["FOO"]; ok {
		t.Fatalf("non-allowlisted key leaked: %v", env)
	}
	for _, k := range []string{"AURIGA_MODE", "AURIGA_INSTANCE_ID", "AURIGA_PLATFORM_URL", "AURIGA_IDENTITY_KEYS_FILE", "AURIGA_INSTANCE_TOKEN_FILE", "DSH_HOME"} {
		if env[k] == "" {
			t.Fatalf("platform key %s missing: %v", k, env)
		}
	}
	for k := range env {
		if !runtime.IsPlatformEnvKey(k) && k != "TZ" {
			t.Fatalf("unexpected key %s", k)
		}
	}
}

func TestPeriodicGCRemovesReleasedKeepsLive(t *testing.T) {
	v1, v2, other := []byte("plugin-v1"), []byte("plugin-v2"), []byte("other-live")
	d1, d2, dOther := digestOf(v1), digestOf(v2), digestOf(other)
	f := newGCFixture(t, bodiesHandler(map[string][]byte{"/v1": v1, "/v2": v2, "/o": other}))

	f.put(t, "inst_a", 1, map[string]string{"/v1": d1}, nil)
	f.ensure(t, "inst_a")
	f.put(t, "inst_b", 1, map[string]string{"/o": dOther}, nil)
	f.ensure(t, "inst_b")
	// Upgrade a: v1 is released.
	f.put(t, "inst_a", 2, map[string]string{"/v2": d2}, nil)
	f.ensure(t, "inst_a")
	// b's refs entry is lost (e.g. refs.json rebuilt); its spec still names it.
	if err := f.art.ReleaseAll("inst_b"); err != nil {
		t.Fatal(err)
	}

	ticks := make(chan time.Time)
	f.prep.NewTicker = manualTicker(ticks)
	stop := f.prep.StartGC(context.Background(), time.Hour)
	defer stop()

	f.clock.Advance(8 * 24 * time.Hour)
	ticks <- time.Time{} // loop picked up tick 1
	ticks <- time.Time{} // tick 1 GC finished, loop picked up tick 2
	ticks <- time.Time{} // tick 2 GC finished

	if f.cached(t, d1) {
		t.Fatal("released v1 should be collected by the periodic GC")
	}
	if !f.cached(t, d2) {
		t.Fatal("live v2 must stay")
	}
	if !f.cached(t, dOther) {
		t.Fatal("artifact named by a live spec must stay even without a refs hold")
	}

	// After the spec is gone and the instance removed, it becomes collectable.
	f.specs.remove("inst_b")
	if err := f.prep.Remove(context.Background(), "inst_b"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(8 * 24 * time.Hour)
	ticks <- time.Time{}
	ticks <- time.Time{}
	if f.cached(t, dOther) {
		t.Fatal("artifact of removed instance should be collected")
	}

	stop()
	stop() // idempotent
	select {
	case ticks <- time.Time{}:
		t.Fatal("loop still running after stop")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStartGCStopsOnContextAndDisabled(t *testing.T) {
	f := newGCFixture(t, bodiesHandler(nil))
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	f.prep.NewTicker = manualTicker(ticks)
	stop := f.prep.StartGC(ctx, time.Minute)
	cancel()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after ctx cancel")
	}

	called := false
	f.prep.NewTicker = func(time.Duration) (<-chan time.Time, func()) {
		called = true
		return ticks, func() {}
	}
	f.prep.StartGC(context.Background(), 0)()
	if called {
		t.Fatal("interval 0 must disable the loop")
	}
}

func TestGCWaitsForInFlightFetch(t *testing.T) {
	body := []byte("slow-plugin")
	dig := digestOf(body)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f := newGCFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_, _ = w.Write(body)
	}))
	f.put(t, "inst_w", 1, map[string]string{"/slow": dig}, nil)

	ensureDone := make(chan error, 1)
	go func() {
		ensureDone <- f.prep.Ensure(context.Background(), &runtime.Spec{InstanceID: "inst_w"}, &runtime.Rendered{})
	}()
	<-started

	gcDone := make(chan struct{})
	go func() {
		_, _ = f.prep.CollectGarbage(context.Background())
		close(gcDone)
	}()
	select {
	case <-gcDone:
		t.Fatal("GC ran while an artifact fetch was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-ensureDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-gcDone:
	case <-time.After(5 * time.Second):
		t.Fatal("GC never ran after fetch finished")
	}
	if !f.cached(t, dig) {
		t.Fatal("freshly fetched artifact was collected")
	}
}

func TestGCSkipsWhenInstanceListingFails(t *testing.T) {
	body := []byte("x-orphan")
	dig := digestOf(body)
	f := newGCFixture(t, bodiesHandler(map[string][]byte{"/x": body}))
	f.put(t, "inst_f", 1, map[string]string{"/x": dig}, nil)
	f.ensure(t, "inst_f")
	_ = f.art.ReleaseAll("inst_f")
	f.prep.Inner = failingList{f.inner}
	f.clock.Advance(30 * 24 * time.Hour)
	if _, err := f.prep.CollectGarbage(context.Background()); err == nil {
		t.Fatal("GC must fail closed when instances cannot be listed")
	}
	if !f.cached(t, dig) {
		t.Fatal("artifact removed although live set was unknown")
	}
}

type failingList struct{ runtime.Driver }

func (failingList) List(context.Context) ([]runtime.Status, error) {
	return nil, context.DeadlineExceeded
}
