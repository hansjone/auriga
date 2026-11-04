package bifrostbackend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Root runs this test in the serial real-PG slot; ordinary workers use TestCatalog*.
func TestPostgresCatalogProviderIsolationAndState(t *testing.T) {
	b, admin, _, tenant := reviewBackend(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer configured-catalog-key" {
			t.Error("configured key missing")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"real-gpt"}]}`))
	}))
	defer s.Close()
	provider, err := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: "catalog", Kind: "openai-compatible", BaseUrl: s.URL, Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/providers/" + provider.Id + "/models"
	read := func(scope string) *httptest.ResponseRecorder {
		return reviewRequest(b, "GET", path, "", "auriga", admin, scope, "")
	}
	check := func(code int, reason string, scope string) {
		t.Helper()
		r := read(scope)
		if r.Code != code || !strings.Contains(r.Body.String(), reason) {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	}
	check(409, "credential_missing", tenant)
	other, _ := newID()
	check(404, "common.not_found", other)
	if calls.Load() != 0 {
		t.Fatal("unauthorized/missing credential contacted provider")
	}
	secret := "configured-catalog-key"
	if _, err = b.SaveProvider(t.Context(), tenant, provider.Id, provider.Configuration, &secret); err != nil {
		t.Fatal(err)
	}
	check(200, "real-gpt", tenant)
	if calls.Load() != 1 {
		t.Fatal("catalog did not contact configured provider once")
	}
	config := provider.Configuration
	config.Enabled = false
	if _, err = b.SaveProvider(t.Context(), tenant, provider.Id, config, nil); err != nil {
		t.Fatal(err)
	}
	check(409, "provider_disabled", tenant)
	if calls.Load() != 1 {
		t.Fatal("disabled provider contacted")
	}
	r := reviewRequest(b, "GET", path, "", "auriga", "wrong-password", tenant, "")
	if r.Code != 401 || calls.Load() != 1 {
		t.Fatal("bad management auth reached provider")
	}
}
