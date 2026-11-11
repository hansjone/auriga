package main

import (
	"net/http"
	"testing"
)

func TestAdminAuditRouteUsesPlatformSessionHop(t *testing.T) {
	for _, domain := range []bool{false, true} {
		calls := 0
		g, _, store := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Header.Get("Authorization") != "Bearer initial" {
				t.Error("admin session bearer missing")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		backend, _ := browserRPCFixture(t, g, store)
		putBrowserSession(g, backend, "admin-cookie", "admin")
		putBrowserSession(g, backend, "portal-cookie", "portal")
		for _, path := range []string{"/api/audit/v1/admin/events", "/api/audit/v1/admin/events/export", "/api/audit/v1/admin/events/01900000-0000-7000-8000-000000000001"} {
			if w := browserRequest(g, "admin", "GET", path, "admin-cookie"); w.Code != 200 {
				t.Fatalf("admin audit route: %d %s", w.Code, w.Body.String())
			}
			before := calls
			if w := browserRequest(g, "portal", "GET", path, "portal-cookie"); w.Code != 403 {
				t.Fatalf("portal audit route: %d", w.Code)
			}
			if w := browserRequest(g, "admin", "GET", path, ""); w.Code != 401 {
				t.Fatalf("anonymous audit route: %d", w.Code)
			}
			if calls != before {
				t.Fatal("denied request reached upstream")
			}
		}
	}
}
