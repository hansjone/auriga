package bifrostbackend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func reservationReply(w http.ResponseWriter, stream, responses bool) {
	if responses {
		reworkRespond(w, stream, []map[string]any{reworkText("msg_reservation", "ok")}) // Actual output=2.
		return
	}
	usage := map[string]int{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		protocolEvent(w, "", map[string]any{"id": "chat_reservation", "model": "private-model", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "ok"}}}})
		protocolEvent(w, "", map[string]any{"id": "chat_reservation", "model": "private-model", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_reservation", "model": "private-model", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": usage})
}

func TestProtocolOutputEstimateCoversSDKMinimum(t *testing.T) {
	for _, kinds := range [][]string{{"openai"}, {"openai-compatible"}, {"openai-compatible", "openai"}} {
		for _, stream := range []bool{false, true} {
			for _, limit := range []int64{1, 15, 16, 65536} {
				t.Run(fmt.Sprintf("%s/stream=%v/limit=%d", strings.Join(kinds, "+"), stream, limit), func(t *testing.T) {
					native := kinds[len(kinds)-1] == "openai"
					want := limit
					if native && limit < 16 {
						want = 16
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]json.RawMessage
						if json.NewDecoder(r.Body).Decode(&body) != nil {
							t.Error("bad body")
						}
						if len(kinds) > 1 && string(body["model"]) == `"first"` {
							if string(body["max_completion_tokens"]) != fmt.Sprint(limit) {
								t.Error("compatible body was changed")
							}
							w.WriteHeader(503)
							_, _ = fmt.Fprint(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
							return
						}
						field := "max_completion_tokens"
						if native {
							field = "max_output_tokens"
						}
						if string(body[field]) != fmt.Sprint(want) {
							t.Errorf("SDK body=%s want=%d", body[field], want)
						}
						reservationReply(w, stream, native)
					}))
					defer server.Close()
					var routes []Route
					for i, kind := range kinds {
						weight, model := float64(0), "second"
						if i == 0 {
							weight, model = 1, "first"
						}
						routes = append(routes, Route{ID: fmt.Sprintf("provider-%d", i), Kind: kind, Model: model, URL: server.URL, Secret: "fixture", Weight: weight})
					}
					bound := protocolOutputEstimate(routes, limit)
					if bound != want {
						t.Errorf("reservation=%d want=%d", bound, want)
					}
					raw := protocolRequest(t, stream, map[string]any{"max_tokens": limit})
					var client reworkClient
					result, err := Run(t.Context(), routes, raw, true, client.emit(t, stream))
					if err != nil || !result.Confirmed || result.OutputTokens > bound {
						t.Fatalf("confirmed usage exceeds reservation: result=%+v bound=%d err=%v", result, bound, err)
					}
					var original map[string]json.RawMessage
					if json.Unmarshal(raw, &original) != nil || string(original["max_tokens"]) != fmt.Sprint(limit) {
						t.Fatal("admission changed caller body")
					}
				})
			}
		}
	}
	for _, kind := range []string{"openai-compatible", "deepseek", "minimax", "anthropic", "gemini"} {
		if protocolOutputEstimate([]Route{{Kind: kind}}, 1) != 1 {
			t.Errorf("OpenAI floor leaked to %s", kind)
		}
	}
	if protocolOutputEstimate([]Route{{Kind: "openai"}}, 0) != 0 {
		t.Error("estimate invented an explicit output bound")
	}
}

// Root executes this fixture in the exclusive real-PG slot. This worker only
// compiles it; success requires the HTTP response, confirmed ledger usage and no holds.
func TestPostgresProtocolMinimumSettlesConfirmedUsage(t *testing.T) {
	for _, kinds := range [][]string{{"openai"}, {"openai-compatible"}, {"openai-compatible", "openai"}} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", strings.Join(kinds, "+"), stream), func(t *testing.T) {
				b, _, _, tenant := reviewBackend(t)
				native := kinds[len(kinds)-1] == "openai"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("bad body")
					}
					if len(kinds) > 1 && string(body["model"]) == `"first"` {
						w.WriteHeader(503)
						_, _ = fmt.Fprint(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
						return
					}
					field, want := "max_completion_tokens", "1"
					if native {
						field, want = "max_output_tokens", "16"
					}
					if string(body[field]) != want {
						t.Errorf("SDK output=%s want=%s", body[field], want)
					}
					reservationReply(w, stream, native)
				}))
				defer server.Close()
				provider, pool, key := reviewPool(t, b, tenant, server.URL)
				secret := "fixture"
				config := provider.Configuration
				config.Kind = kinds[0]
				if _, err := b.SaveProvider(t.Context(), tenant, provider.Id, config, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := b.SaveProvider(t.Context(), tenant, provider.Id, config, &secret); err != nil {
					t.Fatal(err)
				}
				pool.Routes[0].Model = "first"
				if len(kinds) > 1 {
					config.Kind, config.Name = kinds[1], "native backup"
					backup, err := b.SaveProvider(t.Context(), tenant, "", config, nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = b.SaveProvider(t.Context(), tenant, backup.Id, config, &secret); err != nil {
						t.Fatal(err)
					}
					pool.Routes = append(pool.Routes, api.RelayRoute{ProviderId: backup.Id, Model: "second", Weight: 0})
				}
				pool.ContextWindow, pool.MaxOutputTokens = 1000000, 65536 // Directory hints do not set this protocol floor.
				if err := b.SavePool(t.Context(), tenant, pool); err != nil {
					t.Fatal(err)
				}
				request := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"question"}],"max_tokens":1,"stream":%v}`, pool.Id, stream)
				response := reviewRequest(b, "POST", "/v1/chat/completions", request, "", "", "", key.Secret)
				if response.Code != 200 || strings.Contains(response.Body.String(), "event: error") || (stream && !strings.Contains(response.Body.String(), "[DONE]")) {
					t.Fatalf("settlement response=%d %s", response.Code, response.Body.String())
				}
				assertNoHolds(t, b, tenant, key.Key.Id)
				usage, err := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 10, "")
				wantOutput := int64(1)
				if native {
					wantOutput = 2
				}
				if err != nil || len(usage.Items) != 1 {
					t.Fatalf("usage=%+v err=%v", usage, err)
				}
				row := usage.Items[0]
				if row.UsageQuality != "confirmed" || row.Outcome != "completed" || row.GetInputTokens() != 3 || row.GetOutputTokens() != wantOutput || row.GetCostMicros() != 3+wantOutput || row.FrozenTokens != 0 || row.FrozenMicros != 0 {
					t.Fatalf("usage=%+v", row)
				}
			})
		}
	}
}
