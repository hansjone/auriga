package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestCatalogCapabilityMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body, want string
	}{
		{"id-only", "openai", `{"data":[{"id":"model-a"}]}`, `{"id":"model-a"}`},
		{"compatible", "openai", `{"data":[{"id":"model-a","context_window":128000,"context_length":64000,"max_output_tokens":8192,"max_completion_tokens":4096,"top_provider":{"max_completion_tokens":2048},"api_key":"not-for-client"}]}`, `{"id":"model-a","contextWindow":64000,"maxOutputTokens":2048}`},
		{"compatible-nested", "deepseek", `{"data":[{"id":"model-a","context_length":64000,"top_provider":{"max_completion_tokens":4096}}]}`, `{"id":"model-a","contextWindow":64000,"maxOutputTokens":4096}`},
		{"anthropic", "anthropic", `{"data":[{"id":"model-a","max_input_tokens":200000,"max_tokens":64000}]}`, `{"id":"model-a","maxInputTokens":200000,"maxOutputTokens":64000}`},
		{"gemini", "gemini", `{"models":[{"name":"models/model-a","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}]}`, `{"id":"model-a","maxInputTokens":1048576,"maxOutputTokens":65536}`},
		{"unknown-openai-fields", "openai", `{"data":[{"id":"model-a","max_input_tokens":200000,"max_tokens":64000,"inputTokenLimit":100000,"outputTokenLimit":4096}]}`, `{"id":"model-a"}`},
		{"safe-max", "minimax", `{"data":[{"id":"model-a","context_window":9007199254740991,"max_output_tokens":1}]}`, `{"id":"model-a","contextWindow":9007199254740991,"maxOutputTokens":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := catalogFixture(t, tc.kind, tc.body)
			if len(page.Items) != 1 {
				t.Fatalf("items=%d", len(page.Items))
			}
			assertCatalogJSON(t, page.Items[0], tc.want)
		})
	}
}

func TestCatalogCapabilityInvalidMetadata(t *testing.T) {
	for _, value := range []string{`null`, `0`, `-1`, `4096.5`, `4096.0`, `9007199254740992`, `9223372036854775808`, `1e999`, `"4096"`, `true`, `{}`, `[]`} {
		t.Run(value, func(t *testing.T) {
			for _, tc := range []struct{ kind, body string }{
				{"openai", fmt.Sprintf(`{"data":[{"id":"model-a","context_window":%s,"max_output_tokens":%s}]}`, value, value)},
				{"anthropic", fmt.Sprintf(`{"data":[{"id":"model-a","max_input_tokens":%s,"max_tokens":%s}]}`, value, value)},
				{"gemini", fmt.Sprintf(`{"models":[{"name":"models/model-a","inputTokenLimit":%s,"outputTokenLimit":%s,"supportedGenerationMethods":["generateContent"]}]}`, value, value)},
			} {
				page := catalogFixture(t, tc.kind, tc.body)
				assertCatalogJSON(t, page.Items[0], `{"id":"model-a"}`)
			}
		})
	}
	for _, nested := range []string{`null`, `"broken"`, `4`, `[]`} {
		page := catalogFixture(t, "openai", `{"data":[{"id":"model-a","context_window":32768,"top_provider":`+nested+`}]}`)
		assertCatalogJSON(t, page.Items[0], `{"id":"model-a","contextWindow":32768}`)
	}
}

func TestCatalogCapabilityDuplicateMerge(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","max_input_tokens":200000,"max_tokens":64000},{"id":"model-b","max_input_tokens":100000,"max_tokens":8192}],"has_more":true,"last_id":"model-b"}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","max_input_tokens":128000,"max_tokens":32000},{"id":"model-b","max_tokens":4096},{"id":"model-b","max_input_tokens":64000,"max_tokens":2048}]}`))
		}
	}))
	defer s.Close()
	page, err := fetchProviderModels(t.Context(), Route{Kind: "anthropic", URL: s.URL, Secret: "fixture"}, true)
	if err != nil || len(page.Items) != 2 || calls.Load() != 2 {
		t.Fatalf("page=%v err=%v calls=%d", page, err, calls.Load())
	}
	assertCatalogJSON(t, page.Items[0], `{"id":"model-a","maxInputTokens":128000,"maxOutputTokens":32000}`)
	assertCatalogJSON(t, page.Items[1], `{"id":"model-b","maxOutputTokens":2048}`)
}

func catalogFixture(t *testing.T, kind, body string) api.RelayProviderModelPage {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer s.Close()
	page, err := fetchProviderModels(t.Context(), Route{Kind: kind, URL: s.URL, Secret: "fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func assertCatalogJSON(t *testing.T, value any, want string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue map[string]any
	if json.Unmarshal(raw, &gotValue) != nil || json.Unmarshal([]byte(want), &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("got %s; want %s", raw, want)
	}
}

func TestCatalogUsesConfiguredCredentialsAndProjectsIDs(t *testing.T) {
	for _, kind := range []string{"openai", "deepseek", "minimax", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.Header.Get("Cookie") != "" || strings.Contains(r.URL.RawQuery, "secret") {
					t.Error("unsafe request")
				}
				switch kind {
				case "anthropic":
					if r.Header.Get("x-api-key") != "catalog-secret" || r.Header.Get("anthropic-version") != "2023-06-01" || r.URL.Path != "/v1/models" {
						t.Error("anthropic auth/path")
					}
				case "gemini":
					if r.Header.Get("x-goog-api-key") != "catalog-secret" || r.URL.Path != "/v1beta/models" {
						t.Error("gemini auth/path")
					}
				default:
					if r.Header.Get("Authorization") != "Bearer catalog-secret" || r.URL.Path != "/v1/models" {
						t.Error("openai auth/path")
					}
				}
				if kind == "gemini" {
					_, _ = w.Write([]byte(`{"models":[{"name":"models/z-model","supportedGenerationMethods":["generateContent"]},{"name":"models/embedding","supportedGenerationMethods":["embedContent"]},{"name":"models/a-model","supportedGenerationMethods":["generateContent"]}],"secret":"must-not-leak"}`))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":"z-model","credential":"must-not-leak"},{"id":"a-model"},{"id":"z-model"}],"secret":"must-not-leak"}`))
				}
			}))
			defer s.Close()
			page, err := fetchProviderModels(t.Context(), Route{Kind: kind, URL: s.URL, Secret: "catalog-secret"}, true)
			if err != nil || len(page.Items) != 2 || page.Items[0].Id != "a-model" || page.Items[1].Id != "z-model" || calls.Load() != 1 {
				t.Fatalf("catalog %v: %v", page, err)
			}
		})
	}
}

func TestCatalogPaginationAndBounds(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}],"has_more":true,"last_id":"model-a"}`))
		} else {
			if r.URL.Query().Get("after_id") != "model-a" {
				t.Error("wrong cursor")
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"model-b"}],"has_more":false}`))
		}
	}))
	defer s.Close()
	page, err := fetchProviderModels(t.Context(), Route{Kind: "anthropic", URL: s.URL, Secret: "test"}, true)
	if err != nil || len(page.Items) != 2 || calls.Load() != 2 {
		t.Fatalf("paged %v %v", page, err)
	}
}

func TestCatalogFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		status             int
	}{
		{"auth", `{"error":"catalog-secret"}`, "upstream_unauthorized", 401},
		{"forbidden", `catalog-secret`, "upstream_unauthorized", 403},
		{"unsupported", `catalog-secret`, "unsupported", 404},
		{"limited", `catalog-secret`, "upstream_rate_limited", 429},
		{"bad-json", `{`, "upstream_invalid_response", 200},
		{"wrong-shape", `{"error":"catalog-secret"}`, "upstream_invalid_response", 200},
		{"control-id", `{"data":[{"id":"secret\nmodel"}]}`, "upstream_invalid_response", 200},
		{"large", strings.Repeat("x", catalogBytes+1), "upstream_invalid_response", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			_, err := fetchProviderModels(t.Context(), Route{Kind: "openai", URL: s.URL, Secret: "catalog-secret"}, true)
			var f catalogFailure
			if !errors.As(err, &f) || string(f) != tc.reason {
				t.Fatalf("err=%v", err)
			}
			rec := httptest.NewRecorder()
			rejectCatalog(rec, err)
			if strings.Contains(rec.Body.String(), "catalog-secret") || !strings.Contains(rec.Body.String(), tc.reason) {
				t.Fatalf("unsafe error %s", rec.Body.String())
			}
		})
	}
	var contacted atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { contacted.Add(1) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err := fetchProviderModels(t.Context(), Route{Kind: "openai", URL: redirect.URL, Secret: "catalog-secret"}, true)
	if err == nil || contacted.Load() != 0 {
		t.Fatal("followed provider redirect")
	}
	_, err = fetchProviderModels(t.Context(), Route{Kind: "openai", URL: other.URL, Secret: "catalog-secret"}, false)
	if err == nil || contacted.Load() != 0 {
		t.Fatal("private provider bypassed SSRF policy")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	_, err = fetchProviderModels(ctx, Route{Kind: "openai", URL: slow.URL, Secret: "catalog-secret"}, true)
	if !errors.Is(err, catalogFailure("upstream_timeout")) {
		t.Fatalf("timeout %v", err)
	}
}

func TestCatalogRejectsInvalidProviderIdentityBeforeStore(t *testing.T) {
	b := &Backend{}
	for _, tc := range [][2]string{{"", "019a0000-0000-7000-8000-000000000001"}, {"019a0000-0000-7000-8000-000000000001", "../other"}} {
		if _, err := b.ProviderModels(t.Context(), tc[0], tc[1]); !errors.Is(err, invalid) {
			t.Fatalf("identity %v", err)
		}
	}
}
