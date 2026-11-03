package bifrostbackend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestRouteIdentityUsesProviderAndModel(t *testing.T) {
	const provider = "019a0000-0000-7000-8000-000000000001"
	routes := []api.RelayRoute{{ProviderId: provider, Model: "first", Weight: 1}, {ProviderId: provider, Model: "second", Weight: 1}}
	providers, err := routeProviders(routes)
	if err != nil || len(providers) != 1 || !providers[provider] {
		t.Fatalf("providers=%v err=%v", providers, err)
	}
	routes[1].Model = "first"
	if _, err := routeProviders(routes); err != invalid {
		t.Fatal("duplicate route accepted")
	}
	routes[1].Model = " second "
	if _, err := routeProviders(routes); err != invalid {
		t.Fatal("untrimmed model accepted")
	}
	for i := range routes {
		routes[i].Model = "model"
		routes[i].Weight = 0
	}
	if _, err := routeProviders(routes[:1]); err != invalid {
		t.Fatal("zero total weight accepted")
	}
}

func TestRunnerSameProviderModelsKeepWeightsAndFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		weights []float64
		want    []string
	}{
		{"second-selected", []float64{0, 1}, []string{"second"}},
		{"same-provider-fallback", []float64{1, 0}, []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var called []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
				}
				mu.Lock()
				called = append(called, body.Model)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if body.Model == "first" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":{"message":"unavailable","type":"server_error"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"reply","object":"chat.completion","created":1,"model":"second","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
			}))
			defer server.Close()
			routes := []Route{{ID: "same-provider", Kind: "openai", URL: server.URL, Secret: "fixture", Model: "first", Weight: tc.weights[0]}, {ID: "same-provider", Kind: "openai", URL: server.URL, Secret: "fixture", Model: "second", Weight: tc.weights[1]}}
			result, err := Run(t.Context(), routes, []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}],"max_tokens":8}`), true, func([]byte) error { return nil })
			if err != nil || !result.Confirmed {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(called) != len(tc.want) {
				t.Fatalf("called=%v want=%v", called, tc.want)
			}
			for i := range called {
				if called[i] != tc.want[i] {
					t.Fatalf("called=%v want=%v", called, tc.want)
				}
			}
		})
	}
}
