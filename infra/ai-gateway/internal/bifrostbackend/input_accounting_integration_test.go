package bifrostbackend

import (
	"encoding/json"
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
		if w.Code != 400 || !strings.Contains(w.Body.String(), "trusted input-token bound") {
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

// Root owns the real PostgreSQL slot. The server-tool request fits the old JSON
// estimate and the explicit budget, but must be refused before any durable
// admission, dispatch, usage or outbox record is created.
func TestBudgetedDynamicInputIsRejectedBeforeAdmissionWhileLocalAndMeteredToolsPass(t *testing.T) {
	b, _, memberPassword, tenant := reviewBackend(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var wire struct {
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&wire) != nil || len(wire.Tools) != 1 || wire.Tools[0].Name != "web_fetch" {
			t.Error("unexpected Anthropic tool wire")
			w.WriteHeader(400)
			return
		}
		input := "3"
		if wire.Tools[0].Type == "web_fetch_20250910" {
			input = "5000"
		} else if wire.Tools[0].Type != "" {
			t.Error("local function became a server tool")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_fixture","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":` + input + `,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	provider, pool, _ := reviewPool(t, b, tenant, upstream.URL)
	config := provider.Configuration
	config.Kind = "anthropic"
	secret := "fixture-provider-key"
	if _, err := b.SaveProvider(t.Context(), tenant, provider.Id, config, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SaveProvider(t.Context(), tenant, provider.Id, config, &secret); err != nil {
		t.Fatal(err)
	}
	pool.ContextWindow, pool.MaxOutputTokens = 0, 0
	if err := b.SavePool(t.Context(), tenant, pool); err != nil {
		t.Fatal(err)
	}
	key, err := b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "budgeted-dynamic-input", Active: true, MonthlyBudgetMicros: 2000, Models: []string{pool.Id}})
	if err != nil {
		t.Fatal(err)
	}
	prefix := `{"model":"` + pool.Id + `","messages":[{"role":"user","content":"Fetch https://remote.example.com/large"}],"max_tokens":128`
	for _, kind := range []string{"web_fetch_20250910", "web_search_20250305", "code_execution_20250825", "mcp_toolset", "file_search", "future_server_tool"} {
		body := prefix + `,"tools":[{"type":"` + kind + `","name":"web_fetch"}]}`
		if int64(len(body))*2+64+128 >= 2000 {
			t.Fatal("fixture would not exercise the old under-reservation")
		}
		w := reviewRequest(b, "POST", "/v1/chat/completions", body, "", "", "", key.Secret)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"common.validation_failed"`) || !strings.Contains(w.Body.String(), `"field":"/tools"`) || calls.Load() != 0 {
			t.Fatalf("strict server tool %s dispatched or lost field: %d %s calls=%d", kind, w.Code, w.Body.String(), calls.Load())
		}
	}
	for _, tc := range []struct{ field, body string }{
		{"/tool_choice", prefix + `,"tool_choice":{"type":"web_fetch"}}`},
		{"/response_format", prefix + `,"response_format":{"type":"remote_format","url":"https://remote.example.com/schema"}}`},
		{"/messages", `{"model":"` + pool.Id + `","messages":[{"role":"assistant","content":"text","reasoning_details":[{"type":"reasoning.encrypted","data":"remote-state"}]}],"max_tokens":128}`},
	} {
		w := reviewRequest(b, "POST", "/v1/chat/completions", tc.body, "", "", "", key.Secret)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"common.validation_failed"`) || !strings.Contains(w.Body.String(), `"field":"`+tc.field+`"`) || calls.Load() != 0 {
			t.Fatalf("strict %s dispatched or lost field: %d %s calls=%d", tc.field, w.Code, w.Body.String(), calls.Load())
		}
	}
	if err = b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		var subjects, requests, periods, usage, outbox int
		if e := tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_period WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1 AND subject=$2)`, tenant, key.Key.Id).Scan(&subjects, &requests, &periods, &usage, &outbox); e != nil {
			return e
		}
		if subjects != 0 || requests != 0 || periods != 0 || usage != 0 || outbox != 0 {
			t.Fatalf("rejected input left budget records: %d/%d/%d/%d/%d", subjects, requests, periods, usage, outbox)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	localBody := prefix + `,"tools":[{"type":"function","function":{"name":"web_fetch","parameters":{"type":"object","properties":{"url":{"type":"string"}}}}}],"tool_choice":{"type":"function","function":{"name":"web_fetch"}}}`
	w := reviewRequest(b, "POST", "/v1/chat/completions", localBody, "", "", "", key.Secret)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("budgeted local function failed: %d %s calls=%d", w.Code, w.Body.String(), calls.Load())
	}
	usage, err := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 10, "")
	if err != nil || len(usage.Items) != 1 || usage.Items[0].GetInputTokens() != 3 || usage.Items[0].GetOutputTokens() != 1 || usage.Items[0].GetCostMicros() != 4 || usage.Items[0].UsageQuality != "confirmed" {
		t.Fatalf("local function usage=%+v err=%v", usage, err)
	}
	assertNoHolds(t, b, tenant, key.Key.Id)
	account, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	member, err := b.EnsureMember(t.Context(), api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MeteringOnly: &yes})
	if err != nil {
		t.Fatal(err)
	}
	serverBody := prefix + `,"tools":[{"type":"web_fetch_20250910","name":"web_fetch"}]}`
	w = reviewRequest(b, "POST", "/v1/chat/completions", serverBody, "auriga-member", memberPassword, "", member.Secret)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("metered server tool failed: %d %s calls=%d", w.Code, w.Body.String(), calls.Load())
	}
	usage, err = b.ledger.ListUsage(t.Context(), tenant, account, 10, "")
	if err != nil || len(usage.Items) != 1 || usage.Items[0].GetInputTokens() != 5000 || usage.Items[0].GetOutputTokens() != 1 || usage.Items[0].GetCostMicros() != 5001 || usage.Items[0].UsageQuality != "confirmed" {
		t.Fatalf("metered server input usage=%+v err=%v", usage, err)
	}
	assertNoHolds(t, b, tenant, account)
}
