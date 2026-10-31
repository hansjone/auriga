package runtime_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This is the production HTTP client/driver against a controlled Engine model,
// not a Docker daemon. State is a real bbolt DB and all release evidence is signed.
type releaseEngine struct {
	// Publish configuration and snapshot hooks under mu even before the first
	// test request: newReleaseEngine has already started the HTTP server.
	mu          sync.Mutex
	containers  map[string]map[string]any
	names       map[string]string
	starts      []string
	creates     int
	networks    int
	platform    string
	port        string
	mode        string
	imageID     string
	beforeStart func(string)
	afterStart  func()
	afterCreate func()
	healthy     atomic.Bool
}

func newReleaseEngine(t *testing.T) (*releaseEngine, *runtime.Docker, *dockerReleaseFixture) {
	t.Helper()
	e := &releaseEngine{containers: map[string]map[string]any{}, names: map[string]string{}}
	e.healthy.Store(true)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if e.healthy.Load() {
			_, _ = io.WriteString(w, readyBody)
		} else {
			_, _ = io.WriteString(w, `{"ready":false}`)
		}
	}))
	t.Cleanup(health.Close)
	_, e.port, _ = net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	server := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(server.Close)
	client, err := runtime.DialEngine(server.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")
	d.Policy.AllowAnyImage = true
	return e, d, dockerFixture(t, d)
}

func (e *releaseEngine) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/images/"):
		image := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
		if e.imageID == testDigest(fixtureManifest) && image == fixtureConfigID {
			http.NotFound(w, r)
			return
		}
		id, osName, arch, ref := fixtureConfigID, fixtureOS(), fixtureArch(), image
		if e.imageID != "" {
			id = e.imageID
		}
		switch e.mode {
		case "image-id":
			id = "sha256:" + strings.Repeat("0", 64)
		case "image-platform":
			arch = "unsupported"
		case "repo-suffix":
			ref = "evil.example.com/dsh@" + testDigest(fixtureManifest)
		}
		// Deliberately false annotations are never a source of config identity.
		annotation := "sha256:" + strings.Repeat("0", 64)
		if e.mode == "annotation-only" {
			id, annotation = annotation, fixtureConfigID
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Os": osName, "Architecture": arch, "RepoDigests": []string{ref}, "Descriptor": map[string]any{"annotations": map[string]string{"config.digest": annotation}}})
	case path == "/networks/create":
		e.networks++
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"network-id"}`)
	case path == "/containers/create":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		e.creates++
		id := fmt.Sprintf("%064x", e.creates)
		e.platform = r.URL.Query().Get("platform")
		e.containers[id] = body
		e.names[r.URL.Query().Get("name")] = id
		hook := e.afterCreate
		if hook != nil {
			e.runHookUnlocked(hook)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": id})
	case strings.HasSuffix(path, "/json") && strings.HasPrefix(path, "/containers/"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		if named := e.names[id]; named != "" {
			id = named
		}
		body := e.containers[id]
		if body == nil {
			http.NotFound(w, r)
			return
		}
		image := body["Image"]
		if e.mode == "container-image" {
			image = "sha256:" + strings.Repeat("0", 64)
		}
		actualID := id
		if e.mode == "container-id" {
			actualID = strings.Repeat("f", 64)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": actualID, "Image": image, "State": map[string]any{"Running": body["running"] == true}, "Config": map[string]any{"Labels": body["Labels"]}, "HostConfig": body["HostConfig"], "NetworkSettings": map[string]any{"Ports": map[string]any{"7860/tcp": []any{map[string]string{"HostPort": e.port}}}}})
	case strings.HasSuffix(path, "/start"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/start")
		body := e.containers[id]
		if body == nil {
			http.NotFound(w, r)
			return
		}
		hook := e.beforeStart
		if hook != nil {
			e.runHookUnlocked(func() { hook(id) })
		}
		body["running"] = true
		e.starts = append(e.starts, id)
		afterStart := e.afterStart
		if afterStart != nil {
			e.runHookUnlocked(afterStart)
		}
		if e.mode == "start-500" {
			http.Error(w, "start response is uncertain", 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(path, "/stop"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/stop")
		if body := e.containers[id]; body != nil {
			body["running"] = false
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/containers/") && r.Method == http.MethodDelete:
		if e.mode == "remove-fail" {
			http.Error(w, "remove failed", 500)
			return
		}
		id := strings.TrimPrefix(path, "/containers/")
		delete(e.containers, id)
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/networks/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// The caller holds mu and has snapshotted the hook. Restore the lock even on a
// hook panic so serve's deferred Unlock remains paired on every exit path.
func (e *releaseEngine) runHookUnlocked(hook func()) {
	e.mu.Unlock()
	defer e.mu.Lock()
	hook()
}

func fixtureOS() string {
	var c struct {
		OS string `json:"os"`
	}
	_ = json.Unmarshal(fixtureConfig, &c)
	return c.OS
}
func fixtureArch() string {
	var c struct {
		Arch string `json:"architecture"`
	}
	_ = json.Unmarshal(fixtureConfig, &c)
	return c.Arch
}

func releaseSpec(f *dockerReleaseFixture, id string, seq uint64) *nodev1.InstanceSpec {
	d := proto.Clone(f.dsh).(*nodev1.DshRuntime)
	d.ReleaseEnvelope.ReleaseSequence = seq
	return &nodev1.InstanceSpec{InstanceId: id, Generation: 1, Dsh: d, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
}

func executeRelease(ctx context.Context, d *runtime.Docker, s *nodev1.InstanceSpec, home, meta string) error {
	return d.Activation.Activate(ctx, s, func(ctx context.Context) error {
		if err := d.Ensure(ctx, &runtime.Spec{Full: s, InstanceID: s.InstanceId, Generation: s.Generation, Image: s.Dsh.Image, HomeBind: home, MetaBind: meta, HostUID: 200001, ListenPort: 7860}, nil); err != nil {
			return err
		}
		return d.Start(ctx, s.InstanceId)
	})
}

func TestReleaseEngineImageIdentity(t *testing.T) {
	for _, engineID := range []string{fixtureConfigID, testDigest(fixtureManifest)} {
		name := "config"
		if engineID != fixtureConfigID {
			name = "manifest"
		}
		t.Run(name, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			e.mu.Lock()
			e.imageID = engineID
			e.mu.Unlock()
			if engineID != fixtureConfigID {
				if byConfig, err := d.Client.ImageInspect(context.Background(), fixtureConfigID); byConfig != nil || err == nil || !strings.Contains(err.Error(), "image not found") {
					t.Fatal("containerd fixture unexpectedly resolved config ID", byConfig, err)
				}
			}
			s := releaseSpec(f, "engine-identity", 7)
			if err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir()); err != nil {
				t.Fatal(err)
			}
			r, err := f.store.GetExecution(s.InstanceId)
			if err != nil || r == nil || r.ConfigID != fixtureConfigID || r.ProofDigest != testDigest(fixtureManifest) {
				t.Fatal(r, err)
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil || fields["engineImageId"] != engineID || fields["version"] != float64(2) {
				t.Fatalf("execution did not preserve exact Engine identity: %s %v", raw, err)
			}
			e.mu.Lock()
			createdImage, platform := e.containers[r.ContainerID]["Image"], e.platform
			e.mu.Unlock()
			if createdImage != engineID || platform != fixtureOS()+"/"+fixtureArch() {
				t.Fatalf("create image=%v platform=%q", createdImage, platform)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := state.Open(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			d.Activation.Store = reopened
			// A newer, invalid desired and a changed image-store representation
			// cannot replace the original record or its immutable Engine target.
			desired := proto.Clone(s).(*nodev1.InstanceSpec)
			desired.Generation = 99
			desired.Dsh.ReleaseEnvelope.Signature.Value = "invalid-new-desired"
			if err := reopened.PutDesired(desired); err != nil {
				t.Fatal(err)
			}
			otherID := testDigest(fixtureManifest)
			if engineID != fixtureConfigID {
				otherID = fixtureConfigID
			}
			e.mu.Lock()
			e.imageID = otherID
			e.mu.Unlock()
			recoverFull := &nodev1.InstanceSpec{InstanceId: s.InstanceId, Generation: 1, Dsh: s.Dsh}
			err = d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error {
				return d.Ensure(ctx, &runtime.Spec{Full: recoverFull, InstanceID: s.InstanceId, Generation: 1, Image: s.Dsh.Image, HomeBind: t.TempDir(), MetaBind: t.TempDir(), HostUID: 200001}, nil)
			})
			if err == nil || !strings.Contains(err.Error(), "Engine image identity") {
				t.Fatalf("recovery Ensure reselected current image-store identity: %v", err)
			}
			if err := d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) }); err != nil {
				t.Fatal("recovery reselected from newer desired/image inspect", err)
			}
			r, err = reopened.GetExecution(s.InstanceId)
			if err != nil || r.EngineImageID != engineID || r.Generation != 1 || !proto.Equal(r.DSH, s.Dsh) {
				t.Fatal("recovery replaced original execution", r, err)
			}
			e.mu.Lock()
			e.containers[r.ContainerID]["Image"] = otherID
			starts := len(e.starts)
			e.mu.Unlock()
			if _, err := d.Endpoint(s.InstanceId); err == nil {
				t.Fatal("endpoint accepted the other individually allowed digest")
			}
			if err := d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) }); err == nil {
				t.Fatal("recovery reselected the other individually allowed digest")
			}
			e.mu.Lock()
			count, running := len(e.starts), e.containers[r.ContainerID]["running"]
			e.mu.Unlock()
			if count != starts || running != true {
				t.Fatal("failed identity check started or killed the process")
			}
		})
	}
}

func TestReleaseEngineIdentityCannotSwitchWithinScope(t *testing.T) {
	for _, first := range []string{fixtureConfigID, testDigest(fixtureManifest)} {
		t.Run(first, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			e.mu.Lock()
			e.imageID = first
			e.mu.Unlock()
			second := fixtureConfigID
			if first == second {
				second = testDigest(fixtureManifest)
			}
			s := releaseSpec(f, "fixed-engine-scope", 7)
			spec := &runtime.Spec{Full: s, InstanceID: s.InstanceId, Generation: 1, Image: s.Dsh.Image, HomeBind: t.TempDir(), MetaBind: t.TempDir(), HostUID: 200001}
			err := d.Activation.Activate(context.Background(), s, func(ctx context.Context) error {
				if err := d.Ensure(ctx, spec, nil); err != nil {
					return err
				}
				e.mu.Lock()
				e.imageID = second
				e.mu.Unlock()
				return d.Ensure(ctx, spec, nil)
			})
			if err == nil || !strings.Contains(err.Error(), "Engine image identity") {
				t.Fatalf("scope switched execution identity: %v", err)
			}
			e.mu.Lock()
			creates, networks := e.creates, e.networks
			e.mu.Unlock()
			r, readErr := f.store.GetExecution(s.InstanceId)
			if readErr != nil || r == nil || r.EngineImageID != first || creates != 1 || networks != 1 {
				t.Fatal("switch reached replacement side effects", r, readErr, creates, networks)
			}
		})
	}
}

func TestReleaseManifestIdentityRejectsOtherEvidence(t *testing.T) {
	for _, mode := range []string{"annotation-only", "image-id", "image-platform", "repo-suffix", "container-image", "config-tamper", "record-config-tamper"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			e.mu.Lock()
			e.imageID = testDigest(fixtureManifest)
			e.mu.Unlock()
			s := releaseSpec(f, "manifest-reject", 7)
			switch mode {
			case "config-tamper":
				entry := filepath.Join(f.proofs, testDigest(fixtureManifest)[7:], "config.json")
				if err := os.WriteFile(entry, append(bytes.Clone(fixtureConfig), ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "record-config-tamper":
				if err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir()); err != nil {
					t.Fatal(err)
				}
				r, err := f.store.GetExecution(s.InstanceId)
				if err != nil {
					t.Fatal(err)
				}
				r.ConfigID = "sha256:" + strings.Repeat("e", 64)
				if err := f.store.PutExecution(*r); err != nil {
					t.Fatal(err)
				}
				if err := d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) }); err == nil {
					t.Fatal("Engine M hid a record ConfigID not bound by original M")
				}
				if _, err := d.Endpoint(s.InstanceId); err == nil {
					t.Fatal("unbound record config published an endpoint")
				}
				return
			default:
				e.mu.Lock()
				e.mode = mode
				e.mu.Unlock()
			}
			if err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir()); err == nil {
				t.Fatal("invalid evidence executed via manifest identity")
			}
			e.mu.Lock()
			starts, creates, networks := len(e.starts), e.creates, e.networks
			e.mu.Unlock()
			if starts != 0 || (mode != "container-image" && (creates != 0 || networks != 0)) {
				t.Fatal("invalid evidence reached Engine mutation", starts, creates, networks)
			}
		})
	}
}

func TestReleaseEngineHooksRunOutsideMutex(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	var seen atomic.Uint32
	var locked atomic.Bool
	check := func(bit uint32) {
		// No other Engine request is in flight in this sequential lifecycle.
		// A failed TryLock means this callback was invoked under the fixture lock.
		if !e.mu.TryLock() {
			locked.Store(true)
			return
		}
		seen.Or(bit)
		e.mu.Unlock()
	}
	e.mu.Lock()
	e.afterCreate = func() { check(1) }
	e.beforeStart = func(string) { check(2) }
	e.afterStart = func() { check(4) }
	e.mu.Unlock()
	if err := executeRelease(context.Background(), d, releaseSpec(f, "hook-locks", 7), t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if locked.Load() || seen.Load() != 7 {
		t.Fatalf("Engine hooks held mutex or were skipped: locked=%v seen=%d", locked.Load(), seen.Load())
	}
}

func TestReleaseEngineHookPanicRestoresMutex(t *testing.T) {
	e := &releaseEngine{}
	const panicValue = "fixture hook panic"
	func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		defer func() {
			if got := recover(); got != panicValue {
				t.Errorf("hook panic changed or swallowed: %v", got)
			}
		}()
		e.runHookUnlocked(func() {
			if !e.mu.TryLock() {
				t.Error("panic hook ran under fixture mutex")
			} else {
				e.mu.Unlock()
			}
			panic(panicValue)
		})
	}()
	if !e.mu.TryLock() {
		t.Fatal("hook panic leaked fixture mutex")
	}
	e.mu.Unlock()
}

func TestReleaseActualDriverSerializesA7B8AndRejectsOldRecovery(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	accepted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.mu.Lock()
	e.beforeStart = func(string) { once.Do(func() { close(accepted); <-release }) }
	e.mu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	a, b := releaseSpec(f, "release-a", 7), releaseSpec(f, "release-b", 8)
	signRelease(t, b.Dsh, f.key)
	home, meta := t.TempDir(), t.TempDir()
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() { doneA <- executeRelease(context.Background(), d, a, home, meta) }()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("A7 did not reach actual Engine start")
	}
	go func() { doneB <- executeRelease(context.Background(), d, b, home, meta) }()
	select {
	case err := <-doneB:
		t.Fatalf("B8 crossed A7's real start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	record, err := f.store.GetExecution(a.InstanceId)
	if err != nil || record == nil {
		t.Fatal(record, err)
	}
	high, _, err := f.store.GetReleaseHighWater(record.Candidate.Partition)
	if err != nil || high.Sequence != 7 {
		t.Fatal(high, err)
	}
	close(release)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	count := len(e.starts)
	platform := e.platform
	e.mu.Unlock()
	if count != 2 || platform != fixtureOS()+"/"+fixtureArch() {
		t.Fatalf("starts=%d platform=%q", count, platform)
	}
	if err := d.Activation.Recover(context.Background(), a.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, a.InstanceId) }); !errors.Is(err, state.ErrReleaseRollback) {
		t.Fatalf("old A7 recovered after B8: %v", err)
	}
	if _, err := d.Endpoint(a.InstanceId); !errors.Is(err, state.ErrReleaseRollback) {
		t.Fatalf("old endpoint retained: %v", err)
	}
	e.mu.Lock()
	stillRunning := e.containers[record.ContainerID]["running"]
	e.mu.Unlock()
	if stillRunning != true {
		t.Fatal("failed authorization killed existing process")
	}
}

func TestReleaseSubprocessHelper(t *testing.T) {
	if os.Getenv("AURIGA_RT2R_CHILD") != "1" {
		return
	}
	store, err := state.Open(os.Getenv("AURIGA_RT2R_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	raw, err := base64.RawStdEncoding.DecodeString(os.Getenv("AURIGA_RT2R_SPEC"))
	if err != nil {
		t.Fatal(err)
	}
	var s nodev1.InstanceSpec
	if err := proto.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	client, err := runtime.DialEngine(os.Getenv("AURIGA_RT2R_ENGINE"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")
	d.Policy.AllowAnyImage = true
	d.Activation = &runtime.ReleaseActivation{Store: store, ProofsDir: filepath.Join(os.Getenv("AURIGA_RT2R_DIR"), "runtime-proofs"), Policy: func() policy.Policy { return d.Policy }}
	if err := executeRelease(context.Background(), d, &s, t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseProcessDeathKeepsIntentAgainstDelayedEngineStart(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	accepted, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e.mu.Lock()
	e.beforeStart = func(string) { close(accepted); <-release }
	e.afterStart = func() { close(finished) }
	e.mu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	a, b := releaseSpec(f, "release-a", 7), releaseSpec(f, "release-b", 8)
	signRelease(t, b.Dsh, f.key)
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestReleaseSubprocessHelper$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "AURIGA_RT2R_CHILD=1", "AURIGA_RT2R_DIR="+f.dir, "AURIGA_RT2R_ENGINE="+d.Client.BaseURL, "AURIGA_RT2R_SPEC="+base64.RawStdEncoding.EncodeToString(raw))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not reach delayed Engine action")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	reopened, err := state.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	d.Activation.Store = reopened
	err = executeRelease(context.Background(), d, b, t.TempDir(), t.TempDir())
	if !errors.Is(err, state.ErrReleaseUnresolved) {
		t.Fatalf("B8 crossed dead caller's intent: %v; child %s", err, output.String())
	}
	r, err := reopened.GetExecution(a.InstanceId)
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	high, _, err := reopened.GetReleaseHighWater(r.Candidate.Partition)
	if err != nil || high.Sequence != 7 {
		t.Fatal(high, err)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("old Engine action did not finish")
	}
	if err := d.Activation.Recover(context.Background(), a.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, a.InstanceId) }); !errors.Is(err, state.ErrReleaseUnresolved) {
		t.Fatalf("inspect/same-sequence retry cleared crash intent: %v", err)
	}
	e.mu.Lock()
	starts := len(e.starts)
	e.mu.Unlock()
	if starts != 1 {
		t.Fatalf("late A7 Engine starts=%d", starts)
	}
}

func TestReleaseRejectsBadDeclarationsProofsAndActualImages(t *testing.T) {
	for _, mode := range []string{"missing-signature", "bad-signature", "manifest-tamper", "config-tamper", "tag", "signed-platform", "index", "image-id", "image-platform", "repo-suffix", "container-id", "container-image"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			s := releaseSpec(f, "negative-release", 7)
			entry := filepath.Join(f.proofs, testDigest(fixtureManifest)[7:])
			switch mode {
			case "missing-signature":
				s.Dsh.ReleaseEnvelope = nil
			case "bad-signature":
				s.Dsh.ReleaseEnvelope.Signature.Value = strings.Repeat("A", 86)
			case "manifest-tamper":
				if err := os.WriteFile(filepath.Join(entry, "manifest.json"), append(bytes.Clone(fixtureManifest), ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "config-tamper":
				if err := os.WriteFile(filepath.Join(entry, "config.json"), append(bytes.Clone(fixtureConfig), ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "tag":
				s.Dsh.Image = "registry.example.com/auriga/dsh:latest"
			case "signed-platform":
				s.Dsh.ReleaseEnvelope.Os = "unsupported"
				signRelease(t, s.Dsh, f.key)
			case "index":
				m := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
				sum := testDigest(m)
				s.Dsh.Image = "registry.example.com/auriga/dsh@" + sum
				s.Dsh.ReleaseEnvelope.ImageDigest = sum
				signRelease(t, s.Dsh, f.key)
				entry = filepath.Join(f.proofs, sum[7:])
				if err := os.MkdirAll(entry, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(entry, "manifest.json"), m, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(entry, "config.json"), fixtureConfig, 0600); err != nil {
					t.Fatal(err)
				}
			default:
				e.mu.Lock()
				e.mode = mode
				e.mu.Unlock()
			}
			if err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir()); err == nil {
				t.Fatal("untrusted release executed")
			}
			e.mu.Lock()
			starts, creates := len(e.starts), e.creates
			e.mu.Unlock()
			if starts != 0 {
				t.Fatal("invalid evidence reached actual Start")
			}
			if !strings.HasPrefix(mode, "container-") && creates != 0 {
				t.Fatal("pre-create authorization failure created container")
			}
		})
	}
}

type failingExecutionStore struct{ *state.Store }

func (s failingExecutionStore) PutExecution(state.ExecutionRecord) error {
	return fmt.Errorf("injected actual record write failure")
}

func TestReleaseRecordWriteFailureStopsBeforeStartAndRetainsHighWater(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	d.Activation.Store = failingExecutionStore{f.store}
	if err := executeRelease(context.Background(), d, releaseSpec(f, "record-failure", 7), t.TempDir(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "record write failure") {
		t.Fatalf("missing real record failure: %v", err)
	}
	e.mu.Lock()
	creates, starts := e.creates, len(e.starts)
	e.mu.Unlock()
	if creates != 1 || starts != 0 {
		t.Fatalf("creates=%d starts=%d", creates, starts)
	}
	d.Activation.Store = f.store
	if err := d.Activation.Recover(context.Background(), "record-failure", 1, func(ctx context.Context) error { return d.Start(ctx, "record-failure") }); err == nil {
		t.Fatal("unrecorded actual object was guessed")
	}
	p := state.ReleasePartition{Purpose: "dsh_runtime", Name: "dsh", OS: fixtureOS(), Arch: fixtureArch()}
	high, found, err := f.store.GetReleaseHighWater(p)
	if err != nil || !found || high.Sequence != 7 {
		t.Fatal(high, found, err)
	}
}

func TestReleaseKeyExpiryAfterCreateRefusesStart(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	deadline := time.Now().Add(time.Second)
	oldPub, oldKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := &nodev1.ArtifactSigningKey{Kid: "old-child", PublicKey: oldPub, NotAfter: timestamppb.New(deadline), EndorsedByKid: "root", Endorsement: ed25519.Sign(f.key, append([]byte("old-child"), oldPub...))}
	if err := f.store.MergeReleaseKeys([]*nodev1.ArtifactSigningKey{old}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := releaseSpec(f, "key-expiry", 7)
	s.Dsh.ReleaseEnvelope.Signature.Kid = "old-child"
	signRelease(t, s.Dsh, oldKey)
	created, release := make(chan struct{}), make(chan struct{})
	e.mu.Lock()
	e.afterCreate = func() { close(created); <-release }
	e.mu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	home, meta := t.TempDir(), t.TempDir()
	go func() { done <- executeRelease(context.Background(), d, s, home, meta) }()
	select {
	case <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach prepare pause before expiry")
	}
	newPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newKey := &nodev1.ArtifactSigningKey{Kid: "new-child", PublicKey: newPub, NotAfter: timestamppb.New(time.Now().Add(30 * time.Minute)), EndorsedByKid: "root", Endorsement: ed25519.Sign(f.key, append([]byte("new-child"), newPub...))}
	if err := f.store.MergeReleaseKeys([]*nodev1.ArtifactSigningKey{newKey}, time.Now()); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Until(deadline.Add(20 * time.Millisecond)))
	<-timer.C
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expired key started")
	}
	e.mu.Lock()
	starts := len(e.starts)
	e.mu.Unlock()
	if starts != 0 {
		t.Fatal("actual Start crossed key expiry")
	}
	high, found, err := f.store.GetReleaseHighWater(state.ReleasePartition{Purpose: "dsh_runtime", Name: "dsh", OS: fixtureOS(), Arch: fixtureArch()})
	if err != nil || !found || high.Sequence != 7 {
		t.Fatal(high, found, err)
	}
}

func TestReleaseAllDockerEntriesRequireLiveCoordinatorAndRejectConflictingBody(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	s := releaseSpec(f, "all-entries", 7)
	drv := &runtime.Spec{Full: s, InstanceID: s.InstanceId, Generation: 1, Image: s.Dsh.Image, HomeBind: t.TempDir(), MetaBind: t.TempDir(), HostUID: 200001}
	if err := d.Ensure(context.Background(), drv, nil); err == nil {
		t.Fatal("bare Ensure bypassed release qualification")
	}
	if err := d.Start(context.Background(), s.InstanceId); err == nil {
		t.Fatal("bare Start bypassed release qualification")
	}
	var escaped context.Context
	if err := d.Activation.Activate(context.Background(), s, func(ctx context.Context) error { escaped = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(escaped, drv, nil); err == nil {
		t.Fatal("expired callback scope remained usable")
	}
	if err := executeRelease(context.Background(), d, s, drv.HomeBind, drv.MetaBind); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"low-sequence", "same-sequence-new-body"} {
		t.Run(mode, func(t *testing.T) {
			bad := proto.Clone(s).(*nodev1.InstanceSpec)
			want := state.ErrReleaseRollback
			if mode == "low-sequence" {
				bad.Dsh.ReleaseEnvelope.ReleaseSequence = 6
			} else {
				want = state.ErrReleaseConflict
				bad.Dsh.Version = "changed"
				bad.Dsh.ReleaseEnvelope.DshVersion = "changed"
			}
			signRelease(t, bad.Dsh, f.key)
			if err := executeRelease(context.Background(), d, bad, drv.HomeBind, drv.MetaBind); !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
	e.mu.Lock()
	creates, starts := e.creates, len(e.starts)
	e.mu.Unlock()
	if creates != 1 || starts != 1 {
		t.Fatalf("rejected releases mutated Engine: %d %d", creates, starts)
	}
}

func TestReleaseHealthFailureIsCompleteBut500ResponseKeepsIntent(t *testing.T) {
	for _, mode := range []string{"health", "start-500"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			s := releaseSpec(f, "action-outcome", 7)
			if mode == "health" {
				e.healthy.Store(false)
			} else {
				e.mu.Lock()
				e.mode = mode
				e.mu.Unlock()
			}
			err := d.Activation.Activate(context.Background(), s, func(ctx context.Context) error {
				if err := d.Ensure(ctx, &runtime.Spec{Full: s, InstanceID: s.InstanceId, Generation: 1, Image: s.Dsh.Image, HostUID: 200001, HomeBind: t.TempDir(), MetaBind: t.TempDir()}, nil); err != nil {
					return err
				}
				phase, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				return d.Start(phase, s.InstanceId)
			})
			if err == nil {
				t.Fatal("failure accepted")
			}
			e.mu.Lock()
			starts := len(e.starts)
			e.mode = ""
			e.mu.Unlock()
			if starts != 1 {
				t.Fatalf("Start not actually applied: %d", starts)
			}
			e.healthy.Store(true)
			err = d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) })
			if mode == "start-500" {
				if !errors.Is(err, state.ErrReleaseUnresolved) {
					t.Fatalf("uncertain Engine failure cleared intent: %v", err)
				}
			} else if err != nil {
				t.Fatalf("completed Engine start with unhealthy process blocked retry: %v", err)
			}
		})
	}
}

func TestReleaseDeleteFailureAndContainerNameRebinding(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	s := releaseSpec(f, "bound-object", 7)
	if err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.GetExecution(s.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.names["auriga-inst-"+s.InstanceId] = strings.Repeat("f", 64)
	e.mu.Unlock()
	if err := d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) }); err != nil {
		t.Fatal("recovery followed rebound name", err)
	}
	e.mu.Lock()
	e.mode = "remove-fail"
	e.mu.Unlock()
	if err := d.Remove(context.Background(), s.InstanceId); err == nil {
		t.Fatal("delete failure ignored")
	}
	retained, err := f.store.GetExecution(s.InstanceId)
	if err != nil || retained == nil || retained.ContainerID != r.ContainerID {
		t.Fatal(retained, err)
	}
	e.mu.Lock()
	e.mode = ""
	e.mu.Unlock()
	if err := d.Remove(context.Background(), s.InstanceId); err != nil {
		t.Fatal(err)
	}
	retained, err = f.store.GetExecution(s.InstanceId)
	if err != nil || retained != nil {
		t.Fatal(retained, err)
	}
	high, found, err := f.store.GetReleaseHighWater(r.Candidate.Partition)
	if err != nil || !found || high.Sequence != 7 {
		t.Fatal(high, found, err)
	}
}

type configuredReleaseDriver struct {
	*runtime.Docker
	home, meta string
}

func (d *configuredReleaseDriver) Ensure(ctx context.Context, s *runtime.Spec, r *runtime.Rendered) error {
	s.Image = s.Full.GetDsh().GetImage()
	s.HomeBind = d.home
	s.MetaBind = d.meta
	s.HostUID = 200001
	s.ListenPort = 7860
	return d.Docker.Ensure(ctx, s, r)
}

func TestReleaseReconcilerFreezesDesiredAndRecoversOriginalPendingGeneration(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	driver := &configuredReleaseDriver{Docker: d, home: t.TempDir(), meta: t.TempDir()}
	rec := reconciler.New(f.store, driver, nil, 2)
	defer rec.Close()
	statuses := make(chan *nodev1.InstanceStatus, 32)
	rec.Sink = func(s *nodev1.InstanceStatus) { statuses <- s }
	accepted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.mu.Lock()
	e.beforeStart = func(string) { once.Do(func() { close(accepted); <-release }) }
	e.mu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	a := releaseSpec(f, "snapshot-pending", 7)
	if err := rec.Apply(a); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("old snapshot did not reach start")
	}
	newer := proto.Clone(a).(*nodev1.InstanceSpec)
	newer.Generation = 2
	newer.Dsh.ReleaseEnvelope.ReleaseSequence = 8
	newer.Dsh.ReleaseEnvelope.Signature.Value = "invalid-new-desired"
	if err := rec.Apply(newer); err != nil {
		t.Fatal(err)
	}
	newer.Generation = 100
	newer.Dsh.ArtifactId = "mutated-after-apply"
	close(release)
	await := func(phase nodev1.InstancePhase) *nodev1.InstanceStatus {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case s := <-statuses:
				if s.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					t.Fatal(s)
				}
				if s.Phase == phase {
					return s
				}
			case <-timer.C:
				t.Fatal("reconcile status missing")
				return nil
			}
		}
	}
	got := await(nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
	if !got.Ready || got.ObservedGeneration != 1 {
		t.Fatal(got)
	}
	actual, err := f.store.GetExecution(a.InstanceId)
	if err != nil || actual.Generation != 1 || actual.DSH.ReleaseEnvelope.ReleaseSequence != 7 {
		t.Fatal(actual, err)
	}
	desired, err := f.store.GetDesired(a.InstanceId)
	if err != nil || desired.Generation != 2 || desired.Dsh.ArtifactId == "mutated-after-apply" {
		t.Fatal(desired, err)
	}
	// Simulate a new agent driver with no endpoint cache; desired now has a bad
	// different release, but the original signed actual generation is recoverable.
	replacement := runtime.NewDockerWithClient(d.Client, "")
	replacement.Policy = d.Policy
	replacement.Activation = d.Activation
	rec.Driver = replacement
	if err := rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got = await(nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
	if !got.Ready || got.ObservedGeneration != 1 {
		t.Fatal(got)
	}
	e.mu.Lock()
	creates, starts := e.creates, len(e.starts)
	e.mu.Unlock()
	if creates != 1 || starts != 1 {
		t.Fatalf("pending recovery rebuilt/restarted old actual: creates=%d starts=%d", creates, starts)
	}
	// Actual status may be lost independently; the execution record still binds
	// the original generation and prevents newer desired from replacing it.
	if err := f.store.DeleteDesired(a.InstanceId); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutDesired(desired); err != nil {
		t.Fatal(err)
	}
	if err := rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got = await(nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
	if !got.Ready || got.ObservedGeneration != 1 {
		t.Fatal(got)
	}
	// Loss of the record must withdraw readiness without guessing from labels.
	if err := f.store.DeleteExecution(a.InstanceId, actual.ContainerID); err != nil {
		t.Fatal(err)
	}
	if err := rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case s := <-statuses:
		if s.Ready || s.Phase != nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			t.Fatal(s)
		}
	case <-timer.C:
		t.Fatal("missing no-record failure")
	}
	e.mu.Lock()
	running := e.containers[actual.ContainerID]["running"]
	e.mu.Unlock()
	if running != true {
		t.Fatal("no-record observation killed running container")
	}
}

func TestReleaseDockerArchiveHashesActualBytesAndCachedTamper(t *testing.T) {
	for _, mode := range []string{"good", "bad-download", "cached-tamper"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			body := []byte("signed-runtime-archive")
			var bad atomic.Bool
			bad.Store(mode == "bad-download")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if bad.Load() {
					_, _ = w.Write([]byte("wrong bytes"))
				} else {
					_, _ = w.Write(body)
				}
			}))
			defer server.Close()
			pol := d.Policy
			pol.AllowAnyArtifact = true
			art := artifacts.New(t.TempDir(), pol)
			d.Activation.Artifacts = art
			s := releaseSpec(f, "archive-release", 7)
			s.Dsh.Digest = testDigest(body)
			s.Dsh.Url = server.URL + "/runtime"
			s.Dsh.ReleaseEnvelope.ArchiveDigest = s.Dsh.Digest
			signRelease(t, s.Dsh, f.key)
			err := executeRelease(context.Background(), d, s, t.TempDir(), t.TempDir())
			if mode == "bad-download" {
				if err == nil {
					t.Fatal("archive digest mismatch reached Docker")
				}
				e.mu.Lock()
				creates := e.creates
				e.mu.Unlock()
				if creates != 0 {
					t.Fatal("bad archive created container")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cached-tamper" {
				path, err := art.Path(s.Dsh.Digest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("tampered cache"), 0600); err != nil {
					t.Fatal(err)
				}
				bad.Store(true)
				if err := d.Activation.Recover(context.Background(), s.InstanceId, 1, func(ctx context.Context) error { return d.Start(ctx, s.InstanceId) }); err == nil {
					t.Fatal("tampered old archive restored Ready")
				}
			}
		})
	}
}

func TestReleaseReconcilerStoppedCreationStillRequiresAuthorization(t *testing.T) {
	for _, mode := range []string{"fresh-good", "fresh-bad", "existing-running-bad"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			s := releaseSpec(f, "desired-stopped", 7)
			home, meta := t.TempDir(), t.TempDir()
			if mode == "existing-running-bad" {
				if err := executeRelease(context.Background(), d, s, home, meta); err != nil {
					t.Fatal(err)
				}
				s.Generation = 2
			}
			s.Desired = nodev1.DesiredState_DESIRED_STATE_STOPPED
			if mode != "fresh-good" {
				s.Dsh.ReleaseEnvelope.Signature.Value = "bad-signature"
			}
			driver := &configuredReleaseDriver{Docker: d, home: home, meta: meta}
			rec := reconciler.New(f.store, driver, nil, 1)
			defer rec.Close()
			statuses := make(chan *nodev1.InstanceStatus, 16)
			rec.Sink = func(st *nodev1.InstanceStatus) { statuses <- st }
			if err := rec.Apply(s); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			var terminal *nodev1.InstanceStatus
			for terminal == nil {
				select {
				case st := <-statuses:
					if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED || st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED {
						terminal = st
					}
				case <-timer.C:
					t.Fatal("stopped reconcile missing")
				}
			}
			if mode == "fresh-bad" {
				if terminal.Phase != nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					t.Fatal(terminal)
				}
			} else if terminal.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STOPPED || terminal.Ready {
				t.Fatal(terminal)
			}
			e.mu.Lock()
			creates, starts := e.creates, len(e.starts)
			var running bool
			for _, body := range e.containers {
				running = running || body["running"] == true
			}
			e.mu.Unlock()
			if mode == "fresh-bad" {
				if creates != 0 || starts != 0 {
					t.Fatal("bad stopped release bypassed authorization")
				}
			} else if creates != 1 || running {
				t.Fatalf("stopped created wrong object or left process running: creates=%d running=%v", creates, running)
			}
			if mode == "fresh-good" && starts != 0 {
				t.Fatal("desired stopped performed Start")
			}
		})
	}
}

type countedReleaseRestartDriver struct {
	*configuredReleaseDriver
	stops atomic.Int64
}

func (d *countedReleaseRestartDriver) Stop(ctx context.Context, id string, grace time.Duration) error {
	d.stops.Add(1)
	return d.Docker.Stop(ctx, id, grace)
}

func TestReleaseRestartPreparesPendingGenerationAfterDurableStop(t *testing.T) {
	for _, mode := range []string{"signed-next", "bad-signature", "resume-failed-start"} {
		t.Run(mode, func(t *testing.T) {
			e, d, f := newReleaseEngine(t)
			driver := &countedReleaseRestartDriver{configuredReleaseDriver: &configuredReleaseDriver{Docker: d, home: t.TempDir(), meta: t.TempDir()}}
			rec := reconciler.New(f.store, driver, nil, 1)
			defer rec.Close()
			statuses := make(chan *nodev1.InstanceStatus, 64)
			rec.Sink = func(s *nodev1.InstanceStatus) { statuses <- s }
			await := func(phase nodev1.InstancePhase) {
				t.Helper()
				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				for {
					select {
					case s := <-statuses:
						if s.Phase == phase {
							return
						}
						if s.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
							t.Fatal(s)
						}
					case <-timer.C:
						t.Fatal("missing phase", phase)
					}
				}
			}
			old := releaseSpec(f, "restart-release", 7)
			if err := rec.Apply(old); err != nil {
				t.Fatal(err)
			}
			await(nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
			next := proto.Clone(old).(*nodev1.InstanceSpec)
			next.Generation = 2
			next.Dsh.ReleaseEnvelope.ReleaseSequence = 8
			signRelease(t, next.Dsh, f.key)
			if mode == "bad-signature" {
				next.Dsh.ReleaseEnvelope.Signature.Value = "invalid-signature"
			}
			if err := rec.Apply(next); err != nil {
				t.Fatal(err)
			}
			await(nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
			if mode == "resume-failed-start" {
				e.healthy.Store(false)
				rec.StartTimeout = 40 * time.Millisecond
			}
			command := &nodev1.Command{CommandId: "signed-runtime-restart", InstanceId: old.InstanceId, Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}}
			result := rec.Command(context.Background(), command)
			if mode == "resume-failed-start" {
				if result.Ok {
					t.Fatal("unhealthy target completed restart")
				}
				created, err := f.store.GetExecution(old.InstanceId)
				if err != nil || created == nil || created.Generation != 2 {
					t.Fatal("target was not durably created before failed Start", created, err)
				}
				e.healthy.Store(true)
				rec.StartTimeout = 2 * time.Second
				result = rec.Command(context.Background(), command)
			}
			actual, err := f.store.GetActual(old.InstanceId)
			if err != nil {
				t.Fatal(err)
			}
			execution, err := f.store.GetExecution(old.InstanceId)
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			creates, starts := e.creates, len(e.starts)
			e.mu.Unlock()
			if driver.stops.Load() != 1 {
				t.Fatal("resumed restart stopped an already stopped execution")
			}
			if mode == "bad-signature" {
				if result.Ok || actual.Status.Ready || creates != 1 || starts != 1 || execution.Generation != 1 {
					t.Fatalf("restart bypassed signed activation: result=%v actual=%v creates=%d starts=%d execution=%v", result, actual.Status, creates, starts, execution)
				}
				return
			}
			if !result.Ok || !actual.Status.Ready || actual.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || actual.Status.ObservedGeneration != 2 || execution.Generation != 2 || execution.DSH.ReleaseEnvelope.ReleaseSequence != 8 || creates != 2 || starts < 2 {
				t.Fatalf("new release was treated as old-execution recovery: result=%v actual=%v execution=%v creates=%d starts=%d", result, actual.Status, execution, creates, starts)
			}
			if !rec.Command(context.Background(), command).Ok {
				t.Fatal("completed command replay failed")
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.creates != creates || len(e.starts) != starts {
				t.Fatal("completed replay rebuilt or restarted runtime")
			}
		})
	}
}
