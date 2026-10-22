package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

const pathInstanceA = "019a0000-0000-7000-8000-000000000001"
const pathInstanceB = "019a0000-0000-7000-8000-000000000002"

type ipBrowser struct {
	g        *Gateway
	sessions *MemorySessionStore
	client   *http.Client
	root     string
}

func newIPBrowser(t *testing.T, secure bool, upstream http.Handler) *ipBrowser {
	t.Helper()
	var nodeURL string
	var clientTLS *tls.Config
	var configureTLS func(*Config, map[string]any)
	if secure {
		pki := createAccessPKI(t)
		clientTLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "iam.internal.test", RootCAs: pki.roots}
		configureTLS = func(cfg *Config, doc map[string]any) {
			cfg.TLS = writeAccessServerTLS(t, pki)
			for _, key := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
				doc[key].(map[string]any)["scheme"] = "https"
			}
			for _, role := range []string{"portal", "admin"} {
				doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
			}
		}
	}
	g, _, sessions := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(cfg *Config, doc map[string]any) {
		nodeURL = testNode(t, cfg, upstream, testNodeID).URL
		if configureTLS != nil {
			configureTLS(cfg, doc)
		}
	})
	t.Cleanup(g.Ingress.Close)
	s := putPortalSession(sessions, "portal", "csrf", "jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	for _, id := range []string{pathInstanceA, pathInstanceB} {
		g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: id, TenantID: s.TenantID, OwnerID: s.AccountID, NodeID: testNodeID, Ingress: nodeURL, State: "running", UpdatedAt: time.Now()})
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := url.Parse(g.Cfg.Access.PortalURL())
	jar.SetCookies(root, []*http.Cookie{{Name: cookieName(secure, "auriga_session"), Value: "portal", Path: "/", Secure: secure}})
	client := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil, TLSClientConfig: clientTLS}, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	return &ipBrowser{g: g, sessions: sessions, client: client, root: root.String()}
}

func (b *ipBrowser) get(t *testing.T, p string) *http.Response {
	t.Helper()
	resp, err := b.client.Get(b.root + p)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readIPResponse(t *testing.T, resp *http.Response, want int, code string) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want || code != "" && !strings.Contains(string(body), `"code":"`+code+`"`) {
		t.Fatalf("status=%d body=%s want=%d code=%s", resp.StatusCode, body, want, code)
	}
	return string(body)
}

func (b *ipBrowser) open(t *testing.T, id string) (*http.Response, string) {
	t.Helper()
	resp := b.get(t, "/open/"+id)
	readIPResponse(t, resp, 302, "")
	location := resp.Header.Get("Location")
	resp, err := b.client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 302, "")
	if resp.Header.Get("Location") != "/i/"+id+"/" {
		t.Fatalf("redemption escaped mount: %s", resp.Header.Get("Location"))
	}
	return resp, location
}

func TestIPTicketBrowserMountAndCookies(t *testing.T) {
	g, _, sessions := accessFixture(t, false, http.NotFoundHandler())
	s := putPortalSession(sessions, "portal", "csrf", "jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := url.Parse(g.Cfg.Access.PortalURL())
	jar.SetCookies(root, []*http.Cookie{{Name: "auriga_session", Value: "portal", Path: "/"}})
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	for _, id := range []string{pathInstanceA, pathInstanceB} {
		g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: id, TenantID: s.TenantID, OwnerID: s.AccountID, State: "running", UpdatedAt: time.Now()})
		resp, err := client.Get(root.String() + "/open/" + id)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 302 {
			t.Fatalf("open: %d", resp.StatusCode)
		}
		location := resp.Header.Get("Location")
		resp, err = client.Get(location)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		mount := "/i/" + id + "/"
		if resp.StatusCode != 302 || resp.Header.Get("Location") != mount {
			t.Errorf("redeem target: status=%d location=%q want %q", resp.StatusCode, resp.Header.Get("Location"), mount)
		}
		for _, c := range resp.Cookies() {
			if c.Name == "auriga_inst" && (c.Path != mount || c.Domain != "" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode) {
				t.Errorf("instance cookie attributes: %+v", c)
			}
		}
	}
	a, _ := url.Parse(root.String() + "/i/" + pathInstanceA + "/")
	b, _ := url.Parse(root.String() + "/i/" + pathInstanceB + "/")
	find := func(u *url.URL) string {
		for _, c := range jar.Cookies(u) {
			if c.Name == "auriga_inst" {
				return c.Value
			}
		}
		return ""
	}
	if find(a) == "" || find(b) == "" || find(a) == find(b) {
		t.Error("browser did not keep independent A/B instance cookies")
	}
	if find(root) != "" {
		t.Error("instance cookie sent to platform root")
	}
}

func TestIPBrowserHTTPProxyAndResponseBoundaries(t *testing.T) {
	type observed struct {
		URI, Identity, Cookie, Body string
		Headers                     http.Header
	}
	seen := make(chan observed, 32)
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- observed{r.URL.RequestURI(), r.Header.Get("Auriga-Identity"), r.Header.Get("Cookie"), string(body), r.Header.Clone()}
		w.Header().Add("Set-Cookie", "theme=dark; Path=/; HttpOnly; SameSite=Strict; Max-Age=123")
		w.Header().Add("Set-Cookie", "local=yes; SameSite=Lax")
		w.Header().Add("Set-Cookie", "sub=ok; Path=/prefs/")
		w.Header().Add("Set-Cookie", "__Secure-AuRiGa_admin_session=evil; Path=/; Secure")
		w.Header().Add("Set-Cookie", "AuRiGa_unlisted=evil; Path=/")
		w.Header().Set("Clear-Site-Data", `"cookies", "storage"`)
		w.Header().Set("Service-Worker-Allowed", "/")
		if r.URL.Query().Get("redirect") == "relative" {
			w.Header().Set("Location", "../assets/main%20file.js?x=1%2F2#part")
			w.WriteHeader(302)
			return
		}
		_, _ = w.Write([]byte("instance"))
	}))
	a, _ := b.open(t, pathInstanceA)
	bc, _ := b.open(t, pathInstanceB)
	if a.Cookies()[len(a.Cookies())-1].Value == bc.Cookies()[len(bc.Cookies())-1].Value {
		t.Fatal("A/B binding shared")
	}
	for _, id := range []string{pathInstanceA, pathInstanceB} {
		mount := "/i/" + id + "/"
		resp := b.get(t, strings.TrimSuffix(mount, "/")+"?x=1")
		readIPResponse(t, resp, 308, "")
		if resp.Header.Get("Location") != mount+"?x=1" {
			t.Fatal("root slash normalization lost query")
		}
		for _, p := range []string{"", "assets/main%20file.js", "prefs/page", "prefs%20space/page"} {
			resp = b.get(t, mount+p)
			if readIPResponse(t, resp, 200, "") != "instance" {
				t.Fatal("instance request fell into platform SPA")
			}
			if resp.Header.Get("Clear-Site-Data") != "" || resp.Header.Get("Service-Worker-Allowed") != mount {
				t.Fatal("response escaped browser scope")
			}
			for _, c := range resp.Cookies() {
				if strings.Contains(strings.ToLower(c.Name), "auriga_") || c.Domain != "" || !strings.HasPrefix(c.Path, mount) {
					t.Fatalf("unsafe response cookie: %v", c)
				}
				if c.Name == "local" && p == "prefs/page" && c.Path != strings.TrimSuffix(mount, "/")+"/prefs" {
					t.Fatalf("default cookie path: %v", c)
				}
				if c.Name == "local" && p == "prefs%20space/page" && c.Path != mount+"prefs%20space" {
					t.Fatalf("encoded default cookie path changed: %v", c)
				}
				if c.Name == "theme" && (!c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 123) {
					t.Fatalf("cookie attributes lost: %v", c)
				}
			}
			got := <-seen
			if got.URI != "/i/"+id+"/"+p {
				t.Fatalf("node prefix/escaping changed: %s", got.URI)
			}
			claims, _, err := VerifyIdentityToken(got.Identity, b.g.Signer.PublicKey())
			if err != nil || claims.Htu != "/"+p || claims.Aud != id {
				t.Fatalf("identity raw path: %+v %v", claims, err)
			}
		}
		resp = b.get(t, mount+"prefs/page?redirect=relative")
		readIPResponse(t, resp, 302, "")
		<-seen
		if resp.Header.Get("Location") != mount+"assets/main%20file.js?x=1%2F2#part" {
			t.Fatalf("relative redirect: %s", resp.Header.Get("Location"))
		}
	}
	root, _ := url.Parse(b.root)
	b.client.Jar.SetCookies(root, []*http.Cookie{{Name: "auriga_admin_session", Value: "admin-secret", Path: "/"}, {Name: "auriga_csrf", Value: "csrf-secret", Path: "/"}, {Name: "auriga_portal_login_tx", Value: "portal-tx", Path: "/"}, {Name: "__Secure-AuRiGa_future", Value: "future-secret", Path: "/"}})
	req, _ := http.NewRequest("POST", b.root+"/i/"+pathInstanceA+"/upload%20file?x=a%2Fb&x=c", bytes.NewBufferString("upload-body\x00bytes"))
	req.Header = http.Header{"Origin": {b.root}, "Authorization": {"Bearer forged"}, "X-Auriga-CSRF": {"csrf-secret"}, "X-Auriga-CSRF-Bootstrap": {"1"}, "Auriga-Identity": {"forged"}, "Forwarded": {"host=admin;proto=https"}, "X-Forwarded-Host": {"internal-alias"}, "X-Real-Ip": {"192.0.2.4"}, "X-Auriga-Entry-Role": {"admin"}, "Auriga-Tenant-Id": {"forged"}}
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 200, "")
	got := <-seen
	if got.URI != "/i/"+pathInstanceA+"/upload%20file?x=a%2Fb&x=c" || got.Body != "upload-body\x00bytes" || strings.Contains(strings.ToLower(got.Cookie), "auriga_") {
		t.Fatalf("upload semantics/credentials: %+v", got)
	}
	for _, h := range []string{"Authorization", "X-Auriga-CSRF", "X-Auriga-CSRF-Bootstrap", "Forwarded", "X-Forwarded-Host", "X-Real-IP", "X-Auriga-Entry-Role", "Auriga-Tenant-Id"} {
		if got.Headers.Get(h) != "" {
			t.Fatalf("forged header leaked: %s", h)
		}
	}
	claims, _, err := VerifyIdentityToken(got.Identity, b.g.Signer.PublicKey())
	if err != nil || claims.Htu != "/upload%20file" || claims.Htm != "POST" {
		t.Fatalf("signed upload path: %+v %v", claims, err)
	}
}

func TestIPBrowserRejectsUnsafeUpstreamResponses(t *testing.T) {
	tests := []struct{ name, header, value string }{
		{"domain", "Set-Cookie", "app=x; Domain=127.0.0.1; Path=/"},
		{"empty-domain", "Set-Cookie", "app=x; Domain=; Path=/"},
		{"double-path", "Set-Cookie", "app=x; Path=/; Path=/else"},
		{"relative-cookie", "Set-Cookie", "app=x; Path=else"},
		{"traversal-cookie", "Set-Cookie", "app=x; Path=/../"},
		{"encoded-cookie", "Set-Cookie", "app=x; Path=/%2fapi"},
		{"host-cookie", "Set-Cookie", "__Host-app=x; Path=/; Secure"},
		{"secure-over-http", "Set-Cookie", "app=x; Path=/; Secure"},
		{"bad-secure-prefix", "Set-Cookie", "__Secure-app=x; Path=/"},
		{"other-mount-cookie", "Set-Cookie", "app=x; Path=/i/" + pathInstanceB + "/"},
		{"external", "Location", "https://evil.example.com/"},
		{"network-relative", "Location", "//evil.example.com/"},
		{"alias", "Location", "http://instance-control.internal.test/"},
		{"other-instance", "Location", "/i/" + pathInstanceB + "/"},
		{"encoded-redirect", "Location", "/%252fapi"},
		{"separator-redirect", "Location", "/%2fapi"},
		{"backslash-redirect", "Location", `/\evil`},
		{"encoded-dot-redirect", "Location", "/dir/%2e%2e/admin"},
		{"root-escape", "Location", "../../admin"},
		{"other-sw", "Service-Worker-Allowed", "/i/" + pathInstanceB + "/"},
		{"external-sw", "Service-Worker-Allowed", "https://evil.example.com/"},
		{"query-sw", "Service-Worker-Allowed", "/?q=x"},
	}
	byName := map[string]struct{ header, value string }{}
	for _, tt := range tests {
		byName[tt.name] = struct{ header, value string }{tt.header, tt.value}
	}
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tt := byName[r.URL.Query().Get("case")]
		w.Header().Set(tt.header, tt.value)
		w.WriteHeader(302)
	}))
	b.open(t, pathInstanceA)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := b.get(t, "/i/"+pathInstanceA+"/page?case="+tt.name)
			readIPResponse(t, resp, 502, "edge.upstream_error")
			if len(resp.Cookies()) != 0 || resp.Header.Get("Location") != "" || resp.Header.Get("Service-Worker-Allowed") != "" {
				t.Fatal("rejected response leaked browser state")
			}
		})
	}
}

func TestIPResponseRedirectsStayInOwnMount(t *testing.T) {
	var root string
	values := map[string]string{
		"root": "/", "app-api": "/api/iam/v1/me?q=1%2F2#here", "relative": "login?q=one#two",
		"parent": "../assets/x.js", "own-mount": "/i/" + pathInstanceA + "/settings?x=1#tab",
		"encoded": "/asset%20dir/main.js", "fragment": "#part", "query": "?new=1",
	}
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := values[r.URL.Query().Get("case")]
		if r.URL.Query().Get("case") == "absolute" {
			value = root + "/settings?x=1#tab"
		}
		if r.URL.Query().Get("case") == "wrong-port" {
			value = "http://127.0.0.1:1/settings"
		}
		w.Header().Set("Location", value)
		w.WriteHeader(302)
	}))
	root = b.root
	b.open(t, pathInstanceA)
	mount := "/i/" + pathInstanceA + "/"
	expected := map[string]string{
		"root": mount, "app-api": mount + "api/iam/v1/me?q=1%2F2#here", "relative": mount + "prefs/login?q=one#two",
		"parent": mount + "assets/x.js", "own-mount": mount + "settings?x=1#tab", "absolute": mount + "settings?x=1#tab",
		"encoded": mount + "asset%20dir/main.js", "fragment": mount + "prefs/page?case=fragment#part", "query": mount + "prefs/page?new=1",
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			resp := b.get(t, mount+"prefs/page?case="+name)
			readIPResponse(t, resp, 302, "")
			if resp.Header.Get("Location") != want {
				t.Fatalf("Location=%q want=%q", resp.Header.Get("Location"), want)
			}
		})
	}
	readIPResponse(t, b.get(t, mount+"prefs/page?case=wrong-port"), 502, "edge.upstream_error")
}

func TestIPHTTPSCookieSemantics(t *testing.T) {
	b := newIPBrowser(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "__Secure-app=x; Path=/; Secure; HttpOnly; SameSite=None")
		w.WriteHeader(204)
	}))
	for _, id := range []string{pathInstanceA, pathInstanceB} {
		resp, _ := b.open(t, id)
		found := false
		for _, c := range resp.Cookies() {
			if c.Name == "__Secure-auriga_inst" {
				found = true
				if c.Path != "/i/"+id+"/" || !c.Secure || !c.HttpOnly || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
					t.Fatalf("HTTPS binding attributes: %v", c)
				}
			}
		}
		if !found {
			t.Fatal("missing secure path instance cookie")
		}
		resp = b.get(t, "/i/"+id+"/")
		readIPResponse(t, resp, 204, "")
		if len(resp.Cookies()) != 1 || !resp.Cookies()[0].Secure || resp.Cookies()[0].Path != "/i/"+id+"/" || resp.Cookies()[0].SameSite != http.SameSiteNoneMode {
			t.Fatal("secure application cookie semantics changed")
		}
	}
}

func TestIPBrowserRejectsPathsCookiesAndTicketAbuse(t *testing.T) {
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	b.open(t, pathInstanceA)
	for _, p := range []string{"/i/" + pathInstanceA + "/%2fapi", "/i/" + pathInstanceA + "/%5capi", "/i/" + pathInstanceA + "/%252fapi", "/i/" + pathInstanceA + "/%252e%252e/api", "/i/" + pathInstanceA + "/./api", "/i/" + pathInstanceA + "/../api/iam/v1/me", "/i/" + pathInstanceA + "/a//b", "/i/" + pathInstanceA + "/a/..", "/i/" + pathInstanceA + "/%00", "/i/%30" + pathInstanceA[1:] + "/"} {
		t.Run(p, func(t *testing.T) { readIPResponse(t, b.get(t, p), 400, "edge.bad_path") })
	}
	for _, id := range []string{"no-id", strings.ToUpper(pathInstanceA), "019a0000-0000-4000-8000-000000000001", "019a0000-0000-7000-c000-000000000001"} {
		readIPResponse(t, b.get(t, "/i/"+id+"/"), 404, "edge.not_found")
	}
	root, _ := url.Parse(b.root + "/i/" + pathInstanceA + "/")
	var binding string
	for _, c := range b.client.Jar.Cookies(root) {
		if c.Name == "auriga_inst" {
			binding = c.Value
		}
	}
	client := *b.client
	client.Jar = nil
	for _, value := range []string{"auriga_inst=" + binding + "; auriga_inst=evil", "auriga_inst=" + binding + "; auriga_inst=" + binding} {
		req, _ := http.NewRequest("GET", root.String(), nil)
		req.Header.Set("Cookie", value)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		readIPResponse(t, resp, 401, "edge.unauthorized")
	}
	req, _ := http.NewRequest("GET", b.root+"/i/"+pathInstanceB+"/", nil)
	req.Header.Set("Cookie", "auriga_inst="+binding)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 401, "edge.unauthorized")
	for _, tt := range []struct {
		name   string
		status int
		code   string
	}{
		{"missing-nonce", 403, "edge.bad_ticket_nav"}, {"wrong-nonce", 403, "edge.bad_ticket_nav"}, {"duplicate-nonce", 403, "edge.bad_ticket_nav"}, {"cross-instance", 403, "edge.bad_ticket"}, {"expired", 401, "edge.bad_ticket"}, {"revoked", 401, "edge.bad_ticket"}, {"foreign-tenant", 401, "edge.bad_ticket"}, {"replaced-sid", 401, "edge.bad_ticket"}, {"return-query", 400, "edge.bad_ticket"}, {"replay", 401, "edge.bad_ticket"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			browser := newIPBrowser(t, false, http.NotFoundHandler())
			opened := browser.get(t, "/open/"+pathInstanceA)
			readIPResponse(t, opened, 302, "")
			location := opened.Header.Get("Location")
			nonce := opened.Cookies()[0]
			rawClient := *browser.client
			rawClient.Jar = nil
			if tt.name == "cross-instance" {
				location = strings.ReplaceAll(location, pathInstanceA, pathInstanceB)
				nonce.Name = strings.ReplaceAll(nonce.Name, pathInstanceA, pathInstanceB)
			}
			if tt.name == "expired" {
				store := browser.g.Tickets.(*MemoryTicketStore)
				store.now = func() time.Time { return time.Now().Add(61 * time.Second) }
			}
			if tt.name == "revoked" {
				browser.sessions.Revoke("11111111-1111-1111-1111-111111111111")
			}
			if tt.name == "foreign-tenant" || tt.name == "replaced-sid" {
				s, _ := browser.sessions.Get(t.Context(), HashCookie("portal"))
				if tt.name == "foreign-tenant" {
					s.TenantID = "other-tenant"
				} else {
					s.SessionID = "other-session"
				}
				browser.sessions.Put("portal", s)
			}
			if tt.name == "return-query" {
				location += "&return=/api/iam/v1/me"
			}
			request := func() *http.Response {
				req, _ := http.NewRequest("GET", location, nil)
				if tt.name != "missing-nonce" {
					c := *nonce
					if tt.name == "wrong-nonce" {
						c.Value = "wrong"
					}
					req.AddCookie(&c)
					if tt.name == "duplicate-nonce" {
						req.AddCookie(&c)
					}
				}
				resp, err := rawClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			if tt.name == "replay" {
				readIPResponse(t, request(), 302, "")
			}
			resp := request()
			readIPResponse(t, resp, tt.status, tt.code)
			for _, c := range resp.Cookies() {
				if c.Name == "auriga_inst" {
					t.Fatal("failed ticket minted instance binding")
				}
			}
		})
	}
}

func TestIPOutstandingNoncesAndReopenKeepOtherInstance(t *testing.T) {
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	b.open(t, pathInstanceA)
	b.open(t, pathInstanceB)
	bu, _ := url.Parse(b.root + "/i/" + pathInstanceB + "/")
	var oldB string
	for _, c := range b.client.Jar.Cookies(bu) {
		if c.Name == "auriga_inst" {
			oldB = c.Value
		}
	}
	locations := map[string]string{}
	for _, id := range []string{pathInstanceA, pathInstanceB} {
		resp := b.get(t, "/open/"+id)
		readIPResponse(t, resp, 302, "")
		locations[id] = resp.Header.Get("Location")
	}
	resp, err := b.client.Get(locations[pathInstanceA])
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 302, "")
	var gotB, nonceB string
	for _, c := range b.client.Jar.Cookies(bu) {
		if c.Name == "auriga_inst" {
			gotB = c.Value
		}
		if strings.HasPrefix(c.Name, "auriga_open_nonce_") {
			nonceB = c.Value
		}
	}
	if oldB == "" || gotB != oldB || nonceB == "" {
		t.Fatal("redeeming A changed B binding or nonce")
	}
	readIPResponse(t, b.get(t, bu.Path), 204, "")
	resp, err = b.client.Get(locations[pathInstanceB])
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 302, "")
	readIPResponse(t, b.get(t, bu.Path), 204, "")
}

func TestIPTicketAndBindingRejectChangedInstallation(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	issued := b.get(t, "/open/"+pathInstanceA)
	readIPResponse(t, issued, 302, "")
	u, _ := url.Parse(issued.Header.Get("Location"))
	nonce := issued.Cookies()[0]
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "port", true: "scheme"}[secure], func(t *testing.T) {
			issued = b.get(t, "/open/"+pathInstanceA)
			readIPResponse(t, issued, 302, "")
			u, _ = url.Parse(issued.Header.Get("Location"))
			nonce = issued.Cookies()[0]
			other := newIPBrowser(t, secure, http.NotFoundHandler())
			other.g.Tickets = b.g.Tickets
			other.g.Sessions = b.sessions
			rawClient := *other.client
			rawClient.Jar = nil
			req, _ := http.NewRequest("GET", other.root+u.RequestURI(), nil)
			c := *nonce
			if secure {
				c.Name = "__Secure-" + c.Name
			}
			req.AddCookie(&c)
			resp, err := rawClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			readIPResponse(t, resp, 403, "edge.bad_ticket")
		})
	}
	// An already issued binding also remains tied to its original authority.
	b.open(t, pathInstanceA)
	other := newIPBrowser(t, false, http.NotFoundHandler())
	other.g.Tickets = b.g.Tickets
	other.g.Sessions = b.sessions
	original, _ := url.Parse(b.root + "/i/" + pathInstanceA + "/")
	target, _ := url.Parse(other.root + "/i/" + pathInstanceA + "/")
	other.client.Jar.SetCookies(target, b.client.Jar.Cookies(original))
	readIPResponse(t, other.get(t, target.Path), 401, "edge.unauthorized")
}

func TestIPBrowserWebSocketFramesRevocationAndIndependentB(t *testing.T) {
	seen := make(chan string, 8)
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUpgradeRequest(r) {
			w.WriteHeader(204)
			return
		}
		seen <- r.Header.Get("Auriga-Identity")
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		_ = rw.Flush()
		for {
			header := make([]byte, 6)
			if _, err := io.ReadFull(rw, header); err != nil {
				return
			}
			if header[0] != 0x81 || header[1]&0x80 == 0 || header[1]&0x7f > 125 {
				return
			}
			payload := make([]byte, int(header[1]&0x7f))
			if _, err := io.ReadFull(rw, payload); err != nil {
				return
			}
			for i := range payload {
				payload[i] ^= header[2+i%4]
			}
			_, _ = rw.Write(append([]byte{0x81, byte(len(payload))}, payload...))
			if rw.Flush() != nil {
				return
			}
		}
	}))
	b.open(t, pathInstanceA)
	// Establish B under a different portal sid in the same browser jar.
	s, err := b.sessions.Get(t.Context(), HashCookie("portal"))
	if err != nil {
		t.Fatal(err)
	}
	s.SessionID = "019a0000-0000-7000-8000-000000000099"
	s.TenantID = b.g.Cfg.Access.TenantID()
	b.sessions.Put("portal-b", s)
	root, _ := url.Parse(b.root)
	b.client.Jar.SetCookies(root, []*http.Cookie{{Name: "auriga_session", Value: "portal-b", Path: "/"}})
	b.open(t, pathInstanceB)
	for _, origin := range []string{"https://" + root.Host, "http://127.0.0.1:1", b.g.Cfg.Access.AdminURL(), "null"} {
		req, _ := http.NewRequest("GET", b.root+"/i/"+pathInstanceA+"/api/remote.mux", nil)
		req.Header = http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Origin": {origin}}
		resp, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		readIPResponse(t, resp, 403, "common.forbidden")
	}
	connect := func(id string) (net.Conn, *bufio.Reader) {
		u, _ := url.Parse(b.root + "/i/" + id + "/api/remote.mux?q=1%2F2")
		conn, err := net.DialTimeout("tcp", root.Host, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		req, _ := http.NewRequest("GET", u.String(), nil)
		for _, c := range b.client.Jar.Cookies(u) {
			req.AddCookie(c)
		}
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Origin", b.root)
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")))
		if err := req.Write(conn); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 101 {
			t.Fatalf("WS handshake status=%d", resp.StatusCode)
		}
		claims, _, err := VerifyIdentityToken(<-seen, b.g.Signer.PublicKey())
		if err != nil || claims.Aud != id || claims.Htu != "/api/remote.mux" {
			t.Fatalf("WS identity=%+v err=%v", claims, err)
		}
		return conn, reader
	}
	a, ar := connect(pathInstanceA)
	bc, br := connect(pathInstanceB)
	echo := func(conn net.Conn, reader *bufio.Reader) {
		payload := []byte("bidirectional")
		frame := []byte{0x81, 0x80 | byte(len(payload)), 1, 2, 3, 4}
		for i, c := range payload {
			frame = append(frame, c^frame[2+i%4])
		}
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload)+2)
		if _, err := io.ReadFull(reader, got); err != nil {
			t.Fatal(err)
		}
		if got[0] != 0x81 || !bytes.Equal(got[2:], payload) {
			t.Fatal("WS frame changed")
		}
	}
	echo(a, ar)
	echo(bc, br)
	b.sessions.Revoke("11111111-1111-1111-1111-111111111111")
	if _, err := ar.ReadByte(); err == nil {
		t.Fatal("revoked WS stayed open")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("revocation did not close live WS")
	}
	echo(bc, br)
	readIPResponse(t, b.get(t, "/i/"+pathInstanceA+"/"), 401, "edge.unauthorized")
	readIPResponse(t, b.get(t, "/i/"+pathInstanceB+"/"), 204, "")
	// B's existing connection and binding stay usable after only A's sid revoke.
	_ = bc.Close()
}
