package bifrost

import (
	"context"
	"encoding/json"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMemberKeyCreationIsolationAndRevocation(t *testing.T) {
	var mu sync.Mutex
	keys := map[string]api.RelayKeyIssued{}
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		u, p, ok := r.BasicAuth()
		if !ok || u != "auriga-member" || p != "test-password" || r.URL.Path != "/internal/members/ensure" || r.Method != "POST" {
			t.Error("private member boundary")
			w.WriteHeader(401)
			return
		}
		var m api.RelayMemberEnsure
		if json.NewDecoder(r.Body).Decode(&m) != nil || m.MonthlyBudgetMicros != 10 || m.RequestsPerMinute != 60 {
			t.Error("missing member policy")
		}
		name := memberName(m.TenantId, m.AccountId)
		out, exists := keys[name]
		if !exists {
			out = api.RelayKeyIssued{Key: api.RelayKey{Id: m.AccountId, AccountId: m.AccountId, Source: "member", Name: name, Active: true, MonthlyBudgetMicros: 10, RequestsPerMinute: 60, Models: []string{"*"}}, Secret: "sk-bf-" + name}
			keys[name] = out
			creates++
		}
		if !out.Key.Active {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	password := filepath.Join(t.TempDir(), "password")
	if e := os.WriteFile(password, []byte("test-password"), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := NewClient(server.URL, password, MemberPolicy{MonthlyBudgetMicros: 10, RequestsPerMinute: 60})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			k, err := c.MemberKey(context.Background(), "tenant-a", "member-a")
			if err != nil || k.Key.Name != memberName("tenant-a", "member-a") {
				t.Error("member unavailable", err)
			}
		})
	}
	wg.Wait()
	if _, e = c.MemberKey(context.Background(), "tenant-b", "member-a"); e != nil {
		t.Fatal(e)
	}
	if creates != 2 {
		t.Fatal("member identity was not tenant scoped")
	}
	mu.Lock()
	k := keys[memberName("tenant-a", "member-a")]
	k.Key.Active = false
	keys[k.Key.Name] = k
	mu.Unlock()
	if _, e = c.MemberKey(context.Background(), "tenant-a", "member-a"); e == nil || e.Error() != "common.forbidden" {
		t.Fatal("revoked member reissued")
	}
	if creates != 2 {
		t.Fatal("policy reset")
	}
}
func TestMemberKeyFailsClosed(t *testing.T) {
	for _, mode := range []string{"unavailable", "foreign-metadata", "invalid-value", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "unavailable" {
					w.WriteHeader(503)
					return
				}
				k := api.RelayKeyIssued{Key: api.RelayKey{Id: "member", AccountId: "member", Source: "member", Name: memberName("tenant", "member"), Active: true, Models: []string{"*"}}, Secret: "sk-bf-test"}
				if mode == "foreign-metadata" {
					k.Key.AccountId = "foreign"
				}
				if mode == "invalid-value" {
					k.Secret = "admin-password"
				}
				_ = json.NewEncoder(w).Encode(k)
				if mode == "duplicate" {
					_ = json.NewEncoder(w).Encode(k)
				}
			}))
			defer server.Close()
			password := filepath.Join(t.TempDir(), "password")
			if e := os.WriteFile(password, []byte("test-password"), 0600); e != nil {
				t.Fatal(e)
			}
			c, e := NewClient(server.URL, password, MemberPolicy{MonthlyBudgetMicros: 10, RequestsPerMinute: 60})
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			if _, e = c.MemberKey(context.Background(), "tenant", "member"); e == nil {
				t.Fatal("untrusted key accepted")
			}
		})
	}
}
func TestClientConfiguration(t *testing.T) {
	for _, origin := range []string{"http://example.com", "http://bifrost:8080/path", "http://user@bifrost:8080", "http://bifrost:8080?x=1", "http://bifrost:8080#x"} {
		if _, e := NewClient(origin, "password", MemberPolicy{MonthlyBudgetMicros: 10, RequestsPerMinute: 60}); e == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	if _, e := NewClient("http://bifrost:8080", "password", MemberPolicy{MonthlyBudgetMicros: -1, RequestsPerMinute: 60}); e == nil {
		t.Fatal("negative budget")
	}
	if strings.Contains(memberName("a:b", "c"), memberName("a", "b:c")) {
		t.Fatal("ambiguous tuple")
	}
}
