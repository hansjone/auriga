package litellm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPiAIChatShapeAndExplicitStoreRejection(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["store"] != nil {
			t.Error("unsupported store reached upstream")
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	key := filepath.Join(t.TempDir(), "service-key")
	if err := os.WriteFile(key, []byte("sk-"+rand.Text()), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(upstream.URL, key, func(context.Context, string) (Principal, error) {
		return Principal{Tenant: "tenant", Account: "member", Device: "device", Expires: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	// pi-ai Chat Completions uses developer messages and streaming usage options.
	shape := `{"model":"pool-a","messages":[{"role":"developer","content":"Test"},{"role":"user","content":"Hello"}],"stream":true,"stream_options":{"include_usage":true},"max_completion_tokens":64,"temperature":1,"tools":[],"tool_choice":"auto"}`
	if fixture := os.Getenv("AURIGA_PI_AI_REQUEST_FIXTURE"); fixture != "" {
		raw, readErr := os.ReadFile(fixture)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil || body["model"] == nil || body["messages"] == nil || body["store"] != nil {
			t.Fatal("native request fixture must contain a chat without store")
		}
		shape = string(raw)
	}
	for _, store := range []string{"", ",\"store\":false", ",\"store\":true", ",\"store\":null"} {
		request := httptest.NewRequest(http.MethodPost, "/ai/v1/chat/completions", strings.NewReader(strings.TrimSuffix(shape, "}")+store+"}"))
		request.Header.Set("Authorization", "Bearer fixture")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if store == "" {
			if response.Code != 200 {
				t.Fatalf("pi-ai request failed: %d", response.Code)
			}
			continue
		}
		var problem struct {
			Code   string            `json:"code"`
			Params map[string]string `json:"params"`
		}
		if json.Unmarshal(response.Body.Bytes(), &problem) != nil || response.Code != 400 || problem.Code != "common.validation_failed" || problem.Params["field"] != "/store" {
			t.Fatalf("store rejection must identify /store: status=%d", response.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls %d, want 1", calls)
	}
}
