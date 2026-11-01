package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiteLLMExternalOriginFollowing(t *testing.T) {
	g, _, _ := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = "http://127.0.0.1:4000" })
	ui := *g.Cfg.LiteLLMUI
	g.Cfg.LiteLLMUI = &ui
	g.Cfg.LinkOrigins = []string{"https://admin.example.com", "https://relay.example.com"}
	g.Cfg.LinkTrustedProxies = []string{"192.0.2.20"}
	g.Cfg.LiteLLMUI.ExternalOrigins = map[string]string{"https://admin.example.com": "https://relay.example.com"}
	if err := g.Cfg.validateLiteLLMUI(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ peer, host, proto, want string }{
		{"192.0.2.20:1234", "admin.example.com", "https", "https://relay.example.com"},
		{"192.0.2.21:1234", "admin.example.com", "https", g.Cfg.liteLLMUIURL()},
		{"192.0.2.20:1234", "attacker.example.com", "https", g.Cfg.liteLLMUIURL()},
	} {
		r := httptest.NewRequest("GET", g.Cfg.Access.AdminURL()+"/ai/ui/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-Host", tc.host)
		r.Header.Set("X-Forwarded-Proto", tc.proto)
		presentation := g.Cfg.linkOrigin(r, "http")
		r = r.WithContext(context.WithValue(r.Context(), linkOriginKey{}, presentation))
		if got := g.liteLLMHandoffTarget(r); got != tc.want {
			t.Fatalf("source selection=%s", got)
		}
	}
	r := httptest.NewRequest("POST", "/ai/session", nil)
	r = r.WithContext(context.WithValue(r.Context(), linkOriginKey{}, "https://relay.example.com"))
	for _, origin := range []string{"https://admin.example.com", g.Cfg.Access.AdminURL(), "https://attacker.example.com", "null"} {
		r.Header.Set("Origin", origin)
		if g.liteLLMHandoffOrigin(r) != (origin == "https://admin.example.com") {
			t.Fatal("external handoff did not enforce paired admin source")
		}
	}
	r.Header.Add("Origin", "https://admin.example.com")
	if g.liteLLMHandoffOrigin(r) {
		t.Fatal("duplicate origins accepted")
	}
	code, err := g.issueLiteLLMHandoff("admin", "https://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if g.consumeLiteLLMHandoff(code) != "" {
		t.Fatal("external code replayed at internal origin")
	}
	code, err = g.issueLiteLLMHandoff("admin", "https://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if g.consumeLiteLLMHandoff(code, "https://relay.example.com") != "admin" || g.consumeLiteLLMHandoff(code, "https://relay.example.com") != "" {
		t.Fatal("external single-use handoff failed")
	}
	for _, bad := range []map[string]string{
		{"http://admin.example.com": "https://relay.example.com"},
		{"https://admin.example.com": "https://admin.example.com"},
		{"https://admin.example.com": "https://unlisted.example.com"},
		{"https://admin.example.com": "https://relay.example.com/"},
	} {
		g.Cfg.LiteLLMUI.ExternalOrigins = bad
		if err := g.Cfg.validateLiteLLMUI(); err == nil {
			t.Fatal("unsafe external origin declaration accepted")
		}
	}
}

func TestLiteLLMHandoffNativeLoginAndRevocation(t *testing.T) {
	// The native exchange is tested separately; this verifies that a native
	// cookie cannot bypass the existing online IAM gate after platform logout.
	g, addresses, store := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = "http://127.0.0.1:1" })
	backend, _ := browserRPCFixture(t, g, store)
	sess := putBrowserSession(g, backend, "admin", "admin")
	store.Revoke(sess.SessionID)
	requireEntryProblem(t, addresses["relayAdmin"], "GET", "/ai/model/info", originAuthority(g.Cfg.LiteLLMUI.Origin), http.Header{"Cookie": {"auriga_litellm_session=admin; token=native-token"}, "Authorization": {"Bearer sk-native-ui-key"}}, 401, "common.unauthenticated")
}
