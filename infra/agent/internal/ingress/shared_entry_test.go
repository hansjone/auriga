package ingress

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"go.etcd.io/bbolt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type sharedNodeAuthority struct {
	mu      sync.Mutex
	binding map[string]any
	active  bool
	cancels []context.CancelFunc
}

func (a *sharedNodeAuthority) Check(_ context.Context, b map[string]any, s string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || !sharedEqual(b, a.binding) || s != sharedTestID {
		return errors.New("revoked")
	}
	return nil
}
func (a *sharedNodeAuthority) Watch(_ context.Context, _ map[string]any, _ string, c context.CancelFunc) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancels = append(a.cancels, c)
	return func() {}, nil
}
func TestSharedIngressBoundary(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	c := sharedTestClaims(true, now.Unix())
	token := sharedTestSign(c, priv, true)
	verified, err := sharedVerify(token, map[string]ed25519.PublicKey{"test": pub}, true, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	a := &sharedNodeAuthority{binding: verified["binding"].(map[string]any), active: true}
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get(identityHeader) == "" || r.URL.Path != "/api" {
			t.Error("forwarding")
		}
		w.WriteHeader(204)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "replay.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Config{Shared: &SharedIngress{Replay: &SharedIdentityReplayStore{DB: db}, Keys: map[string]ed25519.PublicKey{"test": pub}, Authority: a}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Shared: true, Upstream: u}), Now: func() time.Time { return now }})
	req := func(path, method string, values []string) int {
		r := httptest.NewRequest(method, "http://node/i/"+sharedTestID+path, nil)
		r.Header[identityHeader] = values
		w := httptest.NewRecorder()
		s.route(w, r)
		return w.Code
	}
	if got := req("/api", "GET", []string{token, token}); got != 403 {
		t.Fatal(got)
	}
	if got := req("/api", "POST", []string{token}); got != 403 {
		t.Fatal(got)
	}
	if got := req("/other", "GET", []string{token}); got != 403 {
		t.Fatal(got)
	}
	if got := req("/api", "GET", []string{token}); got != 204 {
		t.Fatal(got)
	}
	if got := req("/api", "GET", []string{token}); got != 403 {
		t.Fatal("replay", got)
	}
	for _, field := range []string{"tenantId", "memberId", "epoch", "revision"} {
		copyClaims := sharedTestClaims(true, now.Unix())
		b := copyClaims["binding"].(map[string]any)
		if field == "epoch" || field == "revision" {
			b[field] = 2
		} else {
			b[field] = "019a0000-0000-7000-8000-000000000002"
		}
		if got := req("/api", "GET", []string{sharedTestSign(copyClaims, priv, true)}); got != 403 {
			t.Fatal(field, got)
		}
	}
	for _, path := range []string{"/files/", "/logs/"} {
		r := httptest.NewRequest("GET", "http://node"+path+sharedTestID, nil)
		w := httptest.NewRecorder()
		s.route(w, r)
		if w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("unexpected upstream requests", calls)
	}
	// Watch is established before the second authority check; cancellation reaches active requests.
	checked, cleanup, err := s.sharedRequest(httptest.NewRequest("GET", "http://node/api", nil), sharedTestID, "/api")
	_ = checked
	_ = cleanup
	if err == nil {
		t.Fatal("missing identity accepted")
	}
	c["jti"] = "019a0000-0000-7000-8000-000000000002"
	r := httptest.NewRequest("GET", "http://node/api", nil)
	r.Header.Set(identityHeader, sharedTestSign(c, priv, true))
	checked, cleanup, err = s.sharedRequest(r, sharedTestID, "/api")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	a.mu.Lock()
	a.active = false
	for _, cancel := range a.cancels {
		cancel()
	}
	a.mu.Unlock()
	select {
	case <-checked.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("stream not revoked")
	}
}
