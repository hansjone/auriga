// Package tlsutil builds TLS/gRPC connections for enrollment and control.
package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DialOptions configures a gRPC dial to the platform.
type DialOptions struct {
	Endpoint          string // https://host:port | grpc://host:port (insecure, tests) | unix:///path
	CAFingerprint     string // SPKI SHA-256 hex (required for join discovery over TLS)
	CAPEM             []byte // trusted CA bundle (preferred once enrolled)
	ClientCertificate *tls.Certificate
}

// Dial opens a gRPC connection (TLS/mTLS, or insecure for grpc:// test endpoints).
func Dial(ctx context.Context, opts DialOptions) (*grpc.ClientConn, error) {
	network, address, insecureDial, err := ParseEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	dialOpts := []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, address)
		}),
	}
	if insecureDial {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		tlsCfg, err := buildTLS(opts)
		if err != nil {
			return nil, err
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	}
	return grpc.NewClient("passthrough:///"+address, dialOpts...)
}

// LoadTLSCertificate builds a tls.Certificate from PEM chain + EC private key PEM.
func LoadTLSCertificate(certPEM, keyPEM []byte) (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(cert.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			cert.Leaf = leaf
		}
	}
	return cert, nil
}

// ParseEndpoint turns an endpoint URL into network, address, and whether to use insecure gRPC.
func ParseEndpoint(endpoint string) (network, address string, insecure bool, err error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", "", false, fmt.Errorf("empty endpoint")
	}
	if strings.HasPrefix(endpoint, "unix://") {
		return "unix", strings.TrimPrefix(endpoint, "unix://"), false, nil
	}
	if strings.HasPrefix(endpoint, "npipe://") {
		return "npipe", strings.TrimPrefix(endpoint, "npipe://"), false, nil
	}
	u, perr := url.Parse(endpoint)
	if perr == nil && u.Scheme != "" && u.Host != "" {
		host := u.Host
		if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
			port := "443"
			switch u.Scheme {
			case "http", "grpc":
				port = "80"
			}
			host = net.JoinHostPort(u.Hostname(), port)
		}
		return "tcp", host, u.Scheme == "grpc" || u.Scheme == "http", nil
	}
	if _, _, splitErr := net.SplitHostPort(endpoint); splitErr == nil {
		return "tcp", endpoint, false, nil
	}
	return "tcp", net.JoinHostPort(endpoint, "443"), false, nil
}

// VerifyCAPEMFingerprint checks that a PEM CA bundle contains a cert whose SPKI SHA-256 matches want.
func VerifyCAPEMFingerprint(caPEM []byte, wantSHA256 string) error {
	want := strings.ToLower(strings.TrimSpace(wantSHA256))
	if want == "" {
		return fmt.Errorf("ca fingerprint is required")
	}
	rest := caPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		if identity.SPKISHA256Hex(c) == want {
			return nil
		}
	}
	return fmt.Errorf("server certificate CA fingerprint mismatch")
}

// VerifyPinnedPeerChain verifies raw peer DER certs against a CA SPKI pin (join discovery).
func VerifyPinnedPeerChain(rawCerts [][]byte, wantSHA256 string) error {
	return verifyPinnedCA(rawCerts, strings.ToLower(strings.TrimSpace(wantSHA256)))
}

func buildTLS(opts DialOptions) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverNameForEndpoint(opts.Endpoint),
	}
	if opts.ClientCertificate != nil {
		cfg.Certificates = []tls.Certificate{*opts.ClientCertificate}
	}

	if len(opts.CAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(opts.CAPEM) {
			return nil, fmt.Errorf("invalid CA bundle")
		}
		cfg.RootCAs = pool
		return cfg, nil
	}

	want := strings.ToLower(strings.TrimSpace(opts.CAFingerprint))
	if want == "" {
		return nil, fmt.Errorf("ca fingerprint or CA bundle required")
	}
	cfg.InsecureSkipVerify = true //nolint:gosec // pin verified below
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return verifyPinnedCA(rawCerts, want)
	}
	return cfg, nil
}

func serverNameForEndpoint(endpoint string) string {
	_, address, _, err := ParseEndpoint(endpoint)
	if err != nil {
		return ""
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

func verifyPinnedCA(rawCerts [][]byte, wantSHA256 string) error {
	if wantSHA256 == "" {
		return fmt.Errorf("ca-sha256 is required")
	}
	var certs []*x509.Certificate
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("parse peer cert: %w", err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return fmt.Errorf("empty peer certificate chain")
	}
	var pinned *x509.Certificate
	for _, c := range certs {
		if identity.SPKISHA256Hex(c) == wantSHA256 {
			pinned = c
			break
		}
	}
	if pinned == nil {
		return fmt.Errorf("server certificate CA fingerprint mismatch")
	}
	if len(certs) == 1 && certs[0] == pinned {
		return nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(pinned)
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		if c != pinned {
			intermediates.AddCert(c)
		}
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := certs[0].Verify(opts); err != nil {
		return fmt.Errorf("verify server chain: %w", err)
	}
	return nil
}
