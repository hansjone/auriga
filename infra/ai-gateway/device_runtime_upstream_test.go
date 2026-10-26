package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

func validUpstreamConfig() deviceConfig {
	c := validDeviceConfig()
	c.D7SyntheticOnly, c.SyntheticTenant = false, ""
	c.Upstream = &deviceUpstreamConfig{AllowedHosts: []string{"model.example.com"}, Models: []deviceUpstreamModel{{Tenant: "01900000-0000-7000-8000-000000000001", Model: "beta-model", ProviderID: syntheticProvider, UpstreamModel: syntheticUpstream, ContextWindow: 4096, MaxOutputTokens: 1024, ContextLimitEnforced: true, TrustUsage: true}}}
	return c
}

func TestDeviceUpstreamConfig(t *testing.T) {
	for _, name := range []string{"AURIGA_AI_PG_DSN", "AURIGA_AI_MIGRATION_PG_DSN", "AURIGA_AI_MASTER_KEY_FILE"} {
		t.Setenv(name, "")
	}
	c := validUpstreamConfig()
	if err := validateDeviceConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*deviceConfig){
		func(c *deviceConfig) { c.D7SyntheticOnly = true },
		func(c *deviceConfig) { c.SyntheticTenant = "01900000-0000-7000-8000-000000000001" },
		func(c *deviceConfig) { c.Upstream = nil },
		func(c *deviceConfig) { c.Upstream.AllowedHosts = []string{"*.example.com"} },
		func(c *deviceConfig) { c.Upstream.Models = nil },
		func(c *deviceConfig) { c.Upstream.Models[0].ContextLimitEnforced = false },
		func(c *deviceConfig) { c.Upstream.Models[0].TrustUsage = false },
		func(c *deviceConfig) { c.Upstream.Models[0].ContextWindow = 0 },
		func(c *deviceConfig) { c.Upstream.Models[0].MaxOutputTokens = 4096 },
		func(c *deviceConfig) { c.Upstream.Models = append(c.Upstream.Models, c.Upstream.Models[0]) },
	} {
		c := validUpstreamConfig()
		mutate(&c)
		if validateDeviceConfig(c) == nil {
			t.Fatal("invalid upstream configuration accepted")
		}
	}
	b, _ := json.Marshal(c)
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeviceConfig(p); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	_ = json.Unmarshal(b, &obj)
	obj["upstream"].(map[string]any)["unknown"] = true
	b, _ = json.Marshal(obj)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeviceConfig(p); err == nil {
		t.Fatal("unknown upstream field accepted")
	}
}

func TestUpstreamContextReservation(t *testing.T) {
	m := validUpstreamConfig().Upstream.Models[0]
	p := upstreamProtocol(m)
	input, err := p.MeasureInput(t.Context(), api.ChatCompletionRequest{MaxTokens: 1024})
	if err != nil || input != 3072 {
		t.Fatal("must reserve the entire trusted context headroom")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.MeasureInput(ctx, api.ChatCompletionRequest{MaxTokens: 1024}); err == nil {
		t.Fatal("cancelled measurement accepted")
	}
	if !p.VerifyUsage(t.Context(), api.TokenUsage{PromptTokens: 3072, CompletionTokens: 1024, TotalTokens: 4096}) || p.VerifyUsage(t.Context(), api.TokenUsage{PromptTokens: 4096, CompletionTokens: 1, TotalTokens: 4097}) {
		t.Fatal("usage attestation failed")
	}
}

func TestDeviceUpstreamCoreJSONAndSSE(t *testing.T) {
	f := newDeviceFixture(t)
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil || request["model"] != syntheticUpstream || r.URL.Path != "/v1/chat/completions" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("runtime dispatch mismatch")
			w.WriteHeader(400)
			return
		}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"fake\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":null}],\"usage\":null}\n\n", syntheticUpstream)
			w.(http.Flusher).Flush()
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"fake\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"fake\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[],\"usage\":{\"prompt_tokens\":32,\"completion_tokens\":2,\"total_tokens\":34}}\n\ndata: [DONE]\n\n", syntheticUpstream, syntheticUpstream)
		} else {
			_, _ = fmt.Fprintf(w, `{"id":"fake","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"OK","refusal":null},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":32,"completion_tokens":2,"total_tokens":34,"prompt_tokens_details":{"cached_tokens":0}},"system_fingerprint":"fake"}`, syntheticUpstream)
		}
	}))
	defer s.Close()
	configureUpstreamFixture(t, f, s.URL+"/v1")
	_, base := startDevice(t, f)
	for _, stream := range []bool{false, true} {
		code, b := deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(stream), "")
		if code != 200 || !strings.Contains(b, `"model":"`+syntheticModel+`"`) || strings.Contains(b, syntheticUpstream) || (stream && !strings.Contains(b, "[DONE]")) {
			t.Fatalf("runtime upstream response: %d %s", code, b)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("runtime replayed upstream call")
	}
	var usage int
	if err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1`, f.control.tenant).Scan(&usage)
	}); err != nil {
		t.Fatal(err)
	}
	if usage != 2 {
		t.Fatal("upstream usage not durably accounted")
	}
}

func configureUpstreamFixture(t *testing.T, f *deviceFixture, base string) {
	t.Helper()
	// Model registration/snapshot validation rejects literal private IPs before
	// Transport is reached. Use a valid hostname with an entirely in-memory DNS
	// fixture; no system DNS configuration or real DNS server is contacted.
	oldResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			stop := context.AfterFunc(ctx, func() { _ = server.Close() })
			defer stop()
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(server, query); err != nil || len(query) < 17 {
				return
			}
			end := 12
			for end < len(query) && query[end] != 0 {
				end += int(query[end]) + 1
			}
			end += 5
			if end > len(query) {
				return
			}
			response := append([]byte{query[0], query[1], 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0}, query[12:end]...)
			if binary.BigEndian.Uint16(query[end-4:end-2]) == 1 {
				response[7] = 1
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, 1)
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(response)))
			_, _ = server.Write(append(size[:], response...))
		}()
		return client, nil
	}}
	t.Cleanup(func() { net.DefaultResolver = oldResolver })
	if addresses, err := net.DefaultResolver.LookupNetIP(t.Context(), "ip4", "models.example.com"); err != nil || len(addresses) != 1 || addresses[0].String() != "127.0.0.1" {
		t.Fatalf("in-memory DNS fixture: %v %v", addresses, err)
	}
	base = strings.Replace(base, "127.0.0.1", "models.example.com", 1)
	f.cfg.D7SyntheticOnly, f.cfg.SyntheticTenant = false, ""
	f.cfg.Upstream = &deviceUpstreamConfig{AllowedHosts: []string{"models.example.com"}, Models: []deviceUpstreamModel{{Tenant: f.control.tenant, Model: syntheticModel, ProviderID: syntheticProvider, UpstreamModel: syntheticUpstream, ContextWindow: 64, MaxOutputTokens: 2, ContextLimitEnforced: true, TrustUsage: true}}}
	cipher, err := admin.LoadCipher(f.cfg.MasterKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(rand.Text()), admin.Binding{Tenant: f.control.tenant, Provider: syntheticProvider, Credential: syntheticCredential, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE ai.provider SET document=jsonb_set(document,'{baseUrl}',to_jsonb($2::text)) WHERE tenant_id=$1`, f.control.tenant, base)
		if err != nil {
			return err
		}
		_, err = tx.Exec(t.Context(), `UPDATE ai.credential SET ciphertext=$2 WHERE tenant_id=$1`, f.control.tenant, encrypted)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceUpstreamCoreResponseLimit(t *testing.T) {
	f := newDeviceFixture(t)
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
	}))
	defer s.Close()
	configureUpstreamFixture(t, f, s.URL)
	r, err := newDeviceRuntime(t.Context(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/ai/v1/chat/completions", strings.NewReader(syntheticBody(false)))
	request.Header.Set("Authorization", "Bearer "+f.control.raw)
	request.Header.Set("Content-Type", "application/json")
	response := &upstreamRecorder{httptest.NewRecorder()}
	r.handler.ServeHTTP(response, request)
	if response.Code != 502 || !strings.Contains(response.Body.String(), "ai.upstream_unavailable") || calls.Load() != 1 || response.Body.Len() > 4096 {
		t.Fatalf("response bound: %d %s", response.Code, response.Body.String())
	}
	// This failure has no remote terminal proof. Shutdown must report unresolved
	// responsibility rather than inventing a stopped execution or refund.
	if r.shutdownRuntime() == nil {
		t.Fatal("truncation fabricated remote stop proof")
	}
	stats := r.supervisor.Stats()
	if stats.Pending != 1 {
		t.Fatalf("truncated execution responsibility lost: %+v", stats)
	}
	// The test owns all handlers, workers and DB sessions; production exits on
	// incomplete shutdown instead of synchronously waiting on borrowed resources.
	r.closeResources()
}

type upstreamRecorder struct{ *httptest.ResponseRecorder }

func (*upstreamRecorder) SetReadDeadline(time.Time) error  { return nil }
func (*upstreamRecorder) SetWriteDeadline(time.Time) error { return nil }
