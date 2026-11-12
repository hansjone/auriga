package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLegacyRelayAndDeviceRouting(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(fmt.Sprintf("domain=%t", domain), func(t *testing.T) {
			var relayCalls, deviceCalls, teamCalls atomic.Int32
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/keys/check" {
					_, _ = w.Write([]byte(`{"name":"imported","active":true,"source":"external"}`))
					return
				}
				relayCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			t.Cleanup(relay.Close)
			device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deviceCalls.Add(1)
				w.WriteHeader(http.StatusCreated)
			}))
			t.Cleanup(device.Close)
			team := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				teamCalls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(team.Close)
			g, addresses, _ := accessFixtureConfigured(t, domain, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
				c.Relay = &RelayConfig{Origin: relay.URL, AdminPasswordFile: "unused"}
				c.Upstreams["ai-device"] = device.URL
				c.Upstreams["ai-gateway"] = team.URL
			})
			// IAM issues ES256 at+jwt device credentials. They have a JOSE header,
			// while relay credentials use the disjoint sk- namespace.
			deviceToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"ctx":"device"}`)) + "." + rand.Text()
			for _, tc := range []struct {
				name, token, method, path string
				status                    int
			}{
				{"legacy-models", "sk-" + rand.Text(), "GET", "/ai/v1/models", 202},
				{"legacy-chat", "sk-" + rand.Text(), "POST", "/ai/v1/chat/completions", 202},
				{"native-models", "sk-bf-" + rand.Text(), "GET", "/ai/v1/models", 202},
				{"native-chat", "sk-bf-" + rand.Text(), "POST", "/ai/v1/chat/completions", 202},
				{"device-models", deviceToken, "GET", "/ai/v1/models", 201},
				{"device-chat", deviceToken, "POST", "/ai/v1/chat/completions", 201},
				{"device-team", deviceToken, "POST", "/ai/mcp", 204},
				{"legacy-team", "sk-" + rand.Text(), "POST", "/ai/mcp", 204},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r, err := http.NewRequest(tc.method, "http://"+addresses["portal"]+tc.path, strings.NewReader(`{"model":"old","messages":[]}`))
					if err != nil {
						t.Fatal(err)
					}
					r.Header.Set("Authorization", "Bearer "+tc.token)
					if domain {
						r.Host = g.Cfg.PortalHost
					}
					response, err := http.DefaultClient.Do(r)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = response.Body.Close() }()
					if response.StatusCode != tc.status {
						t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
					}
				})
			}
			if relayCalls.Load() != 4 || deviceCalls.Load() != 2 || teamCalls.Load() != 2 {
				t.Fatalf("wrong destinations: relay=%d device=%d team=%d", relayCalls.Load(), deviceCalls.Load(), teamCalls.Load())
			}
		})
	}
}
