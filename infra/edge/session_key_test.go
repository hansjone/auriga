package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// keyOnlySessionStore behaves like the NATS store: sessions can only be read
// by cookie hash, never by session id.
type keyOnlySessionStore struct{ inner *MemorySessionStore }

func (k keyOnlySessionStore) Get(ctx context.Context, cookieHash string) (*Session, error) {
	return k.inner.Get(ctx, cookieHash)
}

// Regression (W2 e2e): with a key-only store, instance access after ticket
// redeem failed with "portal session invalid" because the binding looked the
// session up by id.
func TestInstanceAccessWithKeyOnlySessionStore(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.SecureCookies = false
	upstream := testNode(t, &cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), testNodeID)
	t.Cleanup(upstream.Close)

	instID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	host := instID + ".i.example.com"
	routes.Put(&InstanceRoute{
		InstanceID: instID,
		TenantID:   "33333333-3333-3333-3333-333333333333",
		OwnerID:    "22222222-2222-2222-2222-222222222222",
		NodeID:     "44444444-4444-4444-4444-444444444444",
		Host:       &host,
		Ingress:    upstream.URL,
		State:      "running",
		Generation: 1,
		UpdatedAt:  time.Now(),
	})
	putPortalSession(sessions, "portal-sess", "csrf", "tok")
	gw, err := NewGateway(cfg, keyOnlySessionStore{sessions}, routes, tickets, signer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/open/"+instID, nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_session", Value: "portal-sess"})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("open %d %s", rr.Code, rr.Body.String())
	}
	u, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	nonceName := openNonceCookieName(false, openNonceCookieDomain(cfg.InstanceParentDomain), instID)
	nonce := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == nonceName {
			nonce = c.Value
		}
	}

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/_auriga/ticket?t="+url.QueryEscape(u.Query().Get("t")), nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: nonceName, Value: nonce})
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("redeem %d %s", rr.Code, rr.Body.String())
	}
	instCookie := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == "auriga_inst" {
			instCookie = c.Value
		}
	}

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/app", nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_inst", Value: instCookie})
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("instance access %d %s", rr.Code, rr.Body.String())
	}
}
