package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubmitRuntimeMTLS(t *testing.T) {
	dir := t.TempDir()
	issue := func(name string, parent *x509.Certificate, signer *ecdsa.PrivateKey, ca bool, usage x509.ExtKeyUsage) (*x509.Certificate, *ecdsa.PrivateKey) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: ca, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if ca {
			cert.KeyUsage |= x509.KeyUsageCertSign
			cert.ExtKeyUsage = nil
			parent, signer = cert, key
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, parent, &key.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		cert, err = x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		pk, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		for suffix, block := range map[string]*pem.Block{".pem": {Type: "CERTIFICATE", Bytes: der}, ".key": {Type: "PRIVATE KEY", Bytes: pk}} {
			if err := os.WriteFile(filepath.Join(dir, name+suffix), pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return cert, key
	}
	serverCA, serverKey := issue("submit-server-ca", nil, nil, true, 0)
	clientCA, clientKey := issue("submit-client-ca", nil, nil, true, 0)
	wrongCA, wrongKey := issue("unrelated-ca", nil, nil, true, 0)
	iamCA, iamKey := issue("iam-ca", nil, nil, true, 0)
	issue("submit.internal", serverCA, serverKey, false, x509.ExtKeyUsageServerAuth)
	issue("edge", clientCA, clientKey, false, x509.ExtKeyUsageClientAuth)
	issue("wrong", wrongCA, wrongKey, false, x509.ExtKeyUsageClientAuth)
	issue("iam-edge", iamCA, iamKey, false, x509.ExtKeyUsageClientAuth)
	issue("server-only", clientCA, clientKey, false, x509.ExtKeyUsageServerAuth)
	for _, name := range []string{"expired-ca", "future-ca"} {
		cert := *clientCA
		if name == "expired-ca" {
			cert.NotBefore = time.Now().Add(-2 * time.Hour)
			cert.NotAfter = time.Now().Add(-time.Hour)
		} else {
			cert.NotBefore = time.Now().Add(time.Hour)
			cert.NotAfter = time.Now().Add(2 * time.Hour)
		}
		der, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &clientKey.PublicKey, clientKey)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := func(ca string) *SubmitHTTPConfig {
		return &SubmitHTTPConfig{Listen: "127.0.0.1:0", TLSCertFile: filepath.Join(dir, "submit.internal.pem"), TLSKeyFile: filepath.Join(dir, "submit.internal.key"), ClientCAFile: ca}
	}
	for _, ca := range []string{"", filepath.Join(dir, "missing"), filepath.Join(dir, "edge.key"), filepath.Join(dir, "edge.pem"), filepath.Join(dir, "expired-ca.pem"), filepath.Join(dir, "future-ca.pem")} {
		r := &Runtime{}
		if err := r.startSubmitHTTP(context.Background(), config(ca)); err == nil {
			r.Close()
			t.Fatalf("invalid client CA accepted: %q", ca)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config(filepath.Join(dir, "submit-client-ca.pem"))
	cfg.Listen = address
	r := &Runtime{mailbox: &mailboxRouter{}}
	if err := r.startSubmitHTTP(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.submitHTTP.ReadTimeout != 30*time.Second || r.submitHTTP.WriteTimeout != 30*time.Second {
		t.Fatal("submit listener does not cover the mailbox RPC budget")
	}
	roots := x509.NewCertPool()
	roots.AddCert(serverCA)
	for _, name := range []string{"", "wrong", "iam-edge", "server-only", "edge"} {
		t.Run(name, func(t *testing.T) {
			c := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "submit.internal"}
			if name != "" {
				pair, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key"))
				if err != nil {
					t.Fatal(err)
				}
				// Always present the certificate, including when its issuer is not
				// advertised, to exercise server-side verification of wrong CAs.
				c.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil }
			}
			transport := &http.Transport{TLSClientConfig: c, Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Post("https://"+address+SubmitPath, "application/json", nil)
			if name != "edge" {
				if err == nil {
					_ = response.Body.Close()
					t.Fatal("untrusted client reached HTTP handler")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "network.unauthorized") {
				t.Fatalf("real SubmitHandler not reached: %d %s", response.StatusCode, body)
			}
		})
	}
}
