package bifrostbackend

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestPoolCreateOnlyHeader(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		values             []string
		want, invalid      bool
	}{
		{"absent", "POST", "/api/pools", nil, false, false},
		{"create", "POST", "/api/pools", []string{"*"}, true, false},
		{"multiple", "POST", "/api/pools", []string{"*", "*"}, false, true},
		{"empty", "POST", "/api/pools", []string{""}, false, true},
		{"etag", "POST", "/api/pools", []string{`"previous"`}, false, true},
		{"comma", "POST", "/api/pools", []string{"*, *"}, false, true},
		{"other-path", "POST", "/api/providers", []string{"*"}, false, true},
		{"read", "GET", "/api/pools", []string{"*"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			if tc.values != nil {
				r.Header["If-None-Match"] = tc.values
			}
			got, err := poolCreateOnly(r)
			if got != tc.want || errors.Is(err, invalid) != tc.invalid {
				t.Fatalf("createOnly=%v err=%v", got, err)
			}
			if tc.invalid {
				// An invalid condition must fail before any idempotency claim or DB access.
				w := httptest.NewRecorder()
				(&Backend{}).idempotentManagement(w, r, "")
				if w.Code != 400 || !strings.Contains(w.Body.String(), "common.validation_failed") {
					t.Fatalf("invalid condition: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
	w := httptest.NewRecorder()
	reject(w, fault("common.precondition_failed"))
	if w.Code != 412 || !strings.Contains(w.Body.String(), "common.precondition_failed") {
		t.Fatalf("precondition: %d %s", w.Code, w.Body.String())
	}
}

// Root executes this test in the sole real PostgreSQL slot. All concurrent
// insert attempts reach the real unique constraint, not an in-memory mock.
func TestPostgresPoolCreateOnlyRaceAndReplay(t *testing.T) {
	b, password, _, tenant := reviewBackend(t)
	provider, err := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: "fixture", Kind: "openai", BaseUrl: "https://models.example.com", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool := api.RelayPool{Id: "race-model", Enabled: true, ContextWindow: 32768, MaxOutputTokens: 4096,
		Routes: []api.RelayRoute{{ProviderId: provider.Id, Model: "upstream-model", Weight: 1}},
		Price:  api.Price{Currency: "USD", Version: "fixture-v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}}
	type attempt struct {
		pool api.RelayPool
		err  error
	}
	start := make(chan struct{})
	results := make(chan attempt, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		candidate := pool
		candidate.MaxOutputTokens = int64(1000 + i)
		wg.Go(func() {
			<-start
			results <- attempt{candidate, b.CreatePool(t.Context(), tenant, candidate)}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var winner api.RelayPool
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.pool
		} else if !errors.Is(result.err, fault("common.precondition_failed")) {
			t.Fatal(result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent insert winners=%d", successes)
	}
	assertPool := func(id string, output int64) {
		t.Helper()
		pools, e := b.ListPools(t.Context(), tenant)
		if e != nil {
			t.Fatal(e)
		}
		for _, p := range pools.Items {
			if p.Id == id && p.MaxOutputTokens == output {
				return
			}
		}
		t.Fatal("existing pool changed or absent")
	}
	assertPool(winner.Id, winner.MaxOutputTokens)
	request := func(p api.RelayPool, key, condition string) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := p.MarshalJSON()
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/pools", strings.NewReader(string(raw)))
		r.SetBasicAuth("auriga", password)
		r.Header.Set("X-Auriga-Tenant", tenant)
		r.Header.Set("Idempotency-Key", key)
		if condition != "" {
			r.Header.Set("If-None-Match", condition)
		}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	check := func(r *httptest.ResponseRecorder, code int, errorCode string) {
		t.Helper()
		if r.Code != code || errorCode != "" && !strings.Contains(r.Body.String(), errorCode) {
			t.Fatalf("HTTP %d %s", r.Code, r.Body.String())
		}
	}
	changed := winner
	changed.MaxOutputTokens++
	check(request(changed, "existing-create", "*"), 412, "common.precondition_failed")
	assertPool(winner.Id, winner.MaxOutputTokens)
	check(request(changed, "ordinary-edit", ""), 200, "")
	assertPool(winner.Id, changed.MaxOutputTokens)
	check(request(changed, "ordinary-edit", "*"), 409, "common.conflict")
	newPool := pool
	newPool.Id = "replayed-create"
	first := request(newPool, "same-create", "*")
	check(first, 200, "")
	replay := request(newPool, "same-create", "*")
	check(replay, 200, "")
	if first.Body.String() != replay.Body.String() {
		t.Fatal("same create did not replay identical response")
	}
	check(request(newPool, "same-create", ""), 409, "common.conflict")
	check(request(newPool, "another-create", "*"), 412, "common.precondition_failed")
	assertPool(newPool.Id, newPool.MaxOutputTokens)
	other, _ := newID()
	otherProvider, e := b.SaveProvider(t.Context(), other, "", provider.Configuration, nil)
	if e != nil {
		t.Fatal(e)
	}
	newPool.Routes = []api.RelayRoute{{ProviderId: otherProvider.Id, Model: "other-upstream", Weight: 1}}
	if e = b.CreatePool(t.Context(), other, newPool); e != nil {
		t.Fatalf("another tenant's same model ID conflicted: %v", e)
	}
}
