package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verify the generated independent trust material, rather than test fixture PKI.
// Production edge request/peer/URI checks also run in infra/edge's real TLS tests.
func TestNASGeneratedProxyTrust(t *testing.T) {
	out := t.TempDir()
	pki := filepath.Join(out, "pki")
	if err := os.Mkdir(pki, 0o755); err != nil {
		t.Fatal(err)
	}
	mustPKI(pki)
	readCert := func(name string) *x509.Certificate {
		raw, err := os.ReadFile(filepath.Join(pki, name+".crt"))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatal("missing PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	ca := readCert("proxy-ca")
	iamCA := readCert("ca")
	if ca.Equal(iamCA) {
		t.Fatal("front proxy shares IAM/NodeControl CA")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	proxy := readCert("proxy-client")
	if len(proxy.URIs) != 1 || proxy.URIs[0].String() != "auriga://proxy/nas-test" || len(proxy.DNSNames) != 0 {
		t.Fatal("wrong proxy client identity")
	}
	if _, err := proxy.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	server := readCert("proxy-edge")
	if _, err := server.Verify(x509.VerifyOptions{Roots: roots, DNSName: "edge.auriga.internal", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal(err)
	}
	serverKey, err := tls.LoadX509KeyPair(filepath.Join(pki, "proxy-edge.crt"), filepath.Join(pki, "proxy-edge.key"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverKey}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, name := range []string{"proxy-client", "missing", "edge-iam"} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				c, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				err = c.(*tls.Conn).Handshake()
				if err == nil {
					_, err = c.Write([]byte{1})
				}
				done <- err
			}()
			config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "edge.auriga.internal"}
			if name != "missing" {
				cert, err := tls.LoadX509KeyPair(filepath.Join(pki, name+".crt"), filepath.Join(pki, name+".key"))
				if err != nil {
					t.Fatal(err)
				}
				config.Certificates = []tls.Certificate{cert}
			}
			client, err := tls.Dial("tcp", listener.Addr().String(), config)
			if err == nil {
				client.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 1)
				_, err = client.Read(b)
				client.Close()
			}
			serverErr := <-done
			if name == "proxy-client" && (err != nil || serverErr != nil) {
				t.Fatalf("valid proxy rejected: %v %v", err, serverErr)
			}
			if name != "proxy-client" && (err == nil || serverErr == nil) {
				t.Fatal("missing/wrong trust client accepted")
			}
		})
	}
}
