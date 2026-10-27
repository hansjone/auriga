package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceRoutes(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer ai-device" || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" || r.Header.Get("X-Auriga-Entry-Role") != "" {
					t.Error("device identity boundary lost")
				}
				w.Header().Set("Set-Cookie", "unexpected=value")
				w.WriteHeader(202)
			}))
			defer device.Close()
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("device request reached platform/Team")
				w.WriteHeader(500)
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["ai-device"] = device.URL
				c.Upstreams["ai-gateway"] = c.Upstreams["iam"]
			})
			good := http.Header{"Authorization": {"Bearer ai-device"}, "Auriga-Identity": {"forged"}, "X-Auriga-Entry-Role": {"admin"}}
			for _, operation := range []struct{ method, path string }{{"GET", "/ai/v1/models"}, {"POST", "/ai/v1/chat/completions"}, {"GET", "/api/ai/v1/me/usage?limit=2&cursor=next"}} {
				status, headers, body := aiAdminRequest(t, g, addresses, "portal", operation.method, operation.path, good)
				if status != 202 || headers.Get("Set-Cookie") != "" {
					t.Fatalf("%s: %d %s", operation.path, status, body)
				}
			}
			for _, tc := range []struct {
				method, path, role string
				headers            http.Header
				status             int
				code               string
			}{
				{"GET", "/ai/v1/models", "admin", good, 403, "common.forbidden"},
				{"GET", "/api/ai/v1/me/usage", "admin", good, 404, "common.not_found"},
				{"GET", "/ai/v1/models", "portal", nil, 401, "common.unauthenticated"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Cookie": {"auriga_session=portal"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Cookie": {"auriga_admin_session=admin"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer one", "Bearer two"}}, 401, "common.unauthenticated"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Origin": {g.Cfg.Access.PortalURL()}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Mode": {"cors", "cors"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Mode": {"navigate"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Mode": {"no-cors"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Site": {"same-origin"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Dest": {"empty"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-User": {"?1"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Sec-Fetch-Storage-Access": {"active"}}, 403, "common.forbidden"},
				{"GET", "/ai/v1/models", "portal", http.Header{"Authorization": {"Bearer ai-device"}, "Auriga-Forwarded-Tenant": {"forged"}}, 403, "common.forbidden"},
				{"HEAD", "/ai/v1/models", "portal", good, 405, ""},
				{"POST", "/ai/v1/models", "portal", good, 405, "common.validation_failed"},
				{"GET", "/ai/v1/chat/completions", "portal", good, 405, "common.validation_failed"},
				{"GET", "/ai/v1/models?other=1", "portal", good, 400, "common.validation_failed"},
				{"GET", "/ai/v1/models/", "portal", good, 404, "common.not_found"},
				{"GET", "/ai/v1/models/extra", "portal", good, 404, "common.not_found"},
				{"GET", "/ai/v1/%6dodels", "portal", good, 400, "common.validation_failed"},
			} {
				host := g.Cfg.PortalHost
				if tc.role == "admin" {
					host = g.Cfg.AdminHost
				}
				status, body := entryRequest(t, addresses[tc.role], tc.method, tc.path, host, tc.headers)
				if status != tc.status || tc.code != "" && !strings.Contains(body, `"code":"`+tc.code+`"`) {
					t.Fatalf("%s %s: %d %s", tc.method, tc.path, status, body)
				}
			}
			if calls.Load() != 3 {
				t.Fatal("rejected request reached device")
			}
		})
	}
}

// Exercise Node's actual automatic request headers, without a mock fetch or
// dispatcher. The Go fixture is only a proxy target, not production IAM proof.
func TestDeviceNativeNodeFetch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the native fetch integration regression")
	}
	var calls atomic.Int32
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		mode := r.Header.Values("Sec-Fetch-Mode")
		if len(mode) != 1 || mode[0] != "cors" {
			t.Errorf("native Node fetch mode missing or changed: %q", mode)
		}
		if r.Header.Get("Authorization") != "Bearer ai-device" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
			t.Error("native fetch device credential boundary lost")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(device.Close)
	_, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("native fetch reached platform instead of device runtime")
		w.WriteHeader(http.StatusInternalServerError)
	}), func(c *Config, _ map[string]any) { c.Upstreams["ai-device"] = device.URL })
	const script = `
import assert from 'node:assert/strict';
const origin = process.argv[1];
const options = {
  headers: { authorization: 'Bearer ai-device', accept: 'application/json' },
  credentials: 'omit', redirect: 'error', cache: 'no-store',
  referrerPolicy: 'no-referrer',
};
for (const [method, path] of [
  ['GET', '/ai/v1/models'],
  ['POST', '/ai/v1/chat/completions'],
  ['GET', '/api/ai/v1/me/usage?limit=2'],
]) {
  const response = await fetch(origin + path, {
    ...options, method,
    ...(method === 'POST' ? {
      headers: { ...options.headers, 'content-type': 'application/json' },
      body: '{}',
    } : {}),
  });
  const body = await response.text();
  assert.equal(response.status, 202, method + ' ' + path + ': ' + body);
}
for (const headers of [
  { cookie: 'auriga_admin_session=browser' },
  { origin },
  { referer: origin + '/' },
  { 'sec-fetch-site': 'same-origin' },
]) {
  const response = await fetch(origin + '/ai/v1/models', {
    ...options, headers: { ...options.headers, ...headers },
  });
  const body = await response.text();
  assert.equal(response.status, 403, body);
  assert.equal(JSON.parse(body).code, 'common.forbidden');
}
console.log('node=' + process.version + ' native-fetch=3 rejected=4');
`
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, "http://"+addresses["portal"])
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native Node fetch regression: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	if calls.Load() != 3 {
		t.Fatalf("expected three native requests only, upstream calls=%d", calls.Load())
	}
}

func TestDeviceUnconfigured(t *testing.T) {
	g, addresses, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected platform request") }))
	requireEntryProblem(t, addresses["portal"], "GET", "/ai/v1/models", g.Cfg.PortalHost, http.Header{"Authorization": {"Bearer ai-device"}}, 503, "common.unavailable")
}
