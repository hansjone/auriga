//go:build docker

package runtime_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/releaseproof"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

// Integration test against a real Docker Engine. Skipped in default CI
// (build tag docker). Run: go test -tags docker ./internal/runtime/ -count=1
func TestDockerIntegrationSmoke(t *testing.T) {
	pol := policy.Default()
	pol.AllowAnyImage = true // integration only
	d, err := runtime.NewDocker(runtime.DockerConfig{Timeout: 30 * time.Second, Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Client.Ping(ctx); err != nil {
		t.Skip("docker engine not available:", err)
	}
	image := os.Getenv("AURIGA_TEST_IMAGE")
	if image == "" || !containsDigest(image) {
		t.Skip("set AURIGA_TEST_IMAGE=repo@sha256:… to run")
	}
	proofs := os.Getenv("AURIGA_TEST_RUNTIME_PROOFS_DIR")
	if proofs == "" {
		t.Skip("set AURIGA_TEST_RUNTIME_PROOFS_DIR to original public OCI evidence for AURIGA_TEST_IMAGE")
	}
	// This smoke signs the supplied original bytes with a test root. It does
	// not exercise the producer's installed trust root or a production DSH.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	id := "rt2r-smoke-" + hex.EncodeToString(nonce[:])
	containerName, networkName := "auriga-inst-"+id, "auriga-net-"+id
	if old, err := d.Client.ContainerInspect(ctx, containerName); err != nil || old != nil {
		t.Fatalf("smoke container name must be absent: %v %v", old, err)
	}
	if old, err := d.Client.NetworkInspect(ctx, networkName); err != nil || old != nil {
		t.Fatalf("smoke network name must be absent: %v %v", old, err)
	}
	if existing, err := d.Client.ImageInspect(ctx, image); err != nil || existing == nil {
		t.Fatalf("smoke requires the exact image already present: %v", err)
	}
	guard := &smokeEngineGuard{base: d.Client.HTTP.Transport, instance: id}
	d.Client.HTTP.Transport = guard
	t.Logf("smoke instance=%s container=%s network=%s image=%s", id, containerName, networkName, image)
	_, digest, _ := strings.Cut(image, "@")
	m, c, err := releaseproof.Read(proofs, digest)
	if err != nil {
		t.Fatal(err)
	}
	fixture := dockerFixture(t, d)
	entry := filepath.Join(fixture.proofs, digest[7:])
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "manifest.json"), m, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "config.json"), c, 0600); err != nil {
		t.Fatal(err)
	}
	full := releaseSpec(fixture, id, 7)
	full.Dsh.Image = image
	full.Dsh.ReleaseEnvelope.ImageDigest = digest
	signRelease(t, full.Dsh, fixture.key)
	home := t.TempDir()
	meta := t.TempDir()
	spec := &runtime.Spec{
		Full:       full,
		InstanceID: id,
		Generation: 1,
		Image:      image,
		HomeBind:   home,
		MetaBind:   meta,
		HostUID:    200001,
		CPUMillis:  500,
		MemoryMB:   256,
		PIDs:       64,
		ListenPort: 7860,
	}
	// Register before any Engine create, but after the Store and bind temp dirs:
	// cleanup must run while those earlier-registered resources are still live.
	t.Cleanup(func() { cleanupSmoke(t, d, guard) })
	if err := d.Activation.Activate(ctx, full, func(ctx context.Context) error {
		if err := d.Ensure(ctx, spec, &runtime.Rendered{}); err != nil {
			return err
		}
		return d.Start(ctx, id)
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := d.Inspect(ctx, id)
		if err == nil && st.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container not running: %+v %v", st, err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// The smoke transport records successful create receipts before the driver
// consumes them, including when later persistence/Start/health checks fail.
// It also prevents cleanup from following a rebound name to somebody else's
// object. Unknown create outcomes are reported, never guessed from a name.
type smokeEngineGuard struct {
	base      http.RoundTripper
	instance  string
	mu        sync.Mutex
	container string
	network   string
	unknown   bool
}

func (g *smokeEngineGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	kind := ""
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		g.mu.Lock()
		path := req.URL.Path
		switch {
		case req.Method == http.MethodPost && path == "/containers/create" && req.URL.Query().Get("name") == "auriga-inst-"+g.instance:
			kind = "container"
		case req.Method == http.MethodPost && path == "/networks/create":
			kind = "network"
		case g.container != "" && ((req.Method == http.MethodDelete && path == "/containers/"+g.container) || (req.Method == http.MethodPost && (path == "/containers/"+g.container+"/start" || path == "/containers/"+g.container+"/stop"))):
		case req.Method == http.MethodDelete && g.network != "" && (path == "/networks/"+g.network || path == "/networks/auriga-net-"+g.instance):
			req = req.Clone(req.Context())
			req.URL.Path = "/networks/" + g.network
			req.URL.RawPath = ""
		default:
			g.mu.Unlock()
			return nil, fmt.Errorf("smoke refuses mutation outside captured objects: %s %s", req.Method, path)
		}
		if kind != "" {
			if g.unknown || (kind == "container" && g.container != "") || (kind == "network" && g.network != "") {
				g.mu.Unlock()
				return nil, fmt.Errorf("smoke refuses repeated or unresolved create")
			}
			g.unknown = true
		}
		g.mu.Unlock()
	}
	if kind != "" {
		raw, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var body struct {
			Name   string
			Labels map[string]string
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Labels["auriga.instance"] != g.instance || body.Labels["auriga.managed"] != "true" || (kind == "network" && body.Name != "auriga-net-"+g.instance) {
			return nil, fmt.Errorf("smoke refuses create with unexpected ownership")
		}
	}
	resp, err := g.base.RoundTrip(req)
	if err != nil || kind == "" {
		return resp, err
	}
	if resp.StatusCode != http.StatusCreated {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("smoke create has no exclusive creation receipt: HTTP %d", resp.StatusCode)
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("smoke create receipt read failed: %v %v", readErr, closeErr)
	}
	var receipt struct{ ID string }
	if err := json.Unmarshal(raw, &receipt); err != nil || len(receipt.ID) != 64 {
		return nil, fmt.Errorf("smoke create receipt has no exact object ID")
	}
	b, err := hex.DecodeString(receipt.ID)
	if err != nil || hex.EncodeToString(b) != receipt.ID {
		return nil, fmt.Errorf("smoke create receipt has invalid object ID")
	}
	g.mu.Lock()
	if kind == "container" {
		g.container = receipt.ID
	} else {
		g.network = receipt.ID
	}
	g.unknown = false
	g.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

func cleanupSmoke(t *testing.T, d *runtime.Docker, guard *smokeEngineGuard) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	guard.mu.Lock()
	containerID, networkID, unknown := guard.container, guard.network, guard.unknown
	guard.mu.Unlock()
	t.Logf("smoke cleanup captured container=%s network=%s unresolved=%v", containerID, networkID, unknown)
	if unknown {
		t.Error("smoke create outcome unresolved; no cleanup by guessed name")
	}
	if containerID != "" {
		if err := d.Remove(ctx, guard.instance); err != nil {
			t.Errorf("smoke production Remove failed: %v", err)
			// Preserve the production failure while attempting only receipt IDs.
			if err := d.Client.ContainerStop(ctx, containerID, 5); err != nil {
				t.Errorf("smoke exact-container stop failed: %v", err)
			}
			if err := d.Client.ContainerRemove(ctx, containerID, true); err != nil {
				t.Errorf("smoke exact-container remove failed: %v", err)
			}
		}
		if remaining, err := d.Client.ContainerInspect(ctx, containerID); err != nil || remaining != nil {
			t.Errorf("smoke exact-container removal unconfirmed: %v %v", remaining, err)
		}
	}
	if networkID != "" {
		if err := d.Client.NetworkRemove(ctx, networkID); err != nil {
			t.Errorf("smoke exact-network remove failed: %v", err)
		}
		if remaining, err := d.Client.NetworkInspect(ctx, networkID); err != nil || remaining != nil {
			t.Errorf("smoke exact-network removal unconfirmed: %v %v", remaining, err)
		}
	}
}

type smokeRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f smokeRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDockerSmokeGuardUsesOnlyReceiptIDs(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusConflict} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			id := strings.Repeat("c", 64)
			calls := 0
			var deleted string
			g := &smokeEngineGuard{instance: "unique-smoke"}
			g.base = smokeRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method == http.MethodDelete {
					deleted = r.URL.Path
					return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"Id":"` + id + `"}`))}, nil
			})
			req, err := http.NewRequest(http.MethodPost, "http://engine/networks/create", strings.NewReader(`{"Name":"auriga-net-unique-smoke","Labels":{"auriga.managed":"true","auriga.instance":"unique-smoke"}}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := g.RoundTrip(req)
			if status == http.StatusConflict {
				if err == nil || g.network != "" || !g.unknown {
					t.Fatal("name collision was adopted", g.network, err)
				}
			} else {
				if err != nil || g.network != id || g.unknown {
					t.Fatal(g.network, err)
				}
				_ = resp.Body.Close()
			}
			foreign, _ := http.NewRequest(http.MethodDelete, "http://engine/networks/old-resource", nil)
			if _, err := g.RoundTrip(foreign); err == nil || calls != 1 {
				t.Fatal("foreign deletion reached Engine", calls, err)
			}
			owned, _ := http.NewRequest(http.MethodDelete, "http://engine/networks/auriga-net-unique-smoke", nil)
			resp, err = g.RoundTrip(owned)
			if status == http.StatusCreated {
				if err != nil || deleted != "/networks/"+id {
					t.Fatal("owned cleanup followed a name", deleted, err)
				}
				_ = resp.Body.Close()
			} else if err == nil || calls != 1 {
				t.Fatal("collision cleanup reached Engine", calls, err)
			}
		})
	}
	t.Run("cleanup-order", func(t *testing.T) {
		e, d, f := newReleaseEngine(t)
		home, meta := t.TempDir(), t.TempDir()
		base := d.Client.HTTP.Transport
		guard := &smokeEngineGuard{instance: "cleanup-order"}
		guard.base = smokeRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost && r.URL.Path == "/networks/create" {
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"Id":"` + strings.Repeat("d", 64) + `"}`))}, nil
			}
			return base.RoundTrip(r)
		})
		d.Client.HTTP.Transport = guard
		// Same ordering as the opt-in smoke: real Store and bind-dir cleanup
		// are already registered before Engine cleanup, which executes first.
		t.Cleanup(func() {
			for _, dir := range []string{home, meta} {
				if _, err := os.Stat(dir); err != nil {
					t.Errorf("bind directory removed before Engine cleanup: %v", err)
				}
			}
			cleanupSmoke(t, d, guard)
			if r, err := f.store.GetExecution(guard.instance); err != nil || r != nil {
				t.Errorf("production Remove ran after Store close or retained record: %v %v", r, err)
			}
			e.mu.Lock()
			remaining := len(e.containers)
			e.mu.Unlock()
			if remaining != 0 {
				t.Errorf("cleanup retained %d captured containers", remaining)
			}
		})
		// Fail after a definite create but before Start, as failed Activate
		// must still run the registered cleanup with an open real Store.
		s := releaseSpec(f, guard.instance, 7)
		want := fmt.Errorf("failure after actual create")
		err := d.Activation.Activate(context.Background(), s, func(ctx context.Context) error {
			if err := d.Ensure(ctx, &runtime.Spec{Full: s, InstanceID: s.InstanceId, Generation: 1, Image: s.Dsh.Image, HomeBind: home, MetaBind: meta, HostUID: 200001}, nil); err != nil {
				return err
			}
			return want
		})
		if !errors.Is(err, want) {
			t.Fatalf("did not reach controlled post-create failure: %v", err)
		}
	})
}

func containsDigest(image string) bool {
	return len(image) > 0 && (len(image) > 8) && (indexOf(image, "@sha256:") >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
