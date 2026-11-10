package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Standard OpenAI clients (pi-ai in DSH) send store:false to the relay. The
// opt-out is dropped before forwarding; a request to store and any other
// unsupported field are refused and named so the caller can fix them.
func TestExternalRelayDropsStoreOptOut(t *testing.T) {
	var forwarded []map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/keys/check" {
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "test", "active": true, "source": "external"})
			return
		}
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		forwarded = append(forwarded, body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	_, addresses, _ := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
		c.Relay = &RelayConfig{Origin: upstream.URL, AdminPasswordFile: "not-read"}
	})
	send := func(extra string) (int, Problem) {
		body := `{"model":"gpt-x","messages":[{"role":"user","content":"hi"}],"stream":true` + extra + `}`
		r, _ := http.NewRequest("POST", "http://"+addresses["portal"]+"/ai/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer sk-bf-fixture")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var p Problem
		_ = json.Unmarshal(raw, &p)
		return resp.StatusCode, p
	}
	for _, extra := range []string{`,"store":false`, `,"store":null`} {
		if code, _ := send(extra); code != 200 {
			t.Fatalf("%s rejected: %d", extra, code)
		}
	}
	if len(forwarded) != 2 {
		t.Fatalf("forwarded %d", len(forwarded))
	}
	for _, body := range forwarded {
		if _, ok := body["store"]; ok {
			t.Fatal("store reached the relay")
		}
		if body["model"] == nil || body["messages"] == nil {
			t.Fatal("chat fields were lost")
		}
	}
	for extra, field := range map[string]string{`,"store":true`: "/store", `,"store":"false"`: "/store", `,"api_base":"http://evil"`: "/api_base"} {
		code, p := send(extra)
		if code != 400 || p.Code != "common.validation_failed" || p.Params["field"] != field {
			t.Fatalf("%s: %d %+v", extra, code, p)
		}
	}
	if len(forwarded) != 2 {
		t.Fatal("rejected request reached the relay")
	}
}
