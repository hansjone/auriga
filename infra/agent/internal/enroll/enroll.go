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
	RenewBefore time.Duration
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
	// Always pin the returned CA bundle (covers grpc:// test transports and post-join check).
	if err := tlsutil.VerifyCAPEMFingerprint(resp.GetCaBundle(), opts.CAFingerprint); err != nil {
		return nil, err
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

// RenewCertificate renews the node cert when within RenewBefore of expiry (or force).
func RenewCertificate(ctx context.Context, opts Options, force bool) (*identity.Material, error) {
	if opts.AgentDir == "" {
		return nil, fmt.Errorf("agent dir is required")
	}
	mat, err := identity.Load(opts.AgentDir)
	if err != nil {
		return nil, err
	}
	st, err := state.Open(opts.AgentDir)
	if err != nil {
		return nil, err
	}
	defer st.Close()
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

	before := opts.RenewBefore
	if before <= 0 {
		before = 7 * 24 * time.Hour
	}
	if !force && time.Until(mat.CertificateExpiresAt) > before {
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
	mat.CertificatePEM = resp.GetCertificateChain()
	if ts := resp.GetCertificateExpiresAt(); ts != nil {
		mat.CertificateExpiresAt = ts.AsTime()
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
