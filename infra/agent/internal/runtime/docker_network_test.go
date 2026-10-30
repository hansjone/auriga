package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	for _, mode := range []string{"ok", "managed-network", "managed-wrong-alias", "foreign-network", "external-network", "foreign-member", "wrong-edge", "host-edge", "wrong-alias", "connect-failure"} {
		t.Run(mode, func(t *testing.T) {
			connected, created, disconnected, removedNetwork, edgeStopped := false, false, false, false, false
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
					want := 1
					if strings.HasPrefix(mode, "managed-") {
						want = 2
					}
					if body.Container != "edge-id" || len(body.EndpointConfig.Aliases) != want || body.EndpointConfig.Aliases[0] != "portal.example.com" || (want == 2 && body.EndpointConfig.Aliases[1] != "network.example.com") {
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
					if mode == "foreign-member" {
						members["other-id"] = map[string]string{"Name": "unrelated"}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "net1", "Name": "auriga-net-inst_i1", "Driver": "bridge", "Internal": mode != "external-network", "Labels": labels, "Containers": members})
				case strings.HasPrefix(r.URL.Path, "/networks/") && r.Method == http.MethodDelete:
					if !disconnected {
						t.Error("network removed before edge detach")
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
						alias := "portal.example.com"
						if mode == "wrong-alias" {
							alias = "other.example.com"
						}
						aliases := []string{alias}
						if mode == "managed-network" {
							aliases = append(aliases, "network.example.com")
						}
						nets["auriga-net-inst_i1"] = map[string]any{"NetworkID": "net1", "Aliases": aliases}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "edge-id", "State": map[string]bool{"Running": !edgeStopped}, "Config": map[string]any{"Labels": map[string]string{"auriga.role": role}}, "HostConfig": map[string]string{"NetworkMode": networkMode}, "NetworkSettings": map[string]any{"Networks": nets}})
				case r.URL.Path == "/containers/auriga-inst-inst_i1/json":
					if !created {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					base.ServeHTTP(w, r)
				case r.URL.Path == "/containers/create":
					if !connected {
						t.Error("instance created before trusted edge became reachable")
					}
					created = true
					base.ServeHTTP(w, r)
				default:
					base.ServeHTTP(w, r)
				}
			}))
			defer srv.Close()
			portal := ""
			if strings.HasPrefix(mode, "managed-") {
				portal = "network.example.com"
			}
			d, err := runtime.NewDocker(runtime.DockerConfig{Host: srv.URL, EdgeContainer: "test-edge", PlatformHost: "portal.example.com", PortalHost: portal})
			if err != nil {
				t.Fatal(err)
			}
			d.Policy.AllowAnyImage = true
			s := &runtime.Spec{InstanceID: "inst_i1", Image: "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32), HomeBind: t.TempDir(), MetaBind: t.TempDir(), HostUID: 200001}
			err = ensureDocker(t, d, context.Background(), s, nil)
			if mode != "ok" && mode != "managed-network" {
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
	} {
		if _, err := runtime.NewDocker(cfg); err == nil {
			t.Fatal("invalid trusted edge configuration accepted")
		}
	}
}
