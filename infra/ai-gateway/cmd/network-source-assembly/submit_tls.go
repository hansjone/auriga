package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
)

type submitClientMaterial struct {
	ClientCAFile, ClientCertFile, ClientKeyFile string
}

func (m submitClientMaterial) listenerConfig(listen, cert, key string) *network.SubmitHTTPConfig {
	return &network.SubmitHTTPConfig{Listen: listen, TLSCertFile: cert, TLSKeyFile: key, ClientCAFile: m.ClientCAFile}
}

// The private per-run T3a directory already exists. This CA only issues the
// driver/edge client identity; it never trusts IAM or the Broker server CA.
func submitClientFiles(dir string) (submitClientMaterial, error) {
	var result submitClientMaterial
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return result, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return result, err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "assembly submit client CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return result, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return result, err
	}
	serial, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return result, err
	}
	client := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "assembly submit driver"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, client, ca, &key.PublicKey, caKey)
	if err != nil {
		return result, err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return result, err
	}
	defer clear(pk)
	write := func(name, kind string, raw []byte) (string, error) {
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", err
		}
		err = pem.Encode(file, &pem.Block{Type: kind, Bytes: raw})
		closed := file.Close()
		if err != nil {
			return "", err
		}
		if closed != nil {
			return "", closed
		}
		return path, nil
	}
	if result.ClientCAFile, err = write("submit-client-ca.pem", "CERTIFICATE", caDER); err != nil {
		return result, err
	}
	if result.ClientCertFile, err = write("submit-client.pem", "CERTIFICATE", der); err != nil {
		return result, err
	}
	result.ClientKeyFile, err = write("submit-client-key.pem", "PRIVATE KEY", pk)
	return result, err
}
