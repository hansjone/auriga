package bifrost

import (
	"encoding/json"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"net/http"
	"net/http/httptest"
)

func memberServer(data http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/members/ensure" {
			_ = json.NewEncoder(w).Encode(api.RelayKeyIssued{Key: api.RelayKey{Id: "member", AccountId: "member", Source: "member", Name: memberName("tenant", "member"), Active: true, MonthlyBudgetMicros: 10, RequestsPerMinute: 60, Models: []string{"*"}}, Secret: "sk-bf-fixture"})
			return
		}
		data.ServeHTTP(w, r)
	}))
}
