package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRelayChatBodyCapacityAtBothEntrypoints(t *testing.T) {
	for _, external := range []bool{false, true} {
		for _, domain := range []bool{false, true} {
			name := map[bool]string{false: "member", true: "external"}[external] + "/" + map[bool]string{false: "ip", true: "domain"}[domain]
			t.Run(name, func(t *testing.T) {
				content := "start:" + strings.Repeat("context", 300000) + ":end"
				wantHash := sha256.Sum256([]byte(content))
				var complete atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/internal/keys/check" {
						_ = json.NewEncoder(w).Encode(map[string]any{"name": "fixture", "active": true, "source": "external"})
						return
					}
					var request struct {
						Messages  []struct{ Role, Content string } `json:"messages"`
						MaxTokens int                              `json:"max_tokens"`
					}
					decoder := json.NewDecoder(r.Body)
					if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if len(request.Messages) != 1 || request.Messages[0].Role != "user" || sha256.Sum256([]byte(request.Messages[0].Content)) != wantHash || request.MaxTokens != 7 {
						t.Error("chat content or output parameter changed in transit")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					complete.Add(1)
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				defer upstream.Close()
				g, addresses, _ := accessFixtureConfigured(t, domain, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
					if external {
						c.Relay = &RelayConfig{Origin: upstream.URL, AdminPasswordFile: "not-read"}
					} else {
						c.Upstreams["ai-device"] = upstream.URL
						c.Upstreams["ai-gateway"] = c.Upstreams["iam"]
					}
				})
				body, err := json.Marshal(map[string]any{"model": "auto", "messages": []map[string]string{{"role": "user", "content": content}}, "max_tokens": 7})
				if err != nil {
					t.Fatal(err)
				}
				request, err := http.NewRequest(http.MethodPost, "http://"+addresses["portal"]+"/ai/v1/chat/completions", strings.NewReader(string(body)))
				if err != nil {
					t.Fatal(err)
				}
				if domain {
					request.Host = g.Cfg.PortalHost
				}
				if external {
					request.Header.Set("Authorization", "Bearer sk-bf-fixture")
				} else {
					request.Header.Set("Authorization", "Bearer ai-device")
				}
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				if response.StatusCode != http.StatusOK || complete.Load() != 1 {
					t.Fatalf("large chat status=%d complete=%d", response.StatusCode, complete.Load())
				}
				tooLarge := request.Clone(t.Context())
				oversizedBody := `{"model":"auto","messages":[{"role":"user","content":"` + strings.Repeat("x", 32<<20) + `"}]}`
				tooLarge.Body = io.NopCloser(strings.NewReader(oversizedBody))
				tooLarge.ContentLength = int64(len(oversizedBody))
				tooLarge.GetBody = nil
				rejected, err := http.DefaultClient.Do(tooLarge)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rejected.Body.Close() }()
				if rejected.StatusCode == http.StatusOK || complete.Load() != 1 {
					t.Fatalf("oversize chat accepted: status=%d complete=%d", rejected.StatusCode, complete.Load())
				}
			})
		}
	}
}

func TestRelayBodyCapacityDoesNotExpandNetworkSubmit(t *testing.T) {
	var received atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		received.Store(n)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	g, addresses, _ := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
		c.Upstreams["ai-device"] = upstream.URL
		c.Upstreams["ai-gateway"] = c.Upstreams["iam"]
	})
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	g.networkSubmitURL = target
	g.APITransports[target] = http.DefaultTransport
	request, err := http.NewRequest(http.MethodPost, "http://"+addresses["portal"]+"/api/ai/v1/network/submit", strings.NewReader(strings.Repeat("x", 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer ai-device")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusOK || received.Load() > 1<<20 {
		t.Fatalf("network submit body boundary expanded: status=%d received=%d", response.StatusCode, received.Load())
	}
}
