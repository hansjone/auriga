package bifrostbackend

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestPostgresMemberKeyBudgetsRPMRevocationAndUsage(t *testing.T) {
	for _, tenant := range []string{"", "00000000-0000-0000-0000-000000000001"} {
		name := tenant
		if name == "" {
			name = "generated"
		}
		t.Run(name, func(t *testing.T) { testPostgresMemberKeyBudgetsRPMRevocationAndUsage(t, tenant) })
	}
}

func testPostgresMemberKeyBudgetsRPMRevocationAndUsage(t *testing.T, tenant string) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	if _, e := rand.Read(master); e != nil {
		t.Fatal(e)
	}
	password := rand.Text() + rand.Text()
	memberPassword := rand.Text() + rand.Text()
	b, e := New(db, hex.EncodeToString(master), password, memberPassword, true)
	if e != nil {
		t.Fatal(e)
	}
	if tenant == "" {
		tenant, _ = newID()
	}
	member, _ := newID()
	other, _ := newID()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","object":"chat.completion","created":1,"model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"真实PG记录"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	t.Cleanup(upstream.Close)
	cfg := api.RelayProviderInput{Name: "供应商", Kind: "openai-compatible", BaseUrl: upstream.URL, Enabled: true}
	provider, e := b.SaveProvider(t.Context(), tenant, "", cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	secret := rand.Text()
	if _, e = b.SaveProvider(t.Context(), tenant, provider.Id, cfg, &secret); e != nil {
		t.Fatal(e)
	}
	price := api.Price{Currency: "USD", Version: "fixture-v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000}
	pool := api.RelayPool{Id: "pool", Enabled: true, Routes: []api.RelayRoute{{ProviderId: provider.Id, Model: "fixture", Weight: 1}}, ContextWindow: 16, MaxOutputTokens: 8, Price: price}
	if e = b.SavePool(t.Context(), tenant, pool); e != nil {
		t.Fatal(e)
	}
	secondCfg := cfg
	secondCfg.Name = "backup"
	backup, err := b.SaveProvider(t.Context(), tenant, "", secondCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.SaveProvider(t.Context(), tenant, backup.Id, secondCfg, &secret); err != nil {
		t.Fatal(err)
	}
	pool.Routes = append(pool.Routes, api.RelayRoute{ProviderId: backup.Id, Model: "fixture", Weight: 1})
	if err = b.SavePool(t.Context(), tenant, pool); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if _, err = b.SaveProvider(t.Context(), tenant, provider.Id, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, routes, err := b.poolRoutes(t.Context(), tenant, pool.Id); err != nil || len(routes) != 1 || routes[0].ID != backup.Id {
		t.Fatalf("disabled provider retained: %+v %v", routes, err)
	}
	cfg.Enabled = true
	if _, err = b.SaveProvider(t.Context(), tenant, provider.Id, cfg, nil); err != nil {
		t.Fatal(err)
	}
	m := api.RelayMemberEnsure{TenantId: tenant, AccountId: member, MonthlyBudgetMicros: 1000, RequestsPerMinute: 60}
	var wg sync.WaitGroup
	tokens := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := b.EnsureMember(t.Context(), m)
			if err != nil {
				t.Error(err)
				return
			}
			tokens <- out.Secret
		}()
	}
	wg.Wait()
	close(tokens)
	token := ""
	for value := range tokens {
		if token != "" && value != token {
			t.Fatal("member acquired multiple keys")
		}
		token = value
	}
	if token == "" {
		t.Fatal("member key absent")
	}
	request := func(method, path, body, key string, admin bool, tid string) (int, []byte) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == "POST" && strings.HasPrefix(path, "/api/") {
			r.Header.Set("Idempotency-Key", rand.Text())
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if admin {
			if key != "" || path == "/internal/members/ensure" {
				r.SetBasicAuth("auriga-member", memberPassword)
			} else {
				r.SetBasicAuth("auriga", password)
			}
			r.Header.Set("X-Auriga-Tenant", tid)
			if key != "" {
				r.Header.Set("X-Bf-Vk", key)
			}
		}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w.Code, w.Body.Bytes()
	}
	// Durable admin idempotency replays the same VK, rejects changed bodies, and
	// never stores its cleartext response in PostgreSQL.
	createKey := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/virtual-keys", strings.NewReader(body))
		r.SetBasicAuth("auriga", password)
		r.Header.Set("X-Auriga-Tenant", tenant)
		r.Header.Set("Idempotency-Key", "same-admin-request")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	keyBody := `{"name":"idempotent","active":true,"monthlyBudgetMicros":1000,"requestsPerMinute":60,"models":["pool"]}`
	first, again := createKey(keyBody), createKey(keyBody)
	if first.Code != 200 || again.Code != 200 || first.Body.String() != again.Body.String() {
		t.Fatalf("admin replay %d/%d", first.Code, again.Code)
	}
	if changed := createKey(strings.Replace(keyBody, "1000", "2000", 1)); changed.Code != 409 {
		t.Fatalf("changed replay %d", changed.Code)
	}
	chat := `{"model":"pool","messages":[{"role":"user","content":"你好"}],"max_tokens":8,"metadata":{"account_id":"forged"},"user":"forged"}`
	if code, _ := request("POST", "/v1/chat/completions", chat, token, false, tenant); code != 403 {
		t.Fatalf("member key bypassed identity bridge: %d", code)
	}
	if code, raw := request("POST", "/v1/chat/completions", chat, token, true, tenant); code != 200 {
		t.Fatalf("member chat %d %s", code, raw)
	}
	if code, raw := request("GET", "/api/logs?accountId="+member, "", "", true, tenant); code != 200 || !bytes.Contains(raw, []byte(`"inputTokens":3`)) || !bytes.Contains(raw, []byte(`"outputTokens":2`)) {
		t.Fatalf("member usage %d %s", code, raw)
	}
	if code, _ := request("GET", "/api/logs?accountId="+member, "", "", true, other); code != 404 {
		t.Fatalf("cross-tenant usage=%d", code)
	}
	if code, _ := request("GET", "/api/providers", "", "", false, tenant); code != 401 {
		t.Fatalf("unauthenticated admin=%d", code)
	}
	if code, _ := request("GET", "/api/providers", "", "", true, ""); code != 403 {
		t.Fatalf("unbound tenant=%d", code)
	}
	input := api.RelayKeyInput{Name: "external", Active: true, MonthlyBudgetMicros: 1000, RequestsPerMinute: 1, Models: []string{"pool"}}
	external, e := b.SaveKey(t.Context(), tenant, "", input)
	if e != nil {
		t.Fatal(e)
	}
	if code, raw := request("POST", "/v1/chat/completions", chat, external.Secret, false, tenant); code != 200 {
		t.Fatalf("external chat %d %s", code, raw)
	}
	if code, raw := request("POST", "/v1/chat/completions", chat, external.Secret, false, tenant); code != 429 || !bytes.Contains(raw, []byte("common.rate_limited")) {
		t.Fatalf("RPM %d %s", code, raw)
	}
	input.RequestsPerMinute = 60
	input.MonthlyBudgetMicros = 0
	if _, e = b.SaveKey(t.Context(), tenant, external.Key.Id, input); e != nil {
		t.Fatal(e)
	}
	if code, raw := request("POST", "/v1/chat/completions", chat, external.Secret, false, tenant); code != 429 || !bytes.Contains(raw, []byte("ai.budget_exceeded")) {
		t.Fatalf("budget %d %s", code, raw)
	}
	input.Active = false
	if _, e = b.SaveKey(t.Context(), tenant, external.Key.Id, input); e != nil {
		t.Fatal(e)
	}
	if code, _ := request("POST", "/v1/chat/completions", chat, external.Secret, false, tenant); code != 403 {
		t.Fatalf("revoked key=%d", code)
	}
	input.Name = "member policy"
	input.MonthlyBudgetMicros = 1000
	if _, e = b.SaveKey(t.Context(), tenant, member, input); e != nil {
		t.Fatal(e)
	}
	if _, e = b.EnsureMember(t.Context(), m); e != denied {
		t.Fatalf("disabled member was recreated: %v", e)
	}
	if code, _ := request("GET", "/internal/keys/check", "", token, false, tenant); code != 403 {
		t.Fatalf("disabled quota=%d", code)
	}
	if _, _, e = b.parseToken(strings.Replace(external.Secret, tenant, other, 1)); e == nil {
		t.Fatal("forged tenant token accepted")
	}
	providerList, e := b.ListProviders(t.Context(), tenant)
	raw, _ := json.Marshal(providerList)
	if e != nil || bytes.Contains(raw, []byte(secret)) {
		t.Fatal("provider secret exposed")
	}
}
