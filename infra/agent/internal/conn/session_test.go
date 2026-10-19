package conn

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func sessionFixture(t *testing.T, renew func(context.Context, *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error)) (*Session, *testutil.FakeWorkspace, *identity.Material) {
	t.Helper()
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{Renew: renew})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fw.Close)
	dir := filepath.Join(t.TempDir(), "agent")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mat, err := enroll.Join(ctx, enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: dir, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(Config{AgentDir: dir, Store: st, AllowInsecure: true, RenewCheckEvery: 10 * time.Millisecond, MinBackoff: 80 * time.Millisecond, MaxBackoff: 160 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, fw, mat
}

func nearExpiry(t *testing.T, s *Session, fw *testutil.FakeWorkspace, mat *identity.Material) []byte {
	t.Helper()
	cert, err := fw.CA.IssueNode(mat.NodeID, &mat.PrivateKey.PublicKey, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	mat.CertificatePEM = cert
	if err := identity.Save(s.cfg.AgentDir, mat); err != nil {
		t.Fatal(err)
	}
	return cert
}

func runSession(t *testing.T, s *Session) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("session did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
func TestSessionDefaults(t *testing.T) {
	s := New(Config{})
	if s.Connected() || s.cfg.RenewCheckEvery != time.Hour || s.cfg.WelcomeTimeout != 30*time.Second {
		t.Fatal("unsafe session defaults")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run canceled: %v", err)
	}
}

func TestRenewLoopReplacesCertificateAndReconnectLoadsIt(t *testing.T) {
	s, fw, mat := sessionFixture(t, nil)
	old := nearExpiry(t, s, fw, mat)
	stop := runSession(t, s)
	for round := 0; round < 2; round++ {
		if err := testutil.WaitFor(3*time.Second, func() bool {
			got, _, cert, err := s.loadCreds()
			return err == nil && !bytes.Equal(got.CertificatePEM, old) && cert.Leaf.NotAfter.After(time.Now().Add(20*24*time.Hour))
		}); err != nil {
			t.Fatal(err)
		}
		// The session remains alive while the timer renews the next near-expiry cert.
		if err := testutil.WaitFor(time.Second, s.Connected); err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			old = nearExpiry(t, s, fw, mat)
		}
	}
	stop()
	// Exercise the reconnect path and verify the exact TLS certificate it loads.
	got, _, cert, err := s.loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := identity.ParseCertPEM(got.CertificatePEM)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cert.Certificate[0], leaves[0].Raw) || bytes.Equal(got.CertificatePEM, old) {
		t.Fatal("reconnect loaded stale certificate")
	}
	assertMTLSClientCertificate(t, fw.CA, cert, leaves[0].Raw)
	helloBefore, _, _ := fw.Stats()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cfg := s.cfg
	cfg.OnWelcome = func(*nodev1.Welcome) { cancel() }
	reconnect := New(cfg)
	if err := reconnect.runOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("reconnect did not reach Welcome: %v", err)
	}
	helloAfter, _, _ := fw.Stats()
	if helloAfter != helloBefore+1 {
		t.Fatal("reconnect did not send Hello")
	}
}

func TestRenewLoopFailureBackoffAndRetry(t *testing.T) {
	attempts := make(chan time.Time, 8)
	var count atomic.Int32
	release := make(chan struct{})
	var fw *testutil.FakeWorkspace
	s, workspace, mat := sessionFixture(t, func(ctx context.Context, req *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		attempts <- time.Now()
		if count.Add(1) <= 2 {
			return nil, status.Error(codes.Unavailable, "retry renewal")
		}
		cert, err := fw.CA.IssueNodeFromCSR("n_test01", req.GetCsr(), time.Now().Add(30*24*time.Hour))
		return &nodev1.RenewCertificateResponse{CertificateChain: cert}, err
	})
	fw = workspace
	old := nearExpiry(t, s, fw, mat)
	close(release)
	runSession(t, s)
	if err := testutil.WaitFor(3*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(s.cfg.AgentDir, identity.CertFileName))
		return err == nil && !bytes.Equal(b, old)
	}); err != nil {
		t.Fatal(err)
	}
	first, second, third := <-attempts, <-attempts, <-attempts
	if second.Sub(first) < 80*time.Millisecond || third.Sub(second) < 160*time.Millisecond {
		t.Fatalf("backoff bypassed: %v, %v", second.Sub(first), third.Sub(second))
	}
}

// Exercise the reloaded TLS credentials over an in-memory transport; the
// plaintext fake control service cannot observe client certificates itself.
func assertMTLSClientCertificate(t *testing.T, ca *testutil.TestCA, client tls.Certificate, want []byte) {
	t.Helper()
	keyPEM, err := marshalKeyPEM(ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err := tls.X509KeyPair(ca.PEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.PEM) {
		t.Fatal("invalid test CA")
	}
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	if err := a.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := b.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	server := tls.Server(b, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	done := make(chan error, 1)
	go func() { done <- server.Handshake() }()
	conn := tls.Client(a, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost", Certificates: []tls.Certificate{client}})
	if err := conn.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	peer := server.ConnectionState().PeerCertificates
	if len(peer) == 0 || !bytes.Equal(peer[0].Raw, want) {
		t.Fatal("mTLS sent a stale client certificate")
	}
}

func TestRenewLoopInvalidResponseDoesNotReplace(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "wrong-key", "wrong-node", "wrong-ca", "expired"} {
		t.Run(mode, func(t *testing.T) {
			var fw *testutil.FakeWorkspace
			ready := make(chan struct{})
			attempts := make(chan struct{}, 8)
			s, workspace, mat := sessionFixture(t, func(ctx context.Context, req *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error) {
				select {
				case <-ready:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				attempts <- struct{}{}
				ca, nodeID, expires := fw.CA, "n_test01", time.Now().Add(30*24*time.Hour)
				var cert []byte
				var err error
				switch mode {
				case "missing":
					return &nodev1.RenewCertificateResponse{}, nil
				case "malformed":
					return &nodev1.RenewCertificateResponse{CertificateChain: []byte("invalid PEM")}, nil
				case "wrong-key":
					key, e := identity.GenerateKeyPair()
					if e != nil {
						return nil, e
					}
					cert, err = ca.IssueNode(nodeID, &key.PublicKey, expires)
				default:
					if mode == "wrong-node" {
						nodeID = "n_other"
					}
					if mode == "wrong-ca" {
						ca, err = testutil.NewTestCA()
						if err != nil {
							return nil, err
						}
					}
					if mode == "expired" {
						expires = time.Now().Add(-time.Second)
					}
					cert, err = ca.IssueNodeFromCSR(nodeID, req.GetCsr(), expires)
				}
				return &nodev1.RenewCertificateResponse{CertificateChain: cert}, err
			})
			fw = workspace
			old := nearExpiry(t, s, fw, mat)
			beforeMeta, err := s.cfg.Store.GetNodeMeta()
			if err != nil {
				t.Fatal(err)
			}
			close(ready)
			runSession(t, s)
			for i := 0; i < 2; i++ {
				select {
				case <-attempts:
				case <-time.After(3 * time.Second):
					t.Fatal("renew loop did not retry invalid response")
				}
			}
			got, err := os.ReadFile(filepath.Join(s.cfg.AgentDir, identity.CertFileName))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, old) {
				t.Fatal("invalid renewal replaced certificate")
			}
			afterMeta, err := s.cfg.Store.GetNodeMeta()
			if err != nil {
				t.Fatal(err)
			}
			if !afterMeta.ExpiresAt.Equal(beforeMeta.ExpiresAt) {
				t.Fatal("invalid renewal changed metadata")
			}
		})
	}
}

func TestLoadCredsRejectsTamperedCA(t *testing.T) {
	s, _, _ := sessionFixture(t, nil)
	if _, _, _, err := s.loadCreds(); err != nil {
		t.Fatal(err)
	}
	other, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cfg.AgentDir, identity.CAFileName), other.PEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.loadCreds(); err == nil || !strings.Contains(err.Error(), "ca.crt pin") {
		t.Fatalf("tampered CA accepted: %v", err)
	}
	if _, err := enroll.RenewCertificate(context.Background(), enroll.Options{AgentDir: s.cfg.AgentDir, Store: s.cfg.Store, AllowInsecure: true}, true); err == nil || !strings.Contains(err.Error(), "ca.crt pin") {
		t.Fatalf("renew accepted tampered CA: %v", err)
	}
}
