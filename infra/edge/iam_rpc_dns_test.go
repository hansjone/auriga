package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
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
	"golang.org/x/net/dns/dnsmessage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Exercise gRPC's real DNS resolver, not a replacement resolver that simply
// inspects its options. All DNS packets stay in net.Pipe: A succeeds and TXT
// never replies. The process-local override is restored after every client closes.
type iamBlockedTXTDNS struct {
	hostCalls, txtCalls atomic.Int32
}

func (d *iamBlockedTXTDNS) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		stop := context.AfterFunc(ctx, func() { _ = server.Close() })
		defer stop()
		var size [2]byte
		if _, err := io.ReadFull(server, size[:]); err != nil {
			return
		}
		packet := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(server, packet); err != nil {
			return
		}
		var query dnsmessage.Message
		if query.Unpack(packet) != nil || len(query.Questions) != 1 {
			return
		}
		q := query.Questions[0]
		if q.Type == dnsmessage.TypeTXT {
			d.txtCalls.Add(1)
			_, _ = io.Copy(io.Discard, server) // no response until caller cancellation
			return
		}
		reply := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: query.Questions}
		if q.Name.String() != "iam-txt-blocked.test." {
			reply.RCode = dnsmessage.RCodeNameError
		} else if q.Type == dnsmessage.TypeA {
			d.hostCalls.Add(1)
			reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
		}
		packet, err := reply.Pack()
		if err != nil {
			return
		}
		binary.BigEndian.PutUint16(size[:], uint16(len(packet)))
		_, _ = server.Write(append(size[:], packet...))
	}()
	return client, nil
}

type iamDNSRPCServer struct {
	iamv1.UnimplementedSessionServiceServer
	calls atomic.Int32
	slow  atomic.Bool
}

func (s *iamDNSRPCServer) IntrospectSession(ctx context.Context, request *iamv1.IntrospectSessionRequest) (*iamv1.IntrospectSessionResponse, error) {
	s.calls.Add(1)
	if request.SessionToken != "clearly-invalid-session" || request.Audience != iamv1.SessionAudience_SESSION_AUDIENCE_PORTAL {
		return nil, status.Error(codes.InvalidArgument, "unexpected test request")
	}
	if s.slow.Load() {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "deadline")
	}
	return &iamv1.IntrospectSessionResponse{Active: false}, nil
}

func TestIAMRPCDNSWithoutTXT(t *testing.T) {
	dns := &iamBlockedTXTDNS{}
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dns.dial}
	t.Cleanup(func() { net.DefaultResolver = previous })
	probeCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, probeErr := net.DefaultResolver.LookupTXT(probeCtx, "_grpc_config.iam-txt-blocked.test")
	cancel()
	var dnsErr *net.DNSError
	if !errors.As(probeErr, &dnsErr) || !dnsErr.IsTimeout || dns.txtCalls.Load() == 0 {
		t.Fatalf("TXT fixture must time out, not return NotFound: %v", probeErr)
	}
	txtBefore := dns.txtCalls.Load()

	pki := createAccessPKI(t)
	token, key := serviceJWT(t)
	renewed, renewedKey := serviceJWT(t)
	const tenant = "019a0000-0000-7000-8000-000000000001"
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := context.WithValue(context.Background(), ctxKeyTraceparent, traceparent)
	backend := &iamDNSRPCServer{}
	var attempts atomic.Int32
	var forbidden atomic.Bool
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var dialer func(context.Context, string) (net.Conn, error)
	if runtime.GOOS == "windows" {
		// Reuse the repository's Windows local transport fixture. gRPC still
		// resolves the configured hostname and performs verified TLS over it.
		_ = ln.Close()
		var endpoint string
		ln, endpoint = localTestListener(t)
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		dialer = func(ctx context.Context, _ string) (net.Conn, error) { return dialLocalIngress(ctx, u) }
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert})), grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		attempts.Add(1)
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing peer")
		}
		auth, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(auth.State.VerifiedChains) == 0 || len(auth.State.PeerCertificates[0].DNSNames) != 1 || auth.State.PeerCertificates[0].DNSNames[0] != "edge.internal.test" {
			return nil, status.Error(codes.Unauthenticated, "wrong client role")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || !verifyServiceJWT(strings.TrimPrefix(md.Get("authorization")[0], "Bearer "), key) && !verifyServiceJWT(strings.TrimPrefix(md.Get("authorization")[0], "Bearer "), renewedKey) || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != tenant {
			return nil, status.Error(codes.Unauthenticated, "wrong service credential or tenant")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || len(md.Get("traceparent")) != 1 || md.Get("traceparent")[0] != traceparent {
			return nil, status.Error(codes.InvalidArgument, "deadline or trace changed")
		}
		if forbidden.Load() {
			return nil, status.Error(codes.PermissionDenied, "permission denied")
		}
		return handler(ctx, req)
	}))
	iamv1.RegisterSessionServiceServer(server, backend)
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { server.Stop(); _ = ln.Close() })
	file := filepath.Join(t.TempDir(), "service.jwt")
	writeToken := func(t *testing.T, value string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeToken(t, token)
	cfg := IAMRPCConfig{Address: net.JoinHostPort("iam-txt-blocked.test", port), ServerName: "iam.internal.test", ServiceTokenFile: file, TimeoutMillis: 2000}
	open := func(t *testing.T, cfg IAMRPCConfig, hop IAMHopConfig) *iamRPC {
		t.Helper()
		client, err := openIAMRPC(cfg, hop, dialer)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	client := open(t, cfg, pki.hop)
	call := func(t *testing.T, ctx context.Context, client *iamRPC, tenant string) error {
		t.Helper()
		response, err := client.IntrospectSession(ctx, "clearly-invalid-session", "portal", tenant)
		if err == nil && (response == nil || response.Active) {
			t.Fatal("invalid session must remain inactive")
		}
		if err != nil && response != nil {
			t.Fatal("failed RPC returned a usable response")
		}
		return err
	}
	hostBefore := dns.hostCalls.Load()
	started := time.Now()
	if err := call(t, ctx, client, tenant); err != nil {
		t.Fatalf("fixed IAM hostname must reach verified local RPC despite unavailable TXT: %v; A=%d TXT=%d", err, dns.hostCalls.Load(), dns.txtCalls.Load())
	}
	if client.timeout != 2*time.Second || dns.hostCalls.Load() <= hostBefore || dns.txtCalls.Load() != txtBefore || attempts.Load() != 1 || backend.calls.Load() != 1 {
		t.Fatal("hostname lookup, original budget, no-TXT or one-call invariant changed")
	}
	t.Logf("verified hostname RPC: elapsed=%v budget=%v A=%d addedTXT=%d attempts=%d inactive=true", time.Since(started), client.timeout, dns.hostCalls.Load()-hostBefore, dns.txtCalls.Load()-txtBefore, attempts.Load())

	failClosed := func(t *testing.T, client *iamRPC, callCtx context.Context, callTenant string, wantAttempts int32) {
		t.Helper()
		before, callsBefore := attempts.Load(), backend.calls.Load()
		if err := call(t, callCtx, client, callTenant); !errors.Is(err, errIAMUnavailable) {
			t.Fatalf("RPC failure did not remain unavailable: %v", err)
		}
		if attempts.Load()-before != wantAttempts || backend.calls.Load() != callsBefore {
			t.Fatal("rejected request reached handler or retried")
		}
	}
	t.Run("token-reloaded-and-renewed", func(t *testing.T) {
		writeToken(t, "forged")
		failClosed(t, client, ctx, tenant, 1)
		writeToken(t, renewed)
		if err := call(t, ctx, client, tenant); err != nil {
			t.Fatalf("renewed token was not read on the same connection: %v", err)
		}
		writeToken(t, "invalid token whitespace")
		failClosed(t, client, ctx, tenant, 0)
		writeToken(t, renewed)
	})
	t.Run("wrong-or-empty-tenant", func(t *testing.T) {
		failClosed(t, client, ctx, "other-tenant", 1)
		failClosed(t, client, ctx, "", 0)
	})
	t.Run("wrong-server-certificate-name", func(t *testing.T) {
		wrong := cfg
		wrong.ServerName = "wrong.internal.test"
		failClosed(t, open(t, wrong, pki.hop), ctx, tenant, 0)
	})
	t.Run("wrong-client-certificate-role", func(t *testing.T) {
		wrong := pki.hop
		wrong.CertFile = filepath.Join(filepath.Dir(wrong.CertFile), "proxy.internal.test.crt")
		wrong.KeyFile = filepath.Join(filepath.Dir(wrong.KeyFile), "proxy.internal.test.key")
		failClosed(t, open(t, cfg, wrong), ctx, tenant, 1)
	})
	t.Run("permission-denied", func(t *testing.T) {
		forbidden.Store(true)
		defer forbidden.Store(false)
		failClosed(t, client, ctx, tenant, 1)
	})
	for _, budget := range []time.Duration{2 * time.Second, 60 * time.Millisecond} {
		t.Run("deadline-"+budget.String(), func(t *testing.T) {
			backend.slow.Store(true)
			defer backend.slow.Store(false)
			callCtx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			before, callsBefore, started := attempts.Load(), backend.calls.Load(), time.Now()
			if err := call(t, callCtx, client, tenant); !errors.Is(err, errIAMUnavailable) {
				t.Fatalf("deadline did not fail closed: %v", err)
			}
			elapsed := time.Since(started)
			if elapsed < budget*9/10 || elapsed > budget+time.Second || attempts.Load()-before != 1 || backend.calls.Load()-callsBefore != 1 {
				t.Fatalf("budget or single application attempt changed: elapsed=%v", elapsed)
			}
		})
	}
	if dns.txtCalls.Load() != txtBefore {
		t.Fatalf("IAM clients attempted external service-config TXT: %d", dns.txtCalls.Load()-txtBefore)
	}
}
