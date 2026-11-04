package bifrostbackend

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Review PoC: upstream failures leave "unknown" holds that are never released.
func TestReviewUpstreamRecoveryImmediatelyRestoresKey(t *testing.T) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	password := rand.Text() + rand.Text()
	memberPassword := rand.Text() + rand.Text()
	b, e := New(db, hex.EncodeToString(master), password, memberPassword, true)
	if e != nil {
		t.Fatal(e)
	}
	tenant, _ := newID()
	var fail atomic.Bool
	var calls atomic.Int32
	fail.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	t.Cleanup(upstream.Close)
	cfg := api.RelayProviderInput{Name: "p", Kind: "openai-compatible", BaseUrl: upstream.URL, Enabled: true}
	p, _ := b.SaveProvider(t.Context(), tenant, "", cfg, nil)
	s := rand.Text()
	if _, e = b.SaveProvider(t.Context(), tenant, p.Id, cfg, &s); e != nil {
		t.Fatal(e)
	}
	price := api.Price{Currency: "USD", Version: "v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}
	pool := api.RelayPool{Id: "pool", Enabled: true, Routes: []api.RelayRoute{{ProviderId: p.Id, Model: "m", Weight: 1}}, ContextWindow: 1048576, MaxOutputTokens: 32768, Price: price}
	if e = b.SavePool(t.Context(), tenant, pool); e != nil {
		t.Fatal(e)
	}
	concurrency, tokens := int64(1), int64(1<<20)
	k, e := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "ext", Active: true, MonthlyBudgetMicros: 1000000000, RequestsPerMinute: 1000, MaxConcurrency: &concurrency, MaxInFlightTokens: &tokens, Models: []string{"pool"}})
	if e != nil {
		t.Fatal(e)
	}
	do := func() (int, string) {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"pool","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`))
		r.Header.Set("Authorization", "Bearer "+k.Secret)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for i := 0; i < 9; i++ {
		c, body := do()
		t.Logf("fail #%d -> %d %s", i, c, body)
		if c != 400 || !strings.Contains(body, "provider_rejected_request") {
			t.Fatalf("expected the provider's rejection, got %d %s", c, body)
		}
		if calls.Load() != int32(i+1) {
			t.Fatalf("request did not reach the provider exactly once: %d", calls.Load())
		}
		assertNoHolds(t, b, tenant, k.Key.Id)
	}
	fail.Store(false)
	c, body := do()
	t.Logf("after upstream recovers -> %d %s", c, body)
	if c != 200 {
		t.Fatalf("key remains blocked after upstream recovery: %d %s", c, body)
	}
	if calls.Load() != 10 {
		t.Fatalf("recovery did not reach the provider exactly once: %d", calls.Load())
	}
	assertNoHolds(t, b, tenant, k.Key.Id)
}
