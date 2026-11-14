package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const sharedTestID = "019a0000-0000-7000-8000-000000000001"

func sharedTestClaims(identity bool, now int64) map[string]any {
	b := map[string]any{"tenantId": sharedTestID, "memberId": sharedTestID, "placementId": sharedTestID, "physicalInstanceId": sharedTestID, "mode": "shared", "epoch": 1, "revision": 1}
	m := map[string]any{"schemaVersion": 1, "binding": b, "platformSessionId": sharedTestID, "audience": "auriga.placement.exchange", "issuedAt": now, "expiresAt": now + 60, "jti": sharedJTI()}
	if identity {
		m["schemaVersion"] = 2
		m["issuer"] = "auriga.edge"
		m["audience"] = "auriga.shared.ingress"
		m["method"] = "GET"
		m["canonicalPath"] = "/api"
	}
	return m
}
func sharedTestSign(m map[string]any, key ed25519.PrivateKey, identity bool) string {
	typ := "auriga-shared-ticket+jwt"
	if identity {
		typ = "auriga-shared-identity+jwt"
	}
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "test", "typ": typ})
	p, _ := json.Marshal(m)
	s := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	return s + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(s)))
}
func TestSharedWireNegative(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().Unix()
	keys := map[string]ed25519.PublicKey{"test": pub}
	for _, identity := range []bool{false, true} {
		t.Run(map[bool]string{true: "identity", false: "ticket"}[identity], func(t *testing.T) {
			c := sharedTestClaims(identity, now)
			tok := sharedTestSign(c, priv, identity)
			if _, err := sharedVerify(tok, keys, identity, now); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []func(map[string]any){func(c map[string]any) { c["audience"] = "other" }, func(c map[string]any) { c["expiresAt"] = now }, func(c map[string]any) { c["issuedAt"] = now + 1 }, func(c map[string]any) { c["expiresAt"] = now + 61 }, func(c map[string]any) { c["extra"] = true }, func(c map[string]any) { c["binding"].(map[string]any)["epoch"] = 0 }, func(c map[string]any) { c["binding"].(map[string]any)["revision"] = 1.5 }} {
				c = sharedTestClaims(identity, now)
				mutation(c)
				if _, err := sharedVerify(sharedTestSign(c, priv, identity), keys, identity, now); err == nil {
					t.Fatal("accepted mutation")
				}
			}
			if _, err := sharedVerify(tok, keys, !identity, now); err == nil {
				t.Fatal("cross-purpose")
			}
		})
	}
	for _, raw := range []string{`{"a":1,"a":1}`, `{ "a":1}`, `{"b":1,"a":1}`, `{"a":1} {}`} {
		if _, err := sharedDecode([]byte(raw)); err == nil {
			t.Fatal("accepted noncanonical", raw)
		}
	}
	for _, p := range []string{"//api", "/a/../b", "/%2e%2e", "/%2f", "/%5c", "/%zz", "/a\\b", "/a?b", "/%252e"} {
		if sharedPath(p) {
			t.Fatal("accepted path", p)
		}
	}
}

type sharedTestAuthority struct {
	mu          sync.Mutex
	binding     map[string]any
	active      bool
	unavailable bool
	cancels     []context.CancelFunc
}

func (a *sharedTestAuthority) IsShared(context.Context, string) (bool, error) { return true, nil }
func (a *sharedTestAuthority) Check(_ context.Context, b map[string]any, sid string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unavailable {
		return errors.New("common.unavailable")
	}
	if !a.active {
		return errors.New("shared.revoked")
	}
	if !sharedEqual(a.binding, b) || sid != sharedTestID {
		return errors.New("shared.binding_mismatch")
	}
	return nil
}
func (a *sharedTestAuthority) Watch(_ context.Context, _ map[string]any, _ string, c context.CancelFunc) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancels = append(a.cancels, c)
	return func() {}, nil
}

type sharedTestStore struct {
	mu      sync.Mutex
	used    map[string]bool
	cookies map[string]SharedBrowserBinding
}

func (s *sharedTestStore) ConsumeAndBind(_ context.Context, jti string, _ time.Time, c string, b SharedBrowserBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[jti] {
		return errors.New("common.conflict")
	}
	s.used[jti] = true
	s.cookies[c] = b
	return nil
}
func (s *sharedTestStore) Lookup(_ context.Context, c string) (SharedBrowserBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.cookies[c]
	if !ok {
		return b, errors.New("shared.revoked")
	}
	return b, nil
}
func TestSharedExchange(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	claims := sharedTestClaims(false, now.Unix())
	normalized, _ := json.Marshal(claims)
	claims, _ = sharedDecode(normalized)
	a := &sharedTestAuthority{binding: claims["binding"].(map[string]any), active: true}
	s := &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}
	e := &SharedEntry{Authority: a, Store: s, TicketKeys: map[string]ed25519.PublicKey{"test": pub}}
	token := sharedTestSign(claims, priv, false)
	target := InstanceTarget{"https://portal.test", "/i/" + sharedTestID + "/"}
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://portal.test"+target.Mount+"_auriga/placement-ticket", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.exchange(w, r, sharedTestID, target, true, now)
		return w
	}
	body := `{"ticket":"` + token + `"}`
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(body)
			statuses <- w.Code
			if w.Code == 302 {
				cookies := w.Result().Cookies()
				if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != target.Mount || cookies[0].Name != "__Secure-auriga_inst" {
					t.Error("cookie policy")
				}
				if w.Header().Get("Location") != target.Mount || w.Body.Len() != 0 {
					t.Error("redirect")
				}
			}
			if strings.Contains(w.Body.String(), token) {
				t.Error("ticket leaked")
			}
		}()
	}
	wg.Wait()
	close(statuses)
	success := 0
	for code := range statuses {
		if code == 302 {
			success++
		} else if code != 409 {
			t.Fatal(code)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	if w := request(body[:len(body)-1] + `,"returnUrl":"https://evil"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	a.mu.Lock()
	a.active = false
	a.mu.Unlock()
	if w := request(body); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

var _ = http.MethodGet
