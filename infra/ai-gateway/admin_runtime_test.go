package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestAdminRuntimeConfiguration(t *testing.T) {
	t.Setenv("AURIGA_AI_PG_DSN", "")
	t.Setenv("AURIGA_AI_MIGRATION_PG_DSN", "")
	for _, raw := range []string{`{}`, `{"unknown":true}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "admin.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var out, logs bytes.Buffer
		if err := run([]string{"--admin-config", path}, &out, &logs); err == nil {
			t.Fatal("invalid admin config accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "admin.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAdminConfig(path); err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatal("missing DSN error", err)
	}
	t.Setenv("AURIGA_AI_PG_DSN", "postgres://db.example.com/ai")
	t.Setenv("AURIGA_AI_MIGRATION_PG_DSN", "postgres://db.example.com/ai")
	cfg, err := loadAdminConfig(path)
	if err != nil || cfg.Store.DSN != "postgres://db.example.com/ai" {
		t.Fatal("environment configuration", err)
	}
	var out, logs bytes.Buffer
	if err = run([]string{"--admin-config", path, "--team-config", "unused"}, &out, &logs); err == nil || !strings.Contains(err.Error(), "choose one runtime") {
		t.Fatal("multiple runtimes accepted", err)
	}
	if err = os.WriteFile(path, []byte(`{"listen":"0.0.0.0:8080"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadAdminConfig(path); err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatal("insecure listener accepted", err)
	}
}

type adminTLSCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

type adminTLSLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (l *adminTLSLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.Write(p)
}

func (l *adminTLSLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

func adminTLSKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newAdminTLSCA(t *testing.T) adminTLSCA {
	t.Helper()
	key := adminTLSKey(t)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return adminTLSCA{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca adminTLSCA) issue(t *testing.T, change func(*x509.Certificate)) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key := adminTLSKey(t)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: serial, DNSNames: []string{"edge.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if change != nil {
		change(c)
	}
	der, err := x509.CreateCertificate(rand.Reader, c, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM, keyPEM
}

func adminTLSFile(t *testing.T, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func adminTLSFixture(t *testing.T) (adminConfig, adminTLSCA, *x509.CertPool) {
	t.Helper()
	ca := newAdminTLSCA(t)
	_, cert, key := ca.issue(t, func(c *x509.Certificate) {
		c.DNSNames = []string{"admin.example.com"}
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	})
	cfg := adminConfig{Listen: "127.0.0.1:0", TLSCertFile: adminTLSFile(t, "server.pem", cert), TLSKeyFile: adminTLSFile(t, "server.key", key), TLSClientCAFile: adminTLSFile(t, "ca.pem", ca.pem), TLSEdgeDNSNames: []string{"edge.example.com", "edge-2.example.com"}}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	return cfg, ca, roots
}

func TestAdminTLSConfiguration(t *testing.T) {
	t.Setenv("AURIGA_AI_PG_DSN", "postgres://db.example.com/ai")
	t.Setenv("AURIGA_AI_MIGRATION_PG_DSN", "postgres://db.example.com/ai")
	full, _, _ := adminTLSFixture(t)
	for _, listen := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0", "0.0.0.0:0", "[::]:0", "192.0.2.10:0", "10.0.0.1:0"} { // gitleaks:allow
		for mask := 0; mask < 16; mask++ {
			t.Run(listen+"/"+big.NewInt(int64(mask)).String(), func(t *testing.T) {
				cfg := full
				cfg.Listen = listen
				if mask&1 == 0 {
					cfg.TLSCertFile = ""
				}
				if mask&2 == 0 {
					cfg.TLSKeyFile = ""
				}
				if mask&4 == 0 {
					cfg.TLSClientCAFile = ""
				}
				if mask&8 == 0 {
					cfg.TLSEdgeDNSNames = nil
				}
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				loaded, loadErr := loadAdminConfig(adminTLSFile(t, "admin.json", raw))
				host, _, err := net.SplitHostPort(listen)
				if err != nil {
					t.Fatal(err)
				}
				plain := net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() && mask == 0
				wantOK := mask == 15 || plain
				if (loadErr == nil) != wantOK {
					t.Fatalf("load error = %v, wantOK %v", loadErr, wantOK)
				}
				server, serverErr := newAdminServer(cfg, http.NotFoundHandler())
				if (serverErr == nil) != wantOK {
					t.Fatalf("server error = %v, wantOK %v", serverErr, wantOK)
				}
				if wantOK && ((server.TLSConfig == nil) != plain || loaded.Listen != listen) {
					t.Fatal("TLS selection mismatch")
				}
			})
		}
	}
	for _, name := range []string{"", " ", "*.example.com", "https://edge.example.com", "127.0.0.1", "edge.example.com:443", "edge..example.com", "-edge.example.com", "edge.example.com.", "edge_example.com", "EDGE.example.com", strings.Repeat("a", 64) + ".example.com", strings.Repeat("a.", 128) + "com"} {
		t.Run("name/"+name, func(t *testing.T) {
			cfg := full
			cfg.TLSEdgeDNSNames = []string{name}
			if _, err := newAdminServer(cfg, http.NotFoundHandler()); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
	for _, field := range []string{"cert", "key", "ca"} {
		for _, material := range []string{"missing", "directory", "invalid", "empty", "oversize", "mixed", "damaged_prefix"} {
			t.Run(field+"/"+material, func(t *testing.T) {
				cfg := full
				path := filepath.Join(t.TempDir(), "missing")
				switch material {
				case "directory":
					path = t.TempDir()
				case "invalid":
					path = adminTLSFile(t, "invalid", []byte("invalid-private-material"))
				case "empty":
					path = adminTLSFile(t, "empty", nil)
				case "oversize":
					path = adminTLSFile(t, "large", bytes.Repeat([]byte{'x'}, (1<<20)+1))
				case "mixed":
					ca, err := os.ReadFile(full.TLSClientCAFile)
					if err != nil {
						t.Fatal(err)
					}
					path = adminTLSFile(t, "mixed", append(ca, []byte("invalid trailing PEM")...))
				case "damaged_prefix":
					ca, err := os.ReadFile(full.TLSClientCAFile)
					if err != nil {
						t.Fatal(err)
					}
					path = adminTLSFile(t, "damaged", append([]byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n"), ca...))
				}
				switch field {
				case "cert":
					cfg.TLSCertFile = path
				case "key":
					cfg.TLSKeyFile = path
				case "ca":
					cfg.TLSClientCAFile = path
				}
				_, err := newAdminServer(cfg, http.NotFoundHandler())
				if err == nil {
					t.Fatal("invalid TLS material accepted")
				}
				if strings.Contains(err.Error(), "invalid-private-material") {
					t.Fatal("TLS error disclosed material")
				}
			})
		}
	}
	cfg := full
	cfg.TLSClientCAFile = full.TLSCertFile
	if _, err := newAdminServer(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("non-CA trust anchor accepted")
	}
	_, _, wrongKey := newAdminTLSCA(t).issue(t, nil)
	cfg = full
	cfg.TLSKeyFile = adminTLSFile(t, "wrong.key", wrongKey)
	if _, err := newAdminServer(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("mismatched key accepted")
	}
	cfg.Listen = "invalid"
	if _, err := newAdminServer(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("invalid listener accepted")
	}
	if _, err := newAdminServer(adminConfig{Listen: "127.0.0.1:0", TLSEdgeDNSNames: []string{}}, http.NotFoundHandler()); err == nil {
		t.Fatal("explicit empty TLS identity list downgraded to plaintext")
	}
	// Exercise startup itself: TLS material errors must precede missing master key/PG.
	cfg = full
	cfg.TLSClientCAFile = adminTLSFile(t, "bad-ca.pem", []byte("not a CA"))
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := serveAdmin(adminTLSFile(t, "bad-admin.json", raw)); err == nil || !strings.Contains(err.Error(), "CA bundle") {
		t.Fatalf("startup did not reject TLS before dependencies: %v", err)
	}
}

func TestAdminTLSRejectsFileLinks(t *testing.T) {
	cfg, _, _ := adminTLSFixture(t)
	link := filepath.Join(t.TempDir(), "ca-link.pem")
	if err := os.Symlink(cfg.TLSClientCAFile, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	cfg.TLSClientCAFile = link
	if _, err := newAdminServer(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("TLS CA symlink accepted")
	}
}

func startAdminTLSServer(t *testing.T, cfg adminConfig, handler http.Handler) string {
	t.Helper()
	server, err := newAdminServer(cfg, handler)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	var logs adminTLSLog
	server.ErrorLog = log.New(&logs, "", 0)
	done := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			done <- server.ServeTLS(listener, "", "")
		} else {
			done <- server.Serve(listener)
		}
	}()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("server exit: %v", err)
		}
		if t.Failed() {
			t.Logf("TLS server log:\n%s", logs.String())
		}
	})
	return listener.Addr().String()
}

func adminTLSClient(t *testing.T, roots *x509.CertPool, cert *tls.Certificate, version uint16) *http.Client {
	t.Helper()
	cfg := &tls.Config{RootCAs: roots, ServerName: "admin.example.com", MinVersion: version, MaxVersion: version}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	transport := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func TestAdminTLSHandshake(t *testing.T) {
	cfg, ca, roots := adminTLSFixture(t)
	var entered atomic.Int32
	addr := startAdminTLSServer(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered.Add(1)
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("handler has no verified client chain")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	wrongCA := newAdminTLSCA(t)
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, name := range []string{"valid", "second_identity", "no_certificate", "wrong_ca", "wrong_san", "expired", "not_yet_valid", "wrong_eku", "cn_only", "ip_only", "wildcard", "suffix"} {
			t.Run(name+"/"+big.NewInt(int64(version)).String(), func(t *testing.T) {
				issuer := ca
				if name == "wrong_ca" {
					issuer = wrongCA
				}
				cert, _, _ := issuer.issue(t, func(c *x509.Certificate) {
					switch name {
					case "second_identity":
						c.DNSNames = []string{"edge-2.example.com"}
					case "wrong_san":
						c.DNSNames = []string{"other.example.com"}
						c.Subject.CommonName = "edge.example.com"
					case "expired":
						c.NotBefore = time.Now().Add(-2 * time.Hour)
						c.NotAfter = time.Now().Add(-time.Hour)
					case "not_yet_valid":
						c.NotBefore = time.Now().Add(time.Minute)
					case "wrong_eku":
						c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
					case "cn_only":
						c.DNSNames = nil
						c.Subject.CommonName = "edge.example.com"
					case "ip_only":
						c.DNSNames = nil
						c.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
					case "wildcard":
						c.DNSNames = []string{"*.example.com"}
					case "suffix":
						c.DNSNames = []string{"edge.example.com.attacker.example.com"}
					}
				})
				clientCert := &cert
				if name == "no_certificate" {
					clientCert = nil
				}
				client := adminTLSClient(t, roots, clientCert, version)
				req, err := http.NewRequest(http.MethodGet, "https://"+addr, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Host = "edge.example.com"
				req.Header.Set("X-Auriga-Client-IP", "edge.example.com")
				req.Header.Set("X-Forwarded-Client-Cert", "edge.example.com")
				before := entered.Load()
				resp, err := client.Do(req)
				if name == "valid" || name == "second_identity" {
					if err != nil {
						t.Fatal(err)
					}
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent || entered.Load() != before+1 {
						t.Fatal("trusted edge did not reach handler")
					}
				} else {
					if resp != nil {
						_ = resp.Body.Close()
					}
					if err == nil || entered.Load() != before {
						t.Fatalf("untrusted client reached handler: %v", err)
					}
				}
			})
		}
	}
	before := entered.Load()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if entered.Load() != before || (err == nil && resp.StatusCode != http.StatusBadRequest) {
		t.Fatal("TLS listener downgraded to plaintext")
	}
}

func TestAdminPlainLoopback(t *testing.T) {
	addr := startAdminTLSServer(t, adminConfig{Listen: "127.0.0.1:0"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			t.Error("unexpected TLS in legacy loopback mode")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatal(resp.StatusCode)
	}
}

func TestAdminTLSJWTBoundary(t *testing.T) {
	cfg, ca, roots := adminTLSFixture(t)
	key := adminTLSKey(t)
	point, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(map[string]any{"keys": []any{map[string]string{"kty": "EC", "crv": "P-256", "alg": "ES256", "kid": "test", "x": base64.RawURLEncoding.EncodeToString(point[1:33]), "y": base64.RawURLEncoding.EncodeToString(point[33:])}}})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := admin.JWTAuthenticator("https://iam.example.com", adminTLSFile(t, "jwks.json", jwks))
	if err != nil {
		t.Fatal(err)
	}
	// These authorization failures finish before any store, cipher or grant operation.
	handler, err := admin.New(&store.Store{}, &admin.Cipher{}, auth, func(context.Context, *admin.Principal, api.ModelGrants) error {
		t.Error("grant authority reached")
		return nil
	}, func(context.Context, string) error { t.Error("endpoint validation reached"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var entered atomic.Int32
	addr := startAdminTLSServer(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered.Add(1)
		handler.ServeHTTP(w, r)
	}))
	cert, _, _ := ca.issue(t, nil)
	client := adminTLSClient(t, roots, &cert, tls.VersionTLS13)
	for _, name := range []string{"missing", "forged", "wrong_audience", "wrong_context", "no_permission", "no_stepup", "authenticated_invalid_body"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().Unix()
			id := "00000000-0000-7000-8000-000000000011"
			claims := jwt.MapClaims{"iss": "https://iam.example.com", "aud": []string{"auriga"}, "sub": id, "tid": id, "sid": id, "jti": "test", "iat": now, "exp": now + 300, "ctx": "admin", "ptyp": "user", "perms": []string{"ai.providers.write"}, "stepup_time": now}
			wantStatus, wantCode := http.StatusUnauthorized, "common.unauthenticated"
			switch name {
			case "wrong_audience":
				claims["aud"] = []string{"auriga.ai-gateway"}
			case "wrong_context":
				claims["ctx"] = "portal"
				wantStatus, wantCode = http.StatusForbidden, "common.forbidden"
			case "no_permission":
				claims["perms"] = []string{}
				wantStatus, wantCode = http.StatusForbidden, "common.forbidden"
			case "no_stepup":
				delete(claims, "stepup_time")
				wantStatus, wantCode = http.StatusForbidden, "iam.step_up_required"
			case "authenticated_invalid_body":
				wantStatus, wantCode = http.StatusBadRequest, "common.validation_failed"
			}
			token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
			token.Header["kid"], token.Header["typ"] = "test", "at+jwt"
			signKey := key
			if name == "forged" {
				signKey = adminTLSKey(t)
			}
			raw, err := token.SignedString(signKey)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/api/ai/v1/admin/providers", strings.NewReader("{"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if name != "missing" {
				req.Header.Set("Authorization", "Bearer "+raw)
			}
			if name == "authenticated_invalid_body" {
				before := entered.Load()
				untrusted := adminTLSClient(t, roots, nil, tls.VersionTLS13)
				probe := req.Clone(context.Background())
				probe.Body, err = req.GetBody()
				if err != nil {
					t.Fatal(err)
				}
				resp, err := untrusted.Do(probe)
				if resp != nil {
					_ = resp.Body.Close()
				}
				if err == nil || entered.Load() != before {
					t.Fatal("valid JWT bypassed client certificate")
				}
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != wantStatus || !bytes.Contains(body, []byte(wantCode)) {
				t.Fatalf("response %d %s, want %d %s", resp.StatusCode, body, wantStatus, wantCode)
			}
		})
	}
}
