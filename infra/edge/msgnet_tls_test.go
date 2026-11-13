package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"github.com/nats-io/nats.go"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func replaceTLSFiles(t *testing.T, cfg TLSConfig, pki accessPKI) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(pki.server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for file, raw := range map[string][]byte{cfg.CertFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pki.server.Certificate[0]}), cfg.KeyFile: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})} {
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMsgnetTLSNewHandshakesUseProjectedLeaf(t *testing.T) {
	first, second := createAccessPKI(t), createAccessPKI(t)
	files := writeAccessServerTLS(t, first)
	config, err := reloadingServerTLS(files.CertFile, files.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	request := func(pki accessPKI) error {
		client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: pki.hop.ServerName, RootCAs: pki.roots}}}
		defer client.CloseIdleConnections()
		resp, err := client.Get(server.URL)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		return err
	}
	if err := request(first); err != nil {
		t.Fatal(err)
	}
	replaceTLSFiles(t, files, second)
	if err := request(second); err != nil {
		t.Fatal("new leaf not loaded:", err)
	}
	if err := request(first); err == nil {
		t.Fatal("retired CA still accepted")
	}
	if err := os.WriteFile(files.KeyFile, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := request(second); err != nil {
		t.Fatal("valid committed leaf lost on incomplete replacement:", err)
	}
	if _, err := reloadingServerTLS(files.CertFile, files.KeyFile); err == nil {
		t.Fatal("invalid initial projection accepted")
	}
	replaceTLSFiles(t, files, second)
	if err := request(second); err != nil {
		t.Fatal("material repair did not recover:", err)
	}
}

func TestMsgnetWSSUpstreamRotatesDualTrustWithoutRestart(t *testing.T) {
	first, second := createAccessPKI(t), createAccessPKI(t)
	files := writeAccessServerTLS(t, first)
	config, err := reloadingServerTLS(files.CertFile, files.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewUnstartedServer(websocket.Handler(func(ws *websocket.Conn) { _ = ws.Close() }))
	upstream.TLS = config
	upstream.StartTLS()
	defer upstream.Close()
	n, err := newNetworkWSS(NetworkWSSConfig{Enabled: true, UpstreamURL: upstream.URL, CAFile: first.hop.CAFile, ServerName: first.hop.ServerName, AllowedOrigins: []string{"https://portal.example.com"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(n)
	defer edge.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	request := func(want int) {
		t.Helper()
		resp, err := client.Do(networkWSSUpgradeRequest(t, edge.URL))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("upgrade=%d want=%d", resp.StatusCode, want)
		}
	}
	request(101)
	replaceTLSFiles(t, files, second)
	request(502)
	oldCA, err := os.ReadFile(first.hop.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	newCA, err := os.ReadFile(second.hop.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first.hop.CAFile, append(append([]byte(nil), oldCA...), newCA...), 0600); err != nil {
		t.Fatal(err)
	}
	request(101)
	if err := os.WriteFile(first.hop.CAFile, newCA, 0600); err != nil {
		t.Fatal(err)
	}
	request(101)
	if err := os.WriteFile(first.hop.CAFile, []byte("incomplete CA projection"), 0600); err != nil {
		t.Fatal(err)
	}
	request(101)
	replaceTLSFiles(t, files, first)
	request(502)
}

func TestMsgnetPresenceTLSCallbacksReloadClientAndRoots(t *testing.T) {
	first, second := createAccessPKI(t), createAccessPKI(t)
	files := writeAccessServerTLS(t, first)
	config, err := reloadingServerTLS(files.CertFile, files.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	var clientRoots atomic.Pointer[x509.CertPool]
	clientRoots.Store(first.roots)
	reload := config.GetConfigForClient
	config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		current, err := reload(hello)
		if err != nil {
			return nil, err
		}
		current.ClientCAs = clientRoots.Load()
		current.ClientAuth = tls.RequireAndVerifyClientCert
		return current, nil
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("missing verified client identity")
		}
		w.WriteHeader(204)
	}))
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	option, err := reloadingPresenceTLS(DevicePresenceConfig{CAFile: first.hop.CAFile, CertFile: first.hop.CertFile, KeyFile: first.hop.KeyFile, ServerName: first.hop.ServerName})
	if err != nil {
		t.Fatal(err)
	}
	options := nats.Options{}
	if err := option(&options); err != nil {
		t.Fatal(err)
	}
	request := func() {
		t.Helper()
		cert, err := options.TLSCertCB()
		if err != nil {
			t.Fatal(err)
		}
		roots, err := options.RootCAsCB()
		if err != nil {
			t.Fatal(err)
		}
		cfg := options.TLSConfig.Clone()
		cfg.RootCAs = roots
		cfg.Certificates = []tls.Certificate{cert}
		client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: cfg}}
		defer client.CloseIdleConnections()
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	request()
	for dst, src := range map[string]string{first.hop.CAFile: second.hop.CAFile, first.hop.CertFile: second.hop.CertFile, first.hop.KeyFile: second.hop.KeyFile} {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	replaceTLSFiles(t, files, second)
	clientRoots.Store(second.roots)
	request()
	if err := os.WriteFile(first.hop.KeyFile, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := options.TLSCertCB(); err != nil {
		t.Fatal("valid committed client leaf lost:", err)
	}
	request()
	if _, err := reloadingPresenceTLS(DevicePresenceConfig{CAFile: first.hop.CAFile, CertFile: first.hop.CertFile, KeyFile: first.hop.KeyFile}); err == nil {
		t.Fatal("invalid initial client projection accepted")
	}
}
