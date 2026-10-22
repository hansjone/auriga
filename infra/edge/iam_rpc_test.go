package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type accessPKI struct {
	hop           IAMHopConfig
	server, proxy tls.Certificate
	roots         *x509.CertPool
}

func createAccessPKI(t *testing.T) accessPKI {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(name, uri string, serial int64, usage x509.ExtKeyUsage) (tls.Certificate, string, string) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{name}, ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
		if uri != "" {
			u, err := url.Parse(uri)
			if err != nil {
				t.Fatal(err)
			}
			leaf.URIs = []*url.URL{u}
		}
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &k.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		cp, kp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		cert, err := tls.X509KeyPair(cp, kp)
		if err != nil {
			t.Fatal(err)
		}
		certFile, keyFile := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
		for file, data := range map[string][]byte{certFile: cp, keyFile: kp} {
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return cert, certFile, keyFile
	}
	server, _, _ := issue("iam.internal.test", "", 2, x509.ExtKeyUsageServerAuth)
	_, certFile, keyFile := issue("edge.internal.test", "", 3, x509.ExtKeyUsageClientAuth)
	proxy, _, _ := issue("proxy.internal.test", "auriga://proxy/test", 4, x509.ExtKeyUsageClientAuth)
	caFile := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return accessPKI{hop: IAMHopConfig{CertFile: certFile, KeyFile: keyFile, CAFile: caFile, ServerName: "iam.internal.test"}, server: server, proxy: proxy, roots: roots}
}

type csrfRPCServer struct {
	iamv1.UnimplementedSessionServiceServer
	mode    atomic.Int32
	calls   atomic.Int32
	session *iamv1.Session
}

func (s *csrfRPCServer) VerifyAdminSessionCsrf(ctx context.Context, req *iamv1.VerifyAdminSessionCsrfRequest) (*iamv1.VerifyAdminSessionCsrfResponse, error) {
	s.calls.Add(1)
	if req.SessionToken != "actual-admin-cookie" || req.CsrfToken != "actual-tab-token" {
		return nil, status.Error(codes.Unauthenticated, "wrong cookie")
	}
	switch s.mode.Load() {
	case 1:
		return &iamv1.VerifyAdminSessionCsrfResponse{}, nil
	case 2:
		cp := proto.Clone(s.session).(*iamv1.Session)
		cp.AccountId = "wrong-account"
		return &iamv1.VerifyAdminSessionCsrfResponse{Active: true, Session: cp}, nil
	case 3:
		return nil, status.Error(codes.Unauthenticated, "revoked")
	case 4:
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "deadline")
	case 5:
		cp := proto.Clone(s.session).(*iamv1.Session)
		cp.ExpiresAt = timestamppb.New(time.Now().Add(-time.Second))
		return &iamv1.VerifyAdminSessionCsrfResponse{Active: true, Session: cp}, nil
	case 6:
		cp := proto.Clone(s.session).(*iamv1.Session)
		cp.IdleExpiresAt = nil
		return &iamv1.VerifyAdminSessionCsrfResponse{Active: true, Session: cp}, nil
	case 7:
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	return &iamv1.VerifyAdminSessionCsrfResponse{Active: true, Session: s.session}, nil
}

func serviceJWT(t *testing.T) (string, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"sub": "svc:edge", "aud": "auriga.internal", "ptyp": "internal_service", "ctx": "service", "exp": time.Now().Add(time.Minute).Unix()})
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), &key.PublicKey
}

func verifyServiceJWT(token string, key *ecdsa.PublicKey) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	exp, ok := claims["exp"].(float64)
	return ok && exp > float64(time.Now().Unix()) && claims["sub"] == "svc:edge" && claims["aud"] == "auriga.internal" && claims["ptyp"] == "internal_service" && claims["ctx"] == "service"
}

func TestIAMRPCRealMTLSServiceJWTAndFailureClosed(t *testing.T) {
	pki := createAccessPKI(t)
	token, key := serviceJWT(t)
	rotatedToken, rotatedKey := serviceJWT(t)
	expected := &Session{SessionID: "sid", AccountID: "account", TenantID: "019a0000-0000-7000-8000-000000000001", Audience: "admin"}
	backend := &csrfRPCServer{session: &iamv1.Session{SessionId: expected.SessionID, AccountId: expected.AccountID, TenantId: expected.TenantID, Audience: iamv1.SessionAudience_SESSION_AUDIENCE_ADMIN, ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)), IdleExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	var dialer func(context.Context, string) (net.Conn, error)
	if runtime.GOOS == "windows" {
		_ = ln.Close()
		var endpoint string
		ln, endpoint = localTestListener(t)
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		dialer = func(ctx context.Context, _ string) (net.Conn, error) { return dialLocalIngress(ctx, u) }
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert})), grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no peer")
		}
		transport, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(transport.State.VerifiedChains) == 0 || len(transport.State.PeerCertificates[0].DNSNames) != 1 || transport.State.PeerCertificates[0].DNSNames[0] != "edge.internal.test" {
			return nil, status.Error(codes.Unauthenticated, "not edge")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || !verifyServiceJWT(strings.TrimPrefix(md.Get("authorization")[0], "Bearer "), key) && !verifyServiceJWT(strings.TrimPrefix(md.Get("authorization")[0], "Bearer "), rotatedKey) || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != expected.TenantID {
			return nil, status.Error(codes.Unauthenticated, "not authenticated")
		}
		return handler(ctx, req)
	}))
	iamv1.RegisterSessionServiceServer(server, backend)
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { server.Stop(); _ = ln.Close() })
	file := filepath.Join(t.TempDir(), "service.jwt")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := IAMRPCConfig{Address: address, ServerName: "iam.internal.test", ServiceTokenFile: file, TimeoutMillis: 200}
	client, err := openIAMRPC(cfg, pki.hop, dialer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, test := range []struct {
		mode int32
		want error
	}{{0, nil}, {0, nil}, {1, errCSRF}, {2, errIAMUnavailable}, {3, errIAMUnauthenticated}, {4, errIAMUnavailable}, {5, errIAMUnauthenticated}, {6, errIAMUnavailable}, {7, errIAMUnavailable}} {
		backend.mode.Store(test.mode)
		err := client.Verify(context.Background(), "actual-admin-cookie", "actual-tab-token", expected, time.Now())
		if !errors.Is(err, test.want) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, diagnostic := client.client.VerifyAdminSessionCsrf(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "auriga-tenant-id", expected.TenantID)), &iamv1.VerifyAdminSessionCsrfRequest{SessionToken: "actual-admin-cookie", CsrfToken: "actual-tab-token"})
			t.Fatalf("mode %d: %v want %v; transport diagnostic: %v", test.mode, err, test.want, diagnostic)
		}
	}
	if backend.calls.Load() != 9 {
		t.Fatalf("cached RPC result: calls=%d", backend.calls.Load())
	}
	if err := os.WriteFile(file, []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background(), "actual-admin-cookie", "actual-tab-token", expected, time.Now()); !errors.Is(err, errIAMUnauthenticated) {
		t.Fatal(err)
	}
	backend.mode.Store(0)
	if err := os.WriteFile(file, []byte(rotatedToken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background(), "actual-admin-cookie", "actual-tab-token", expected, time.Now()); err != nil {
		t.Fatalf("renewed service credential was not consumed: %v", err)
	}
	if err := os.WriteFile(file, make([]byte, (16<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background(), "actual-admin-cookie", "actual-tab-token", expected, time.Now()); !errors.Is(err, errIAMUnavailable) {
		t.Fatalf("oversized service credential did not fail closed: %v", err)
	}
	if err := os.WriteFile(file, []byte(rotatedToken), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.ServerName = "wrong.internal.test"
	wrong, err := openIAMRPC(cfg, pki.hop, dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wrong.Close() }()
	if err := wrong.Verify(context.Background(), "actual-admin-cookie", "actual-tab-token", expected, time.Now()); !errors.Is(err, errIAMUnavailable) {
		t.Fatalf("wrong server cert accepted: %v", err)
	}
}

func TestIAMHopRejectsUntrustedTransport(t *testing.T) {
	pki := createAccessPKI(t)
	for _, raw := range []string{"http://192.0.2.1:8080", "http://localhost:8080", "http://127.0.0.1:8080", "https://user:secret@iam.internal.test"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := newIAMHopTransport(pki.hop, u); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	u, _ := url.Parse("http://127.0.0.1:8080")
	pki.hop.AllowLoopbackHTTP = true
	tr, err := newIAMHopTransport(pki.hop, u)
	if err != nil {
		t.Fatal(err)
	}
	tr.CloseIdleConnections()
	u, _ = url.Parse("https://iam.internal.test:8443")
	tr, err = newIAMHopTransport(pki.hop, u)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.ServerName != "iam.internal.test" || len(tr.TLSClientConfig.Certificates) != 1 {
		t.Fatal("untrusted IAM transport")
	}
	tr.CloseIdleConnections()
}
