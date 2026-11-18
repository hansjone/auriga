package bifrostbackend

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
)

func TestCompatibleChatProviderRejectionDiagnostics(t *testing.T) {
	const upstreamModel = "gpt-6.1-sol-synthetic-route"
	const marker = "private-test-marker"
	const publicID = "synthetic-public-model"
	type failureRecord struct {
		Event         string            `json:"msg"`
		Status        int               `json:"status"`
		Code          string            `json:"code"`
		Reason        string            `json:"reason"`
		Parameter     string            `json:"parameter"`
		TraceID       string            `json:"request_trace_id"`
		BifrostError  *bool             `json:"bifrost_error"`
		RouteProtocol string            `json:"route_protocol"`
		ProviderType  string            `json:"provider_error_type"`
		Stream        bool              `json:"stream"`
		Fields        map[string]string `json:"relay_chat_fields"`
	}
	for _, tc := range []struct {
		name       string
		stream     bool
		preFields  map[string]any
		reject     string
		errorType  string
		wantType   string
		wantParam  string
		wantFields map[string]string
	}{
		{
			name:      "minimal chat succeeds",
			preFields: map[string]any{"max_completion_tokens": 64},
		},
		{
			name:      "legacy limit is renamed on the wire",
			preFields: map[string]any{"max_tokens": 64},
			reject:    "max_completion_tokens", errorType: "invalid_request_error",
			wantType: "invalid_request_error", wantParam: "max_completion_tokens",
			wantFields: map[string]string{"max_tokens": "number"},
		},
		{
			name:   "streaming usage is forced",
			stream: true,
			preFields: map[string]any{
				"max_completion_tokens": 64,
				"stream_options":        map[string]bool{"include_usage": false},
			},
			reject:   "stream_options",
			wantType: "missing", wantParam: "other",
			wantFields: map[string]string{"max_completion_tokens": "number", "stream_options": "object"},
		},
		{
			name: "optional tool arguments and vendor text never enter diagnostics",
			preFields: map[string]any{
				"max_completion_tokens": 64,
				"reasoning_effort":      "medium",
				"tools": []any{map[string]any{"type": "function", "function": map[string]any{
					"name": "fixture_lookup", "description": marker, "parameters": map[string]any{"type": "object"},
				}}},
			},
			reject: "tools", errorType: marker,
			wantType: "other", wantParam: "other",
			wantFields: map[string]string{"max_completion_tokens": "number", "reasoning_effort": "string", "tools": "array"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Error("compatible route did not use Chat Completions")
				}
				var fields map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
					t.Error("synthetic upstream could not decode request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if string(fields["model"]) != "\""+upstreamModel+"\"" || fields["messages"] == nil || fields["input"] != nil {
					t.Error("unexpected upstream model mapping or protocol")
				}
				if fields["max_tokens"] != nil || string(fields["max_completion_tokens"]) != "64" {
					t.Error("adapter did not normalize output limit to max_completion_tokens")
				}
				if tc.stream && string(fields["stream_options"]) != `{"include_usage":true}` ||
					!tc.stream && fields["stream_options"] != nil {
					t.Error("unexpected stream usage options")
				}
				if tc.reject == "" {
					protocolRespond(w, upstreamModel, tc.stream, false, false)
					return
				}
				if fields[tc.reject] == nil {
					t.Error("synthetic rejection was not triggered by the requested wire field")
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				failure := map[string]any{"message": marker}
				if tc.errorType != "" {
					failure["type"] = tc.errorType
				}
				if tc.wantParam != "other" {
					failure["param"] = tc.wantParam
				} else if tc.errorType == marker {
					failure["code"] = marker
					failure["param"] = marker
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"error": failure})
			}))
			t.Cleanup(upstream.Close)

			extra := map[string]any{"model": publicID, "user": marker,
				"messages": []any{map[string]string{"role": "user", "content": marker}},
			}
			for k, v := range tc.preFields {
				extra[k] = v
			}
			raw := protocolRequest(t, tc.stream, extra)
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				t.Fatal("invalid test fixture")
			}
			routes := []Route{{ID: "private-route-" + marker, Kind: "openai-compatible", URL: upstream.URL, Secret: marker, Model: upstreamModel, Weight: 1}}
			response := httptest.NewRecorder()
			req, done := observability.Begin(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			defer done()
			ctx := withCompatibleChatDiagnostic(req.Context(), "external", routes, tc.stream, fields)
			emitted := 0
			var publicIDPreserved bool
			result, err := Run(ctx, routes, raw, true, func(raw []byte) error {
				emitted++
				var reply map[string]json.RawMessage
				if json.Unmarshal(raw, &reply) == nil && string(reply["model"]) == "\""+publicID+"\"" {
					publicIDPreserved = true
				}
				return nil
			})
			if tc.reject == "" {
				if err != nil || !result.Confirmed || emitted == 0 || !publicIDPreserved {
					t.Fatalf("minimal compatible chat did not complete: %v", err)
				}
				if strings.Contains(logs.String(), "relay_chat_fields") {
					t.Fatal("request shape leaked on success")
				}
				return
			}

			var rejection *ProviderFailure
			if !errors.As(err, &rejection) || rejection.Status != 400 || rejection.Code != "provider_rejected_request" || emitted != 0 {
				t.Fatalf("expected the existing generic provider 400: %v", err)
			}
			providerReject(response, err)
			if response.Code != 400 || !strings.Contains(response.Body.String(), "provider_rejected_request") {
				t.Fatal("the public rejection changed")
			}

			var event failureRecord
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var record failureRecord
				if json.Unmarshal([]byte(line), &record) == nil && record.Event == "relay.provider_failure" {
					event = record
					count++
				}
			}
			if count != 1 || event.Status != 400 || event.Code != "provider_rejected_request" ||
				event.Reason != "other" || event.Parameter != tc.wantParam || event.BifrostError == nil || *event.BifrostError ||
				event.RouteProtocol != "openai_compatible_chat" || event.ProviderType != tc.wantType ||
				event.TraceID == "" || event.TraceID != response.Header().Get("X-Trace-Id") ||
				event.Stream != tc.stream || !reflect.DeepEqual(event.Fields, tc.wantFields) {
				t.Fatalf("unexpected diagnostic fields: %+v", event)
			}
			for _, private := range []string{marker, upstream.URL, upstreamModel, publicID} {
				if strings.Contains(logs.String(), private) || strings.Contains(response.Body.String(), private) {
					t.Fatal("diagnostics exposed a request field value or private upstream detail")
				}
			}
		})
	}
}

func TestCompatibleChatDiagnosticOnlyForExternalSingleCompatibleRoute(t *testing.T) {
	for _, tc := range []struct {
		source string
		routes []Route
	}{
		{"member", []Route{{Kind: "openai-compatible"}}},
		{"external", []Route{{Kind: "openai"}}},
		{"external", []Route{{Kind: "openai-compatible"}, {Kind: "openai-compatible"}}},
	} {
		ctx := withCompatibleChatDiagnostic(t.Context(), tc.source, tc.routes, false, nil)
		if ctx.Value(compatibleChatDiagnosticKey{}) != nil {
			t.Fatal("diagnostics mislabeled a different route or key source")
		}
	}
}
