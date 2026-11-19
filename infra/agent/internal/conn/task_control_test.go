package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type transportTaskAuthority struct {
	workspacev1.UnimplementedSharedDshServiceServer
	mu             sync.Mutex
	address        string
	live           context.Context
	admit, release func(context.Context)
}

func (a *transportTaskAuthority) check(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "TLS required")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.PeerCertificates) == 0 {
		return status.Error(codes.Unauthenticated, "mTLS required")
	}
	if a.live == nil || a.live.Err() != nil || a.address != p.Addr.String() {
		return status.Error(codes.PermissionDenied, "different TLS connection")
	}
	return nil
}
func (a *transportTaskAuthority) AdmitSharedDshTask(ctx context.Context, in *workspacev1.AdmitSharedDshTaskRequest) (*workspacev1.AdmitSharedDshTaskResponse, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}
	a.mu.Lock()
	callback := a.admit
	a.mu.Unlock()
	if callback != nil {
		callback(ctx)
	}
	return &workspacev1.AdmitSharedDshTaskResponse{Admitted: true, Binding: in.Binding, TaskId: in.TaskId}, nil
}
func (a *transportTaskAuthority) ReleaseSharedDshTask(ctx context.Context, in *workspacev1.ReleaseSharedDshTaskRequest) (*workspacev1.ReleaseSharedDshTaskResponse, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}
	a.mu.Lock()
	callback := a.release
	a.mu.Unlock()
	if callback != nil {
		callback(ctx)
	}
	return &workspacev1.ReleaseSharedDshTaskResponse{Fenced: true, Binding: in.Binding, TaskId: in.TaskId}, nil
}

func taskTransportFixture(t *testing.T) (*Session, *transportTaskAuthority) {
	t.Helper()
	a := &transportTaskAuthority{}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{{Kid: "task-test-root", PublicKey: publicKey, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}}, RegisterServices: func(server grpc.ServiceRegistrar) { workspacev1.RegisterSharedDshServiceServer(server, a) }, SessionInterceptor: func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod == nodev1.NodeControlService_Session_FullMethodName {
			p, _ := peer.FromContext(stream.Context())
			a.mu.Lock()
			a.address, a.live = p.Addr.String(), stream.Context()
			a.mu.Unlock()
		}
		return handler(srv, stream)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fw.Close)
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err = enroll.Join(t.Context(), enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return New(Config{AgentDir: dir, Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), SharedRuntime: &runtime.PersistentPools{Memory: func(context.Context, string) (*nodev1.SharedDshPoolMemorySample, error) { return poolSample(), nil }}}), a
}
func runTaskTransport(t *testing.T, s *Session) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.runOnce(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled {
					t.Errorf("unexpected Session shutdown: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("Session did not stop")
			}
		})
	}
	t.Cleanup(stop)
	if err := testutil.WaitFor(3*time.Second, s.Connected); err != nil {
		t.Fatal(err)
	}
	return stop
}
func taskTransportBinding() *nodev1.SharedDshSessionBinding {
	v := poolSample()
	return &nodev1.SharedDshSessionBinding{TenantId: v.TenantId, PhysicalInstanceId: v.PhysicalInstanceId, MemberId: v.TenantId, PlacementId: v.TenantId, SessionId: v.TenantId, PlatformSessionId: v.TenantId, Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1, AuthGeneration: 1}
}

func TestTaskControlRequiresActualSessionTLSConnection(t *testing.T) {
	s, _ := taskTransportFixture(t)
	stop := runTaskTransport(t, s)
	b := taskTransportBinding()
	id := "00000000-0000-4000-8000-000000000010"
	if err := s.AdmitSharedTask(t.Context(), b, id, poolSample()); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSharedTask(b, id); err != nil {
		t.Fatal(err)
	}
	mat, meta, certificate, err := s.loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	other, err := tlsutil.Dial(t.Context(), tlsutil.DialOptions{Endpoint: meta.ControlEndpoint, CAFingerprint: meta.CAFingerprint, CAPEM: mat.CAPEM, ClientCertificate: &certificate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	_, err = workspacev1.NewSharedDshServiceClient(other).AdmitSharedDshTask(t.Context(), &workspacev1.AdmitSharedDshTaskRequest{Binding: b, TaskId: id})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("a second connection with the same certificate was accepted", err)
	}
	stop()
	if err = s.CheckSharedTask(b, id); err == nil {
		t.Fatal("disconnected task retained current lease")
	}
	if err = s.ReleaseSharedTask(t.Context(), b, id); err == nil {
		t.Fatal("offline Release returned confirmation")
	}
	runTaskTransport(t, s)
	if err = s.CheckSharedTask(b, id); err == nil {
		t.Fatal("reconnect adopted prior holder's task")
	}
	if err = s.ReleaseSharedTask(t.Context(), b, id); err != nil {
		t.Fatal("new current holder could not drain exact old reservation", err)
	}
}

func TestTaskControlLateRepliesCannotCrossSession(t *testing.T) {
	for _, phase := range []string{"Admit", "Release"} {
		t.Run(phase, func(t *testing.T) {
			s, a := taskTransportFixture(t)
			started, resume := make(chan struct{}), make(chan struct{})
			block := func(context.Context) { close(started); <-resume }
			a.mu.Lock()
			if phase == "Admit" {
				a.admit = block
			} else {
				a.release = block
			}
			a.mu.Unlock()
			t.Cleanup(func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			})
			stop := runTaskTransport(t, s)
			b := taskTransportBinding()
			id := "00000000-0000-4000-8000-000000000010"
			if phase == "Release" {
				if err := s.AdmitSharedTask(t.Context(), b, id, poolSample()); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				if phase == "Admit" {
					done <- s.AdmitSharedTask(t.Context(), b, id, poolSample())
				} else {
					done <- s.ReleaseSharedTask(t.Context(), b, id)
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("RPC boundary not reached")
			}
			stop()
			close(resume)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("late RPC was accepted after Session ended")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("old task RPC did not cancel")
			}
			if err := s.CheckSharedTask(b, id); err == nil {
				t.Fatal("late ACK revived current task")
			}
		})
	}
}
