//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestMsgnetPrivateIsolationListenerRequiresOwnedFiles(t *testing.T) {
	g, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	g.DevicesRPC = deviceBindingsFunc(func(context.Context, string) ([]deviceBinding, error) { return nil, nil })
	pki := createAccessPKI(t)
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		file := filepath.Join(directory, name)
		if e := os.WriteFile(file, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return file
	}
	der, e := x509.MarshalPKCS8PrivateKey(pki.server.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	key := write("server.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	cert := write("server.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pki.server.Certificate[0]}))
	clientTLS, e := iamTLS(pki.hop, pki.hop.ServerName)
	if e != nil {
		t.Fatal(e)
	}
	client, e := x509.ParseCertificate(clientTLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	targets, e := trust.Bytes([]msgnetIsolationTarget{{Tenant: g.Cfg.Access.TenantID(), Realm: "1", Target: "empty-node", Devices: []string{}}})
	if e != nil {
		t.Fatal(e)
	}
	cfg := MsgnetIsolationConfig{Listen: "127.0.0.1:0", CertFile: cert, KeyFile: key, CAFile: pki.hop.CAFile, CallerSPKI: base64.RawURLEncoding.EncodeToString(client.RawSubjectPublicKeyInfo), TargetsFile: write("targets.json", targets), SchemaFile: "../../contracts/network/auto-renewal-v2.schema.json"}
	if e = os.Chmod(key, 0644); e != nil {
		t.Fatal(e)
	}
	if _, _, e = newMsgnetIsolation(g, cfg); e == nil {
		t.Fatal("shared execution TLS key accepted")
	}
	if e = os.Chmod(key, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(cfg.TargetsFile, 0644); e != nil {
		t.Fatal(e)
	}
	if _, _, e = newMsgnetIsolation(g, cfg); e == nil {
		t.Fatal("shared target mapping accepted")
	}
	if e = os.Chmod(cfg.TargetsFile, 0600); e != nil {
		t.Fatal(e)
	}
	handler, serverTLS, e := newMsgnetIsolation(g, cfg)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", cfg.Listen)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(tls.NewListener(listener, serverTLS)) }()
	defer func() {
		_ = server.Close()
		if serveError := <-done; serveError != nil && !errors.Is(serveError, http.ErrServerClosed) {
			t.Error(serveError)
		}
	}()
	transport := &http.Transport{TLSClientConfig: clientTLS, Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	raw, e := trust.Bytes(api.IsolationRecord{TenantId: g.Cfg.Access.TenantID(), Realm: "1", TargetId: "empty-node", MinimumControlRevision: "1", ControlDigest: "sha256:" + strings.Repeat("0", 64), State: "quarantined", Challenge: "challenge"})
	if e != nil {
		t.Fatal(e)
	}
	request, e := http.NewRequest(http.MethodPost, "https://"+listener.Addr().String()+"/api/collab/v2/network/isolation", bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	response, e := httpClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("private listener status %d", response.StatusCode)
	}
}
