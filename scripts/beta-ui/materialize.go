// Generate installation keys and a private local CA without installing certificates in Windows.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	if err := generate(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "cannot generate private Beta installation material")
		os.Exit(1)
	}
}

func generate(dir string) error {
	write := func(name string, data []byte) error {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		closed := f.Close()
		if err != nil {
			return err
		}
		return closed
	}
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120)); return n }
	caKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Auriga Beta local CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err = write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return err
	}
	// CA private key stays in memory and is discarded after issuing the fixed local identities.
	for _, name := range []string{"platform", "edge", "ai-gateway"} {
		key, keyErr := rsa.GenerateKey(rand.Reader, 3072)
		if keyErr != nil {
			return keyErr
		}
		cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name + ".beta.internal"},
			DNSNames: []string{name + ".beta.internal"}, NotBefore: now.Add(-time.Minute), NotAfter: ca.NotAfter,
			KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		encoded, certErr := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if certErr != nil {
			return certErr
		}
		if err = write(name+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})); err != nil {
			return err
		}
		pkcs8, keyErr := x509.MarshalPKCS8PrivateKey(key)
		if keyErr != nil {
			return keyErr
		}
		if err = write(name+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err != nil {
			return err
		}
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	identity, err := json.Marshal(map[string]any{"keys": []any{map[string]string{
		"kty": "OKP", "crv": "Ed25519", "x": b64(pub), "d": b64(key.Seed()), "kid": "beta-edge", "alg": "EdDSA", "state": "CURRENT"}}})
	if err != nil {
		return err
	}
	return write("identity.json", identity)
}
