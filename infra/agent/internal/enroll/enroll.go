// Package enroll joins the platform and renews node certificates.
package enroll

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Options for Join / Renew.
type Options struct {
	Server        string // https://host:port
	Token         string
	CAFingerprint string
	DataRoot      string
	AgentDir      string
	AgentVersion  string
	Logger        *slog.Logger
	// RenewBefore renews when NotAfter - now < RenewBefore (default 7 days).
	// Also renews when remaining lifetime < 1/3 of total validity.
	RenewBefore time.Duration
	// AllowInsecure permits grpc:// / http:// (tests / AURIGA_AGENT_ALLOW_INSECURE).
	AllowInsecure bool
	// Force overwrites an existing enrollment.
	Force bool
	// Store, if set, is reused instead of opening state.db (avoids bbolt lock with the running agent).
	Store *state.Store
}

func (o Options) log() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

// Join exchanges a one-time token for a node certificate and persists identity + meta.
func Join(ctx context.Context, opts Options) (*identity.Material, error) {
	if opts.Server == "" {
		return nil, fmt.Errorf("server is required")
	}
	if opts.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if opts.CAFingerprint == "" {
		return nil, fmt.Errorf("ca-sha256 is required")
	}
	if opts.AgentDir == "" {
		return nil, fmt.Errorf("agent dir is required")
	}
	if err := tlsutil.ValidateJoinEndpoint(opts.Server, opts.AllowInsecure); err != nil {
		return nil, err
	}
	if identity.Enrolled(opts.AgentDir) && !opts.Force {
		return nil, fmt.Errorf("already enrolled in %s (pass --force to replace)", opts.AgentDir)
	}

	key, err := identity.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	csrDER, err := identity.CreateCSR(key, "auriga-node")
	if err != nil {
		return nil, fmt.Errorf("create csr: %w", err)
	}
	sealPriv, sealPub, err := identity.GenerateSealingKey()
	if err != nil {
		return nil, fmt.Errorf("generate sealing key: %w", err)
	}

	hostname, _ := os.Hostname()
	conn, err := tlsutil.Dial(ctx, tlsutil.DialOptions{
		Endpoint:      opts.Server,
		CAFingerprint: opts.CAFingerprint,
		AllowInsecure: opts.AllowInsecure,
	})
	if err != nil {
		return nil, fmt.Errorf("dial enrollment: %w", err)
	}
	defer conn.Close()

	client := nodev1.NewNodeEnrollmentServiceClient(conn)
	resp, err := client.Join(ctx, &nodev1.JoinRequest{
		Token:            opts.Token,
		Csr:              csrDER,
		SealingPublicKey: sealPub[:],
		Hostname:         hostname,
		Os:               goos(),
		Arch:             runtime.GOARCH,
		AgentVersion:     opts.AgentVersion,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && (st.Code() == codes.Unauthenticated || st.Code() == codes.PermissionDenied || st.Code() == codes.InvalidArgument) {
			return nil, fmt.Errorf("join rejected: %w", err)
		}
		return nil, fmt.Errorf("join: %w", err)
	}
	if resp.GetNodeId() == "" || len(resp.GetCertificateChain()) == 0 || len(resp.GetCaBundle()) == 0 {
		return nil, fmt.Errorf("join response incomplete")
	}
	if err := tlsutil.VerifyCAPEMFingerprint(resp.GetCaBundle(), opts.CAFingerprint); err != nil {
		return nil, err
	}
	if err := tlsutil.ValidateControlEndpoint(resp.GetControlEndpoint(), opts.AllowInsecure); err != nil {
		return nil, fmt.Errorf("control endpoint from server: %w", err)
	}
	if err := validateIssuedCert(resp.GetCertificateChain(), resp.GetCaBundle(), key, resp.GetNodeId()); err != nil {
		return nil, fmt.Errorf("join certificate: %w", err)
	}

	expires := time.Time{}
	if ts := resp.GetCertificateExpiresAt(); ts != nil {
		expires = ts.AsTime()
	}
	mat := &identity.Material{
		NodeID:               resp.GetNodeId(),
		ControlEndpoint:      resp.GetControlEndpoint(),
		CertificateExpiresAt: expires,
		PrivateKey:           key,
		CertificatePEM:       resp.GetCertificateChain(),
		CAPEM:                resp.GetCaBundle(),
		SealingPrivate:       sealPriv,
		SealingPublic:        sealPub,
	}
	if err := identity.Save(opts.AgentDir, mat); err != nil {
		return nil, fmt.Errorf("save identity: %w", err)
	}

	st, err := state.Open(opts.AgentDir)
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	defer st.Close()
	meta := state.NodeMeta{
		NodeID:          mat.NodeID,
		ControlEndpoint: mat.ControlEndpoint,
		ExpiresAt:       expires,
		CAFingerprint:   opts.CAFingerprint,
	}
	if err := st.PutNodeMeta(meta); err != nil {
		return nil, fmt.Errorf("save node meta: %w", err)
	}

	opts.log().Info("joined platform",
		"node_id", mat.NodeID,
		"control_endpoint", mat.ControlEndpoint,
		"cert_expires_at", expires.UTC().Format(time.RFC3339),
	)
	return mat, nil
}

// NeedsRenew reports whether the cert should be renewed (force, < RenewBefore, or < 1/3 lifetime left).
func NeedsRenew(mat *identity.Material, renewBefore time.Duration) bool {
	if mat == nil || mat.CertificateExpiresAt.IsZero() {
		return false
	}
	if renewBefore <= 0 {
		renewBefore = 7 * 24 * time.Hour
	}
	left := time.Until(mat.CertificateExpiresAt)
	if left <= renewBefore {
		return true
	}
	notBefore := mat.CertificateNotBefore
	if notBefore.IsZero() {
		return false
	}
	lifetime := mat.CertificateExpiresAt.Sub(notBefore)
	if lifetime > 0 && left <= lifetime/3 {
		return true
	}
	return false
}

// RenewCertificate renews the node cert when within RenewBefore / 1/3 lifetime (or force).
func RenewCertificate(ctx context.Context, opts Options, force bool) (*identity.Material, error) {
	if opts.AgentDir == "" {
		return nil, fmt.Errorf("agent dir is required")
	}
	mat, err := identity.Load(opts.AgentDir)
	if err != nil {
		return nil, err
	}
	st := opts.Store
	owned := false
	if st == nil {
		var openErr error
		st, openErr = state.Open(opts.AgentDir)
		if openErr != nil {
			return nil, openErr
		}
		owned = true
	}
	if owned {
		defer st.Close()
	}
	meta, err := st.GetNodeMeta()
	if err != nil {
		return nil, err
	}
	mat.ControlEndpoint = meta.ControlEndpoint
	if mat.ControlEndpoint == "" {
		mat.ControlEndpoint = opts.Server
	}
	fp := meta.CAFingerprint
	if fp == "" {
		fp = opts.CAFingerprint
	}
	if fp == "" {
		return nil, fmt.Errorf("ca fingerprint missing; re-join required")
	}
	if err := tlsutil.VerifyCAPEMFingerprint(mat.CAPEM, fp); err != nil {
		return nil, fmt.Errorf("ca.crt pin: %w", err)
	}
	if err := tlsutil.ValidateControlEndpoint(mat.ControlEndpoint, opts.AllowInsecure); err != nil {
		return nil, err
	}

	if !force && !NeedsRenew(mat, opts.RenewBefore) {
		return mat, nil
	}

	csrDER, err := identity.CreateCSR(mat.PrivateKey, "auriga-node")
	if err != nil {
		return nil, fmt.Errorf("create csr: %w", err)
	}
	keyPEM, err := marshalKeyPEM(mat.PrivateKey)
	if err != nil {
		return nil, err
	}
	tlsCert, err := tlsutil.LoadTLSCertificate(mat.CertificatePEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	conn, err := tlsutil.Dial(ctx, tlsutil.DialOptions{
		Endpoint:          mat.ControlEndpoint,
		CAFingerprint:     fp,
		CAPEM:             mat.CAPEM,
		ClientCertificate: &tlsCert,
		AllowInsecure:     opts.AllowInsecure,
	})
	if err != nil {
		return nil, fmt.Errorf("dial renew: %w", err)
	}
	defer conn.Close()

	client := nodev1.NewNodeEnrollmentServiceClient(conn)
	resp, err := client.RenewCertificate(ctx, &nodev1.RenewCertificateRequest{
		Csr: csrDER,
	})
	if err != nil {
		return nil, fmt.Errorf("renew: %w", err)
	}
	if len(resp.GetCertificateChain()) == 0 {
		return nil, fmt.Errorf("renew response missing certificate chain")
	}
	if err := validateIssuedCert(resp.GetCertificateChain(), mat.CAPEM, mat.PrivateKey, mat.NodeID); err != nil {
		return nil, fmt.Errorf("renew certificate: %w", err)
	}

	mat.CertificatePEM = resp.GetCertificateChain()
	if ts := resp.GetCertificateExpiresAt(); ts != nil {
		mat.CertificateExpiresAt = ts.AsTime()
	} else {
		certs, _ := identity.ParseCertPEM(mat.CertificatePEM)
		if len(certs) > 0 {
			mat.CertificateExpiresAt = certs[0].NotAfter
			mat.CertificateNotBefore = certs[0].NotBefore
		}
	}
	if err := identity.Save(opts.AgentDir, mat); err != nil {
		return nil, err
	}
	meta.ExpiresAt = mat.CertificateExpiresAt
	if err := st.PutNodeMeta(meta); err != nil {
		return nil, err
	}
	opts.log().Info("renewed certificate",
		"node_id", mat.NodeID,
		"cert_expires_at", mat.CertificateExpiresAt.UTC().Format(time.RFC3339),
	)
	return mat, nil
}

func validateIssuedCert(certPEM, caPEM []byte, key *ecdsa.PrivateKey, wantNodeID string) error {
	certs, err := identity.ParseCertPEM(certPEM)
	if err != nil {
		return err
	}
	leaf := certs[0]
	if !identity.PublicKeysEqual(leaf, key) {
		return fmt.Errorf("certificate public key does not match local private key")
	}
	nodeID, err := identity.NodeIDFromCert(leaf)
	if err != nil {
		return err
	}
	if wantNodeID != "" && nodeID != wantNodeID {
		return fmt.Errorf("node id changed: got %q want %q", nodeID, wantNodeID)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("invalid CA bundle")
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf("certificate does not chain to CA: %w", err)
	}
	return nil
}

func goos() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func marshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// ReadToken resolves token from flag, file, or AURIGA_AGENT_JOIN_TOKEN.
func ReadToken(flagToken, tokenFile string) (string, error) {
	if strings.TrimSpace(flagToken) != "" {
		return strings.TrimSpace(flagToken), nil
	}
	if tokenFile != "" {
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read token file: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	if v := strings.TrimSpace(os.Getenv("AURIGA_AGENT_JOIN_TOKEN")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("token required (--token, --token-file, or AURIGA_AGENT_JOIN_TOKEN)")
}
