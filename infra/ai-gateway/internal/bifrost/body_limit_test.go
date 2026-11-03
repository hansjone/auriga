package bifrost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemberBridgeCarriesLongContextWithoutTruncation(t *testing.T) {
	want := strings.Repeat("a", 2<<20)
	upstream := memberServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 1 || req.Messages[0].Content != want {
			t.Error("long context was truncated")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	file := filepath.Join(t.TempDir(), "service-key")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(upstream.URL, file, MemberPolicy{}, func(context.Context, string) (Principal, error) {
		return Principal{Tenant: "tenant", Account: "member", Device: "device", Expires: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	r := httptest.NewRequest("POST", "/ai/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"`+want+`"}]}`))
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
}
