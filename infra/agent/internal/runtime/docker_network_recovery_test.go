package runtime_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

// Model an existing running instance and a replacement edge that has lost its
// endpoint. Only the network connect and idempotent container start may mutate
// the Engine; creating/replacing the instance or network is always a test error.
type edgeRecoveryFixture struct {
	driver   *runtime.Docker
	client   *runtime.EngineClient
	mu       sync.Mutex
	failure  string
	attached bool
	starts   atomic.Int64
	connects atomic.Int64
	probes   atomic.Int64
}

func newEdgeRecoveryFixture(t *testing.T, failure string) *edgeRecoveryFixture {
	t.Helper()
	f := &edgeRecoveryFixture{failure: failure, attached: failure == "existing-alias"}
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.probes.Add(1)
		_, _ = w.Write([]byte(readyBody))
	}))
	t.Cleanup(health.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				alias, networkID := "portal.example.com", "existing-network-id"
				if f.failure == "existing-alias" || f.failure == "connect-alias" {
					alias = "other.example.com"
				}
				if f.failure == "connect-network" {
					networkID = "foreign-network-id"
				}
				networks["auriga-net-recovery-test"] = map[string]any{"NetworkID": networkID, "Aliases": []string{alias}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "replacement-edge-id", "Config": map[string]any{"Labels": map[string]string{"auriga.role": role}},
				"State": map[string]bool{"Running": f.failure != "stopped-edge"}, "HostConfig": map[string]string{"NetworkMode": mode},
				"NetworkSettings": map[string]any{"Networks": networks}})
		case r.Method == http.MethodGet && (r.URL.Path == "/containers/auriga-inst-recovery-test/json" || r.URL.Path == "/containers/"+fixtureContainerID+"/json"):
			owner := "recovery-test"
			if f.failure == "instance-owner" {
				owner = "other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": fixtureContainerID, "Image": fixtureConfigID, "State": map[string]any{"Running": true, "StartedAt": "2026-01-01T01:02:03Z"},
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
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Container != "replacement-edge-id" ||
				!reflect.DeepEqual(body.EndpointConfig.Aliases, []string{"portal.example.com"}) {
				t.Errorf("wrong recovery edge/alias: %+v %v", body, err)
			}
			if f.failure == "connect-error" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			f.attached = f.failure != "connect-noop"
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
	f.client, err = runtime.DialEngine(engine.URL, time.Second)
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

func TestDockerStartRestoresReplacementEdgeWithoutRebuildingInstance(t *testing.T) {
	f := newEdgeRecoveryFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	before, err := f.client.ContainerInspect(ctx, "auriga-inst-recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := startDocker(t, f.driver, ctx, "recovery-test"); err != nil {
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
	for failure, expected := range map[string]string{
		"network-owner": "ownership/isolation mismatch", "external-network": "ownership/isolation mismatch", "network-driver": "ownership/isolation mismatch",
		"foreign-member": "unexpected instance network member", "instance-owner": "actual container identity mismatch",
		"edge-owner": "running labelled bridge container", "host-edge": "running labelled bridge container", "stopped-edge": "running labelled bridge container",
		"existing-alias": "existing edge endpoint has wrong platform alias", "connect-error": "403",
		"connect-noop": "edge platform alias was not applied", "connect-alias": "edge platform alias was not applied", "connect-network": "edge platform alias was not applied",
	} {
		t.Run(failure, func(t *testing.T) {
			f := newEdgeRecoveryFixture(t, failure)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := startDocker(t, f.driver, ctx, "recovery-test")
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
