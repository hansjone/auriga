package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func accessFixture(t *testing.T, domain bool, upstream http.Handler) (*Gateway, map[string]string, *MemorySessionStore) {
	return accessFixtureConfigured(t, domain, upstream, nil)
}

func accessFixtureConfigured(t *testing.T, domain bool, upstream http.Handler, configure func(*Config, map[string]any)) (*Gateway, map[string]string, *MemorySessionStore) {
	t.Helper()
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	cfg.Upstreams = map[string]string{"iam": server.URL, "org": server.URL, "workspace": server.URL}
	cfg.IAMHop.AllowLoopbackHTTP = true
	sockets := map[string]net.Listener{}
	addresses := map[string]string{}
	for _, role := range []string{"portal", "admin", "instanceControl"} {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		sockets[role] = ln
		addresses[role] = ln.Addr().String()
		t.Cleanup(func() { _ = ln.Close() })
	}
	fixture := "../../contracts/deployment/fixtures/ip-http.json"
	if domain {
		fixture = "../../contracts/deployment/fixtures/domain-proxy.json"
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	ports := map[string]int{}
	for role, ln := range sockets {
		ports[role] = ln.Addr().(*net.TCPAddr).Port
	}
	portalHost, adminHost := "127.0.0.1", "127.0.0.1"
	if domain {
		portalHost, adminHost = "portal.example.com", "admin.example.com"
	}
	document["portalOrigin"] = map[string]any{"scheme": "http", "host": portalHost, "port": ports["portal"]}
	document["issuerOrigin"] = document["portalOrigin"]
	document["adminOrigin"] = map[string]any{"scheme": "http", "host": adminHost, "port": ports["admin"]}
	document["insecureHttpAcknowledged"] = true
	if domain {
		document["instances"] = map[string]any{"mode": "subdomain", "parentDomain": "i.example.com", "scheme": "http", "port": ports["portal"]}
	}
	document["ingress"] = map[string]any{"kind": "direct"}
	listeners := document["listeners"].(map[string]any)
	for role := range sockets {
		listeners[role] = map[string]any{"address": "127.0.0.1", "port": ports[role], "tls": false}
	}
	control := document["instanceControl"].(map[string]any)
	control["origin"] = map[string]any{"scheme": "http", "host": control["dnsAlias"], "port": ports["instanceControl"]}
	if configure != nil {
		configure(&cfg, document)
	}
	if cfg.LiteLLMOrigin != "" {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		sockets["relayAdmin"], addresses["relayAdmin"] = ln, ln.Addr().String()
		t.Cleanup(func() { _ = ln.Close() })
		host := "127.0.0.1"
		if domain {
			host = "litellm.example.com"
		}
		port := int32(ln.Addr().(*net.TCPAddr).Port)
		cfg.LiteLLMUI = &LiteLLMUIConfig{Origin: deployment.Origin{Scheme: "http", Host: host, Port: port}, Listener: deployment.Listener{Address: "127.0.0.1", Port: port}}
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PortalHost, cfg.AdminHost = originAuthority(cfg.Access.PortalOrigin()), originAuthority(cfg.Access.AdminOrigin())
	cfg.SecureCookies = cfg.Access.PortalOrigin().Scheme == "https"
	cfg.TrustedProxies = nil
	cfg.InstanceParentDomain = cfg.Access.InstanceParentDomain()
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	s := &Server{Cfg: cfg, Handler: g.Handler(), Listen: func(_, addr string) (net.Listener, error) {
		for role, actual := range addresses {
			if actual == addr {
				return sockets[role], nil
			}
		}
		return nil, fmt.Errorf("unexpected listener")
	}}
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("access server did not stop")
		}
	})
	return g, addresses, sessions
}

func entryRequest(t *testing.T, address, method, path, authority string, headers http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+address+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = authority
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func requireEntryProblem(t *testing.T, address, method, path, authority string, headers http.Header, want int, code string) {
	t.Helper()
	status, body := entryRequest(t, address, method, path, authority, headers)
	if status != want || !strings.Contains(body, `"code":"`+code+`"`) {
		t.Fatalf("got %d %s, want %d %s", status, body, want, code)
	}
}

func TestAccessRealIPv4RolesAuthorityAndOrigin(t *testing.T) {
	g, addresses, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	portal, admin := g.Cfg.PortalHost, g.Cfg.AdminHost
	for role, host := range map[string]string{"portal": portal, "admin": admin} {
		status, _ := entryRequest(t, addresses[role], "GET", "/", host, nil)
		if status != 200 {
			t.Fatalf("%s: %d", role, status)
		}
	}
	requireEntryProblem(t, addresses["portal"], "GET", "/", admin, nil, 403, "common.forbidden")
	requireEntryProblem(t, addresses["admin"], "GET", "/", portal, nil, 403, "common.forbidden")
	for _, host := range []string{"127.0.0.01:8080", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1.:80", "user@127.0.0.1", "127.0.0.1%3a80", "127.0.0.1:0x50"} {
		requireEntryProblem(t, addresses["portal"], "GET", "/", host, nil, 400, "common.validation_failed")
	}
	for _, origins := range [][]string{nil, {"null"}, {g.Cfg.Access.PortalURL()}, {g.Cfg.Access.AdminURL() + "/"}, {strings.Replace(g.Cfg.Access.AdminURL(), "http:", "https:", 1)}, {g.Cfg.Access.AdminURL(), g.Cfg.Access.AdminURL()}} {
		headers := http.Header{"Cookie": {"auriga_admin_session=admin"}, "Authorization": {"Bearer arbitrary"}, "Sec-Fetch-Site": {"same-site"}}
		for _, origin := range origins {
			headers.Add("Origin", origin)
		}
		requireEntryProblem(t, addresses["admin"], "POST", "/api/org/v1/admin/members", admin, headers, 403, "common.forbidden")
	}
	headers := http.Header{"Forwarded": {"host=forged;proto=https"}, "X-Forwarded-Proto": {"https"}, "X-Auriga-Entry-Role": {"admin"}, "X-Auriga-Tenant": {"forged"}}
	requireEntryProblem(t, addresses["portal"], "GET", "/", admin, headers, 403, "common.forbidden")
	status, _ := entryRequest(t, addresses["portal"], "GET", "/", portal, headers)
	if status != 200 {
		t.Fatalf("forged headers changed direct scheme: %d", status)
	}
	requireEntryProblem(t, addresses["admin"], "GET", "/api/iam/v1/me", admin, http.Header{"Upgrade": {"websocket"}, "Origin": {g.Cfg.Access.AdminURL()}}, 403, "common.forbidden")
}

func TestAccessControlListenerAndTrustedHopCookies(t *testing.T) {
	seen := make(chan *http.Request, 10)
	g, addresses, sessions := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		w.WriteHeader(204)
	}))
	admin := putPortalSession(sessions, "admin", "oldcsrf", "admin-jwt")
	admin.Audience, admin.TenantID = "admin", g.Cfg.Access.TenantID()
	sessions.Put("admin", admin)
	authority := originAuthority(g.Cfg.Access.InstanceControl().Origin)
	requireEntryProblem(t, addresses["instanceControl"], "GET", "/", authority, nil, 404, "common.not_found")
	requireEntryProblem(t, addresses["instanceControl"], "GET", "/api/workspace/v1/instance/self", authority, http.Header{"Authorization": {"Bearer user-jwt"}}, 401, "common.unauthenticated")
	status, _ := entryRequest(t, addresses["instanceControl"], "GET", "/api/workspace/v1/instance/self", authority, http.Header{"Authorization": {"Bearer ait_actual"}, "Cookie": {"auriga_admin_session=secret"}, "X-Auriga-Tenant": {"forged"}})
	if status != 204 {
		t.Fatalf("self: %d", status)
	}
	request := <-seen
	if request.Header.Get("Cookie") != "" || request.Header.Get("X-Auriga-Tenant") != "" {
		t.Fatal("control credential leak")
	}
	headers := http.Header{"Cookie": {"auriga_admin_session=admin; auriga_session=portal; auriga_csrf=oldcsrf; auriga_admin_login_tx=tx"}, "Origin": {g.Cfg.Access.AdminURL()}, "Content-Type": {"application/json"}, "X-Auriga-CSRF-Bootstrap": {"admin-session-v1"}, "X-Auriga-Entry-Origin": {"http://forged"}, "Forwarded": {"proto=https"}}
	status, body := entryRequest(t, addresses["admin"], "POST", adminCSRFBootstrapPath, g.Cfg.AdminHost, headers)
	if status != 204 {
		t.Fatalf("bootstrap %d %s", status, body)
	}
	request = <-seen
	if request.Header.Get("Cookie") != "auriga_admin_session=admin" || request.Header.Get("X-Auriga-Entry-Origin") != g.Cfg.Access.AdminURL() || request.Header.Get("X-Auriga-Entry-Role") != "admin" || request.Header.Get("Forwarded") != "" || request.Host != g.Cfg.AdminHost || request.Header.Get("Authorization") != "Bearer admin-jwt" {
		t.Fatalf("bad authenticated hop: %v host=%s", request.Header, request.Host)
	}
	headers.Del("X-Auriga-CSRF-Bootstrap")
	delete(headers, "X-Auriga-CSRF-Bootstrap")
	requireEntryProblem(t, addresses["admin"], "POST", adminCSRFBootstrapPath, g.Cfg.AdminHost, headers, 403, "common.forbidden")
	headers.Set("X-Auriga-CSRF", "oldcsrf")
	requireEntryProblem(t, addresses["admin"], "POST", "/api/org/v1/admin/members", g.Cfg.AdminHost, headers, 503, "common.unavailable")
	select {
	case <-seen:
		t.Fatal("old CSRF fallback reached upstream")
	default:
	}
}

func TestAccessDomainSameOriginLoginAndOAuthRegression(t *testing.T) {
	seen := make(chan *http.Request, 10)
	g, addresses, sessions := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		w.WriteHeader(204)
	}))
	s := putPortalSession(sessions, "portal", "csrf", "jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	headers := http.Header{"Origin": {g.Cfg.Access.PortalURL()}, "Content-Type": {"application/json"}, "Cookie": {"auriga_session=portal; auriga_admin_session=admin; auriga_portal_login_tx=tx; auriga_admin_login_tx=admin-tx; auriga_csrf=csrf"}}
	status, body := entryRequest(t, addresses["portal"], "POST", "/api/iam/v1/login/transactions", strings.ToUpper(g.Cfg.PortalHost), headers)
	if status != 204 {
		t.Fatalf("login %d %s", status, body)
	}
	r := <-seen
	if r.Header.Get("Cookie") != "auriga_portal_login_tx=tx; auriga_session=portal; auriga_csrf=csrf" || r.Header.Get("Authorization") != "Bearer jwt" {
		t.Fatalf("login selected cookies: %v", r.Header)
	}
	for _, method := range []string{"GET", "POST"} {
		status, body = entryRequest(t, addresses["portal"], method, "/oauth2/authorize", g.Cfg.PortalHost, headers)
		if status != 204 {
			t.Fatalf("OAuth %s %d %s", method, status, body)
		}
		r = <-seen
		if r.Header.Get("Cookie") != "auriga_session=portal; auriga_csrf=csrf" {
			t.Fatalf("OAuth cookie: %s", r.Header.Get("Cookie"))
		}
	}
	headers.Del("Origin")
	status, body = entryRequest(t, addresses["portal"], "POST", "/api/iam/v1/login/callback/019a0000-0000-7000-8000-000000000001", g.Cfg.PortalHost, headers)
	if status != 204 {
		t.Fatalf("form callback %d %s", status, body)
	}
	r = <-seen
	if r.Header.Get("Cookie") != "auriga_portal_login_tx=tx" {
		t.Fatal("callback leaked session")
	}
	requireEntryProblem(t, addresses["portal"], "POST", "/api/iam/v1/login/unknown", g.Cfg.PortalHost, nil, 403, "common.forbidden")
}

func TestAuthorityDefaultPortAndStrictDNS(t *testing.T) {
	for _, value := range []string{"Portal.Example.com", "portal.example.com:80"} {
		h, p, err := parseAuthority(value, "http")
		if err != nil || h != "portal.example.com" || p != 80 {
			t.Fatal(h, p, err)
		}
	}
	for _, value := range []string{"portal.example.com.", "1.2.3", "2130706433", "0177.0.0.1", "portal.example.com:+80", "portal.example.com: 80", "portal..example.com", "https://portal.example.com"} {
		if _, _, err := parseAuthority(value, "http"); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestAccessDuplicateHostRejectedByActualHTTPParser(t *testing.T) {
	g, addresses, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("duplicate Host reached upstream") }))
	conn, err := net.DialTimeout("tcp", addresses["portal"], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nHost: %s\r\nConnection: close\r\n\r\n", g.Cfg.PortalHost, g.Cfg.AdminHost)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 400 {
		t.Fatalf("duplicate Host: %d", response.StatusCode)
	}
}

func TestAccessSplitOriginWithoutAuthenticatedTransportFailsClosed(t *testing.T) {
	g, addresses, sessions := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unconfigured split request reached IAM") }), func(cfg *Config, doc map[string]any) { cfg.Upstreams["org"] = "https://org.other.example.com:9443" })
	s := putPortalSession(sessions, "portal", "csrf", "jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	requireEntryProblem(t, addresses["portal"], "GET", "/api/org/v1/me", g.Cfg.PortalHost, http.Header{"Cookie": {"auriga_session=portal"}}, 503, "common.unavailable")
}
