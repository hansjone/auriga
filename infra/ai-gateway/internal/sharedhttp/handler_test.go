package sharedhttp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
)

type authority struct {
	mu          sync.Mutex
	bindings    map[string]sharedauth.Binding
	revoked     bool
	unavailable bool
	version     int64
}

func (a *authority) Resolve(_ context.Context, q sharedauth.Lookup) (sharedauth.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unavailable {
		return sharedauth.Snapshot{}, sharedauth.ErrUnavailable
	}
	b, ok := a.bindings[q.SessionID]
	scopes := []string{"model.invoke"}
	if q.Audience == sharedauth.Message {
		scopes = []string{"message.send"}
	}
	return sharedauth.Snapshot{Binding: b, Active: ok && !a.revoked, CredentialVersion: a.version, Scopes: scopes}, nil
}

type relayFunc func(context.Context, sharedmodel.SharedCall, func([]byte) error) (sharedmodel.SharedUsage, error)

func (f relayFunc) Stream(ctx context.Context, call sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
	return f(ctx, call, emit)
}

type ledger struct {
	mu        sync.Mutex
	calls     map[string][32]byte
	finished  atomic.Int32
	cancelled atomic.Bool
	finishErr error
}

func (l *ledger) Begin(_ context.Context, b sharedauth.Binding, call string, fingerprint [32]byte) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := sharedmodel.CallKey(b, call)
	if saved, ok := l.calls[k]; ok {
		if saved != fingerprint {
			return false, sharedauth.ErrBinding
		}
		return false, nil
	}
	l.calls[k] = fingerprint
	return true, nil
}
func (l *ledger) Finish(ctx context.Context, _ sharedauth.Binding, _ string, _ sharedmodel.SharedUsage, _ error) error {
	l.cancelled.Store(ctx.Err() != nil)
	l.finished.Add(1)
	return l.finishErr
}

type fixture struct {
	tenant, node, pool string
	a                  *authority
	gate               *sharedauth.Gate
	private            ed25519.PrivateKey
	l                  *ledger
	h                  http.Handler
	calls              atomic.Int32
}

func newFixture(t *testing.T, relay relayFunc) *fixture {
	t.Helper()
	f := &fixture{tenant: uuid.NewString(), node: uuid.NewString(), pool: uuid.NewString(), a: &authority{bindings: map[string]sharedauth.Binding{}, version: 1}, l: &ledger{calls: map[string][32]byte{}}}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.private = priv
	f.gate, err = sharedauth.New(f.a, map[string]ed25519.PublicKey{"model": pub}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if relay == nil {
		relay = func(_ context.Context, call sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
			f.calls.Add(1)
			raw, _ := json.Marshal(map[string]any{"member": call.Binding.MemberID, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}})
			return sharedmodel.SharedUsage{InputTokens: 1, OutputTokens: 2, Known: true}, emit(raw)
		}
	}
	p, err := sharedmodel.NewSharedProvider(f.gate, relay, f.l)
	if err != nil {
		t.Fatal(err)
	}
	f.h, err = NewHandler(f.tenant, []string{f.node}, p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) request(t *testing.T, streaming bool) *http.Request {
	t.Helper()
	b := sharedauth.Binding{TenantID: f.tenant, MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: f.pool, Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1}
	f.a.mu.Lock()
	f.a.bindings[b.SessionID] = b
	f.a.mu.Unlock()
	task := uuid.NewString()
	token, err := f.gate.Issue(t.Context(), sharedauth.Lookup{TenantID: b.TenantID, SessionID: b.SessionID, TaskID: task, Audience: sharedauth.Model}, []string{"model.invoke"}, time.Minute, "model", f.private)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"example-model","messages":[{"role":"user","content":"hello"}],"stream":false}`
	if streaming {
		body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
	}
	r := httptest.NewRequest(http.MethodPost, InvokePath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Auriga-Shared-Capability", token)
	r.Header.Set("Auriga-Shared-Session-Id", b.SessionID)
	r.Header.Set("Auriga-Shared-Task-Id", task)
	r.Header.Set("Idempotency-Key", uuid.NewString())
	u, _ := url.Parse("auriga://node/" + f.node)
	cert := &x509.Certificate{URIs: []*url.URL{u}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	return r
}
func TestSharedModelHTTPAttributesConcurrentMembersAndRejectsReplay(t *testing.T) {
	f := newFixture(t, nil)
	a, b := f.request(t, false), f.request(t, false)
	var wg sync.WaitGroup
	for _, r := range []*http.Request{a, b} {
		wg.Go(func() {
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Errorf("response %d %s", w.Code, w.Body.String())
				return
			}
			var out struct{ Member string }
			if json.Unmarshal(w.Body.Bytes(), &out) != nil {
				t.Error("invalid response")
			}
			f.a.mu.Lock()
			binding := f.a.bindings[r.Header.Get("Auriga-Shared-Session-Id")]
			f.a.mu.Unlock()
			if out.Member != binding.MemberID {
				t.Error("attribution crossed members")
			}
		})
	}
	wg.Wait()
	if f.calls.Load() != 2 || f.l.finished.Load() != 2 {
		t.Fatal("missing settlement")
	}
	replay := a.Clone(t.Context())
	replay.Body = io.NopCloser(strings.NewReader(`{"model":"example-model","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, replay)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "common.conflict") || f.calls.Load() != 2 {
		t.Fatal("replay dispatched", w.Code, w.Body.String())
	}
	replay.Body = io.NopCloser(strings.NewReader(`{"model":"example-model","messages":[{}]}`))
	w = httptest.NewRecorder()
	f.h.ServeHTTP(w, replay)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "shared.binding_mismatch") || f.calls.Load() != 2 {
		t.Fatal("changed replay dispatched", w.Code, w.Body.String())
	}
}
func TestSharedModelHTTPRejectsUntrustedOrChangedAttribution(t *testing.T) {
	for _, name := range []string{"no-mtls", "unverified", "other-node", "expired-node", "tenant", "session", "task", "signature", "audience", "member", "placement", "pool", "epoch", "revision", "auth-generation", "platform-session", "credential-version", "duplicate-header", "body-too-large", "unknown-path", "query", "wrong-method", "not-json", "revoked", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			r := f.request(t, false)
			want := 403
			switch name {
			case "no-mtls":
				r.TLS = nil
			case "unverified":
				r.TLS.VerifiedChains = nil
			case "other-node":
				u, _ := url.Parse("auriga://node/" + uuid.NewString())
				r.TLS.PeerCertificates[0].URIs = []*url.URL{u}
			case "expired-node":
				r.TLS.PeerCertificates[0].NotAfter = time.Now().Add(-time.Second)
			case "tenant":
				f.tenant = uuid.NewString()
				var err error
				f.h, err = NewHandler(f.tenant, []string{f.node}, mustProvider(t, f))
				if err != nil {
					t.Fatal(err)
				}
			case "session":
				r.Header.Set("Auriga-Shared-Session-Id", uuid.NewString())
			case "task":
				r.Header.Set("Auriga-Shared-Task-Id", uuid.NewString())
			case "signature":
				r.Header.Set("Auriga-Shared-Capability", "member-platform-jwt")
			case "audience":
				token, err := f.gate.Issue(t.Context(), sharedauth.Lookup{TenantID: f.tenant, SessionID: r.Header.Get("Auriga-Shared-Session-Id"), TaskID: r.Header.Get("Auriga-Shared-Task-Id"), Audience: sharedauth.Message}, []string{"message.send"}, time.Minute, "model", f.private)
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Auriga-Shared-Capability", token)
			case "member", "placement", "pool", "epoch", "revision", "auth-generation", "platform-session":
				session := r.Header.Get("Auriga-Shared-Session-Id")
				b := f.a.bindings[session]
				switch name {
				case "member":
					b.MemberID = uuid.NewString()
				case "placement":
					b.PlacementID = uuid.NewString()
				case "pool":
					b.PhysicalInstanceID = uuid.NewString()
				case "epoch":
					b.Epoch++
				case "revision":
					b.Revision++
				case "auth-generation":
					b.AuthGeneration++
				case "platform-session":
					b.PlatformSessionID = uuid.NewString()
				}
				f.a.bindings[session] = b
			case "credential-version":
				f.a.version++
			case "duplicate-header":
				r.Header.Add("Auriga-Shared-Task-Id", r.Header.Get("Auriga-Shared-Task-Id"))
				want = 400
			case "body-too-large":
				r.Body = io.NopCloser(bytes.NewReader(make([]byte, 4*1024*1024+1)))
				want = 413
			case "unknown-path":
				r.URL.Path = "/v1/chat/completions"
				want = 404
			case "query":
				r.URL.RawQuery = "tenant=forged"
				want = 400
			case "wrong-method":
				r.Method = http.MethodGet
				want = 405
			case "not-json":
				r.Body = io.NopCloser(strings.NewReader(`{"stream":null}`))
				want = 400
			case "revoked":
				f.a.revoked = true
			case "unavailable":
				f.a.unavailable = true
				want = 503
			}
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			if w.Code != want || f.calls.Load() != 0 {
				t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}
func mustProvider(t *testing.T, f *fixture) *sharedmodel.SharedProvider {
	t.Helper()
	p, err := sharedmodel.NewSharedProvider(f.gate, relayFunc(func(context.Context, sharedmodel.SharedCall, func([]byte) error) (sharedmodel.SharedUsage, error) {
		f.calls.Add(1)
		return sharedmodel.SharedUsage{}, errors.New("must not dispatch")
	}), f.l)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestSharedModelHTTPStreamsAndNeverCompletesFailedStream(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "revoked"}[fail], func(t *testing.T) {
			var f *fixture
			f = newFixture(t, func(_ context.Context, _ sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
				if err := emit([]byte("{\n\"choices\":[]}\n")); err != nil {
					return sharedmodel.SharedUsage{}, err
				}
				if fail {
					f.a.mu.Lock()
					f.a.revoked = true
					f.a.mu.Unlock()
				}
				return sharedmodel.SharedUsage{Known: true}, nil
			})
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, f.request(t, true))
			if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), `data: {"choices":[]}`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "[DONE]") == fail || strings.Contains(w.Body.String(), `"error"`) != fail {
				t.Fatal("wrong stream completion", w.Body.String())
			}
			if f.l.finished.Load() != 1 {
				t.Fatal("stream did not settle")
			}
		})
	}
}
func TestSharedModelHTTPDisconnectCancelsAndSettlesWithoutRequestContext(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	f := newFixture(t, func(ctx context.Context, _ sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
		if err := emit([]byte(`{"choices":[]}`)); err != nil {
			return sharedmodel.SharedUsage{}, err
		}
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return sharedmodel.SharedUsage{}, ctx.Err()
	})
	r := f.request(t, true)
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); f.h.ServeHTTP(httptest.NewRecorder(), r) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("relay not entered")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("relay not cancelled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not end")
	}
	if f.l.finished.Load() != 1 || f.l.cancelled.Load() {
		t.Fatal("cancel suppressed independent settlement")
	}
}

func TestSharedModelHTTPBuffersJSONUntilFinalAuthorizationAndSettlement(t *testing.T) {
	for _, name := range []string{"revoked", "settlement", "malformed-response"} {
		t.Run(name, func(t *testing.T) {
			var f *fixture
			f = newFixture(t, func(_ context.Context, _ sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
				raw := []byte(`{"content":"must-not-escape"}`)
				if name == "malformed-response" {
					raw = []byte("not-json-must-not-escape")
				}
				if err := emit(raw); err != nil {
					return sharedmodel.SharedUsage{}, err
				}
				if name == "revoked" {
					f.a.mu.Lock()
					f.a.revoked = true
					f.a.mu.Unlock()
				}
				return sharedmodel.SharedUsage{Known: true}, nil
			})
			if name == "settlement" {
				f.l.finishErr = errors.New("private-ledger-error-must-not-escape")
			}
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, f.request(t, false))
			want := 503
			if name == "revoked" {
				want = 403
			}
			if w.Code != want || strings.Contains(w.Body.String(), "must-not-escape") || f.l.finished.Load() != 1 {
				t.Fatal("unconfirmed output escaped", w.Code, w.Body.String())
			}
		})
	}
}
