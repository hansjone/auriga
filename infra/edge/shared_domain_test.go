package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSharedDomainCookieAndExchangeInputs(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	claims := sharedTestClaims(false, now.Unix())
	raw, _ := json.Marshal(claims)
	claims, _ = sharedDecode(raw)
	authority := &sharedTestAuthority{binding: claims["binding"].(map[string]any), active: true}
	entry := &SharedEntry{Authority: authority, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"test": pub}}
	target := InstanceTarget{"https://" + sharedTestID + ".instances.test", "/"}
	exchange := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", target.Origin+"/_auriga/placement-ticket", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		entry.exchange(w, r, sharedTestID, target, true, now)
		return w
	}
	token := sharedTestSign(claims, priv, false)
	body := `{"ticket":"` + token + `"}`
	for _, bad := range []string{`{"ticket":"bad"}`, `{"ticket":"` + token + `","ticket":"` + token + `"}`, `{"ticket":"` + token + `","extra":true}`, body + body} {
		w := exchange(bad)
		if w.Code != 400 || len(w.Result().Cookies()) != 0 {
			t.Fatal("invalid body", w.Code)
		}
	}
	w := exchange(body)
	cookies := w.Result().Cookies()
	if w.Code != 302 || len(cookies) != 1 || cookies[0].Name != "__Host-auriga_inst" || cookies[0].Path != "/" || cookies[0].Domain != "" {
		t.Fatal("domain cookie", w.Code, cookies)
	}
	authority.mu.Lock()
	authority.unavailable = true
	authority.mu.Unlock()
	if w = exchange(body); w.Code != 503 {
		t.Fatal("authority outage", w.Code)
	}
}
func TestSharedHeaderCaseStripping(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.test", nil)
	r.Header["auriga-identity"] = []string{"forged"}
	r.Header["AURIGA-IDENTITY"] = []string{"forged2"}
	rewriteOutboundInstance(r, "", "real", "", "")
	count := 0
	for k, v := range r.Header {
		if strings.EqualFold(k, "Auriga-Identity") {
			count += len(v)
		}
	}
	if count != 1 || r.Header.Get("Auriga-Identity") != "real" {
		t.Fatal("external identity survived", r.Header)
	}
}
