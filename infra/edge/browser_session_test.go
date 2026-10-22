package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A real generated-protocol server: every call crosses verified mTLS and the
// service-JWT/tenant interceptor. The clock and projection visibility are controlled.
type browserRPCServer struct {
	iamv1.UnimplementedSessionServiceServer
	mu                   sync.Mutex
	clock                atomic.Int64
	rows                 map[string]*Session
	store                *MemorySessionStore
	mode                 string
	project              bool
	introspects, touches int
	onTouch              func()
	inactiveTouch        bool
}

func (s *browserRPCServer) now() time.Time { return time.Unix(s.clock.Load(), 0).UTC() }

func (s *browserRPCServer) IntrospectSession(ctx context.Context, request *iamv1.IntrospectSessionRequest) (*iamv1.IntrospectSessionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.introspects++
	if s.mode == "timeout" {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "deadline")
	}
	if s.mode == "unavailable" {
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	if s.mode == "auth" {
		return nil, status.Error(codes.Unauthenticated, "service credential")
	}
	row := s.rows[request.SessionToken]
	if row == nil || row.Valid(s.now(), "") != nil || sessionAudience(row.Audience) != request.Audience {
		return &iamv1.IntrospectSessionResponse{}, nil
	}
	if !s.now().Add(time.Minute).Before(row.AccessTokenExpiresAt) {
		row.AccessToken = fmt.Sprintf("renewed-%d", s.introspects)
		row.AccessTokenExpiresAt = s.now().Add(5 * time.Minute)
		if s.project {
			s.store.Put(request.SessionToken, row)
		}
	}
	result := &iamv1.IntrospectSessionResponse{Active: true, AccessToken: row.AccessToken, AccessTokenExpiresAt: timestamppb.New(row.AccessTokenExpiresAt), Session: &iamv1.Session{SessionId: row.SessionID, AccountId: row.AccountID, TenantId: row.TenantID, Audience: request.Audience, CreatedAt: timestamppb.New(row.AuthTime), ExpiresAt: timestamppb.New(row.ExpiresAt), IdleExpiresAt: timestamppb.New(row.IdleExpiresAt), Mfa: row.MFA}}
	switch s.mode {
	case "sid":
		result.Session.SessionId = "different"
	case "account":
		result.Session.AccountId = "different"
	case "tenant":
		result.Session.TenantId = "different"
	case "audience":
		result.Session.Audience = iamv1.SessionAudience_SESSION_AUDIENCE_UNSPECIFIED
	case "absolute":
		result.Session.ExpiresAt = timestamppb.New(row.ExpiresAt.Add(time.Hour))
	case "jwt-expired":
		result.AccessTokenExpiresAt = timestamppb.New(s.now())
	case "idle-expired":
		result.Session.IdleExpiresAt = timestamppb.New(s.now())
	case "malformed":
		result.Session.IdleExpiresAt = nil
	}
	return result, nil
}

func (s *browserRPCServer) TouchSession(_ context.Context, request *iamv1.TouchSessionRequest) (*iamv1.TouchSessionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touches++
	if s.inactiveTouch {
		return &iamv1.TouchSessionResponse{}, nil
	}
	row := s.rows[request.SessionToken]
	if row == nil || row.Valid(s.now(), "") != nil || sessionAudience(row.Audience) != request.Audience {
		return &iamv1.TouchSessionResponse{}, nil
	}
	row.IdleExpiresAt = s.now().Add(30 * time.Minute)
	if row.IdleExpiresAt.After(row.ExpiresAt) {
		row.IdleExpiresAt = row.ExpiresAt
	}
	if s.project {
		s.store.Put(request.SessionToken, row)
	}
	if s.onTouch != nil {
		s.onTouch()
	}
	return &iamv1.TouchSessionResponse{Active: true, IdleExpiresAt: timestamppb.New(row.IdleExpiresAt)}, nil
}

func browserRPCFixture(t *testing.T, g *Gateway, store *MemorySessionStore) (*browserRPCServer, string) {
	t.Helper()
	pki := createAccessPKI(t)
	token, key := serviceJWT(t)
	backend := &browserRPCServer{rows: make(map[string]*Session), store: store, project: true}
	backend.clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
	g.Now = backend.now
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	var dialer func(context.Context, string) (net.Conn, error)
	if runtime.GOOS == "windows" {
		_ = ln.Close()
		var endpoint string
		ln, endpoint = localTestListener(t)
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		dialer = func(ctx context.Context, _ string) (net.Conn, error) { return dialLocalIngress(ctx, u) }
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert})), grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no peer")
		}
		transport, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(transport.State.VerifiedChains) == 0 || !strings.EqualFold(transport.State.PeerCertificates[0].DNSNames[0], "edge.internal.test") {
			return nil, status.Error(codes.Unauthenticated, "not edge")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || !verifyServiceJWT(strings.TrimPrefix(md.Get("authorization")[0], "Bearer "), key) || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != g.Cfg.Access.TenantID() {
			return nil, status.Error(codes.Unauthenticated, "not authenticated")
		}
		return handler(ctx, req)
	}))
	iamv1.RegisterSessionServiceServer(server, backend)
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { server.Stop(); _ = ln.Close() })
	file := filepath.Join(t.TempDir(), "service.jwt")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := openIAMRPC(IAMRPCConfig{Address: address, ServerName: "iam.internal.test", ServiceTokenFile: file, TimeoutMillis: 100}, pki.hop, dialer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	g.SessionRPC = client
	return backend, file
}

func putBrowserSession(g *Gateway, backend *browserRPCServer, cookie, audience string) *Session {
	s := putPortalSession(backend.store, cookie, "csrf", "initial")
	s.SessionID += audience
	s.TenantID = g.Cfg.Access.TenantID()
	s.Audience = audience
	s.AuthTime = backend.now().Add(-time.Minute)
	s.ExpiresAt = backend.now().Add(8 * time.Hour)
	s.IdleExpiresAt = backend.now().Add(30 * time.Minute)
	s.AccessTokenExpiresAt = backend.now().Add(5 * time.Minute)
	backend.rows[cookie] = s
	backend.store.Put(cookie, s)
	return s
}

func browserRequest(g *Gateway, role, method, path, cookie string) *httptest.ResponseRecorder {
	o := g.Cfg.Access.PortalOrigin()
	if role == "admin" {
		o = g.Cfg.Access.AdminOrigin()
	}
	r := httptest.NewRequest(method, "http://"+originAuthority(o)+path, nil)
	r.Header.Set("Origin", "http://"+originAuthority(o))
	r.AddCookie(&http.Cookie{Name: sessionCookieBase(role), Value: cookie})
	r.AddCookie(&http.Cookie{Name: csrfCookieBase(), Value: "csrf"})
	r.Header.Set("X-Auriga-CSRF", "csrf")
	r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, role))
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	return w
}

func TestBrowserSessionsAcrossJWTLifetime(t *testing.T) {
	for _, domain := range []bool{false, true} {
		for _, audience := range []string{"portal", "admin"} {
			t.Run(fmt.Sprintf("domain=%v/%s", domain, audience), func(t *testing.T) {
				var bearer string
				g, _, store := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					bearer = r.Header.Get("Authorization")
					w.WriteHeader(204)
				}))
				backend, _ := browserRPCFixture(t, g, store)
				putBrowserSession(g, backend, "cookie", audience)
				path := "/api/org/v1/members"
				if audience == "admin" {
					path = "/api/org/v1/admin/members"
				}
				if w := browserRequest(g, audience, "GET", path, "cookie"); w.Code != 204 || bearer != "Bearer initial" {
					t.Fatalf("initial: %d %s", w.Code, bearer)
				}
				backend.clock.Add(int64((6 * time.Minute) / time.Second))
				if w := browserRequest(g, audience, "GET", path, "cookie"); w.Code != 204 || bearer != "Bearer renewed-1" {
					t.Fatalf("renewal: %d %s", w.Code, bearer)
				}
				if backend.introspects != 1 || backend.touches != 2 {
					t.Fatalf("calls: introspect=%d touch=%d", backend.introspects, backend.touches)
				}
				if domain || audience == "portal" {
					if w := browserRequest(g, audience, "POST", path, "cookie"); w.Code != 204 {
						t.Fatalf("CSRF after renewal: %d", w.Code)
					}
				}
			})
		}
	}
}

func TestBrowserIntrospectionRejectsBindingAndControlErrors(t *testing.T) {
	for _, mode := range []string{"sid", "account", "tenant", "audience", "absolute", "jwt-expired", "idle-expired", "malformed", "auth", "unavailable", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			g, _, store := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("forwarded rejected request")
				w.WriteHeader(204)
			}))
			backend, _ := browserRPCFixture(t, g, store)
			row := putBrowserSession(g, backend, "cookie", "portal")
			row.AccessTokenExpiresAt = backend.now().Add(-time.Second)
			store.Put("cookie", row)
			backend.mode = mode
			want := 503
			if mode == "idle-expired" {
				want = 401
			}
			if w := browserRequest(g, "portal", "GET", "/api/org/v1/members", "cookie"); w.Code != want {
				t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}

func TestBrowserKVMissCSRFAndProjectionVisibility(t *testing.T) {
	for _, scenario := range []string{"read-rebuild", "write-missing", "async-projection", "wrong-projection", "empty-csrf"} {
		t.Run(scenario, func(t *testing.T) {
			g, _, store := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			backend, _ := browserRPCFixture(t, g, store)
			row := putBrowserSession(g, backend, "cookie", "portal")
			store.Delete("cookie")
			method, want := "GET", 503
			switch scenario {
			case "read-rebuild":
				want = 204
			case "write-missing":
				method = "POST"
			case "async-projection":
				backend.project = false
			case "wrong-projection":
				backend.onTouch = func() { cp := *row; cp.AccountID = "wrong"; store.Put("cookie", &cp) }
			case "empty-csrf":
				row.CSRFHash = ""
			}
			if w := browserRequest(g, "portal", method, "/api/org/v1/members", "cookie"); w.Code != want {
				t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
			}
			if scenario == "write-missing" && backend.touches != 0 {
				t.Fatal("missing CSRF write touched session")
			}
			if scenario == "read-rebuild" {
				if w := browserRequest(g, "portal", "POST", "/api/org/v1/members", "cookie"); w.Code != 204 {
					t.Fatalf("write after GET recovery: %d", w.Code)
				}
			}
			if scenario == "async-projection" {
				// Delivery may happen after the first 503. It must not trigger
				// another Touch or retain a five-minute projection-failure cache.
				store.Put("cookie", row)
				if w := browserRequest(g, "portal", "GET", "/api/org/v1/members", "cookie"); w.Code != 204 {
					t.Fatalf("late projection recovery: %d", w.Code)
				}
				if backend.touches != 1 {
					t.Fatal("late projection bypassed Touch interval")
				}
			}
		})
	}
}

func TestBrowserTouchConcurrencyAudienceCapacityAndRevoke(t *testing.T) {
	g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	backend, _ := browserRPCFixture(t, g, store)
	portal := putBrowserSession(g, backend, "portal", "portal")
	admin := putBrowserSession(g, backend, "admin", "admin")
	var group sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := g.touchBrowserSession(context.Background(), "portal", portal)
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.touchBrowserSession(context.Background(), "admin", admin); err != nil {
		t.Fatal(err)
	}
	if backend.touches != 2 {
		t.Fatalf("audiences/concurrency: %d", backend.touches)
	}
	backend.clock.Add(299)
	if _, err := g.touchBrowserSession(context.Background(), "portal", portal); err != nil || backend.touches != 2 {
		t.Fatal("early touch")
	}
	backend.clock.Add(1)
	if _, err := g.touchBrowserSession(context.Background(), "portal", portal); err != nil || backend.touches != 3 {
		t.Fatalf("five-minute boundary: %v %d", err, backend.touches)
	}
	// Fill only still-live keys; saturation cannot evict one to bypass its window.
	for i := len(g.Activity.attempts); i < sessionTouchCapacity; i++ {
		g.Activity.attempts[sessionActivityKey{sid: fmt.Sprint(i)}] = &sessionActivityAttempt{until: backend.now().Add(sessionTouchInterval)}
	}
	other := *portal
	other.SessionID = "other"
	if _, err := g.touchBrowserSession(context.Background(), "other", &other); !errors.Is(err, errIAMUnavailable) {
		t.Fatalf("saturation: %v", err)
	}
	if len(g.Activity.attempts) != sessionTouchCapacity || backend.touches != 3 {
		t.Fatal("saturation evicted key or called RPC")
	}
	store.Revoke(portal.SessionID)
	if _, err := g.touchBrowserSession(context.Background(), "portal", portal); !errors.Is(err, errIAMUnavailable) {
		t.Fatalf("revoked throttled request: %v", err)
	}
}

func TestBrowserPublicLoginIgnoresStaleCookieRPCFailure(t *testing.T) {
	g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("old token on recovery")
		}
		w.WriteHeader(204)
	}))
	backend, file := browserRPCFixture(t, g, store)
	row := putBrowserSession(g, backend, "cookie", "portal")
	row.AccessTokenExpiresAt = backend.now()
	store.Put("cookie", row)
	if err := os.WriteFile(file, []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := browserRequest(g, "portal", "POST", "/api/iam/v1/login/transactions", "cookie"); w.Code != 204 {
		t.Fatalf("public recovery blocked: %d", w.Code)
	}
	if w := browserRequest(g, "portal", "GET", "/api/org/v1/members", "cookie"); w.Code != 503 {
		t.Fatalf("control auth did not close protected request: %d", w.Code)
	}
	if backend.introspects != 0 || backend.touches != 0 {
		t.Fatal("forged service JWT accepted")
	}
}

func TestBrowserInvalidSessionsNeverTouch(t *testing.T) {
	for _, scenario := range []string{"tenant", "role", "absolute", "idle", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("invalid session forwarded"); w.WriteHeader(204) }))
			backend, _ := browserRPCFixture(t, g, store)
			row := putBrowserSession(g, backend, "cookie", "portal")
			switch scenario {
			case "tenant":
				row.TenantID = "different"
			case "role":
				row.Audience = "admin"
			case "absolute":
				row.ExpiresAt = backend.now()
			case "idle":
				row.IdleExpiresAt = backend.now()
			case "revoked":
				row.Revoked = true
			}
			store.Put("cookie", row)
			if w := browserRequest(g, "portal", "GET", "/api/org/v1/members", "cookie"); w.Code != 401 {
				t.Fatalf("got %d", w.Code)
			}
			if backend.touches != 0 || backend.introspects != 0 {
				t.Fatal("invalid projection renewed")
			}
		})
	}
}

func TestBrowserRevokeDuringTouchCancelsOriginalConnection(t *testing.T) {
	g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	backend, _ := browserRPCFixture(t, g, store)
	row := putBrowserSession(g, backend, "cookie", "portal")
	ctx, cancel := g.Conns.Attach(context.Background(), row.SessionID, row.ExpiresAt)
	defer cancel()
	backend.onTouch = func() { store.Revoke(row.SessionID) }
	if w := browserRequest(g, "portal", "GET", "/api/org/v1/members", "cookie"); w.Code != 503 {
		t.Fatalf("racing revoke accepted: %d", w.Code)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("original connection survived revoke")
	}
}

func TestBrowserRejectedCSRFAndStaticNeverTouch(t *testing.T) {
	g, _, store := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("invalid CSRF reached upstream")
		w.WriteHeader(204)
	}))
	backend, _ := browserRPCFixture(t, g, store)
	putBrowserSession(g, backend, "cookie", "portal")
	origin := "http://" + originAuthority(g.Cfg.Access.PortalOrigin())
	r := httptest.NewRequest("POST", origin+"/api/org/v1/members", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-Auriga-CSRF", "wrong")
	r.AddCookie(&http.Cookie{Name: sessionCookieBase("portal"), Value: "cookie"})
	r.AddCookie(&http.Cookie{Name: csrfCookieBase(), Value: "csrf"})
	r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "portal"))
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("CSRF: %d", w.Code)
	}
	_ = browserRequest(g, "portal", "GET", "/", "cookie")
	_ = browserRequest(g, "portal", "GET", "/actuator/health", "cookie")
	if backend.touches != 0 || backend.introspects != 0 {
		t.Fatal("non-activity renewed browser session")
	}
}

func TestBrowserOpenTouchInactiveUsesUnauthenticatedCode(t *testing.T) {
	g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	backend, _ := browserRPCFixture(t, g, store)
	row := putBrowserSession(g, backend, "cookie", "portal")
	const instanceID = "019a0000-0000-7000-8000-000000000001"
	g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: instanceID, TenantID: row.TenantID, OwnerID: row.AccountID, State: "running"})
	backend.inactiveTouch = true
	w := browserRequest(g, "portal", "GET", "/open/"+instanceID, "cookie")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"common.unauthenticated"`) {
		t.Fatalf("status/code: %d %s", w.Code, w.Body.String())
	}
}

func TestBrowserPublicLinkAndStepupKeepRenewedPrincipal(t *testing.T) {
	for _, purpose := range []string{"link", "stepup"} {
		t.Run(purpose, func(t *testing.T) {
			g, _, store := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// IAM still owns purpose-specific principal/CSRF enforcement.
				if r.Header.Get("Authorization") == "" {
					w.WriteHeader(401)
					return
				}
				if r.Header.Get("Authorization") != "Bearer renewed-1" || !strings.Contains(r.Header.Get("Cookie"), "auriga_session=cookie") || r.Header.Get("X-Auriga-CSRF") != "csrf" {
					t.Error("lost authenticated login purpose binding")
				}
				w.WriteHeader(204)
			}))
			backend, file := browserRPCFixture(t, g, store)
			putBrowserSession(g, backend, "cookie", "portal")
			backend.clock.Add(360)
			request := func() *httptest.ResponseRecorder {
				origin := "http://" + originAuthority(g.Cfg.Access.PortalOrigin())
				r := httptest.NewRequest("POST", origin+"/api/iam/v1/login/transactions", strings.NewReader(`{"purpose":"`+purpose+`"}`))
				r.Header.Set("Origin", origin)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Auriga-CSRF", "csrf")
				r.AddCookie(&http.Cookie{Name: "auriga_session", Value: "cookie"})
				r.AddCookie(&http.Cookie{Name: "auriga_csrf", Value: "csrf"})
				r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "portal"))
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, r)
				return w
			}
			if w := request(); w.Code != 204 {
				t.Fatalf("authenticated %s: %d", purpose, w.Code)
			}
			backend.clock.Add(360)
			if err := os.WriteFile(file, []byte("forged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if w := request(); w.Code != 401 {
				t.Fatalf("failed %s gained principal: %d", purpose, w.Code)
			}
			if backend.touches != 0 {
				t.Fatal("public login purpose touched idle deadline")
			}
		})
	}
}
