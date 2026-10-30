package relayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const plainRequest = `{"model":"synthetic","messages":[{"role":"user","content":"private prompt"}],"max_tokens":16}`
const usageJSON = `{"prompt_tokens":32,"completion_tokens":2,"total_tokens":34}`
const textChoice = `{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}`
const toolChoice = `{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}`
const filterChoice = `{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}`
const toolsRequest = `"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"required",`

func syntheticPolicy() modelprotocol.Policy {
	return modelprotocol.Policy{Model: "synthetic", ContextWindow: 4096, MaxOutputTokens: 512, Tools: true, Temperature: true, TopP: true, Stop: true,
		// This finite synthetic provider defines the whole effective request as 32
		// input tokens. This is not a tokenizer or a production model policy.
		MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return 32, nil },
		VerifyUsage:  func(_ context.Context, u api.TokenUsage) bool { return u.PromptTokens == 32 && u.CompletionTokens == 2 },
	}
}
func completion(choice string) string {
	return `{"id":"completion","object":"chat.completion","created":1,"model":"synthetic","choices":[` + choice + `],"usage":` + usageJSON + `}`
}
func sse(choice string) string {
	choice = strings.Replace(choice, `"message":`, `"delta":`, 1)
	choice = strings.Replace(choice, `"tool_calls":[{`, `"tool_calls":[{"index":0,`, 1)
	return `data: {"id":"completion","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[` + choice + `],"usage":` + usageJSON + "}\n\ndata: [DONE]\n\n"
}

func (f *fixture) server(t *testing.T, transport relay.Transport) *httptest.Server {
	t.Helper()
	f.cfg.Transport = transport
	core, err := relay.New(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.http.Core = core
	h, err := New(f.http)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.finished != nil {
			defer f.finished()
		}
		if f.observe != nil {
			w = &observeWriter{ResponseWriter: w, before: f.observe}
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}
func (f *fixture) request(t *testing.T, s *httptest.Server, method, path, body, key string) (int, http.Header, string) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+f.raw)
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	res, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header, string(b)
}
func finite(body string, calls *atomic.Int32) relay.Transport {
	return func(context.Context, relay.Submission, []byte) (relay.Exchange, error) {
		calls.Add(1)
		stop := make(chan struct{})
		close(stop)
		return relay.Exchange{Body: io.NopCloser(strings.NewReader(body)), Stopped: stop}, nil
	}
}

func TestHTTPPostgresProtocol(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for n, choice := range []string{textChoice, toolChoice, filterChoice} {
			t.Run(fmt.Sprintf("stream=%v/choice=%d", stream, n), func(t *testing.T) {
				f := fixtureNew(t)
				f.observe = func(b []byte) {
					if strings.Contains(string(b), "data: [DONE]") || strings.Contains(string(b), `"object":"chat.completion"`) {
						f.assertState(t, "completed", 0, 0)
					}
				}
				var calls atomic.Int32
				body, req := completion(choice), plainRequest
				if choice != textChoice {
					req = strings.Replace(req, `"max_tokens":`, toolsRequest+`"max_tokens":`, 1)
				}
				if stream {
					body = sse(choice)
					req = strings.Replace(req, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
				}
				s := f.server(t, finite(body, &calls))
				status, headers, result := f.request(t, s, "POST", chatPath, req, "")
				if status != 200 || calls.Load() != 1 {
					t.Fatalf("%d %s calls=%d", status, result, calls.Load())
				}
				if stream && (!strings.HasSuffix(result, "data: [DONE]\n\n") || headers.Get("Content-Type") != "text/event-stream") {
					t.Fatal(result)
				}
				if !stream {
					var c api.ChatCompletion
					if err := json.Unmarshal([]byte(result), &c); err != nil || c.Choices[0].FinishReason != []string{"stop", "tool_calls", "content_filter"}[n] {
						t.Fatalf("%s %v", result, err)
					}
				}
				for _, secret := range []string{"Reservation", "StopProof", "fixed-price", "fixed-synthetic-v1", "upstream.example.com", "credential", "private prompt"} {
					if strings.Contains(result, secret) {
						t.Fatalf("private field %s", secret)
					}
				}
				status, _, result = f.request(t, s, "GET", usagePath+"?limit=1", "", "")
				var page api.UsagePage
				if err := json.Unmarshal([]byte(result), &page); err != nil || status != 200 || len(page.Items) != 1 || page.Items[0].Outcome != "completed" {
					t.Fatalf("%d %s %v", status, result, err)
				}
				// A response already read through DONE/JSON must have a committed
				// usage/outbox and released slot, visible from another transaction.
				f.assertState(t, "completed", 0, 0)
				status, _, result = f.request(t, s, "GET", modelsPath, "", "")
				var catalog map[string]json.RawMessage
				if err := json.Unmarshal([]byte(result), &catalog); err != nil || status != 200 {
					t.Fatal(result, err)
				}
				var entries []map[string]any
				if err := json.Unmarshal(catalog["data"], &entries); err != nil || len(entries) != 1 || len(entries[0]) != 4 {
					t.Fatal(result, err)
				}
				if calls.Load() != 1 {
					t.Fatal("read endpoint dispatched")
				}
			})
		}
	}
}

type observeWriter struct {
	http.ResponseWriter
	before func([]byte)
}

func (w *observeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observeWriter) Write(b []byte) (int, error) { w.before(b); return w.ResponseWriter.Write(b) }

func (f *fixture) assertState(t *testing.T, state string, active, held int64) {
	t.Helper()
	err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var got string
		var a, h, outbox int64
		if err := tx.QueryRow(t.Context(), `SELECT state FROM ai.budget_request WHERE tenant_id=$1`, f.control.tenant).Scan(&got); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT active,held_tokens FROM ai.budget_subject WHERE tenant_id=$1`, f.control.tenant).Scan(&a, &h); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1`, f.control.tenant).Scan(&outbox); err != nil {
			return err
		}
		if got != state || a != active || h != held || outbox != 1 {
			t.Errorf("state=%s active=%d held=%d outbox=%d", got, a, h, outbox)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
