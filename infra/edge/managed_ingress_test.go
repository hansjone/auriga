package main

import (
	"context"
	"crypto/tls"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func managedFixture(t *testing.T) Config {
	t.Helper()
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	c := defaultConfig()
	c.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://" + c.Access.InstanceControl().DnsAlias + ":18446"
	c.LinkOrigins = []string{origin}
	c.InstanceControlIngress = &InstanceControlIngressConfig{DenyLocalIPv4: []string{"192.0.2.10"}}
	c.ManagedIngress = ManagedIngressConfig{Origin: origin, CertFile: "server.crt", KeyFile: "server.key"}
	return c
}

func TestManagedIngressRequiresApprovedHTTPSAndExistingInstanceAlias(t *testing.T) {
	c := managedFixture(t)
	if _, err := c.managedEntry(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"http://platform.internal:18446", "https://foreign.internal:18446", c.ManagedIngress.Origin + "/path", c.ManagedIngress.Origin + "?", c.ManagedIngress.Origin + "#"} {
		broken := c
		broken.ManagedIngress.Origin = value
		if _, err := broken.managedEntry(); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	c.LinkOrigins = nil
	if _, err := c.managedEntry(); err == nil {
		t.Fatal("unapproved origin accepted")
	}
}

func TestManagedIngressExposesOnlyNativeDeviceNetworkAndModelPaths(t *testing.T) {
	c := managedFixture(t)
	g := Gateway{Cfg: c}
	calls := 0
	handler := g.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		entry := r.Context().Value(accessEntryKey{}).(accessEntry)
		if entry.role != "portal" {
			t.Error("native downstream must retain normal portal authorization")
		}
		w.WriteHeader(204)
	}))
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/oauth2/token", 204}, {"POST", "/api/iam/v1/network/register", 204},
		{"POST", "/api/iam/v1/network/tickets", 204}, {"GET", "/api/collab/v1/network/keys", 204},
		{"GET", "/api/collab/v1/network/keys/announcement", 204}, {"GET", "/oauth2/jwks", 204},
		{"GET", "/ai/v1/models", 204}, {"POST", "/ai/v1/chat/completions", 204},
		{"GET", "/api/iam/v1/network/systems", 204}, {"POST", "/api/ai/v1/network/submit", 204},
		{"GET", "/api/iam/v1/admin/accounts", 404}, {"GET", "/api/workspace/v1/me/instance", 404},
		{"POST", "/api/iam/v1/login/password", 404}, {"GET", "/", 404}, {"POST", "/ai/v1/models", 404},
	} {
		r := httptest.NewRequest(tc.method, c.ManagedIngress.Origin+tc.path, nil)
		r.TLS = &tls.ConnectionState{}
		r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "managed"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
	if calls != 10 {
		t.Fatalf("unexpected allowed path count %d", calls)
	}
	for _, header := range []string{"Cookie", "Origin"} {
		r := httptest.NewRequest("POST", c.ManagedIngress.Origin+"/oauth2/token", nil)
		r.TLS = &tls.ConnectionState{}
		r.Header.Set(header, "untrusted")
		r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "managed"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("accepted %s", header)
		}
	}
}
