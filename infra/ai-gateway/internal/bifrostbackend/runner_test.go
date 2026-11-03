package bifrostbackend

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRunnerUsesBifrostFallbackAndStreaming(t *testing.T) {
	var failed, succeeded atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"unavailable","type":"server_error"}}`))
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		succeeded.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-provider" || (r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/chat/completions") {
			t.Error("provider boundary")
		}
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "upstream-model" {
			t.Error("route was not resolved")
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"reply\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"mention upstream-model unchanged\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"reply\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"reply\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"mention upstream-model unchanged"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	t.Cleanup(second.Close)
	routes := []Route{{ID: "first", Kind: "openai", URL: first.URL, Secret: "fixture-provider", Model: "primary-private-model", Weight: 1}, {ID: "second", Kind: "openai", URL: second.URL, Secret: "fixture-provider", Model: "upstream-model", Weight: 0}}
	for _, stream := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"model": "pool", "messages": []map[string]string{{"role": "user", "content": "你好"}}, "stream": stream, "max_tokens": 16, "stream_options": map[string]bool{"include_usage": true}})
		chunks := 0
		result, err := Run(t.Context(), routes, body, true, func(raw []byte) error {
			chunks++
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || fields["extra_fields"] != nil {
				t.Error("private fields leaked")
			}
			if (!stream || chunks == 1) && !bytes.Contains(raw, []byte("mention upstream-model unchanged")) {
				t.Error("renaming the model must not rewrite message content")
			}
			var model string
			if json.Unmarshal(fields["model"], &model) != nil || model != "pool" {
				t.Error("reply must retain the public alias on every JSON/SSE chunk")
			}
			return nil
		})
		if err != nil || result.InputTokens != 3 || result.OutputTokens != 2 || chunks == 0 {
			t.Fatalf("stream=%v result=%+v chunks=%d err=%v", stream, result, chunks, err)
		}
	}
	for _, kind := range []string{"minimax", "deepseek"} {
		route := routes[1]
		route.Kind = kind
		route.Weight = 1
		result, err := Run(t.Context(), []Route{route}, []byte(`{"model":"pool","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`), true, func([]byte) error { return nil })
		if err != nil || !result.Confirmed || result.InputTokens != 3 {
			t.Fatalf("native %s: %+v %v", kind, result, err)
		}
	}
	if failed.Load() != 2 || succeeded.Load() != 4 {
		t.Fatalf("fallback calls=%d/%d", failed.Load(), succeeded.Load())
	}
}
func TestRunnerBlocksLoopbackUnlessExplicitlyAllowed(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	_, err := Run(t.Context(), []Route{{ID: "private", Kind: "openai", URL: server.URL, Secret: "test", Model: "test", Weight: 1}}, []byte(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":1}`), false, func([]byte) error { return nil })
	if err == nil || called.Load() != 0 {
		t.Fatalf("loopback contacted: %v/%d", err, called.Load())
	}
}
