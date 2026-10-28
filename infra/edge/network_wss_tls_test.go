package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func networkWSSUpgradeRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, target+DefaultNetworkWSSPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://portal.example.com")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "MDEyMzQ1Njc4OWFiY2RlZg==")
	return r
}

func TestNetworkWSSTLSFrames(t *testing.T) {
	pki := createAccessPKI(t)
	var upgrades atomic.Int32
	var upstreamHost string
	up := httptest.NewUnstartedServer(websocket.Server{
		Handshake: func(_ *websocket.Config, r *http.Request) error {
			if r.TLS == nil || r.TLS.ServerName != pki.hop.ServerName {
				t.Errorf("wrong upstream TLS name: %v", r.TLS)
			}
			if r.Host != upstreamHost || r.URL.Path != "/" || r.URL.Query().Get("client") != "test" {
				t.Errorf("unexpected target: host=%q url=%s", r.Host, r.URL)
			}
			for _, key := range []string{"Cookie", "Authorization", "Auriga-Identity", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Auriga-Upstream", "X-Auriga-Server-Name", "X-Tenant-ID", "X-Audience"} {
				if r.Header.Get(key) != "" {
					t.Errorf("sensitive header forwarded: %s", key)
				}
			}
			upgrades.Add(1)
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer func() { _ = ws.Close() }()
			if err := ws.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Error(err)
				return
			}
			if err := websocket.Message.Send(ws, "broker-ready"); err != nil {
				t.Error(err)
				return
			}
			var frame []byte
			if err := websocket.Message.Receive(ws, &frame); err != nil {
				t.Error(err)
				return
			}
			if string(frame) != "client-frame" {
				t.Errorf("client frame=%q", frame)
			}
			if err := websocket.Message.Send(ws, frame); err != nil {
				t.Error(err)
			}
		},
	})
	up.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}}
	up.StartTLS()
	upstreamHost = strings.TrimPrefix(up.URL, "https://")
	t.Cleanup(up.Close)
	n, err := newNetworkWSS(NetworkWSSConfig{Enabled: true, UpstreamURL: strings.Replace(up.URL, "https://", "wss://", 1), CAFile: pki.hop.CAFile, ServerName: pki.hop.ServerName, AllowedOrigins: []string{"https://portal.example.com"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	tr := n.proxy.Transport.(*http.Transport)
	t.Cleanup(tr.CloseIdleConnections)
	if tr.Proxy != nil || tr.TLSClientConfig == nil || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.RootCAs == nil || tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("upstream transport must use dedicated verified TLS without environment proxy")
	}
	edge := httptest.NewServer(n)
	t.Cleanup(edge.Close)
	cfg, err := websocket.NewConfig(strings.Replace(edge.URL, "http://", "ws://", 1)+DefaultNetworkWSSPath+"?client=test", "https://portal.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Cookie", "Authorization", "Auriga-Identity", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Auriga-Upstream", "X-Auriga-Server-Name", "X-Tenant-ID", "X-Audience"} {
		cfg.Header.Set(key, "untrusted.example.com")
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	if err := ws.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var greeting string
	if err := websocket.Message.Receive(ws, &greeting); err != nil || greeting != "broker-ready" {
		t.Fatalf("broker frame=%q err=%v", greeting, err)
	}
	if err := websocket.Message.Send(ws, []byte("client-frame")); err != nil {
		t.Fatal(err)
	}
	var echo []byte
	if err := websocket.Message.Receive(ws, &echo); err != nil || string(echo) != "client-frame" {
		t.Fatalf("echo=%q err=%v", echo, err)
	}
	if upgrades.Load() != 1 {
		t.Fatalf("upgrades=%d", upgrades.Load())
	}
}

func TestNetworkWSSTLSVerificationFailure(t *testing.T) {
	pki, other := createAccessPKI(t), createAccessPKI(t)
	var calls atomic.Int32
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	up.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}}
	up.StartTLS()
	t.Cleanup(up.Close)
	for _, failure := range []string{"foreign-ca", "wrong-server-name", "inferred-server-name", "system-roots"} {
		t.Run(failure, func(t *testing.T) {
			cfg := NetworkWSSConfig{Enabled: true, UpstreamURL: up.URL, CAFile: pki.hop.CAFile, ServerName: pki.hop.ServerName, AllowedOrigins: []string{"https://portal.example.com"}}
			switch failure {
			case "foreign-ca":
				cfg.CAFile = other.hop.CAFile
			case "wrong-server-name":
				cfg.ServerName = "wrong.example.com"
			case "inferred-server-name":
				cfg.ServerName = ""
			case "system-roots":
				cfg.CAFile = ""
			}
			n, err := newNetworkWSS(cfg, "")
			if err != nil {
				t.Fatalf("startup failed before certificate verification: %v", err)
			}
			t.Cleanup(n.proxy.Transport.(*http.Transport).CloseIdleConnections)
			failures := make(chan error, 1)
			original := n.proxy.ErrorHandler
			n.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				failures <- err
				original(w, r, err)
			}
			edge := httptest.NewServer(n)
			t.Cleanup(edge.Close)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(networkWSSUpgradeRequest(t, edge.URL))
			if err != nil {
				t.Fatalf("edge request failed before upstream response: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"code":"edge.upstream_error"`) || calls.Load() != 0 {
				t.Fatalf("TLS rejection: status=%d body=%s upstream HTTP calls=%d", resp.StatusCode, body, calls.Load())
			}
			select {
			case err := <-failures:
				var verification *tls.CertificateVerificationError
				if !errors.As(err, &verification) {
					t.Fatalf("want TLS certificate verification stage, got %T: %v", err, err)
				}
				if failure == "wrong-server-name" || failure == "inferred-server-name" {
					var name x509.HostnameError
					if !errors.As(err, &name) || name.Host == "" || failure == "wrong-server-name" && name.Host != cfg.ServerName || failure == "inferred-server-name" && name.Host != "127.0.0.1" {
						t.Fatalf("want precise hostname rejection, got %v", err)
					}
				} else {
					var authority x509.UnknownAuthorityError
					if !errors.As(err, &authority) {
						t.Fatalf("want unknown authority rejection, got %v", err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing upstream TLS error")
			}
		})
	}
}

func TestNetworkWSSTLSStartupConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(bad, []byte("PRIVATE KEY CONTENT MUST NOT APPEAR IN ERRORS"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, upstream, ca, serverName, want string
	}{
		{"missing-ca", "https://broker.example.com", filepath.Join(dir, "missing.pem"), "", "networkWss.caFile must be a readable regular file"},
		{"bad-ca", "https://broker.example.com", bad, "", "networkWss.caFile must contain PEM certificates"},
		{"directory-ca", "https://broker.example.com", dir, "", "networkWss.caFile must be a readable regular file"},
		{"http-ca", "http://broker.example.com", bad, "", "networkWss TLS settings require an HTTPS/WSS upstream"},
		{"http-name", "ws://broker.example.com", "", "broker.example.com", "networkWss TLS settings require an HTTPS/WSS upstream"},
		{"name-port", "https://broker.example.com", "", "broker.example.com:443", "networkWss.serverName must be a DNS name or IP address without a port"},
		{"name-url", "https://broker.example.com", "", "https://broker.example.com", "networkWss.serverName must be a DNS name or IP address without a port"},
		{"unsupported-scheme", "ftp://broker.example.com", "", "", "networkWss.upstreamUrl must be an HTTP(S)/WS(S) URL without credentials or fragment"},
		{"upstream-credentials", "https://alice:secret@broker.example.com", "", "", "networkWss.upstreamUrl must be an HTTP(S)/WS(S) URL without credentials or fragment"},
		{"missing-host", "https:///", "", "", "networkWss.upstreamUrl must be an HTTP(S)/WS(S) URL without credentials or fragment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := newNetworkWSS(NetworkWSSConfig{Enabled: true, UpstreamURL: tc.upstream, CAFile: tc.ca, ServerName: tc.serverName}, "")
			if n != nil || err == nil || err.Error() != tc.want {
				t.Fatalf("startup result n=%v err=%v, want %q", n, err, tc.want)
			}
		})
	}
}

func TestNetworkWSSTLSConfigFields(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"networkWss":{"enabled":true,"upstreamUrl":"wss://broker.example.com:9443","caFile":"broker-ca.pem","serverName":"broker.example.com"}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.NetworkWSS.CAFile != "broker-ca.pem" || cfg.NetworkWSS.ServerName != "broker.example.com" {
		t.Fatalf("JSON TLS settings not loaded: %+v", cfg.NetworkWSS)
	}
	t.Setenv("AURIGA_EDGE_NETWORK_WSS_CA_FILE", "env-ca.pem")
	t.Setenv("AURIGA_EDGE_NETWORK_WSS_SERVER_NAME", "env-broker.example.com")
	applyEnv(&cfg)
	if cfg.NetworkWSS.CAFile != "env-ca.pem" || cfg.NetworkWSS.ServerName != "env-broker.example.com" {
		t.Fatalf("environment TLS settings not loaded: %+v", cfg.NetworkWSS)
	}
}

func TestNetworkWSSTLSGatewayStartupFailure(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.NetworkWSS = NetworkWSSConfig{Enabled: true, UpstreamURL: "wss://broker.example.com:9443", CAFile: filepath.Join(t.TempDir(), "missing-ca.pem")}
	g, err := NewGateway(cfg, sessions, routes, tickets, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if g != nil || err == nil || err.Error() != "networkWss.caFile must be a readable regular file" {
		t.Fatalf("gateway must reject missing CA before serving: gateway=%v err=%v", g, err)
	}
}

func TestNetworkWSSTLSGatewayGates(t *testing.T) {
	pki := createAccessPKI(t)
	var calls atomic.Int32
	up := httptest.NewUnstartedServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		calls.Add(1)
		if err := ws.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		if err := websocket.Message.Send(ws, "connected"); err != nil {
			t.Error(err)
			return
		}
		var message string
		_ = websocket.Message.Receive(ws, &message)
	}))
	up.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}}
	up.StartTLS()
	t.Cleanup(up.Close)
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.NetworkWSS = NetworkWSSConfig{Enabled: true, UpstreamURL: up.URL, CAFile: pki.hop.CAFile, ServerName: pki.hop.ServerName, MaxConcurrent: 1, AllowedOrigins: []string{"https://" + cfg.PortalHost}}
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	t.Cleanup(g.networkWSS.proxy.Transport.(*http.Transport).CloseIdleConnections)
	edge := httptest.NewServer(g.Handler())
	t.Cleanup(edge.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, tc := range []struct {
		name, host, path, origin, code string
		status                         int
	}{
		{"admin-host", cfg.AdminHost, DefaultNetworkWSSPath, "https://" + cfg.PortalHost, "edge.forbidden", http.StatusForbidden},
		{"missing-origin", cfg.PortalHost, DefaultNetworkWSSPath, "", "edge.bad_origin", http.StatusForbidden},
		{"foreign-origin", cfg.PortalHost, DefaultNetworkWSSPath, "https://evil.example.com", "edge.bad_origin", http.StatusForbidden},
		{"wrong-origin-port", cfg.PortalHost, DefaultNetworkWSSPath, "https://" + cfg.PortalHost + ":444", "edge.bad_origin", http.StatusForbidden},
		{"path-prefix", cfg.PortalHost, "/net/v1/w", "https://" + cfg.PortalHost, "", http.StatusOK},
		{"path-suffix", cfg.PortalHost, DefaultNetworkWSSPath + "-other", "https://" + cfg.PortalHost, "", http.StatusOK},
		{"path-child", cfg.PortalHost, DefaultNetworkWSSPath + "/child", "https://" + cfg.PortalHost, "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := networkWSSUpgradeRequest(t, edge.URL)
			r.Host, r.URL.Path = tc.host, tc.path
			r.Header.Set("Origin", tc.origin)
			resp, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status || tc.code != "" && !strings.Contains(string(body), `"code":"`+tc.code+`"`) || tc.code == "" && string(body) != "portal" || calls.Load() != 0 {
				t.Fatalf("gate status=%d body=%s upstream calls=%d", resp.StatusCode, body, calls.Load())
			}
		})
	}
	r := networkWSSUpgradeRequest(t, edge.URL)
	r.Host = cfg.PortalHost
	first, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Body.Close() }()
	if first.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("portal upgrade status=%d", first.StatusCode)
	}
	frame := make([]byte, 11)
	if _, err := io.ReadFull(first.Body, frame); err != nil || string(frame) != "\x81\x09connected" {
		t.Fatalf("portal WebSocket frame=%q err=%v", frame, err)
	}
	r = networkWSSUpgradeRequest(t, edge.URL)
	r.Host = cfg.PortalHost
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"code":"edge.overloaded"`) || calls.Load() != 1 {
		t.Fatalf("TLS concurrency limit status=%d body=%s upstream calls=%d", resp.StatusCode, body, calls.Load())
	}
}
