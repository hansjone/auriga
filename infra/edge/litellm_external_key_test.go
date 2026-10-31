package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiteLLMNativeUIKeyCannotEscapeToPublicData(t *testing.T) {
	master, key := rand.Text(), "sk-"+rand.Text()
	filename := filepath.Join(t.TempDir(), "master")
	if err := os.WriteFile(filename, []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	mode := "dashboard"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai/key/info" || r.Header.Get("Authorization") != "Bearer "+master || r.Header.Get("Cookie") != "" || r.URL.Query().Get("key") != fmt.Sprintf("%x", sha256.Sum256([]byte(key))) || strings.Contains(r.URL.String(), key) || strings.Contains(r.URL.String(), master) {
			t.Error("key inspection leaked credentials or used wrong authority")
		}
		switch mode {
		case "missing":
			w.WriteHeader(404)
		case "down":
			w.WriteHeader(503)
		case "malformed":
			_, _ = w.Write([]byte(`{"info":null}`))
		case "dashboard":
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"team_id": "litellm-dashboard"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"team_id": nil}})
		}
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	g := &Gateway{Cfg: Config{LiteLLMUI: &LiteLLMUIConfig{Login: &LiteLLMUILogin{PasswordFile: filename}}}, litellmURL: u}
	r := httptest.NewRequest("POST", "/ai/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	for _, tc := range []struct {
		mode   string
		status int
		code   string
	}{
		{"dashboard", 403, "common.forbidden"}, {"external", 0, ""}, {"missing", 401, "common.unauthenticated"}, {"down", 503, "common.unavailable"}, {"malformed", 503, "common.unavailable"},
	} {
		mode = tc.mode
		if status, code := g.checkLiteLLMExternalKey(r); status != tc.status || code != tc.code {
			t.Fatalf("%s: %d %s", mode, status, code)
		}
	}
	// There is no local key state: a fresh edge still rejects the same UI key.
	mode = "dashboard"
	restarted := &Gateway{Cfg: g.Cfg, litellmURL: u}
	if status, _ := restarted.checkLiteLLMExternalKey(r); status != 403 {
		t.Fatal("edge restart forgot UI key restriction")
	}
}
