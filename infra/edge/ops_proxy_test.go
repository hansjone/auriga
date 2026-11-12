package main

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpsAdminRoutes(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, store := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !strings.HasPrefix(r.URL.Path, "/api/ops/v1/admin/") || r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" {
					t.Error("ops authentication lost")
				}
				if r.Method == "PATCH" && r.Header.Get("If-Match") != `"v1"` || r.Method == "POST" && r.Header.Get("Idempotency-Key") != "resource-create" {
					t.Error("conditional header lost")
				}
				w.Header().Set("ETag", `"v2"`)
				w.WriteHeader(200)
			}), func(c *Config, _ map[string]any) { c.Upstreams["resource"] = c.Upstreams["iam"] })
			closeAIAdminTransports(t, g)
			var bindingReads atomic.Int32
			g.DevicesRPC = deviceBindingsFunc(func(context.Context, string) ([]deviceBinding, error) {
				bindingReads.Add(1)
				return nil, nil
			})
			backend, _ := browserRPCFixture(t, g, store)
			// Exercise proxy policy with the production default RPC budget; this is not a deadline test.
			g.SessionRPC.(*iamRPC).timeout = 2 * time.Second
			putBrowserSession(g, backend, "admin", "admin").AccessToken = "admin-token"
			store.Put("admin", backend.rows["admin"])
			g.AdminCSRF = aiAdminCSRF{}
			good := http.Header{"Cookie": {"auriga_admin_session=admin; auriga_csrf=csrf"}, "Origin": {g.Cfg.Access.AdminURL()}, "X-Auriga-CSRF": {"csrf"}, "If-Match": {`"v1"`}, "Idempotency-Key": {"resource-create"}, "Authorization": {"Bearer forged"}, "Auriga-Identity": {"forged"}}
			for _, op := range []struct{ method, path string }{{"GET", "/api/ops/v1/admin/alerts?limit=50"}, {"POST", "/api/ops/v1/admin/alerts"}, {"PATCH", "/api/ops/v1/admin/alerts/example"}} {
				status, headers, body := aiAdminRequest(t, g, addresses, "admin", op.method, op.path, good)
				if status != 200 || headers.Get("ETag") != `"v2"` {
					t.Fatalf("%d %s", status, body)
				}
			}
			bad := good.Clone()
			bad.Set("X-Auriga-CSRF", "wrong")
			requireEntryProblem(t, addresses["admin"], "POST", "/api/ops/v1/admin/alerts", g.Cfg.AdminHost, bad, 403, "common.forbidden")
			requireEntryProblem(t, addresses["portal"], "PATCH", "/api/ops/v1/admin/notification-channels/example", g.Cfg.PortalHost, nil, 403, "common.forbidden")
			requireEntryProblem(t, addresses["admin"], "GET", "/api/ops/v1/admin/alerts", g.Cfg.AdminHost, nil, 401, "common.unauthenticated")
			store.Revoke(backend.rows["admin"].SessionID)
			requireEntryProblem(t, addresses["admin"], "GET", "/api/ops/v1/admin/alerts", g.Cfg.AdminHost, good, 401, "common.unauthenticated")
			if calls.Load() != 3 {
				t.Fatal("rejected request reached resource")
			}
			if bindingReads.Load() != 0 {
				t.Fatal("ops proxy queried device connection bindings")
			}
		})
	}
}
