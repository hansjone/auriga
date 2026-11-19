package sharedhttp

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"time"
)

type TLSFiles struct {
	TLSCertFile  string `json:"tlsCertFile"`
	TLSKeyFile   string `json:"tlsKeyFile"`
	ClientCAFile string `json:"clientCAFile"`
}

// TLSConfig accepts only verified Node URI identities from a deployment-owned
// client CA. There is no plaintext or platform JWT fallback, including loopback.
func TLSConfig(files TLSFiles, nodeIDs []string) (*tls.Config, error) {
	nodes, err := nodeSet(nodeIDs)
	if err != nil || files.TLSCertFile == "" || files.TLSKeyFile == "" || files.ClientCAFile == "" {
		return nil, errors.New("shared model listener requires TLS keypair, Node client CA and exact node allowlist")
	}
	file, err := os.Open(files.ClientCAFile)
	if err != nil {
		return nil, errors.New("shared model client CA unavailable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("invalid shared model client CA")
	}
	roots := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(raw)) > 0 {
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("invalid shared model client CA")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, errors.New("invalid shared model client CA")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		now := time.Now()
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, errors.New("invalid shared model client CA")
		}
		roots.AddCert(cert)
		count++
		raw = rest
	}
	if count == 0 {
		return nil, errors.New("invalid shared model client CA")
	}
	pair, err := tls.LoadX509KeyPair(files.TLSCertFile, files.TLSKeyFile)
	if err != nil {
		return nil, errors.New("shared model TLS keypair unavailable")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pair},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			if !nodePeer(&state, nodes) {
				return errors.New("shared model peer is not an authorized Node")
			}
			return nil
		},
	}, nil
}
