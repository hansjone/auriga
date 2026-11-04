package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

func TestDockerRejectsForeignNetworkConflict(t *testing.T) {
	for _, change := range []string{"internal", "owner", "driver"} {
		t.Run(change, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/networks/create" {
					w.WriteHeader(http.StatusConflict)
					return
				}
				n := map[string]any{"Name": "auriga-net-inst_i1", "Driver": "bridge", "Internal": true,
					"Labels": map[string]string{"auriga.instance": "inst_i1", "auriga.managed": "true"}}
				switch change {
				case "internal":
					n["Internal"] = false
				case "owner":
					n["Labels"] = map[string]string{"auriga.instance": "other", "auriga.managed": "true"}
				case "driver":
					n["Driver"] = "host"
				}
				_ = json.NewEncoder(w).Encode(n)
			}))
			defer srv.Close()
			c, _ := runtime.DialEngine(srv.URL, time.Second)
			_, err := c.NetworkCreate(context.Background(), "auriga-net-inst_i1", true,
				map[string]string{"auriga.instance": "inst_i1", "auriga.managed": "true"})
			if err == nil {
				t.Fatal("foreign/non-internal network reused after conflict")
			}
		})
	}
}

// The fixture models actual Engine inspect/connect state: an API success alone
// does not prove the edge is attached to the correct isolated network.
func TestDockerTrustedEdgeNetwork(t *testing.T) {
	for _, mode := range []string{"ok", "egress-managed", "managed-network", "managed-wrong-alias", "native-origin", "native-missing-alias", "foreign-network", "external-network", "foreign-member", "wrong-edge", "host-edge", "wrong-alias", "connect-failure"} {
		t.Run(mode, func(t *testing.T) {
			platform, portal := "portal.example.com", ""
			var managed []string
			wantAliases := []string{platform}
			if strings.HasPrefix(mode, "managed-") {
				portal = "network.example.com"
				wantAliases = append(wantAliases, portal)
			}
			if strings.HasPrefix(mode, "native-") {
				platform = "auriga-instance.internal"
				managed = []string{platform, "native.example.com"}
				wantAliases = []string{platform, "native.example.com"}
			}
			connected, created, disconnected, removedNetwork, edgeStopped := false, false, false, false, false
			egressConnected, egressDisconnected := false, false
			base := (&fakeEngine{}).handler()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/networks/create":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"Id":"net1"}`))
				case r.URL.Path == "/networks/net1/connect":
					var body struct {
						Container      string
						EndpointConfig struct{ Aliases []string }
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if mode == "egress-managed" && body.Container == "egress-id" {
						if !reflect.DeepEqual(body.EndpointConfig.Aliases, []string{"auriga-egress.internal"}) {
							t.Error("unexpected egress alias attachment")
						}
						egressConnected = true
						w.WriteHeader(http.StatusOK)
						return
					}
					if body.Container != "edge-id" || !reflect.DeepEqual(body.EndpointConfig.Aliases, wantAliases) {
						t.Error("unexpected edge/alias attachment")
					}
					if mode == "connect-failure" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					connected = true
					w.WriteHeader(http.StatusOK)
				case r.URL.Path == "/networks/net1/disconnect":
					var body struct {
						Container string
						Force     bool
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if mode == "egress-managed" && body.Container == "egress-id" {
						if body.Force {
							t.Error("egress detach must not be forced")
						}
						egressConnected, egressDisconnected = false, true
						w.WriteHeader(http.StatusOK)
						return
					}
					if body.Container != "edge-id" || body.Force {
						t.Error("disconnect must target the trusted edge without force")
					}
					disconnected, connected = true, false
					w.WriteHeader(http.StatusOK)
				case strings.HasPrefix(r.URL.Path, "/networks/") && r.Method == http.MethodGet:
					labels := map[string]string{"auriga.instance": "inst_i1", "auriga.managed": "true"}
					if mode == "foreign-network" {
						labels["auriga.instance"] = "other"
					}
					members := map[string]any{}
					if connected {
						members["edge-id"] = map[string]string{"Name": "test-edge"}
					}
					if egressConnected {
						members["egress-id"] = map[string]string{"Name": "test-egress"}
					}
					if mode == "foreign-member" {
						members["other-id"] = map[string]string{"Name": "unrelated"}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "net1", "Name": "auriga-net-inst_i1", "Driver": "bridge", "Internal": mode != "external-network", "Labels": labels, "Containers": members})
				case strings.HasPrefix(r.URL.Path, "/networks/") && r.Method == http.MethodDelete:
					if !disconnected {
						t.Error("network removed before edge detach")
					}
					if mode == "egress-managed" && !egressDisconnected {
						t.Error("network removed before egress detach")
					}
					removedNetwork = true
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == "/containers/test-edge/json" || r.URL.Path == "/containers/edge-id/json":
					role, networkMode := "edge", "bridge"
					if mode == "wrong-edge" {
						role = "unrelated"
					}
					if mode == "host-edge" {
						networkMode = "host"
					}
					nets := map[string]any{}
					if connected {
						alias := platform
						if mode == "wrong-alias" {
							alias = "other.example.com"
						}
						aliases := []string{alias}
						if mode == "managed-network" {
							aliases = append(aliases, "network.example.com")
						}
						if mode == "native-origin" {
							aliases = append(aliases, "native.example.com")
						}
						nets["auriga-net-inst_i1"] = map[string]any{"NetworkID": "net1", "Aliases": aliases}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "edge-id", "Image": fixtureConfigID, "State": map[string]bool{"Running": !edgeStopped}, "Config": map[string]any{"Labels": map[string]string{"auriga.role": role, "com.docker.compose.project": "test"}}, "HostConfig": map[string]string{"NetworkMode": networkMode}, "NetworkSettings": map[string]any{"Networks": nets}})
				case r.URL.Path == "/containers/test-egress/json":
					f := &edgeRecoveryFixture{egressID: "egress-id", egressStartedAt: "2026-01-01T01:02:03Z"}
					insp := f.egressInspect()
					cfg := insp["Config"].(map[string]any)
					cfg["Labels"].(map[string]string)["auriga.egress.for-edge"] = "test-edge"
					insp["State"].(map[string]any)["Running"] = !edgeStopped
					nets := map[string]any{}
					if egressConnected {
						nets["auriga-net-inst_i1"] = map[string]any{"NetworkID": "net1", "Aliases": []string{"auriga-egress.internal"}}
					}
					insp["NetworkSettings"] = map[string]any{"Networks": nets}
					_ = json.NewEncoder(w).Encode(insp)
				case r.URL.Path == "/containers/auriga-inst-inst_i1/json":
					if !created {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					base.ServeHTTP(w, r)
				case r.URL.Path == "/containers/create":
					raw, readErr := io.ReadAll(r.Body)
					if readErr != nil {
						t.Error(readErr)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					var body struct{ Labels map[string]string }
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if body.Labels["auriga.observability.edge"] != "test-edge" || body.Labels["auriga.managed"] != "true" {
						t.Error("missing observation-only deployment owner")
					}
					for label := range body.Labels {
						if strings.HasPrefix(label, "com.docker.compose.") {
							t.Error("runtime instance claims Compose membership")
						}
					}
					r.Body = io.NopCloser(strings.NewReader(string(raw)))
					if !connected {
						t.Error("instance created before trusted edge became reachable")
					}
					if mode == "egress-managed" && !egressConnected {
						t.Error("instance created before trusted egress became reachable")
					}
					created = true
					base.ServeHTTP(w, r)
				default:
					base.ServeHTTP(w, r)
				}
			}))
			defer srv.Close()
			d, err := runtime.NewDocker(runtime.DockerConfig{Host: srv.URL, EdgeContainer: "test-edge", PlatformHost: platform, PortalHost: portal, ManagedHosts: managed})
			if err != nil {
				t.Fatal(err)
			}
			d.Policy.AllowAnyImage = true
			if mode == "egress-managed" {
				d.EgressContainer = "test-egress"
			}
			s := &runtime.Spec{InstanceID: "inst_i1", Image: "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32), HomeBind: t.TempDir(), MetaBind: t.TempDir(), HostUID: 200001}
			err = ensureDocker(t, d, context.Background(), s, nil)
			if mode != "ok" && mode != "egress-managed" && mode != "managed-network" && mode != "native-origin" {
				if err == nil || created {
					t.Fatalf("unsafe instance created: err=%v created=%v", err, created)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Re-prepare reuses the same endpoint/alias and removal still works when
			// the edge is stopped. No forced disconnect of unrelated endpoints.
			if err := ensureDocker(t, d, context.Background(), s, nil); err != nil {
				t.Fatal(err)
			}
			edgeStopped = true
			if err := d.Remove(context.Background(), s.InstanceID); err != nil {
				t.Fatal(err)
			}
			if !disconnected || !removedNetwork {
				t.Fatal("edge endpoint/network leaked after instance removal")
			}
		})
	}
}

func TestDockerEdgeConfigRequiresDNSAndExplicitContainer(t *testing.T) {
	for _, cfg := range []runtime.DockerConfig{
		{EdgeContainer: "test-edge"}, {PlatformHost: "portal.example.com"},
		{EdgeContainer: "test-edge", PlatformHost: "192.0.2.1"},
		{EdgeContainer: "../test-edge", PlatformHost: "portal.example.com"},
		{EdgeContainer: "test-edge", PlatformHost: "*.example.com"},
		{EdgeContainer: "test-edge", PlatformHost: "internal.example.com", ManagedHosts: []string{"192.0.2.10"}},
		{EdgeContainer: "test-edge", PlatformHost: "internal.example.com", ManagedHosts: []string{"*.example.com"}},
		{EdgeContainer: "test-edge", PlatformHost: "internal.example.com", ManagedHosts: []string{"a.example.com", "b.example.com", "c.example.com"}},
		{EgressContainer: "test-egress"},
		{EdgeContainer: "test-edge", PlatformHost: "internal.example.com", EgressContainer: "test-edge"},
		{EdgeContainer: "test-edge", PlatformHost: "internal.example.com", EgressContainer: "../egress"},
	} {
		if _, err := runtime.NewDocker(cfg); err == nil {
			t.Fatal("invalid trusted edge configuration accepted")
		}
	}
}
