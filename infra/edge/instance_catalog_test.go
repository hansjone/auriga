package main

import (
	"crypto/tls"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInstanceCatalogWriteMarker(t *testing.T) {
	const catalog = "/auriga/resources/catalog/"
	for _, domain := range []bool{false, true} {
		mode := "ip"
		if domain {
			mode = "domain"
		}
		t.Run(mode, func(t *testing.T) {
			seen := make(chan *http.Request, 1)
			platform := make(chan http.Header, 1)
			pki := createAccessPKI(t)
			clientTLS := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "iam.internal.test", RootCAs: pki.roots}
			var nodeURL string
			g, addresses, sessions := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				platform <- r.Header.Clone()
				w.WriteHeader(http.StatusNoContent)
			}), func(cfg *Config, doc map[string]any) {
				nodeURL = testNode(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen <- r
					w.WriteHeader(http.StatusNoContent)
				}), testNodeID).URL
				cfg.TLS = writeAccessServerTLS(t, pki)
				for _, key := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
					doc[key].(map[string]any)["scheme"] = "https"
				}
				for _, role := range []string{"portal", "admin"} {
					doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
				}
				if domain {
					doc["instances"].(map[string]any)["scheme"] = "https"
					doc["ingress"] = map[string]any{"kind": "trusted_proxy", "peerCidrs": []string{"127.0.0.1/32"}, "requireMtls": true, "preserveAuthority": true}
					cfg.ProxyTLS = ProxyTLSConfig{CAFile: pki.hop.CAFile, IdentityURI: "auriga://proxy/test"}
					clientTLS.Certificates = []tls.Certificate{pki.proxy}
				}
			})
			t.Cleanup(g.Ingress.Close)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: clientTLS}}
			t.Cleanup(client.CloseIdleConnections)

			sess := putPortalSession(sessions, "portal", "csrf", "jwt")
			sess.TenantID = g.Cfg.Access.TenantID()
			sessions.Put("portal", sess)
			routes := g.Routes.(*MemoryRouteStore)
			route := InstanceRoute{InstanceID: pathInstanceA, TenantID: sess.TenantID, OwnerID: sess.AccountID, NodeID: testNodeID, Ingress: nodeURL, State: "running", UpdatedAt: time.Now()}
			routes.Put(&route)
			target := g.instanceTarget(nil, pathInstanceA)
			authority := strings.TrimPrefix(target.Origin, "https://")
			binding, err := g.Tickets.BindInstance(sess.SessionID, HashCookie("portal"), sess.AccountID, sess.TenantID, pathInstanceA, sess.ExpiresAt, target)
			if err != nil {
				t.Fatal(err)
			}
			cookie := &http.Cookie{Name: instanceCookieName(true, target.Mount), Value: binding.CookieRaw}

			request := func(t *testing.T, method, path, host string) *http.Request {
				t.Helper()
				r, err := http.NewRequest(method, "https://"+addresses["portal"]+path, strings.NewReader("{}"))
				if err != nil {
					t.Fatal(err)
				}
				r.Host = host
				r.Header.Set("Origin", "https://"+host)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				r.Header.Set("Authorization", "Bearer forged")
				r.Header["auriga-identity"] = []string{"forged"}
				r.Header.Add("Auriga-Identity", "forged2")
				r.Header.Set("X-Auriga-Tenant", "forged")
				return r
			}
			send := func(t *testing.T, r *http.Request, want int, code string) {
				t.Helper()
				resp, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				readIPResponse(t, resp, want, code)
			}
			for _, tc := range []struct {
				name, method, path, marker, second, connection string
				want                                           []string
			}{
				{name: "actions", method: "POST", path: "actions", marker: "1", want: []string{"1"}},
				{name: "observe", method: "POST", path: "observe", marker: "1", want: []string{"1"}},
				{name: "submission-targets", method: "POST", path: "submission-targets", marker: "1", want: []string{"1"}},
				{name: "submissions", method: "POST", path: "submissions", marker: "1", want: []string{"1"}},
				{name: "missing", method: "POST", path: "actions"},
				{name: "invalid", method: "POST", path: "actions", marker: "2"},
				{name: "duplicates", method: "POST", path: "actions", marker: "1", second: "1"},
				{name: "combined", method: "POST", path: "actions", marker: "1, 1"},
				{name: "connection", method: "POST", path: "actions", marker: "1", connection: "x-auriga-catalog"},
				{name: "connection-fetch-site", method: "POST", path: "actions", marker: "1", connection: "sec-fetch-site"},
				{name: "get", method: "GET", path: "actions", marker: "1"},
				{name: "other", method: "POST", path: "installations", marker: "1"},
				{name: "suffix", method: "POST", path: "actions/extra", marker: "1"},
				{name: "escape", method: "POST", path: "%61ctions", marker: "1"},
				{name: "query", method: "POST", path: "actions?x=1", marker: "1"},
				{name: "empty-query", method: "POST", path: "actions?", marker: "1"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := catalog + tc.path
					r := request(t, tc.method, strings.TrimSuffix(target.Mount, "/")+path, authority)
					r.AddCookie(cookie)
					if tc.marker != "" {
						r.Header.Set("X-Auriga-Catalog", tc.marker)
					}
					if tc.second != "" {
						r.Header["x-auriga-catalog"] = []string{tc.second}
					}
					if tc.connection != "" {
						r.Header.Set("Connection", tc.connection)
					}
					send(t, r, http.StatusNoContent, "")
					var got *http.Request
					select {
					case got = <-seen:
					default:
						t.Fatal("request did not reach node ingress")
					}
					if values := got.Header.Values("X-Auriga-Catalog"); !slices.Equal(values, tc.want) {
						t.Fatalf("upstream marker = %q, want %q", values, tc.want)
					}
					tokens := got.Header.Values("Auriga-Identity")
					if len(tokens) != 1 || got.Header.Get("X-Auriga-Tenant") != "" || got.Header.Get("Authorization") != "" || got.Header.Get("Cookie") != "" {
						t.Fatal("expected one Edge identity and no browser credentials or tenant header")
					}
					claims, header, err := VerifyIdentityToken(tokens[0], g.Signer.PublicKey())
					wantPath, _, _ := strings.Cut(path, "?")
					if err != nil || header.Kid != g.Signer.Kid() || claims.Iss != "auriga-edge" ||
						claims.Aud != pathInstanceA || claims.Sub != sess.AccountID || claims.Tid != sess.TenantID || claims.Sid != sess.SessionID ||
						claims.Htm != tc.method || claims.Htu != wantPath || got.URL.EscapedPath() != "/i/"+pathInstanceA+wantPath {
						t.Fatalf("upstream signed identity = %+v, path = %q, err = %v", claims, got.URL.EscapedPath(), err)
					}
					if len(tc.want) != 0 && (got.Header.Get("Origin") != target.Origin ||
						got.Header.Get("Content-Type") != "application/json" || got.Header.Get("Sec-Fetch-Site") != "same-origin") {
						t.Fatal("upstream lost browser write headers")
					}
				})
			}

			for _, tc := range []struct {
				name, code string
				status     int
			}{
				{"no-session-cookie", "edge.unauthorized", http.StatusUnauthorized},
				{"cross-origin", "common.forbidden", http.StatusForbidden},
				{"wrong-owner", "edge.not_owner", http.StatusForbidden},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := request(t, "POST", strings.TrimSuffix(target.Mount, "/")+catalog+"actions", authority)
					r.Header.Set("X-Auriga-Catalog", "1")
					if tc.name != "no-session-cookie" {
						r.AddCookie(cookie)
					}
					if tc.name == "cross-origin" {
						r.Header.Set("Origin", "https://other.example.com")
					}
					if tc.name == "wrong-owner" {
						other := route
						other.OwnerID = "55555555-5555-5555-5555-555555555555"
						routes.Put(&other)
						defer routes.Put(&route)
					}
					send(t, r, tc.status, tc.code)
					select {
					case <-seen:
						t.Fatal("denied request reached node ingress")
					default:
					}
				})
			}

			t.Run("platform", func(t *testing.T) {
				r := request(t, "POST", "/oauth2/token", g.Cfg.PortalHost)
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("X-Auriga-Catalog", "1")
				send(t, r, http.StatusNoContent, "")
				select {
				case h := <-platform:
					if len(h.Values("X-Auriga-Catalog")) != 0 || len(h.Values("Auriga-Identity")) != 0 {
						t.Fatal("platform upstream received an instance header")
					}
				default:
					t.Fatal("request did not reach platform upstream")
				}
			})
		})
	}
}
