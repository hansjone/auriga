//go:build ignore

// 合成内部证书链夹具：只输出公有证书，所有私钥仅存于进程内存。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"time"
)

func main() {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var chains []map[string]string
	for n := int64(1); n <= 2; n++ {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		ca := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: "synthetic-msgnet-ca"}, NotBefore: start.Add(-300 * time.Second), NotAfter: start.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
		if err != nil {
			panic(err)
		}
		leafPub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(n + 10), Subject: pkix.Name{CommonName: "synthetic-submit"}, DNSNames: []string{"submit.example.com"}, NotBefore: start.Add(-300 * time.Second), NotAfter: start.AddDate(0, 0, 90), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, leafPub, key)
		if err != nil {
			panic(err)
		}
		chains = append(chains, map[string]string{"ca_id": "synthetic-ca-" + big.NewInt(n).String(), "ca_der": base64.RawURLEncoding.EncodeToString(der), "leaf_der": base64.RawURLEncoding.EncodeToString(leafDER)})
	}
	data, err := json.MarshalIndent(chains, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile("contracts/network/fixtures/msgnet-transport-public.json", append(data, '\n'), 0600); err != nil {
		panic(err)
	}
}
