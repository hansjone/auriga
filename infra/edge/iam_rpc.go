package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type IAMHopConfig struct {
	CertFile          string `json:"certFile"`
	KeyFile           string `json:"keyFile"`
	CAFile            string `json:"caFile"`
	ServerName        string `json:"serverName"`
	AllowLoopbackHTTP bool   `json:"allowLoopbackHttp"`
}
type IAMRPCConfig struct {
	Address          string `json:"address"`
	ServerName       string `json:"serverName"`
	ServiceTokenFile string `json:"serviceTokenFile"`
	TimeoutMillis    int    `json:"timeoutMillis"`
}

func iamTLS(cfg IAMHopConfig, serverName string) (*tls.Config, error) {
	if serverName == "" || net.ParseIP(serverName) != nil {
		return nil, fmt.Errorf("IAM TLS requires fixed DNS serverName")
	}
	for _, file := range []string{cfg.CertFile, cfg.KeyFile, cfg.CAFile} {
		info, err := os.Lstat(file)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("IAM TLS material must be regular files")
		}
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("IAM CA bundle is empty")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: serverName}, nil
}

func newIAMHopTransport(cfg IAMHopConfig, target *url.URL) (*http.Transport, error) {
	if target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Host == "" || target.Path != "" && target.Path != "/" {
		return nil, fmt.Errorf("invalid IAM hop URL")
	}
	tr := &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxIdleConnsPerHost: 16, IdleConnTimeout: 60 * time.Second}
	if target.Scheme == "http" && cfg.AllowLoopbackHTTP {
		ip, err := netip.ParseAddr(target.Hostname())
		if err == nil && ip.IsLoopback() {
			return tr, nil
		}
	}
	if target.Scheme != "https" {
		return nil, fmt.Errorf("IAM hop requires HTTPS mTLS or explicit numeric loopback development HTTP")
	}
	tlsConfig, err := iamTLS(cfg, cfg.ServerName)
	if err != nil {
		return nil, err
	}
	tr.TLSClientConfig = tlsConfig
	return tr, nil
}

func sameInternalHTTPSOrigin(a, b *url.URL) bool {
	if a.Scheme != "https" || b.Scheme != "https" || a.User != nil || b.User != nil {
		return false
	}
	ah, ap, ae := parseAuthority(a.Host, "https")
	bh, bp, be := parseAuthority(b.Host, "https")
	return ae == nil && be == nil && ah == bh && ap == bp
}

type AdminCSRFVerifier interface {
	Verify(context.Context, string, string, *Session, time.Time) error
}

type BrowserSessionRPC interface {
	IntrospectSession(context.Context, string, string, string) (*iamv1.IntrospectSessionResponse, error)
	TouchSession(context.Context, string, string, string) (*iamv1.TouchSessionResponse, error)
}

func sessionAudience(audience string) iamv1.SessionAudience {
	switch audience {
	case "portal":
		return iamv1.SessionAudience_SESSION_AUDIENCE_PORTAL
	case "admin":
		return iamv1.SessionAudience_SESSION_AUDIENCE_ADMIN
	default:
		return iamv1.SessionAudience_SESSION_AUDIENCE_UNSPECIFIED
	}
}

func (c *iamRPC) sessionContext(ctx context.Context, tenant string) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	token, err := readIAMServiceToken(c.tokenFile)
	if err != nil || tenant == "" {
		cancel()
		return nil, nil, errIAMUnavailable
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "auriga-tenant-id", tenant)), cancel, nil
}

func (c *iamRPC) IntrospectSession(ctx context.Context, cookie, audience, tenant string) (*iamv1.IntrospectSessionResponse, error) {
	ctx, cancel, err := c.sessionContext(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := c.client.IntrospectSession(ctx, &iamv1.IntrospectSessionRequest{SessionToken: cookie, Audience: sessionAudience(audience)})
	if err != nil {
		// Authentication errors here concern the control-plane credential, not
		// the user's cookie. Never use the old user JWT after an RPC failure.
		return nil, errIAMUnavailable
	}
	return response, nil
}

func (c *iamRPC) TouchSession(ctx context.Context, cookie, audience, tenant string) (*iamv1.TouchSessionResponse, error) {
	ctx, cancel, err := c.sessionContext(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := c.client.TouchSession(ctx, &iamv1.TouchSessionRequest{SessionToken: cookie, Audience: sessionAudience(audience)})
	if err != nil {
		return nil, errIAMUnavailable
	}
	return response, nil
}

type iamRPC struct {
	conn      *grpc.ClientConn
	client    iamv1.SessionServiceClient
	tokenFile string
	timeout   time.Duration
}

func newIAMRPC(cfg IAMRPCConfig, hop IAMHopConfig) (*iamRPC, error) {
	return openIAMRPC(cfg, hop, nil)
}

// A dialer is injected only by local transport tests; production always uses
// the fixed installation TCP address and the same verified mTLS credentials.
func openIAMRPC(cfg IAMRPCConfig, hop IAMHopConfig, dialer func(context.Context, string) (net.Conn, error)) (*iamRPC, error) {
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, fmt.Errorf("iamRpc.address must be a fixed host:port")
	}
	tlsConfig, err := iamTLS(hop, cfg.ServerName)
	if err != nil {
		return nil, err
	}
	_, err = readIAMServiceToken(cfg.ServiceTokenFile)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutMillis) * time.Millisecond
	if cfg.TimeoutMillis == 0 {
		timeout = 2 * time.Second
	}
	if timeout <= 0 || timeout > 5*time.Second {
		return nil, fmt.Errorf("iamRpc.timeoutMillis must be 1..5000")
	}
	options := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithNoProxy()}
	if dialer != nil {
		options = append(options, grpc.WithContextDialer(dialer))
	}
	conn, err := grpc.NewClient(cfg.Address, options...)
	if err != nil {
		return nil, err
	}
	return &iamRPC{conn: conn, client: iamv1.NewSessionServiceClient(conn), tokenFile: cfg.ServiceTokenFile, timeout: timeout}, nil
}

// The installer owns credential issuance and atomically replaces this fixed
// file. Read it for each RPC so a renewed service JWT does not require an edge
// restart. Never retain the credential in a positive authorization cache.
func readIAMServiceToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("IAM service token unavailable")
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("IAM service token must be regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("IAM service token unavailable")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return "", fmt.Errorf("IAM service token must be regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return "", fmt.Errorf("IAM service token unavailable")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("invalid IAM service token")
	}
	return token, nil
}

func (c *iamRPC) Close() error { return c.conn.Close() }

var errIAMUnavailable = fmt.Errorf("IAM unavailable")
var errIAMUnauthenticated = fmt.Errorf("IAM session invalid")

func (c *iamRPC) Verify(ctx context.Context, cookie, token string, expected *Session, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if expected == nil || expected.TenantID == "" {
		return errIAMUnauthenticated
	}
	serviceToken, err := readIAMServiceToken(c.tokenFile)
	if err != nil {
		return errIAMUnavailable
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+serviceToken, "auriga-tenant-id", expected.TenantID))
	response, err := c.client.VerifyAdminSessionCsrf(ctx, &iamv1.VerifyAdminSessionCsrfRequest{SessionToken: cookie, CsrfToken: token})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return errIAMUnauthenticated
		}
		return errIAMUnavailable
	}
	if !response.GetActive() {
		if response.GetSession() != nil {
			return errIAMUnavailable
		}
		return errCSRF
	}
	s := response.GetSession()
	if s == nil || expected == nil || s.GetSessionId() == "" || s.GetAccountId() == "" || s.GetTenantId() == "" || s.GetAudience() != iamv1.SessionAudience_SESSION_AUDIENCE_ADMIN || s.GetSessionId() != expected.SessionID || s.GetAccountId() != expected.AccountID || s.GetTenantId() != expected.TenantID {
		return errIAMUnavailable
	}
	if s.GetExpiresAt() == nil || s.GetIdleExpiresAt() == nil || s.ExpiresAt.CheckValid() != nil || s.IdleExpiresAt.CheckValid() != nil {
		return errIAMUnavailable
	}
	if !now.Before(s.ExpiresAt.AsTime()) || !now.Before(s.IdleExpiresAt.AsTime()) {
		return errIAMUnauthenticated
	}
	return nil
}
