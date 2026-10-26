package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const networkKeysPath = "/api/collab/v1/network/keys"

// A locally signed access-JWT authority used only by the HTTP fixture. This
// checks real signature/audience/expiry/tenant boundaries, not IAM issuance.
func networkKeysBearerFixture(t *testing.T) (func(string, string, time.Time) string, func(string, string) int) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	issue := func(tenant, kind string, expiry time.Time) string {
		claims, err := json.Marshal(map[string]any{"iss": "https://issuer.example.com", "aud": "auriga", "sub": "fixture-account", "tid": tenant, "ptyp": kind, "ctx": kind, "did": "fixture-device", "scope": "auriga.connect", "exp": expiry.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
		return unsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(unsigned)))
	}
	verify := func(auth, tenant string) int {
		parts := strings.Split(strings.TrimPrefix(auth, "Bearer "), ".")
		if !strings.HasPrefix(auth, "Bearer ") || len(parts) != 3 || parts[0] != header {
			return 401
		}
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), sig) {
			return 401
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Issuer string `json:"iss"`
			Aud    string `json:"aud"`
			Tenant string `json:"tid"`
			Expiry int64  `json:"exp"`
		}
		if err != nil || json.Unmarshal(raw, &claims) != nil || claims.Issuer != "https://issuer.example.com" || claims.Aud != "auriga" || claims.Expiry <= time.Now().Unix() {
			return 401
		}
		if claims.Tenant != tenant {
			return 403
		}
		return 200
	}
	return issue, verify
}

// These are HTTP fixtures, not the Java controller or a real IAM authority.
// The fixed ASCII payloads are already in JCS key order; outer whitespace
// deliberately makes byte equality stronger than parsed JSON equality.
func signedNetworkPublications(t *testing.T) map[string]string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"issued_at":"2026-10-26T00:00:00Z","keys":[],"keyset_version":"7","realm":"1","schema_version":1,"valid_until":"2026-10-26T01:00:00Z"}`
	sign := func(domain, body string) string {
		return base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(domain+"\x00"+body)))
	}
	digest := sha256.Sum256([]byte(payload))
	announcement := fmt.Sprintf(`{"keyset_version":"7","manifest_digest":"sha256:%s","root_kid":"fixture-root","valid_until":"2026-10-26T01:00:00Z"}`, hex.EncodeToString(digest[:]))
	return map[string]string{
		networkKeysPath:                   "{\n  \"keyset\": " + payload + ",\n  \"root_auth\": {\"alg\":\"Ed25519\",\"kid\":\"fixture-root\",\"signature\":\"" + sign("Auriga.NetworkKeyset.v1", payload) + "\"}\n}\n",
		networkKeysPath + "/announcement": strings.TrimSuffix(announcement, "}") + ",\"root_signature\":\"" + sign("Auriga.NetworkKeysetAnnouncement.v1", announcement) + "\"}\n",
	}
}

func TestNetworkKeysAccessMatrix(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(fmt.Sprintf("domain=%v", domain), func(t *testing.T) {
			publications := signedNetworkPublications(t)
			issue, verify := networkKeysBearerFixture(t)
			var tenant atomic.Value
			tenant.Store("")
			var calls atomic.Int32
			var responseStatus atomic.Int32
			responseStatus.Store(200)
			seen := make(chan *http.Request, 32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				seen <- r.Clone(context.Background())
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				switch verify(r.Header.Get("Authorization"), tenant.Load().(string)) {
				case 200:
					w.WriteHeader(int(responseStatus.Load()))
					_, _ = w.Write([]byte(publications[r.URL.Path]))
				case 403:
					writeProblem(w, problem(403, "common.forbidden", "Forbidden", "fixture tenant rejected", "fixture"))
				default:
					writeProblem(w, problem(401, "common.unauthenticated", "Unauthorized", "fixture token rejected", "fixture"))
				}
			}))
			t.Cleanup(server.Close)
			g, addresses, store := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("collab reached another API")
				w.WriteHeader(500)
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["collab"] = server.URL
				c.CollabHop.AllowLoopbackHTTP = true
			})
			closeAIAdminTransports(t, g)
			installationTenant := g.Cfg.Access.TenantID()
			tenant.Store(installationTenant)
			deviceAccess := issue(installationTenant, "device", time.Now().Add(time.Minute))
			browserAccess := map[string]string{}
			backend, _ := browserRPCFixture(t, g, store)
			for _, role := range []string{"portal", "admin"} {
				s := putBrowserSession(g, backend, role, role)
				browserAccess[role] = issue(installationTenant, role, time.Now().Add(time.Minute))
				s.AccessToken = browserAccess[role]
				store.Put(role, s)
			}
			for _, role := range []string{"portal", "admin"} {
				for path, expected := range publications {
					for _, browser := range []bool{false, true} {
						headers := http.Header{}
						headers.Set("Authorization", "Bearer "+deviceAccess)
						headers.Set("Origin", g.Cfg.Access.PortalURL())
						if role == "admin" {
							headers.Set("Origin", g.Cfg.Access.AdminURL())
						}
						wantBearer := "Bearer " + deviceAccess
						if browser {
							headers.Set("Cookie", sessionCookieBase(role)+"="+role+"; unrelated=private")
							wantBearer = "Bearer " + browserAccess[role]
						}
						for _, name := range []string{"Auriga-Tenant-Id", "X-Tenant-Id", "X-Audience", "Auriga-Identity", "Auriga-Forwarded-Account", "Auriga-Forwarded-Tenant", "X-Auriga-Entry-Origin", "X-Auriga-Entry-Role", "Forwarded", "X-Forwarded-Host"} {
							headers.Set(name, "forged")
						}
						status, h, body := aiAdminRequest(t, g, addresses, role, "GET", path, headers)
						if status != 200 || h.Get("Cache-Control") != "no-store" || body != expected {
							t.Fatalf("publication changed: status=%d bytesEqual=%v cache=%q", status, body == expected, h.Get("Cache-Control"))
						}
						r := <-seen
						if r.Method != "GET" || r.URL.Path != path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != wantBearer || r.Header.Get("Cookie") != "" {
							t.Fatal("credential/path forwarding differs from authenticated API")
						}
						for name := range headers {
							if name != "Authorization" && name != "Origin" && name != "Cookie" && r.Header.Get(name) != "" {
								t.Errorf("untrusted header survived: %s", name)
							}
						}
					}
				}
			}
			if calls.Load() != 8 {
				t.Fatal("unexpected success call count")
			}
			good := http.Header{"Authorization": {"Bearer " + deviceAccess}}
			for _, tc := range []struct {
				method, path string
				headers      http.Header
				status       int
				code         string
			}{
				{"GET", networkKeysPath, nil, 401, "common.unauthenticated"},
				{"GET", networkKeysPath, http.Header{"Cookie": {"auriga_session=missing"}}, 401, "common.unauthenticated"},
				{"GET", networkKeysPath, http.Header{"Authorization": {"Basic invalid"}}, 401, "common.unauthenticated"},
				{"GET", networkKeysPath, http.Header{"Authorization": {"Bearer one", "Bearer two"}}, 401, "common.unauthenticated"},
				{"GET", networkKeysPath, http.Header{"Authorization": {"Bearer a b"}}, 401, "common.unauthenticated"},
				{"GET", networkKeysPath, http.Header{"Authorization": {"Bearer device-access"}, "Origin": {"https://attacker.example.com"}}, 403, "common.forbidden"},
				{"GET", networkKeysPath, http.Header{"Cookie": {"auriga_session=portal"}, "Origin": {"null"}}, 403, "common.forbidden"},
				{"POST", networkKeysPath, good, 405, "common.validation_failed"},
				{"PUT", networkKeysPath + "/announcement", good, 405, "common.validation_failed"},
				{"HEAD", networkKeysPath, good, 405, ""},
				{"OPTIONS", networkKeysPath, good, 405, "common.validation_failed"},
				{"GET", networkKeysPath + "/", good, 404, "common.not_found"},
				{"GET", networkKeysPath + "-other", good, 404, "common.not_found"},
				{"GET", networkKeysPath + "/announcement/", good, 404, "common.not_found"},
				{"GET", "/api/collab/v1/admin/other", good, 403, "common.forbidden"},
				{"GET", "/api/collab/v1/other", good, 404, "common.not_found"},
				{"GET", networkKeysPath + "?realm=2&root=foreign&target=https://attacker.example.com", good, 400, "common.validation_failed"},
				{"GET", "/api/collab/v1/network/%6beys", good, 404, "common.not_found"},
			} {
				status, _, body := aiAdminRequest(t, g, addresses, "portal", tc.method, tc.path, tc.headers)
				if status != tc.status || tc.code != "" && !strings.Contains(body, `"code":"`+tc.code+`"`) {
					t.Errorf("%s %s: status=%d expected=%d", tc.method, tc.path, status, tc.status)
				}
			}
			// Explicitly exercise the real listener's unknown-authority gate.
			status, body := entryRequest(t, addresses["portal"], "GET", networkKeysPath, "unknown.example.com", good)
			if status != 403 || !strings.Contains(body, `"code":"common.forbidden"`) || calls.Load() != 8 {
				t.Fatalf("rejection reached backend: status=%d calls=%d", status, calls.Load())
			}
			for _, code := range []int{200, 404, 503} {
				responseStatus.Store(int32(code))
				for path, expected := range publications {
					status, h, body := aiAdminRequest(t, g, addresses, "portal", "GET", path, good)
					if status != code || h.Get("Cache-Control") != "no-store" || body != expected {
						t.Fatalf("backend status/body changed: %d expected=%d", status, code)
					}
					<-seen
				}
			}
			for token, want := range map[string]int{issue("foreign-tenant", "device", time.Now().Add(time.Minute)): 403, "invalid": 401, issue(installationTenant, "device", time.Now().Add(-time.Minute)): 401} {
				status, _, _ := aiAdminRequest(t, g, addresses, "portal", "GET", networkKeysPath, http.Header{"Authorization": {"Bearer " + token}})
				if status != want {
					t.Fatalf("fixture authentication status=%d expected=%d", status, want)
				}
				<-seen
			}
			if calls.Load() != 17 {
				t.Fatal("response caching or unexpected delivery")
			}
			backend.mu.Lock()
			touches := backend.touches
			backend.mu.Unlock()
			if touches == 0 {
				t.Fatal("browser authentication did not use IAM touch")
			}
		})
	}
}

func TestNetworkKeysConfiguration(t *testing.T) {
	pki, other := createAccessPKI(t), createAccessPKI(t)
	badCA := filepath.Join(t.TempDir(), "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("not a CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	closeAIAdminTransports(t, g)
	for _, tc := range []struct {
		name, raw string
		change    func(*Config)
	}{
		{"orphan", "", nil},
		{"legacy", "https://collab.example.com", func(c *Config) { c.Access = Config{}.Access }},
		{"no-hop", "https://collab.example.com", func(c *Config) { c.CollabHop = IAMHopConfig{} }},
		{"http", "http://127.0.0.1:1234", nil},
		{"localhost", "http://localhost:1234", func(c *Config) { c.CollabHop.AllowLoopbackHTTP = true }},
		{"non-loopback", "http://192.0.2.10:1234", func(c *Config) { c.CollabHop.AllowLoopbackHTTP = true }},
		{"userinfo", "https://user:password@collab.example.com", nil},
		{"query", "https://collab.example.com?realm=2", nil},
		{"empty-query", "https://collab.example.com?", nil},
		{"fragment", "https://collab.example.com#x", nil},
		{"empty-fragment", "https://collab.example.com#", nil},
		{"prefix", "https://collab.example.com/api", nil},
		{"encoded-root", "https://collab.example.com/%2f", nil},
		{"port-zero", "https://collab.example.com:0", nil},
		{"port-large", "https://collab.example.com:65536", nil},
		{"opaque", "https:collab.example.com", nil},
		{"scheme", "file:///tmp/keys", nil},
		{"dns-alias", "https://127.1", nil},
		{"no-ca", "https://collab.example.com", func(c *Config) { c.CollabHop.CAFile = "" }},
		{"no-cert", "https://collab.example.com", func(c *Config) { c.CollabHop.CertFile = "" }},
		{"no-key", "https://collab.example.com", func(c *Config) { c.CollabHop.KeyFile = "" }},
		{"ip-sni", "https://collab.example.com", func(c *Config) { c.CollabHop.ServerName = "127.0.0.1" }},
		{"sni-port", "https://collab.example.com", func(c *Config) { c.CollabHop.ServerName = "collab.example.com:443" }},
		{"bad-ca", "https://collab.example.com", func(c *Config) { c.CollabHop.CAFile = badCA }},
		{"missing-file", "https://collab.example.com", func(c *Config) { c.CollabHop.CAFile = "missing.pem" }},
		{"mismatched-key", "https://collab.example.com", func(c *Config) { c.CollabHop.KeyFile = other.hop.KeyFile }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := g.Cfg
			cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "collab": tc.raw}
			cfg.CollabHop = pki.hop
			if tc.change != nil {
				tc.change(&cfg)
			}
			if _, err := NewGateway(cfg, g.Sessions, g.Routes, g.Tickets, g.Signer, nil); err == nil {
				t.Fatal("invalid optional collab configuration accepted")
			}
			if _, err := cfg.parseCollabUpstream(); err == nil && tc.name != "bad-ca" && tc.name != "missing-file" && tc.name != "mismatched-key" {
				t.Fatal("invalid origin/hop shape accepted by config parser")
			}
		})
	}
	for _, raw := range []string{"https://collab.example.com", "https://127.0.0.1:1234", "https://[::1]:1234", "http://127.0.0.1:1234", "http://[::1]:1234"} {
		cfg := g.Cfg
		cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "collab": raw}
		cfg.CollabHop = pki.hop
		cfg.CollabHop.AllowLoopbackHTTP = true
		created, err := NewGateway(cfg, g.Sessions, g.Routes, g.Tickets, g.Signer, nil)
		if err != nil {
			t.Fatal(err)
		}
		closeAIAdminTransports(t, created)
	}
	t.Setenv("AURIGA_EDGE_UPSTREAM_COLLAB", "https://collab.example.com")
	cfg := defaultConfig()
	applyEnv(&cfg)
	if cfg.Upstreams["collab"] != "https://collab.example.com" {
		t.Fatal("collab environment override not applied")
	}
	var parsed Config
	if err := json.Unmarshal([]byte(`{"collabHop":{"serverName":"collab.example.com","allowLoopbackHttp":true}}`), &parsed); err != nil || parsed.CollabHop.ServerName != "collab.example.com" || !parsed.CollabHop.AllowLoopbackHTTP {
		t.Fatal("collabHop JSON mapping failed")
	}
}

func TestNetworkKeysTLSFailureClosed(t *testing.T) {
	for _, domain := range []bool{true, false} {
		for _, failure := range []string{"valid", "wrong-ca", "wrong-san", "wrong-client-ca", "missing-client-cert"} {
			t.Run(fmt.Sprintf("domain=%v/%s", domain, failure), func(t *testing.T) {
				pki, other := createAccessPKI(t), createAccessPKI(t)
				publications := signedNetworkPublications(t)
				var calls atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.TLS == nil || r.TLS.ServerName != pki.hop.ServerName || len(r.TLS.VerifiedChains) == 0 || r.TLS.PeerCertificates[0].DNSNames[0] != "edge.internal.test" {
						t.Error("unverified collab mTLS peer/SNI")
					}
					if r.Header.Get("Authorization") != "Bearer device-access" {
						t.Error("lost device access")
					}
					w.Header().Set("Cache-Control", "no-store")
					_, _ = w.Write([]byte(publications[r.URL.Path]))
				}))
				server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
				server.StartTLS()
				t.Cleanup(server.Close)
				g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("wrong API"); w.WriteHeader(500) }), func(c *Config, _ map[string]any) {
					c.Upstreams["collab"], c.CollabHop = server.URL, pki.hop
					// Same origin, deliberately unusable IAM identity: collab must
					// build its own transport instead of sharing those credentials.
					c.Upstreams["iam"], c.IAMHop = server.URL, other.hop
					c.IAMHop.AllowLoopbackHTTP = true
					switch failure {
					case "wrong-ca":
						c.CollabHop.CAFile = other.hop.CAFile
					case "wrong-san":
						c.CollabHop.ServerName = "wrong.example.com"
					case "wrong-client-ca":
						c.CollabHop.CertFile, c.CollabHop.KeyFile = other.hop.CertFile, other.hop.KeyFile
					}
				})
				closeAIAdminTransports(t, g)
				tr := g.APITransports[g.collabURL].(*http.Transport)
				if tr == g.IAMTransport || tr.ResponseHeaderTimeout != 5*time.Second || tr.TLSHandshakeTimeout != 2*time.Second || tr.Proxy != nil {
					t.Fatal("collab transport shared credentials or changed policy")
				}
				if failure == "missing-client-cert" {
					tr.TLSClientConfig.Certificates = nil
				}
				for path, expected := range publications {
					status, h, body := aiAdminRequest(t, g, addresses, "portal", "GET", path, http.Header{"Authorization": {"Bearer device-access"}})
					if failure == "valid" {
						if status != 200 || h.Get("Cache-Control") != "no-store" || body != expected {
							t.Fatalf("valid actual TLS hop failed: status=%d bytesEqual=%v", status, body == expected)
						}
					} else if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != 0 {
						t.Fatalf("TLS rejection delivered HTTP: status=%d calls=%d", status, calls.Load())
					}
				}
				if failure == "valid" && calls.Load() != 2 {
					t.Fatal("missing actual TLS calls")
				}
			})
		}
	}
}

func TestNetworkKeysRedirectAndTimeout(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(fmt.Sprintf("domain=%v", domain), func(t *testing.T) {
			var escaped, calls atomic.Int32
			pki := createAccessPKI(t)
			outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); w.WriteHeader(200) }))
			t.Cleanup(outside.Close)
			var mode atomic.Int32
			mode.Store(302)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode.Load() == 0 {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Location", outside.URL+networkKeysPath)
				w.WriteHeader(int(mode.Load()))
			}))
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
			server.StartTLS()
			t.Cleanup(server.Close)
			g, _, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }), func(c *Config, _ map[string]any) {
				c.Upstreams["collab"] = server.URL
				c.CollabHop = pki.hop
			})
			closeAIAdminTransports(t, g)
			headers := http.Header{"Authorization": {"Bearer device-access"}}
			for _, code := range []int32{301, 302, 303, 307, 308, 0} {
				mode.Store(code)
				for _, path := range []string{networkKeysPath, networkKeysPath + "/announcement"} {
					// Use the real Gateway handler and an actual backend socket.
					// Recorder avoids giving the frontend client the same 5s timeout
					// as the unmodified production hop's response-header deadline.
					r := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+path, nil)
					r.Header = headers.Clone()
					r = r.WithContext(context.WithValue(r.Context(), accessRoleKey{}, "portal"))
					w := httptest.NewRecorder()
					g.Handler().ServeHTTP(w, r)
					if w.Code != 503 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), `"code":"common.unavailable"`) || escaped.Load() != 0 {
						t.Fatalf("redirect/timeout was not closed: mode=%d status=%d", code, w.Code)
					}
				}
			}
			if calls.Load() != 12 {
				t.Fatal("unexpected backend delivery count")
			}
		})
	}
}

func TestNetworkKeysUnconfigured(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(fmt.Sprintf("domain=%v", domain), func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(204)
			}))
			closeAIAdminTransports(t, g)
			for _, path := range []string{"/api/collab", "/api/collab/", "/api/collab/v1/network/keys", "/api/collab/v1/network/keys/announcement", "/api/collab/v1/other"} {
				status, _, body := aiAdminRequest(t, g, addresses, "portal", "GET", path, nil)
				if status != 404 || !strings.Contains(body, `"code":"common.not_found"`) || calls.Load() != 0 {
					t.Fatalf("API fell through: status=%d calls=%d", status, calls.Load())
				}
			}
		})
	}
}
