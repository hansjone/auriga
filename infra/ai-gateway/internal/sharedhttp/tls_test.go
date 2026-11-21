package sharedhttp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type tlsFixture struct {
	files TLSFiles
	root  *x509.Certificate
	key   *ecdsa.PrivateKey
	caPEM []byte
	dir   string
}

func newTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	f := &tlsFixture{dir: t.TempDir()}
	var err error
	f.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "shared transport fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &f.key.PublicKey, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	f.caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caPath := filepath.Join(f.dir, "ca.pem")
	if err = os.WriteFile(caPath, f.caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	_, cert, key := f.issue(t, "server", true)
	f.files = TLSFiles{TLSCertFile: cert, TLSKeyFile: key, ClientCAFile: caPath}
	return f
}
func (f *tlsFixture) issue(t *testing.T, uri string, server bool) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "fixture peer"}, NotBefore: f.root.NotBefore, NotAfter: f.root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		cert.DNSNames = []string{"example.com"}
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		u, err := url.Parse(uri)
		if err != nil {
			t.Fatal(err)
		}
		cert.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, f.root, &key.PublicKey, f.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(f.dir, serial.String()+".pem")
	keyPath := filepath.Join(f.dir, serial.String()+"-key.pem")
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	return pair, certPath, keyPath
}
func TestSharedModelTLSRequiresExactNodeCertificate(t *testing.T) {
	f := newTLSFixture(t)
	node := uuid.NewString()
	serverTLS, err := TLSConfig(f.files, []string{node})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(f.caPEM) {
		t.Fatal("missing fixture CA")
	}
	otherCA := newTLSFixture(t)
	for _, name := range []string{"allowed", "other-node", "edge", "wrong-ca", "none"} {
		t.Run(name, func(t *testing.T) {
			var certs []tls.Certificate
			switch name {
			case "allowed":
				p, _, _ := f.issue(t, "auriga://node/"+node, false)
				certs = []tls.Certificate{p}
			case "other-node":
				p, _, _ := f.issue(t, "auriga://node/"+uuid.NewString(), false)
				certs = []tls.Certificate{p}
			case "edge":
				p, _, _ := f.issue(t, "auriga://edge", false)
				certs = []tls.Certificate{p}
			case "wrong-ca":
				p, _, _ := otherCA.issue(t, "auriga://node/"+node, false)
				certs = []tls.Certificate{p}
			}
			tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "example.com", Certificates: certs}}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(req)
			if name == "allowed" {
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				if response.StatusCode != 204 {
					t.Fatal(response.StatusCode)
				}
			} else if err == nil {
				_ = response.Body.Close()
				t.Fatal("untrusted peer connected")
			}
		})
	}
}
func TestSharedModelTLSRejectsIncompleteOrNonCACredentials(t *testing.T) {
	f := newTLSFixture(t)
	node := uuid.NewString()
	for _, c := range []TLSFiles{{}, {TLSCertFile: f.files.TLSCertFile, TLSKeyFile: f.files.TLSKeyFile}, {TLSCertFile: f.files.TLSCertFile, TLSKeyFile: f.files.TLSKeyFile, ClientCAFile: f.files.TLSCertFile}} {
		if _, err := TLSConfig(c, []string{node}); err == nil {
			t.Fatal("incomplete or non-CA credentials accepted")
		}
	}
	for _, nodes := range [][]string{nil, {node, node}, {uuid.Nil.String()}, {"forged"}} {
		if _, err := TLSConfig(f.files, nodes); err == nil {
			t.Fatal("invalid node allowlist accepted")
		}
	}
}
