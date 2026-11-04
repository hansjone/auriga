package bifrostbackend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

func TestMemberMeteringPersistsOriginAndDoesNotOverrideAdminOrRevocation(t *testing.T) {
	b, _, memberPassword, tenant := reviewBackend(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid upstream request")
		}
		if body["max_tokens"] != nil || body["max_completion_tokens"] != nil {
			t.Error("relay invented an output ceiling")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500}}`))
	}))
	defer upstream.Close()
	_, pool, _ := reviewPool(t, b, tenant, upstream.URL)
	account, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	m := api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MonthlyBudgetMicros: 0, RequestsPerMinute: 1, MeteringOnly: &yes}
	k, err := b.EnsureMember(t.Context(), m)
	if err != nil || !k.Key.GetMeteringOnly() || k.Key.GetPolicyOrigin() != "default" {
		t.Fatalf("member=%+v err=%v", k.Key, err)
	}
	chat := `{"model":"` + pool.Id + `","messages":[{"role":"user","content":"hello"}]}`
	for i := 0; i < 2; i++ {
		if i == 1 {
			messages := make([]map[string]string, 1001)
			for j := range messages {
				messages[j] = map[string]string{"role": "user", "content": "hello"}
			}
			messages[0]["content"] = strings.Repeat("a", 2<<20)
			raw, _ := json.Marshal(map[string]any{"model": pool.Id, "messages": messages})
			chat = string(raw)
		}
		w := reviewRequest(b, "POST", "/v1/chat/completions", chat, "auriga-member", memberPassword, "", k.Secret)
		if w.Code != 200 {
			t.Fatalf("metered request %d=%d %s", i, w.Code, w.Body.String())
		}
	}
	assertNoHolds(t, b, tenant, account)
	_, err = b.SaveKey(t.Context(), tenant, account, api.RelayKeyInput{Name: k.Key.Name, Active: true, MonthlyBudgetMicros: 0, RequestsPerMinute: 0, Models: []string{pool.Id}, MeteringOnly: &yes})
	if err != nil {
		t.Fatal(err)
	}
	m.MeteringOnly = &no
	k, err = b.EnsureMember(t.Context(), m)
	if err != nil || !k.Key.GetMeteringOnly() || k.Key.GetPolicyOrigin() != "administrator" {
		t.Fatal("ensure overwrote administrator policy")
	}
	_, err = b.SaveKey(t.Context(), tenant, account, api.RelayKeyInput{Name: k.Key.Name, Active: false, MonthlyBudgetMicros: 0, RequestsPerMinute: 0, Models: []string{pool.Id}, MeteringOnly: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.EnsureMember(t.Context(), m); err != denied {
		t.Fatal("ensure revived revoked member")
	}
	if _, err = b.SaveKey(t.Context(), tenant, "", api.RelayKeyInput{Name: "external", Active: true, MonthlyBudgetMicros: 10, RequestsPerMinute: 0, Models: []string{pool.Id}, MeteringOnly: &yes}); err != invalid {
		t.Fatal("external budget bypass accepted")
	}
}

func TestLegacyMemberPolicyIsNotGuessedFromOldDefaults(t *testing.T) {
	b, _, _, tenant := reviewBackend(t)
	account, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	m := api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MonthlyBudgetMicros: 1000, RequestsPerMinute: 60, MeteringOnly: &yes}
	if _, err = b.EnsureMember(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err = b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `UPDATE ai.relay_key SET document=document-'policyOrigin'-'meteringOnly' WHERE tenant_id=$1 AND id=$2`, tenant, account)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	k, err := b.EnsureMember(t.Context(), m)
	if err != nil || k.Key.GetMeteringOnly() || k.Key.GetPolicyOrigin() != "legacy" || k.Key.RequestsPerMinute != 60 || k.Key.MonthlyBudgetMicros != 1000 {
		t.Fatal("legacy explicitness was guessed")
	}
}
