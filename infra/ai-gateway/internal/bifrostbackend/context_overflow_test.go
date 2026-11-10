package bifrostbackend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/maximhq/bifrost/core/schemas"
)

// Every provider's context-overflow wording becomes the OpenAI code that DSH's
// compaction recognises; other rejections keep their existing projection.
func TestProviderFailureNormalisesContextOverflow(t *testing.T) {
	ptr := schemas.Ptr[string]
	cases := []struct {
		name    string
		status  int
		code    *string
		typ     *string
		message string
		want    string
	}{
		{"openai code", 400, ptr("context_length_exceeded"), ptr("invalid_request_error"), "Your input exceeds the context window of this model", "context_length_exceeded"},
		{"deepseek", 400, nil, ptr("invalid_request_error"), "This model's maximum context length is 65536 tokens. However, you requested 70000 tokens.", "context_length_exceeded"},
		{"minimax", 400, ptr("2013"), nil, "invalid params, context window exceeds limit", "context_length_exceeded"},
		{"anthropic style", 400, nil, nil, "prompt is too long: 213462 tokens > 200000 maximum", "context_length_exceeded"},
		{"payload too large", 413, nil, ptr("request_too_large"), "Request exceeds the maximum size", "context_length_exceeded"},
		{"qwen", 400, ptr("invalid_parameter_error"), nil, "Range of input length should be [1, 129024]", "context_length_exceeded"},
		{"generic input wording", 400, nil, nil, "The input (300000 tokens) is longer than the model's context length (262144 tokens).", "context_length_exceeded"},
		{"plain bad request", 400, nil, ptr("invalid_request_error"), "bad request", "provider_rejected_request"},
		{"rate limited tokens", 429, ptr("rate_limit_exceeded"), nil, "Too many tokens per minute: maximum context length is irrelevant", "rate_limit_exceeded"},
		{"server error wording", 500, nil, nil, "maximum context length exceeded while warming up", "provider_unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := providerFailure(&schemas.BifrostError{StatusCode: schemas.Ptr(c.status), Error: &schemas.ErrorField{Code: c.code, Type: c.typ, Message: c.message}})
			if got.Code != c.want {
				t.Fatalf("code %q want %q", got.Code, c.want)
			}
			if c.want == "context_length_exceeded" && got.Status != c.status {
				t.Fatalf("provider status must be preserved: %d want %d", got.Status, c.status)
			}
		})
	}
}

// End to end through the relay HTTP path: a DeepSeek-style overflow reaches the
// client as an OpenAI context_length_exceeded error without the upstream text,
// for both streaming and non-streaming requests, and nothing stays reserved.
func TestRelayReturnsStandardContextOverflow(t *testing.T) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	b, e := New(db, hex.EncodeToString(master), rand.Text()+rand.Text(), rand.Text()+rand.Text(), true)
	if e != nil {
		t.Fatal(e)
	}
	tenant, _ := newID()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 65536 tokens. However, you requested 70000 tokens.","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`))
	}))
	t.Cleanup(upstream.Close)
	cfg := api.RelayProviderInput{Name: "p", Kind: "openai-compatible", BaseUrl: upstream.URL, Enabled: true}
	p, _ := b.SaveProvider(t.Context(), tenant, "", cfg, nil)
	secret := rand.Text()
	if _, e = b.SaveProvider(t.Context(), tenant, p.Id, cfg, &secret); e != nil {
		t.Fatal(e)
	}
	price := api.Price{Currency: "USD", Version: "v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}
	pool := api.RelayPool{Id: "pool", Enabled: true, Routes: []api.RelayRoute{{ProviderId: p.Id, Model: "m", Weight: 1}}, ContextWindow: 1000000, MaxOutputTokens: 32768, Price: price}
	if e = b.SavePool(t.Context(), tenant, pool); e != nil {
		t.Fatal(e)
	}
	k, e := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "ext", Active: true, MonthlyBudgetMicros: 1000000000, RequestsPerMinute: 1000, Models: []string{"pool"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, stream := range []string{"false", "true"} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"pool","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"stream":`+stream+`}`))
		r.Header.Set("Authorization", "Bearer "+k.Secret)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		var body struct {
			Error struct{ Type, Code, Message string }
		}
		if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "context_length_exceeded" || body.Error.Type != "invalid_request_error" {
			t.Fatalf("stream=%s: %d %s", stream, w.Code, w.Body.String())
		}
		if !strings.Contains(body.Error.Message, "context_length_exceeded") || strings.Contains(w.Body.String(), "65536") {
			t.Fatalf("stream=%s: message must be recognisable and never echo upstream text: %s", stream, w.Body.String())
		}
		assertNoHolds(t, b, tenant, k.Key.Id)
	}
}
