// Bootstrap generates PKI and identity keys for the Auriga NAS test stack.
// It does NOT emit fake DSH templates or CI-style unsigned placeholders.
//
//	go run . -out /path/to/config
package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("out", "", "config output directory (…/config)")
	mode := flag.String("mode", "full", "full | keys-only | pki-only")
	flag.Parse()
	if *out == "" {
		fatalf("-out is required")
	}
	mustMkdir(*out, 0o755)
	pki := filepath.Join(*out, "pki")
	mustMkdir(pki, 0o755)

	switch *mode {
	case "keys-only":
		mustIdentity(*out)
	case "pki-only":
		mustPKI(pki)
	case "full":
		mustPKI(pki)
		mustIdentity(*out)
	default:
		fatalf("unknown -mode %q", *mode)
	}
	fmt.Printf("nas-bootstrap ok out=%s mode=%s\n", *out, *mode)
}

func mustPKI(pki string) {
	if fileExists(filepath.Join(pki, "ca.key")) || fileExists(filepath.Join(pki, "ca.crt")) {
		fatalf("PKI already present under %s; refusing to overwrite", pki)
	}
	caKey, caCert, caPEM := mustCA()
	writeFile(filepath.Join(pki, "ca.crt"), caPEM, 0o644)
	writeFile(filepath.Join(pki, "ca.key"), pkcs8PEM(caKey), 0o600)

	wsCert, wsKey := mustIssue(caKey, caCert, "workspace", "auriga://workspace",
		[]string{"platform", "localhost"},
		[]net.IP{net.ParseIP("127.0.0.1")},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	writeFile(filepath.Join(pki, "workspace.crt"), append(wsCert, caPEM...), 0o644)
	writeFile(filepath.Join(pki, "workspace.key"), wsKey, 0o600)

	edgeCert, edgeKey := mustIssue(caKey, caCert, "edge-nas-test", "auriga://edge/nas-test",
		nil, nil,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	writeFile(filepath.Join(pki, "edge.crt"), edgeCert, 0o644)
	// 0600 + chown 65532 by init.sh so distroless edge can read without world-readable keys.
	writeFile(filepath.Join(pki, "edge.key"), edgeKey, 0o600)

	fp := spkiSHA256(caCert)
	writeFile(filepath.Join(filepath.Dir(pki), "ca-sha256.txt"), []byte(fp+"\n"), 0o644)
}

func mustIdentity(out string) {
	privPath := filepath.Join(out, "identity-signing-keys.json")
	pubPath := filepath.Join(out, "identity-keys.json")
	if fileExists(privPath) || fileExists(pubPath) {
		fatalf("identity keys already present under %s; refusing to overwrite", out)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	kid := "nas-id-1"
	signing := map[string]any{
		"keys": []map[string]any{{
			"kty":   "OKP",
			"crv":   "Ed25519",
			"x":     base64.RawURLEncoding.EncodeToString(pub),
			"d":     base64.RawURLEncoding.EncodeToString(priv.Seed()),
			"kid":   kid,
			"alg":   "EdDSA",
			"use":   "sig",
			"state": "CURRENT",
		}},
	}
	pubOnly := map[string]any{
		"keys": []map[string]any{{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(pub),
			"kid": kid,
			"alg": "EdDSA",
			"use": "sig",
		}},
	}
	writeJSON(privPath, signing, 0o600)
	writeJSON(pubPath, pubOnly, 0o644)
}

func mustCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	uri, _ := url.Parse("auriga://workspace")
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "auriga-nas-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"platform", "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		fatalf("%v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		fatalf("%v", err)
	}
	return key, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func mustIssue(caKey *ecdsa.PrivateKey, ca *x509.Certificate, cn, uriSAN string, dns []string, ips []net.IP, eku []x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatalf("%v", err)
	}
	uri, err := url.Parse(uriSAN)
	if err != nil {
		fatalf("%v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		fatalf("%v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  eku,
		DNSNames:     dns,
		IPAddresses:  ips,
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		fatalf("%v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pkcs8PEM(key)
}

func pkcs8PEM(key *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		fatalf("%v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func spkiSHA256(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, v any, mode os.FileMode) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatalf("%v", err)
	}
	writeFile(path, append(raw, '\n'), mode)
}

func writeFile(path string, b []byte, mode os.FileMode) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		fatalf("%v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		fatalf("%v", err)
	}
	_ = os.Chmod(path, mode)
}

func mustMkdir(path string, mode os.FileMode) {
	if err := os.MkdirAll(path, mode); err != nil {
		fatalf("%v", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
