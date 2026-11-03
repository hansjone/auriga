package bifrostbackend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Root alone runs this under real PostgreSQL. Catalog reads, pool writes,
// reopened configuration and both authorized public directories use real paths.
func TestPostgresUpstreamCapabilitiesPersistForMemberAndExternalDirectories(t *testing.T) {
	b, admin, memberPassword, tenant := reviewBackend(t)
	var directory atomic.Value
	directory.Store(`{"data":[{"id":"large","context_window":2000000,"max_output_tokens":128000,"api_key":"not-public"},{"id":"small","context_window":32768,"max_output_tokens":4096}]}`)
	var chatCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(directory.Load().(string)))
			return
		}
		chatCalls.Add(1)
		var fields map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&fields) != nil {
			t.Error("invalid chat wire")
		}
		limit, _, err := outputTokenLimit(fields, 4096)
		if err != nil || limit != 131072 {
			t.Error("directory metadata clipped an actual request", limit, err)
		}
		_, _ = w.Write([]byte(reviewReply))
	}))
	defer upstream.Close()
	provider, pool, external := reviewPool(t, b, tenant, upstream.URL)
	pool.Routes = []api.RelayRoute{{ProviderId: provider.Id, Model: "small", Weight: 0}, {ProviderId: provider.Id, Model: "large", Weight: 1}}
	pool.Capabilities = &api.RelayPoolCapabilities{SourceProviderId: provider.Id, SourceModel: "large", ContextWindowMode: "inherit", MaxOutputTokensMode: "inherit"}
	account, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	member, err := b.EnsureMember(t.Context(), api.RelayMemberEnsure{TenantId: tenant, AccountId: account, MeteringOnly: &yes})
	if err != nil {
		t.Fatal(err)
	}
	readBoth := func(context, output int64) {
		t.Helper()
		for _, identity := range []struct{ user, password, key string }{{"", "", external.Secret}, {"auriga-member", memberPassword, member.Secret}} {
			w := reviewRequest(b, "GET", "/v1/models", "", identity.user, identity.password, "", identity.key)
			var response struct {
				Data []map[string]any `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 1 {
				t.Fatalf("directory status=%d body=%s", w.Code, w.Body.String())
			}
			entry := response.Data[0]
			if context == 0 {
				if _, exists := entry["context_window"]; exists {
					t.Fatal("unknown context invented", entry)
				}
			} else if entry["context_window"] != float64(context) {
				t.Fatal("member/external context differs", entry)
			}
			if output == 0 {
				if _, exists := entry["max_output_tokens"]; exists {
					t.Fatal("unknown output invented", entry)
				}
			} else if entry["max_output_tokens"] != float64(output) {
				t.Fatal("member/external output differs", entry)
			}
			if strings.Contains(w.Body.String(), "large") || strings.Contains(w.Body.String(), provider.Id) || strings.Contains(w.Body.String(), "not-public") {
				t.Fatal("upstream identity escaped public metadata")
			}
		}
	}
	refreshAndSave := func() {
		t.Helper()
		page, e := b.ProviderModels(t.Context(), tenant, provider.Id)
		if e != nil {
			t.Fatal(e)
		}
		pool.Capabilities.Upstream = &api.RelayPoolUpstreamCapabilities{ProviderId: provider.Id, Model: api.RelayProviderModel{Id: "large"}}
		for _, model := range page.Items {
			if model.Id == "large" {
				pool.Capabilities.Upstream.Model = model
			}
		}
		raw, e := json.Marshal(pool)
		if e != nil {
			t.Fatal(e)
		}
		w := reviewRequest(b, "POST", "/api/pools", string(raw), "auriga", admin, tenant, "")
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &pool) != nil {
			t.Fatalf("save status=%d body=%s", w.Code, w.Body.String())
		}
		pagePools, e := b.ListPools(t.Context(), tenant)
		if e != nil || len(pagePools.Items) != 1 || pagePools.Items[0].ContextWindow != pool.ContextWindow || pagePools.Items[0].MaxOutputTokens != pool.MaxOutputTokens || pagePools.Items[0].Capabilities == nil {
			t.Fatal("reopened pool differs from saved publication", pagePools, e)
		}
	}
	refreshAndSave()
	readBoth(2000000, 128000)
	directory.Store(`{"data":[{"id":"large","context_window":4000000,"max_output_tokens":256000}]}`)
	refreshAndSave()
	readBoth(4000000, 256000)
	recommended := int64(1000000)
	pool.Capabilities.RecommendedContextWindow = &recommended
	directory.Store(`{"data":[{"id":"large","max_output_tokens":128000}]}`)
	refreshAndSave()
	readBoth(1000000, 128000)
	pool.Capabilities.RecommendedContextWindow = nil
	directory.Store(`{"data":[{"id":"large"}]}`)
	refreshAndSave()
	readBoth(0, 0)
	pool.ContextWindow, pool.MaxOutputTokens = 1000000, 4096
	pool.Capabilities.ContextWindowMode, pool.Capabilities.MaxOutputTokensMode = "manual", "manual"
	directory.Store(`{"data":[{"id":"large","context_window":2000000,"max_output_tokens":128000}]}`)
	refreshAndSave()
	readBoth(1000000, 4096)
	w := reviewRequest(b, "POST", "/v1/chat/completions", `{"model":"`+pool.Id+`","messages":[{"role":"user","content":"hello"}],"max_tokens":131072}`, "auriga-member", memberPassword, "", member.Secret)
	if w.Code != 200 || chatCalls.Load() != 1 {
		t.Fatalf("actual call was capped by directory values: %d %s", w.Code, w.Body.String())
	}
	assertNoHolds(t, b, tenant, account)
}
