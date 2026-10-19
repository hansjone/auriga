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
	Endpoint          string // https:// | grpcs:// | unix:// | npipe:// ; grpc/http only if AllowInsecure
	CAFingerprint     string // SPKI SHA-256 hex
	CAPEM             []byte // trusted CA bundle (still pin-checked when fingerprint set)
	ClientCertificate *tls.Certificate
	// AllowInsecure permits grpc:// and http:// (tests / explicit AURIGA_AGENT_ALLOW_INSECURE).
	AllowInsecure bool
}

// Dial opens a gRPC connection.
func Dial(ctx context.Context, opts DialOptions) (*grpc.ClientConn, error) {
	network, address, insecureDial, err := ParseEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}
	if insecureDial && !opts.AllowInsecure {
		return nil, fmt.Errorf("insecure endpoint %q rejected (set AllowInsecure only for local dev/tests)", opts.Endpoint)
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

// ValidateJoinEndpoint requires https/grpcs unless AllowInsecure.
func ValidateJoinEndpoint(endpoint string, allowInsecure bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("empty endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("join server must be an absolute URL with scheme")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "grpcs":
		return nil
	case "grpc", "http":
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("join requires https:// (got %s://); enable allowInsecure only for local dev", u.Scheme)
	default:
		return fmt.Errorf("unsupported join scheme %q", u.Scheme)
	}
}

// ValidateControlEndpoint allows https/grpcs/unix/npipe; rejects plaintext unless allowInsecure.
func ValidateControlEndpoint(endpoint string, allowInsecure bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("empty control endpoint")
	}
	if strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://") {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("control endpoint must be an absolute URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "grpcs":
		return nil
	case "grpc", "http":
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("control endpoint must use TLS (https/grpcs), got %s://", u.Scheme)
	default:
		return fmt.Errorf("unsupported control endpoint scheme %q", u.Scheme)
	}
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
			switch strings.ToLower(u.Scheme) {
			case "http", "grpc":
				port = "80"
			}
			host = net.JoinHostPort(u.Hostname(), port)
		}
		scheme := strings.ToLower(u.Scheme)
		return "tcp", host, scheme == "grpc" || scheme == "http", nil
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
func VerifyPinnedPeerChain(rawCerts [][]byte, wantSHA256, serverName string) error {
	return verifyPinnedCA(rawCerts, strings.ToLower(strings.TrimSpace(wantSHA256)), serverName)
}

func buildTLS(opts DialOptions) (*tls.Config, error) {
	serverName := serverNameForEndpoint(opts.Endpoint)
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}
	if opts.ClientCertificate != nil {
		cfg.Certificates = []tls.Certificate{*opts.ClientCertificate}
	}

	want := strings.ToLower(strings.TrimSpace(opts.CAFingerprint))
	if len(opts.CAPEM) > 0 {
		if want != "" {
			if err := VerifyCAPEMFingerprint(opts.CAPEM, want); err != nil {
				return nil, fmt.Errorf("local CA bundle pin: %w", err)
			}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(opts.CAPEM) {
			return nil, fmt.Errorf("invalid CA bundle")
		}
		cfg.RootCAs = pool
		return cfg, nil
	}

	if want == "" {
		return nil, fmt.Errorf("ca fingerprint or CA bundle required")
	}
	cfg.InsecureSkipVerify = true //nolint:gosec // pin + hostname verified below
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return verifyPinnedCA(rawCerts, want, serverName)
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

func verifyPinnedCA(rawCerts [][]byte, wantSHA256, serverName string) error {
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
	now := time.Now()
	for _, c := range certs {
		if now.Before(c.NotBefore) || now.After(c.NotAfter) {
			return fmt.Errorf("peer certificate %q not valid at current time", c.Subject.CommonName)
		}
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
		if serverName != "" {
			if err := certs[0].VerifyHostname(serverName); err != nil {
				return fmt.Errorf("hostname mismatch: %w", err)
			}
		}
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
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if serverName != "" && net.ParseIP(serverName) == nil {
		opts.DNSName = serverName
	}
	if _, err := certs[0].Verify(opts); err != nil {
		return fmt.Errorf("verify server chain: %w", err)
	}
	if serverName != "" {
		if err := certs[0].VerifyHostname(serverName); err != nil {
			return fmt.Errorf("hostname mismatch: %w", err)
		}
	}
	return nil
}
