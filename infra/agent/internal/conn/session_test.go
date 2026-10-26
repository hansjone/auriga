package conn

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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
	"google.golang.org/protobuf/types/known/timestamppb"
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
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("session stopped with unexpected error: %v", err)
				}
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
	// These are scheduling guards, not a certificate-renewal business SLA.
	const guard = 10 * time.Second
	type result struct {
		started, returned time.Time
		cert              []byte
		err               error
	}
	results := make(chan result, 3)
	var count atomic.Int32
	var handlers sync.WaitGroup
	var fw *testutil.FakeWorkspace
	s, workspace, mat := sessionFixture(t, func(ctx context.Context, req *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error) {
		handlers.Add(1)
		defer handlers.Done()
		r := result{started: time.Now()}
		if count.Add(1) <= 2 {
			r.err = status.Error(codes.Unavailable, "retry renewal")
		} else {
			r.cert, r.err = fw.CA.IssueNodeFromCSR("n_test01", req.GetCsr(), time.Now().Add(30*24*time.Hour))
		}
		r.returned = time.Now()
		select {
		case results <- r:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &nodev1.RenewCertificateResponse{CertificateChain: r.cert}, r.err
	})
	fw = workspace
	old := nearExpiry(t, s, fw, mat)
	// Complete PKI and file preparation before starting any phase guard.
	phase := "RPC 1 failure"
	var lastErr error
	certPath := filepath.Join(s.cfg.AgentDir, identity.CertFileName)
	diagnostic := func() string {
		b, err := os.ReadFile(certPath)
		return fmt.Sprintf("phase=%s attempts=%d last_error=%v certificate_changed=%t file_error=%v",
			phase, count.Load(), lastErr, err == nil && !bytes.Equal(b, old), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	joined := false
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if !joined {
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, context.Canceled) {
					t.Errorf("session exit: %v; %s", err, diagnostic())
				}
			case <-time.After(guard):
				t.Errorf("session join guard expired; %s", diagnostic())
				return
			}
		}
		// Stop dispatch before joining server callbacks, including canceled RPCs.
		fw.Close()
		handlersDone := make(chan struct{})
		go func() { handlers.Wait(); close(handlersDone) }()
		select {
		case <-handlersDone:
		case <-time.After(guard):
			t.Errorf("RPC handler join guard expired; %s", diagnostic())
		}
	}
	// Registered after fixture cleanup: cancel + join precede store/TempDir removal.
	t.Cleanup(stop)
	var previous time.Time
	var issued []byte
	for i := 0; i < 3; i++ {
		phase = fmt.Sprintf("RPC %d return", i+1)
		timer := time.NewTimer(guard)
		var r result
		select {
		case r = <-results:
			timer.Stop()
		case err := <-done:
			timer.Stop()
			joined = true
			t.Fatalf("session ended before renewal: %v; %s", err, diagnostic())
		case <-timer.C:
			t.Fatalf("RPC scheduling guard expired; %s", diagnostic())
		}
		lastErr = r.err
		if i > 0 {
			minimum := time.Duration(80*i) * time.Millisecond
			if gap := r.started.Sub(previous); gap < minimum {
				t.Fatalf("backoff bypassed: got %v want >= %v; %s", gap, minimum, diagnostic())
			}
		}
		previous = r.returned
		if i < 2 {
			if status.Code(r.err) != codes.Unavailable {
				t.Fatalf("expected Unavailable; %s", diagnostic())
			}
		} else {
			if r.err != nil || len(r.cert) == 0 || bytes.Equal(r.cert, old) {
				t.Fatalf("third RPC did not issue a new certificate; %s", diagnostic())
			}
			issued = r.cert
		}
	}
	phase = "new certificate persisted and reloadable"
	timer := time.NewTimer(guard)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, err := os.ReadFile(certPath)
		if err == nil && bytes.Equal(b, issued) {
			got, _, cert, loadErr := s.loadCreds()
			lastErr = loadErr
			if loadErr == nil && bytes.Equal(got.CertificatePEM, issued) &&
				!bytes.Equal(got.CertificatePEM, old) && cert.Leaf.NotAfter.After(time.Now().Add(20*24*time.Hour)) {
				break
			}
		} else {
			lastErr = err
		}
		select {
		case <-ticker.C:
		case err := <-done:
			joined = true
			t.Fatalf("session ended before persistence: %v; %s", err, diagnostic())
		case <-timer.C:
			t.Fatalf("persistence scheduling guard expired; %s", diagnostic())
		}
	}
	phase = "session cancellation and join"
	stop()
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
func TestTLSRenewTrustFailureRetainsCertificateAndReconnect(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &nodev1.ArtifactSigningKey{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{root}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fw.Close)
	dir := filepath.Join(t.TempDir(), "agent")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mat, err := enroll.Join(ctx, enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var logs bytes.Buffer
	s := New(Config{AgentDir: dir, Store: st, RenewCheckEvery: time.Hour, MinBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	old := nearExpiry(t, s, fw, mat)
	badPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fw.SetArtifactSigningKeys([]*nodev1.ArtifactSigningKey{{Kid: "unapproved-root", PublicKey: badPub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}})
	if err := s.tryRenew(ctx); err != nil {
		t.Fatalf("completed certificate renewal was treated as certificate failure: %v", err)
	}
	if !strings.Contains(logs.String(), "certificate renewed; runtime release trust unchanged") {
		t.Fatal("split trust failure was not reported")
	}
	_, meta, cert, err := s.loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	oldCerts, err := identity.ParseCertPEM(old)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(cert.Certificate[0], oldCerts[0].Raw) {
		t.Fatal("reconnect loaded old certificate")
	}
	if !meta.ExpiresAt.Equal(cert.Leaf.NotAfter) {
		t.Fatal("certificate and metadata differ")
	}
	ring, err := st.GetReleaseTrust()
	if err != nil || len(ring.Records) != 1 || ring.Records[0].Kid != "root" {
		t.Fatalf("bad roots replaced trust: %v", err)
	}
	// Stop before reading log bytes again: the session logger runs in another goroutine.
	stop := runSession(t, s)
	if err := testutil.WaitFor(3*time.Second, s.Connected); err != nil {
		t.Fatal(err)
	}
	stop()
}
