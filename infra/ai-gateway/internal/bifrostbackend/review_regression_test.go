package bifrostbackend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func reviewBackend(t *testing.T) (*Backend, string, string, string) {
	t.Helper()
	db := testpg.Open(t)
	master := sha256.Sum256([]byte(rand.Text()))
	admin, member := rand.Text()+rand.Text(), rand.Text()+rand.Text()
	b, e := New(db, hex.EncodeToString(master[:]), admin, member, true)
	if e != nil {
		t.Fatal(e)
	}
	tenant, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	return b, admin, member, tenant
}
func reviewRequest(b *Backend, method, path, body, user, password, tenant, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		r.SetBasicAuth(user, password)
	} else if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if user != "" && key != "" {
		r.Header.Set("X-Bf-Vk", key)
	}
	if tenant != "" {
		r.Header.Set("X-Auriga-Tenant", tenant)
	}
	if method == "POST" && strings.HasPrefix(path, "/api/") {
		r.Header.Set("Idempotency-Key", rand.Text())
	}
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}
func reviewPool(t *testing.T, b *Backend, tenant, url string) (api.RelayProvider, api.RelayPool, api.RelayKeyIssued) {
	t.Helper()
	p, e := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: "review", Kind: "openai-compatible", BaseUrl: url, Enabled: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	secret := rand.Text()
	if _, e = b.SaveProvider(t.Context(), tenant, p.Id, p.Configuration, &secret); e != nil {
		t.Fatal(e)
	}
	pool := api.RelayPool{Id: "review-pool", Enabled: true, ContextWindow: 128, MaxOutputTokens: 16, Routes: []api.RelayRoute{{ProviderId: p.Id, Model: "test", Weight: 1}}, Price: api.Price{Currency: "USD", Version: "review-v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000}}
	if e = b.SavePool(t.Context(), tenant, pool); e != nil {
		t.Fatal(e)
	}
	key, e := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "review-key", Active: true, MonthlyBudgetMicros: 1000000000, RequestsPerMinute: 10000, Models: []string{pool.Id}})
	if e != nil {
		t.Fatal(e)
	}
	return p, pool, key
}

const reviewReply = `{"id":"review","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
const reviewChat = `{"model":"review-pool","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`

func assertNoHolds(t *testing.T, b *Backend, tenant, id string) {
	t.Helper()
	if e := b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		var active, tokens, micros int64
		if e := tx.QueryRow(t.Context(), `SELECT active,held_tokens,held_micros FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2`, tenant, id).Scan(&active, &tokens, &micros); e != nil {
			return e
		}
		if active != 0 || tokens != 0 || micros != 0 {
			t.Fatalf("unreleased subject: %d/%d/%d", active, tokens, micros)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestReviewServiceCredentialsHaveDisjointPrivileges(t *testing.T) {
	b, admin, member, tenant := reviewBackend(t)
	account, _ := newID()
	raw, _ := json.Marshal(api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MonthlyBudgetMicros: 100, RequestsPerMinute: 60})
	if got := reviewRequest(b, "POST", "/internal/members/ensure", string(raw), "auriga", admin, "", "").Code; got != 403 {
		t.Fatalf("admin on member endpoint: %d", got)
	}
	w := reviewRequest(b, "POST", "/internal/members/ensure", string(raw), "auriga-member", member, "", "")
	if w.Code != 200 {
		t.Fatalf("member ensure: %d", w.Code)
	}
	var issued api.RelayKeyIssued
	if json.Unmarshal(w.Body.Bytes(), &issued) != nil {
		t.Fatal("invalid member response")
	}
	if got := reviewRequest(b, "GET", "/api/providers", "", "auriga-member", member, tenant, "").Code; got != 401 {
		t.Fatalf("member administrative access: %d", got)
	}
	if got := reviewRequest(b, "GET", "/v1/models", "", "auriga", admin, "", issued.Secret).Code; got != 403 {
		t.Fatalf("admin in data channel: %d", got)
	}
	if got := reviewRequest(b, "GET", "/v1/models", "", "auriga-member", member, "", issued.Secret).Code; got != 200 {
		t.Fatalf("member models: %d", got)
	}
}
func TestReviewOriginChangeCannotReuseSavedCredential(t *testing.T) {
	b, _, _, tenant := reviewBackend(t)
	p, _, key := reviewPool(t, b, tenant, "https://one.example.com")
	changed := p.Configuration
	changed.BaseUrl = "https://other.example.com"
	result, e := b.SaveProvider(t.Context(), tenant, p.Id, changed, nil)
	if e != nil || result.HasCredential {
		t.Fatalf("credential moved to a new origin: %v", e)
	}
	if _, _, e = b.poolRoutes(t.Context(), tenant, "review-pool"); e == nil {
		t.Fatal("changed origin remained routable")
	}
	forged := api.RelayKeyInput{Name: memberName(tenant, key.Key.Id), Active: true, MonthlyBudgetMicros: 100, RequestsPerMinute: 10, Models: []string{"*"}}
	if _, e = b.SaveKey(t.Context(), tenant, key.Key.Id, forged); !errors.Is(e, invalid) {
		t.Fatal("external key stole a member name")
	}
}
func TestReviewProviderErrorsRefundAndPreserveSafeStatus(t *testing.T) {
	for _, status := range []int{400, 413, 422, 429, 500, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			b, _, _, tenant := reviewBackend(t)
			var fail atomic.Bool
			fail.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if fail.Load() {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"private-provider-secret"}}`))
					return
				}
				_, _ = w.Write([]byte(reviewReply))
			}))
			t.Cleanup(server.Close)
			_, _, key := reviewPool(t, b, tenant, server.URL)
			expected := status
			if status >= 500 && status != 504 {
				expected = 502
			}
			for range 10 {
				w := reviewRequest(b, "POST", "/v1/chat/completions", reviewChat, "", "", "", key.Secret)
				if w.Code != expected || strings.Contains(w.Body.String(), "private-provider-secret") {
					t.Fatalf("unsafe status: %d", w.Code)
				}
			}
			assertNoHolds(t, b, tenant, key.Key.Id)
			usage, e := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 200, "")
			if e != nil {
				t.Fatal(e)
			}
			for _, u := range usage.Items {
				if u.GetCostMicros() != 0 {
					t.Fatalf("explicit rejection charged: %d", u.GetCostMicros())
				}
			}
			fail.Store(false)
			if w := reviewRequest(b, "POST", "/v1/chat/completions", reviewChat, "", "", "", key.Secret); w.Code != 200 {
				t.Fatalf("recovered key: %d", w.Code)
			}
		})
	}
}
func TestReviewExpiredUnknownReconcilesExactlyOnce(t *testing.T) {
	b, _, _, tenant := reviewBackend(t)
	p, pool, key := reviewPool(t, b, tenant, "https://example.com")
	expiry := time.Now().Add(time.Minute)
	snapshot := budget.TrustedSnapshot{Admission: &ai.BudgetAdmission{TenantId: tenant, AccountId: key.Key.Id, ModelId: pool.Id, ProviderId: p.Id, CredentialId: p.Id, Currency: "USD", PriceVersion: pool.Price.Version, InputTokenUpperBound: 112, MaxOutputTokens: 16, MonthlyBudgetMicros: &key.Key.MonthlyBudgetMicros, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: key.Key.Id}, TokenExpiresAt: timestamppb.New(expiry)}, Price: pool.Price, ValidUntil: expiry}
	r, e := b.admit(t.Context(), key.Key, snapshot, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Fingerprint: sha256.Sum256([]byte("reconcile"))})
	if e != nil {
		t.Fatal(e)
	}
	ref := budget.Ref{TenantID: tenant, AccountID: key.Key.Id, RequestID: r.Admission.RequestId}
	r, e = b.ledger.MarkDispatching(t.Context(), ref, r.Revision)
	if e != nil {
		t.Fatal(e)
	}
	r, e = b.ledger.Settle(t.Context(), ref, r.Revision, budget.Finish{Outcome: "unknown", ExecutionStopped: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = b.reconcileSubject(t.Context(), tenant, key.Key.Id); e != nil {
		t.Fatal(e)
	}
	before, e := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 50, "")
	if e != nil || len(before.Items) != 1 || before.Items[0].FrozenTokens == 0 {
		t.Fatal("fresh uncertain execution was released early")
	}
	r.Admission.TokenExpiresAt = timestamppb.New(time.Now().Add(-20 * time.Minute))
	raw, e := proto.Marshal(r.Admission)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `UPDATE ai.budget_request SET created_at=clock_timestamp()-interval '20 minutes',admission=$4 WHERE tenant_id=$1 AND subject=$2 AND request_id=$3`, tenant, key.Key.Id, ref.RequestID, raw)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	b.tenant = tenant // Simulate a freshly started service with only its deployment identity.
	for range 2 {
		if e = b.reconcileTenant(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	assertNoHolds(t, b, tenant, key.Key.Id)
	usage, e := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 50, "")
	if e != nil || len(usage.Items) != 1 || usage.Items[0].UsageQuality != "estimated" || usage.Items[0].FrozenMicros != 0 {
		t.Fatalf("reconciled usage invalid: %v", e)
	}
}
func TestReviewIndependentConfigurableKeyLimits(t *testing.T) {
	b, _, _, tenant := reviewBackend(t)
	p, pool, base := reviewPool(t, b, tenant, "https://example.com")
	for _, concurrency := range []int64{2, 4} {
		n := int64(1024)
		input := api.RelayKeyInput{Name: rand.Text(), Active: true, MonthlyBudgetMicros: 1000000, RequestsPerMinute: 60, Models: []string{pool.Id}, MaxConcurrency: &concurrency, MaxInFlightTokens: &n}
		key, e := b.SaveKey(t.Context(), tenant, "", input)
		if e != nil {
			t.Fatal(e)
		}
		expiry := time.Now().Add(time.Minute)
		snapshot := budget.TrustedSnapshot{Admission: &ai.BudgetAdmission{TenantId: tenant, AccountId: key.Key.Id, ModelId: pool.Id, ProviderId: p.Id, CredentialId: p.Id, Currency: "USD", PriceVersion: pool.Price.Version, InputTokenUpperBound: 112, MaxOutputTokens: 16, MonthlyBudgetMicros: &base.Key.MonthlyBudgetMicros, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: key.Key.Id}, TokenExpiresAt: timestamppb.New(expiry)}, Price: pool.Price, ValidUntil: expiry}
		for i := int64(0); i <= concurrency; i++ {
			_, e = b.admit(t.Context(), key.Key, snapshot, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Fingerprint: sha256.Sum256([]byte(rand.Text()))})
			if i < concurrency && e != nil {
				t.Fatal(e)
			}
			if i == concurrency && e == nil {
				t.Fatal("configured concurrency ignored")
			}
		}
	}
}

type reviewBrokenWriter struct{ *httptest.ResponseRecorder }

func (w reviewBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("downstream disconnected")
}
func TestReviewTimeoutAndDisconnectedStreamReleaseCapacity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "stream-disconnect"}[stream], func(t *testing.T) {
			b, _, _, tenant := reviewBackend(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"id\":\"partial\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(250 * time.Millisecond):
				}
			}))
			t.Cleanup(server.Close)
			_, _, key := reviewPool(t, b, tenant, server.URL)
			for range 6 {
				body := reviewChat
				if stream {
					body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				}
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key.Secret)
				ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
				r = r.WithContext(ctx)
				rec := httptest.NewRecorder()
				if stream {
					b.ServeHTTP(reviewBrokenWriter{rec}, r)
				} else {
					b.ServeHTTP(rec, r)
					if rec.Code != 504 {
						t.Fatalf("timeout status %d", rec.Code)
					}
				}
				cancel()
			}
			assertNoHolds(t, b, tenant, key.Key.Id)
			usage, e := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 50, "")
			if e != nil || len(usage.Items) != 6 {
				t.Fatalf("missing interrupted usage %v", e)
			}
			for _, row := range usage.Items {
				if row.UsageQuality != "estimated" || row.FrozenTokens != 0 {
					t.Fatal("interruption left unknown/frozen usage")
				}
			}
		})
	}
}
func TestReviewPaginatedAdministrationAndTenantBinding(t *testing.T) {
	b, admin, member, tenant := reviewBackend(t)
	b.tenant = tenant
	other, _ := newID()
	if w := reviewRequest(b, "GET", "/api/providers", "", "auriga", admin, other, ""); w.Code != 403 {
		t.Fatal("admin escaped installed tenant")
	}
	account, _ := newID()
	if _, e := b.EnsureMember(t.Context(), api.RelayMemberEnsure{TenantId: other, AccountId: account}); !errors.Is(e, denied) {
		t.Fatal("member escaped installed tenant")
	}
	if w := reviewRequest(b, "POST", "/internal/members/ensure", `{"tenantId":"`+other+`","accountId":"`+account+`","monthlyBudgetMicros":10,"requestsPerMinute":10}`, "auriga-member", member, "", ""); w.Code != 403 {
		t.Fatal("member channel escaped installed tenant")
	}
	for range 205 {
		if _, e := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: rand.Text(), Kind: "openai-compatible", BaseUrl: "https://example.com", Enabled: true}, nil); e != nil {
			t.Fatal(e)
		}
		if _, e := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: rand.Text(), Active: true, Models: []string{"*"}, MonthlyBudgetMicros: 100, RequestsPerMinute: 60}); e != nil {
			t.Fatal(e)
		}
	}
	for _, resource := range []string{"providers", "virtual-keys"} {
		cursor := ""
		seen := map[string]bool{}
		pages := 0
		for {
			path := "/api/" + resource + "?limit=73"
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			w := reviewRequest(b, "GET", path, "", "auriga", admin, tenant, "")
			var page struct {
				Items      []struct{ Id string }
				NextCursor string
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) > 73 {
				t.Fatal("invalid page")
			}
			for _, item := range page.Items {
				if seen[item.Id] {
					t.Fatal("duplicate page item")
				}
				seen[item.Id] = true
			}
			cursor = page.NextCursor
			pages++
			if cursor == "" {
				break
			}
			if pages > 4 {
				t.Fatal("cursor did not advance")
			}
		}
		if len(seen) != 205 || pages != 3 {
			t.Fatalf("missing late entries: %d", len(seen))
		}
		for _, q := range []string{"limit=201", "limit=0", "cursor=invalid", "cursor=" + listCursor(other, resource, account), "cursor=" + listCursor(tenant, "pools", "model")} {
			if w := reviewRequest(b, "GET", "/api/"+resource+"?"+q, "", "auriga", admin, tenant, ""); w.Code != 400 {
				t.Fatalf("invalid cursor accepted: %d", w.Code)
			}
		}
	}
}
func TestReviewStreamingExplicitRejectionsRefund(t *testing.T) {
	for _, status := range []int{400, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			b, _, _, tenant := reviewBackend(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"private upstream error"}}`))
			}))
			t.Cleanup(server.Close)
			_, _, key := reviewPool(t, b, tenant, server.URL)
			expected := status
			if status == 500 {
				expected = 502
			}
			for range 10 {
				w := reviewRequest(b, "POST", "/v1/chat/completions", strings.TrimSuffix(reviewChat, "}")+`,"stream":true}`, "", "", "", key.Secret)
				if w.Code != expected {
					t.Fatalf("stream rejection: %d", w.Code)
				}
				assertNoHolds(t, b, tenant, key.Key.Id)
			}
			usage, e := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 50, "")
			if e != nil {
				t.Fatal(e)
			}
			for _, row := range usage.Items {
				if row.GetCostMicros() != 0 {
					t.Fatal("explicit stream rejection charged")
				}
			}
		})
	}
}
func TestReviewKeyLimitDefaultsAndBounds(t *testing.T) {
	for _, member := range []bool{false, true} {
		c, n, e := limits(nil, nil, member)
		wantC, wantN := int64(0), int64(0)
		if e != nil || c != wantC || n != wantN {
			t.Fatal("wrong source defaults")
		}
	}
	for _, v := range []int64{-1, 101} {
		if _, _, e := limits(&v, nil, false); e == nil {
			t.Fatal("invalid concurrency accepted")
		}
	}
	for _, v := range []int64{-1, 1<<30 + 1} {
		if _, _, e := limits(nil, &v, true); e == nil {
			t.Fatal("invalid token ceiling accepted")
		}
	}
}
