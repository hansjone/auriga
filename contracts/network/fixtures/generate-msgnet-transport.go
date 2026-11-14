//go:build ignore

// 合成内部证书链夹具：只输出公有证书，所有私钥仅存于进程内存。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"time"
)

func main() {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var chains []map[string]string
	for n := int64(1); n <= 2; n++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("Auriga synthetic CA test only %d", n)))
		key := ed25519.NewKeyFromSeed(seed[:])
		pub := key.Public()
		ca := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: "synthetic-msgnet-ca"}, NotBefore: start.Add(-300 * time.Second), NotAfter: start.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(nil, ca, ca, pub, key)
		if err != nil {
			panic(err)
		}
		leafSeed := sha256.Sum256([]byte(fmt.Sprintf("Auriga synthetic leaf test only %d", n)))
		leafPub := ed25519.NewKeyFromSeed(leafSeed[:]).Public()
		leaf := &x509.Certificate{SerialNumber: big.NewInt(n + 10), Subject: pkix.Name{CommonName: "synthetic-submit"}, DNSNames: []string{"submit.example.com"}, NotBefore: start.Add(-300 * time.Second), NotAfter: start.AddDate(0, 0, 90), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		leafDER, err := x509.CreateCertificate(nil, leaf, ca, leafPub, key)
		if err != nil {
			panic(err)
		}
		chains = append(chains, map[string]string{"ca_id": "synthetic-ca-" + big.NewInt(n).String(), "ca_der": base64.RawURLEncoding.EncodeToString(der), "leaf_der": base64.RawURLEncoding.EncodeToString(leafDER)})
	}
	data, err := json.MarshalIndent(chains, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	path := "contracts/network/fixtures/msgnet-transport-public.json"
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		prior, err := os.ReadFile(path)
		if err != nil || string(prior) != string(data) {
			panic("transport vectors drift")
		}
		return
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		panic(err)
	}
}
