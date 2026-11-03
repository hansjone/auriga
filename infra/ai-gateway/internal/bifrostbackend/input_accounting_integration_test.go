package bifrostbackend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

// Root runs this under the exclusive real PostgreSQL slot. No provider hop may
// occur before a budgeted media request has a trusted input-token bound.
func TestBudgetedMediaIsRejectedBeforeAdmissionWhileMeteredMemberPasses(t *testing.T) {
	b, _, memberPassword, tenant := reviewBackend(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5000,"completion_tokens":1,"total_tokens":5001}}`))
	}))
	defer upstream.Close()
	_, pool, _ := reviewPool(t, b, tenant, upstream.URL)
	pool.ContextWindow, pool.MaxOutputTokens = 0, 0
	if err := b.SavePool(t.Context(), tenant, pool); err != nil {
		t.Fatal(err)
	}
	key, err := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "budgeted-media", Active: true, MonthlyBudgetMicros: 2000, RequestsPerMinute: 0, Models: []string{pool.Id}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"` + pool.Id + `","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://media.example.com/large.png"}}]}],"max_tokens":1}`
	for _, hint := range []int64{0, 65536} {
		pool.ContextWindow = hint
		if err = b.SavePool(t.Context(), tenant, pool); err != nil {
			t.Fatal(err)
		}
		w := reviewRequest(b, "POST", "/v1/chat/completions", body, "", "", "", key.Secret)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "trusted media token accounting") {
			t.Fatalf("strict media accepted: %d %s", w.Code, w.Body.String())
		}
		if calls.Load() != 0 {
			t.Fatal("provider consumed before input accounting")
		}
	}
	if err = b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		var requests, subjects int
		if e := tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2)`, tenant, key.Key.Id).Scan(&requests, &subjects); e != nil {
			return e
		}
		if requests != 0 || subjects != 0 {
			t.Fatal("rejected media acquired a reservation or holds")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pool.ContextWindow = 0
	if err = b.SavePool(t.Context(), tenant, pool); err != nil {
		t.Fatal(err)
	}
	account, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	member, err := b.EnsureMember(t.Context(), api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MonthlyBudgetMicros: 0, RequestsPerMinute: 0, MeteringOnly: &yes})
	if err != nil {
		t.Fatal(err)
	}
	w := reviewRequest(b, "POST", "/v1/chat/completions", body, "auriga-member", memberPassword, "", member.Secret)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("metered media failed: %d calls=%d", w.Code, calls.Load())
	}
	usage, err := b.ledger.ListUsage(t.Context(), tenant, account, 10, "")
	if err != nil || len(usage.Items) != 1 || usage.Items[0].GetInputTokens() != 5000 || usage.Items[0].GetOutputTokens() != 1 || usage.Items[0].GetCostMicros() != 5001 || usage.Items[0].UsageQuality != "confirmed" {
		t.Fatalf("metered media usage=%+v err=%v", usage, err)
	}
	assertNoHolds(t, b, tenant, account)
}
