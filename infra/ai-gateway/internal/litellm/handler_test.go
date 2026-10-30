package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityAndStreaming(t *testing.T) {
	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-fixture" || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" {
			t.Error("credential boundary")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("sk-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h, err := New(upstream.URL, key, func(_ context.Context, raw string) (Principal, error) {
		calls++
		if raw != "device-token" {
			return Principal{}, errors.New("common.unauthenticated")
		}
		return Principal{Tenant: "tenant", Account: "member", Device: "device", Expires: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	r := httptest.NewRequest("POST", "/ai/v1/chat/completions", strings.NewReader(`{"model":"pool","messages":[],"stream":true,"user":"forged","metadata":{"tenant_id":"forged","api_key":"injected"}}`))
	r.Header.Set("Authorization", "Bearer device-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "data: [DONE]\n\n" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	metadata := got["metadata"].(map[string]any)
	if got["user"] != "tenant:member" || metadata["tenant_id"] != "tenant" || metadata["account_id"] != "member" || metadata["device_id"] != "device" || metadata["api_key"] != nil || calls != 1 {
		t.Fatalf("untrusted attribution: %v", got)
	}
}

func TestRejectsBeforeUpstream(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(200) }))
	defer upstream.Close()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("sk-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(upstream.URL, key, func(_ context.Context, raw string) (Principal, error) {
		if raw == "revoked" {
			return Principal{}, errors.New("common.unauthenticated")
		}
		if raw == "forbidden" {
			return Principal{}, errors.New("common.forbidden")
		}
		return Principal{Tenant: "tenant", Account: "member", Device: "device", Expires: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, tc := range []struct {
		name, path, token, body string
		status                  int
	}{
		{"missing", "/ai/v1/chat/completions", "", `{}`, 401},
		{"revoked", "/ai/v1/chat/completions", "revoked", `{}`, 401},
		{"permission", "/ai/v1/chat/completions", "forbidden", `{}`, 403},
		{"path", "/key/generate", "good", `{}`, 404},
		{"malformed", "/ai/v1/chat/completions", "good", `{} {}`, 400},
		{"null", "/ai/v1/chat/completions", "good", `null`, 400},
		{"upstream-override", "/ai/v1/chat/completions", "good", `{"model":"pool","api_base":"https://attacker.example.com"}`, 400},
		{"key-override", "/ai/v1/chat/completions", "good", `{"model":"pool","api_key":"injected"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatal("rejected request reached upstream")
	}
}
