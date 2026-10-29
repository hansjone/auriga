package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceCrossProxyAndResponse(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Get("traceparent"); w.WriteHeader(204) }))
	defer backend.Close()
	cfg.Upstreams["iam"] = backend.URL
	cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	var logs bytes.Buffer
	g.Log = newLogLogger(&logs)
	req := httptest.NewRequest("GET", "http://portal.example.com/api/iam/v1/login/providers", nil)
	req.Header.Set("traceparent", parent)
	req.Header.Set("X-Request-Id", "business-id")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if (<-seen)[3:35] != parent[3:35] || w.Header().Get("X-Trace-Id") != parent[3:35] {
		t.Fatal("trace broke at proxy/response")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["trace_id"] != parent[3:35] || event["service"] != "edge" {
			t.Fatal(event)
		}
	}
}
func TestTraceInvalidAndStructuredSecrets(t *testing.T) {
	for _, v := range []string{"", strings.Repeat("x", 10000), "00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("1", 16) + "-01", "ff-" + strings.Repeat("1", 32) + "-" + strings.Repeat("1", 16) + "-01"} {
		if validTraceparent(v) {
			t.Fatal("invalid accepted")
		}
	}
	t.Setenv("AURIGA_LOG_LEVEL", "DEBUG")
	var logs bytes.Buffer
	newLogLogger(&logs).Debug("account_id=alice password=secret-pass Authorization: Bearer secret-auth\n-----BEGIN PRIVATE KEY-----\nsecret-key\n-----END PRIVATE KEY-----")
	for _, secret := range []string{"secret-pass", "secret-auth", "secret-key"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
	if !strings.Contains(logs.String(), "alice") || !strings.Contains(logs.String(), `"level":"DEBUG"`) {
		t.Fatal(logs.String())
	}
}
