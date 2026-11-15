package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestMsgnetSubmitTLSRequiresVerifiedSnapshotSource(t *testing.T) {
	if e := configureMsgnetSubmitTLS(&http.Transport{}, nil); e == nil {
		t.Fatal("missing verifier accepted")
	}
	if e := configureMsgnetSubmitTLS(&http.Transport{}, func() (*tls.Config, error) { return &tls.Config{InsecureSkipVerify: true}, nil }); e == nil {
		t.Fatal("unsafe snapshot accepted")
	}
}

func TestMsgnetSubmitTLSRotatesDualRootsAndClientLeaf(t *testing.T) {
	first, second := createAccessPKI(t), createAccessPKI(t)
	var active atomic.Pointer[tls.Config]
	active.Store(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{first.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: first.roots})
	var clientSPKI atomic.Pointer[x509.Certificate]
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("unverified message client")
		}
		clientSPKI.Store(r.TLS.PeerCertificates[0])
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return active.Load().Clone(), nil }}
	server.StartTLS()
	defer server.Close()
	tr := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
	if e := configureMsgnetSubmitTLS(tr, func() (*tls.Config, error) { return iamTLS(first.hop, first.hop.ServerName) }); e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	request := func() {
		t.Helper()
		resp, e := client.Get(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatal(resp.Status)
		}
	}
	request()
	oldKey := append([]byte(nil), clientSPKI.Load().RawSubjectPublicKeyInfo...)
	oldCA, e := os.ReadFile(first.hop.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	newCA, e := os.ReadFile(second.hop.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(first.hop.CAFile, append(oldCA, newCA...), 0600); e != nil {
		t.Fatal(e)
	}
	dual := x509.NewCertPool()
	if !dual.AppendCertsFromPEM(append(oldCA, newCA...)) {
		t.Fatal("dual root fixture")
	}
	active.Store(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{second.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: dual})
	request() // New server CA with the old client identity, during dual trust.
	for destination, source := range map[string]string{first.hop.CertFile: second.hop.CertFile, first.hop.KeyFile: second.hop.KeyFile} {
		data, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(destination, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(first.hop.CAFile, newCA, 0600); e != nil {
		t.Fatal(e)
	}
	active.Store(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{second.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: second.roots})
	request()
	if string(clientSPKI.Load().RawSubjectPublicKeyInfo) == string(oldKey) {
		t.Fatal("old client leaf remained active")
	}
	if e = os.WriteFile(first.hop.KeyFile, []byte("partial replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	if response, e := client.Get(server.URL); e == nil {
		_ = response.Body.Close()
		t.Fatal("verifier failure reused an older trust snapshot")
	}
}

func TestMsgnetSubmitTLSColdProjectionRecoversWithoutRestart(t *testing.T) {
	pki := createAccessPKI(t)
	certificate, e := os.ReadFile(pki.hop.CertFile)
	if e != nil {
		t.Fatal(e)
	}
	clientPair, e := tls.LoadX509KeyPair(pki.hop.CertFile, pki.hop.KeyFile)
	if e != nil {
		t.Fatal(e)
	}
	expired, e := x509.ParseCertificate(clientPair.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	expired.NotBefore, expired.NotAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	der, e := x509.CreateCertificate(rand.Reader, expired, expired, expired.PublicKey, clientPair.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(pki.hop.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pki.roots}
	server.StartTLS()
	defer server.Close()
	tr := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
	if e = configureMsgnetSubmitTLS(tr, func() (*tls.Config, error) { return iamTLS(pki.hop, pki.hop.ServerName) }); e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	if response, e := client.Get(server.URL); e == nil {
		_ = response.Body.Close()
		t.Fatal("cold projection allowed HTTP")
	}
	if calls.Load() != 0 {
		t.Fatal("pending connection reached submit")
	}
	if e = os.WriteFile(pki.hop.CertFile, certificate, 0600); e != nil {
		t.Fatal(e)
	}
	response, e := client.Get(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	_ = response.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("projection did not recover")
	}
}
