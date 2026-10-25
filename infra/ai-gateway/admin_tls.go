package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func validateAdminTLS(cfg adminConfig) error {
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return errors.New("invalid AI admin listener")
	}
	configured := cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.TLSClientCAFile != "" || cfg.TLSEdgeDNSNames != nil
	if !configured {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("non-loopback AI admin listener requires TLS with client CA and edge DNS identities")
		}
		return nil
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" || cfg.TLSClientCAFile == "" || len(cfg.TLSEdgeDNSNames) == 0 {
		return errors.New("AI admin TLS requires server certificate, key, client CA and edge DNS identities")
	}
	for _, name := range cfg.TLSEdgeDNSNames {
		if !adminDNSIdentity(name) {
			return errors.New("invalid AI admin edge DNS identity")
		}
	}
	return nil
}

// Identities are literal, canonical DNS SANs, never wildcard hostname patterns.
func adminDNSIdentity(name string) bool {
	if name == "" || len(name) > 253 || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
				return false
			}
		}
	}
	return true
}

// Construct the actual production server before opening the store or listener.
func newAdminServer(cfg adminConfig, handler http.Handler) (*http.Server, error) {
	if err := validateAdminTLS(cfg); err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	if cfg.TLSCertFile == "" {
		return server, nil
	}
	certPEM, err := readAdminTLSFile(cfg.TLSCertFile)
	if err != nil {
		return nil, err
	}
	keyPEM, err := readAdminTLSFile(cfg.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(keyPEM)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.New("invalid AI admin TLS certificate/key pair")
	}
	caPEM, err := readAdminTLSFile(cfg.TLSClientCAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if len(bytes.TrimSpace(caPEM)) == 0 {
		return nil, errors.New("empty AI admin client CA bundle")
	}
	for remaining := bytes.TrimSpace(caPEM); len(remaining) > 0; {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("invalid AI admin client CA bundle")
		}
		endMarker := []byte("-----END CERTIFICATE-----")
		end := bytes.Index(remaining, endMarker)
		if end < 0 {
			return nil, errors.New("invalid AI admin client CA bundle")
		}
		end += len(endMarker)
		block, rest := pem.Decode(remaining[:end])
		if block == nil || len(rest) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("invalid AI admin client CA bundle")
		}
		ca, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errors.New("invalid AI admin client CA certificate")
		}
		roots.AddCert(ca)
		remaining = bytes.TrimSpace(remaining[end:])
	}
	allowed := append([]string(nil), cfg.TLSEdgeDNSNames...)
	server.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		// Normal TLS verification has already checked chain, clientAuth and time.
		// VerifyConnection also runs on resumed sessions; only the leaf SAN counts.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return errors.New("AI admin requires a verified edge certificate")
			}
			for _, san := range state.PeerCertificates[0].DNSNames {
				for _, name := range allowed {
					if san == name {
						return nil
					}
				}
			}
			return errors.New("AI admin edge DNS identity is not allowed")
		},
	}
	return server, nil
}

// Match the existing Lstat/open/SameFile pattern without changing deployment ACLs.
func readAdminTLSFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("AI admin TLS requires regular certificate/key/CA files")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open AI admin TLS material")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("AI admin TLS file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		return nil, errors.New("cannot read AI admin TLS material (1 MiB limit)")
	}
	return raw, nil
}
