package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func entryDocument(r *http.Request) {
	r.Header.Set("Accept", "text/html")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
}

func TestInstanceEntryLostBindingDocumentUsesNormalOpen(t *testing.T) {
	for _, secure := range []bool{false, true} {
		b := newIPBrowser(t, secure, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		b.open(t, pathInstanceA)
		b.g.Tickets = NewMemoryTicketStore()
		r, _ := http.NewRequest("GET", b.root+instanceMount(pathInstanceA)+"?returnTo=https://evil.example.com", nil)
		entryDocument(r)
		resp, err := b.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body := readIPResponse(t, resp, 401, "")
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, `href="`+b.root+"/open/"+pathInstanceA+`"`) || strings.Contains(body, "evil.example.com") {
			t.Fatal("lost binding did not offer the fixed approved normal open")
		}
		if resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'sha256-") || strings.Contains(body, "instance/open") {
			t.Fatal("recovery is cacheable, permits arbitrary script or starts the instance")
		}
		b.open(t, pathInstanceA)
		readIPResponse(t, b.get(t, instanceMount(pathInstanceA)), 204, "")
	}
}

func TestInstanceEntryOnlyTopDocumentGetsRecovery(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	for _, tc := range []struct{ method, suffix, mode, dest string }{
		{"GET", "", "", ""}, {"HEAD", "", "navigate", "document"},
		{"GET", "_auriga/health", "cors", "empty"},
		{"GET", "", "navigate", "iframe"}, {"GET", "api/llm/listProviders", "navigate", "document"},
		{"GET", "assets/main.js", "no-cors", "script"}, {"POST", "", "navigate", "document"},
		{"GET", "api/remote.mux", "websocket", "empty"},
	} {
		r, _ := http.NewRequest(tc.method, b.root+instanceMount(pathInstanceA)+tc.suffix, nil)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("Sec-Fetch-Mode", tc.mode)
		r.Header.Set("Sec-Fetch-Dest", tc.dest)
		if tc.mode == "websocket" {
			r.Header.Set("Origin", b.root)
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
		}
		resp, err := b.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		want := 401
		if tc.method == "POST" {
			want = 403 // the original entry Origin fence rejects this first
		}
		readIPResponse(t, resp, want, "")
		if strings.Contains(resp.Header.Get("Content-Type"), "text/html") || resp.Header.Get("Location") != "" {
			t.Fatalf("non-document got navigation recovery: %+v", tc)
		}
		if want == 401 && resp.Header.Get("X-Auriga-Instance-Entry") != "required" {
			t.Fatal("edge entry rejection lacks its exact private probe marker")
		}
	}
}

func TestInstanceEntryDomainRecoveryUsesApprovedPortal(t *testing.T) {
	g, _, sessions := accessFixture(t, true, http.NotFoundHandler())
	s := putPortalSession(sessions, "portal", "csrf", "jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	base := httptest.NewRequest("GET", g.Cfg.Access.PortalURL(), nil)
	target := g.instanceTarget(base, pathInstanceA)
	r := httptest.NewRequest("GET", target.Origin+"/", nil)
	entryDocument(r)
	r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "portal"))
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), `href="`+g.Cfg.Access.PortalURL()+"/open/"+pathInstanceA+`"`) {
		t.Fatal("subdomain recovery did not choose the checked portal")
	}
}

func TestInstanceEntryRecoveryKeepsOnlyApprovedProxyPresentation(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	upstream, _ := url.Parse(b.root)
	var presentation *url.URL
	proxy := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(upstream)
		pr.Out.Host = upstream.Host
		pr.Out.Header.Set("X-Forwarded-Host", presentation.Host)
		pr.Out.Header.Set("X-Forwarded-Proto", presentation.Scheme)
	}})
	t.Cleanup(proxy.Close)
	presentation, _ = url.Parse(strings.Replace(proxy.URL, "127.0.0.1", "localhost", 1))
	b.g.Cfg.LinkOrigins = []string{presentation.String()}
	b.g.Cfg.LinkTrustedProxies = []string{"127.0.0.1"}
	r, _ := http.NewRequest("GET", presentation.String()+instanceMount(pathInstanceA), nil)
	entryDocument(r)
	client := &http.Client{}
	t.Cleanup(client.CloseIdleConnections)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body := readIPResponse(t, resp, 401, "")
	if !strings.Contains(body, `href="`+presentation.String()+"/open/"+pathInstanceA+`"`) {
		t.Fatal("approved presentation was replaced by an inaccessible internal portal")
	}
	// Without explicit proxy trust its forwarding headers cannot select a
	// destination; only the installation's original portal is available.
	b.g.Cfg.LinkTrustedProxies = nil
	resp, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body = readIPResponse(t, resp, 401, "")
	if strings.Contains(body, `href="`+presentation.String()) || !strings.Contains(body, `href="`+b.root+"/open/") {
		t.Fatal("untrusted forwarding headers selected the recovery destination")
	}
}

func TestInstanceEntryStoppedAndUnauthorizedOpenStillFailClosed(t *testing.T) {
	b := newIPBrowser(t, false, http.NotFoundHandler())
	routes := b.g.Routes.(*MemoryRouteStore)
	route, _ := routes.Get(t.Context(), pathInstanceA)
	route.State = "stopped"
	routes.Put(route)
	readIPResponse(t, b.get(t, "/open/"+pathInstanceA), 503, "edge.instance_not_running")
	route.State, route.OwnerID = "running", "other-owner"
	routes.Put(route)
	readIPResponse(t, b.get(t, "/open/"+pathInstanceA), 403, "edge.not_owner")
	s, _ := b.sessions.Get(t.Context(), HashCookie("portal"))
	b.sessions.Revoke(s.SessionID)
	readIPResponse(t, b.get(t, "/open/"+pathInstanceA), 401, "edge.unauthorized")
}

func TestInstanceEntryUpstreamCannotClaimRecoveryMarker(t *testing.T) {
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Auriga-Instance-Entry", "required")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"code":"edge.unauthorized","status":401,"detail":"instance cookie required"}`)
	}))
	b.open(t, pathInstanceA)
	r, _ := http.NewRequest("GET", b.root+instanceMount(pathInstanceA), nil)
	entryDocument(r)
	resp, err := b.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 401, "edge.unauthorized")
	if resp.Header.Get("X-Auriga-Instance-Entry") != "" || strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatal("upstream 401 was confused with the edge entry gate")
	}
}
