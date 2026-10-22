package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAccessServerTLS(t *testing.T, pki accessPKI) TLSConfig {
	t.Helper()
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "listener.crt"), filepath.Join(dir, "listener.key")
	der, err := x509.MarshalPKCS8PrivateKey(pki.server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pki.server.Certificate[0]}), key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return TLSConfig{Mode: "files", CertFile: cert, KeyFile: key}
}

func TestAccessTLSActualProxyPeerAndCertificate(t *testing.T) {
	pki := createAccessPKI(t)
	for _, test := range []struct {
		name, peerCIDR, identity string
		want                     int
	}{{"valid", "127.0.0.1/32", "auriga://proxy/test", 200}, {"wrong-peer", "192.0.2.0/24", "auriga://proxy/test", 403}, {"wrong-identity", "127.0.0.1/32", "auriga://proxy/other", 403}} {
		t.Run(test.name, func(t *testing.T) {
			g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), func(cfg *Config, doc map[string]any) {
				cfg.TLS = writeAccessServerTLS(t, pki)
				cfg.ProxyTLS = ProxyTLSConfig{CAFile: pki.hop.CAFile, IdentityURI: test.identity}
				doc["ingress"] = map[string]any{"kind": "trusted_proxy", "peerCidrs": []string{test.peerCIDR}, "requireMtls": true, "preserveAuthority": true}
				for _, role := range []string{"portal", "admin"} {
					doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
				}
			})
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "iam.internal.test", RootCAs: pki.roots, Certificates: []tls.Certificate{pki.proxy}}}}
			defer client.CloseIdleConnections()
			req, err := http.NewRequest("GET", "https://"+addresses["portal"]+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = g.Cfg.PortalHost
			req.Header.Set("Forwarded", "proto=https;host=wrong")
			req.Header.Set("X-Forwarded-Proto", "https")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != test.want {
				t.Fatalf("%d %s", resp.StatusCode, body)
			}
			if test.want == 403 && !strings.Contains(string(body), `"code":"common.forbidden"`) {
				t.Fatal(string(body))
			}
		})
	}
}

func TestAccessDirectTLSAndMissingClientCertificate(t *testing.T) {
	pki := createAccessPKI(t)
	g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), func(cfg *Config, doc map[string]any) {
		cfg.TLS = writeAccessServerTLS(t, pki)
		for _, key := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
			doc[key].(map[string]any)["scheme"] = "https"
		}
		for _, role := range []string{"portal", "admin"} {
			doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
		}
	})
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "iam.internal.test", RootCAs: pki.roots}}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("GET", "https://"+addresses["portal"]+"/", nil)
	req.Host = g.Cfg.PortalHost
	req.Header.Set("X-Forwarded-Proto", "http")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	// Use the actual mTLS HTTP hop transport against a server requiring the
	// installed edge client cert, with complete server DNS verification.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	tlsLn := tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert})
	go func() { _ = server.Serve(tlsLn) }()
	t.Cleanup(func() { _ = server.Close() })
	u, _ := url.Parse("https://" + ln.Addr().String())
	transport, err := newIAMHopTransport(pki.hop, u)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	hopClient := &http.Client{Timeout: 3 * time.Second, Transport: transport}
	resp, err = hopClient.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, err := client.Get(u.String()); err == nil {
		t.Fatal("IAM accepted missing edge client certificate")
	}
}

func TestAccessStartupFailureClosesPreviouslyBoundSockets(t *testing.T) {
	g, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	var first net.Listener
	calls := 0
	server := &Server{Cfg: g.Cfg, Handler: g.Handler(), Listen: func(_, addr string) (net.Listener, error) {
		calls++
		if calls == 1 {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			first = ln
			return ln, err
		}
		return nil, fmt.Errorf("bind failure")
	}}
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("expected bind failure")
	}
	if first == nil {
		t.Fatal("first socket not bound")
	}
	conn, err := net.DialTimeout("tcp", first.Addr().String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("partial startup leaked listener")
	}
}

func TestAccessCombinedConnectorMTLSWithoutCookieOrIdentityLeak(t *testing.T) {
	pki := createAccessPKI(t)
	seen := make(chan *http.Request, 4)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates[0].DNSNames) != 1 || r.TLS.PeerCertificates[0].DNSNames[0] != "edge.internal.test" {
			t.Error("combined connector did not authenticate edge")
		}
		seen <- r.Clone(context.Background())
		w.WriteHeader(204)
	})}
	tlsLn := tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert})
	go func() { _ = backend.Serve(tlsLn) }()
	t.Cleanup(func() { _ = backend.Close() })
	internalURL := "https://" + ln.Addr().String()
	g, addresses, sessions := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), func(cfg *Config, doc map[string]any) {
		cfg.Upstreams = map[string]string{"iam": internalURL, "org": internalURL, "workspace": internalURL}
		cfg.IAMHop = pki.hop
	})
	s := putPortalSession(sessions, "portal", "csrf", "portal-jwt")
	s.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", s)
	for _, component := range []string{"iam", "org", "workspace"} {
		path := "/api/" + component + "/v1/me"
		headers := http.Header{"Cookie": {"auriga_session=portal; auriga_admin_session=admin; auriga_portal_login_tx=tx; auriga_csrf=csrf"}, "Forwarded": {"host=forged"}, "X-Auriga-Entry-Role": {"admin"}, "X-Auriga-Tenant": {"forged"}}
		status, body := entryRequest(t, addresses["portal"], "GET", path, g.Cfg.PortalHost, headers)
		if status != 204 {
			t.Fatalf("%s %d %s", component, status, body)
		}
		r := <-seen
		if r.Header.Get("Cookie") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Auriga-Tenant") != "" || r.Header.Get("Authorization") != "Bearer portal-jwt" {
			t.Fatalf("%s leaked credentials: %v", component, r.Header)
		}
		if component == "iam" {
			if r.Header.Get("X-Auriga-Entry-Origin") != g.Cfg.Access.PortalURL() || r.Header.Get("X-Auriga-Entry-Role") != "portal" {
				t.Fatal("IAM hop metadata absent")
			}
		} else if r.Header.Get("X-Auriga-Entry-Origin") != "" || r.Header.Get("X-Auriga-Entry-Role") != "" {
			t.Fatal("IAM metadata leaked to component")
		}
	}
}
