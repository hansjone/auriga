package runtime_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/conn"
	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Synthetic W authority on an actual loopback TLS server. This verifies the
// production Node transport and persistent caller, not the Java/PG authority.
type poolTaskControlFixture struct {
	workspacev1.UnimplementedSharedDshServiceServer
	t                         *testing.T
	iam                       *admissionAuthority
	mu                        sync.Mutex
	address                   string
	live                      context.Context
	memory                    map[string]*nodev1.SharedDshPoolMemorySample
	notify                    chan struct{}
	active, ended             map[string]*nodev1.SharedDshSessionBinding
	admits, releases, samples atomic.Int32
	admitMode, releaseMode    atomic.Int32
}

func (a *poolTaskControlFixture) checkLocked(ctx context.Context) error {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no TLS peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.PeerCertificates) != 1 || len(ti.State.VerifiedChains) == 0 || len(ti.State.PeerCertificates[0].URIs) != 1 || ti.State.PeerCertificates[0].URIs[0].String() != "auriga://node/n_test01" {
		return status.Error(codes.Unauthenticated, "wrong Node mTLS")
	}
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 0 || len(md.Get("auriga-tenant-id")) != 0 {
		a.t.Error("service JWT leaked onto NodeControl task transport")
	}
	if a.live == nil || a.live.Err() != nil || p.Addr.String() != a.address {
		return status.Error(codes.PermissionDenied, "not the actual Session TLS connection")
	}
	return nil
}

type poolControlStream struct {
	grpc.ServerStream
	authority *poolTaskControlFixture
}

func (s *poolControlStream) RecvMsg(value any) error {
	if err := s.ServerStream.RecvMsg(value); err != nil {
		return err
	}
	if frame, ok := value.(*nodev1.SessionRequest); ok && frame.GetSharedDshPoolMemorySample() != nil {
		a := s.authority
		a.mu.Lock()
		defer a.mu.Unlock()
		if err := a.checkLocked(s.Context()); err != nil {
			return err
		}
		sample := frame.GetSharedDshPoolMemorySample()
		a.memory[sample.PhysicalInstanceId] = proto.Clone(sample).(*nodev1.SharedDshPoolMemorySample)
		a.samples.Add(1)
		close(a.notify)
		a.notify = make(chan struct{})
	}
	return nil
}

func (a *poolTaskControlFixture) AdmitSharedDshTask(ctx context.Context, in *workspacev1.AdmitSharedDshTaskRequest) (*workspacev1.AdmitSharedDshTaskResponse, error) {
	a.admits.Add(1)
	// Session Send is not a server transaction ACK. Wait here only to contain
	// loopback scheduling in the synthetic authority, never in Node production.
	for {
		a.mu.Lock()
		if err := a.checkLocked(ctx); err != nil {
			a.mu.Unlock()
			return nil, err
		}
		if a.memory[in.Binding.PhysicalInstanceId] != nil {
			break
		}
		notify := a.notify
		a.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer a.mu.Unlock()
	sample := a.memory[in.Binding.PhysicalInstanceId]
	if sample.TenantId != in.Binding.TenantId || sample.ValidUntil <= time.Now().Unix() || sample.MemoryUsedBytes*100 >= sample.MemoryLimitBytes*80 {
		return nil, status.Error(codes.Unavailable, "memory unavailable")
	}
	if !proto.Equal(a.iam.bindings[in.Binding.SessionId], in.Binding) {
		return nil, status.Error(codes.NotFound, "binding absent")
	}
	if _, ended := a.ended[in.TaskId]; ended {
		return nil, status.Error(codes.AlreadyExists, "ended")
	}
	for id, b := range a.active {
		if id != in.TaskId && b.MemberId == in.Binding.MemberId {
			return nil, status.Error(codes.AlreadyExists, "active member")
		}
	}
	if len(a.active) >= 4 && a.active[in.TaskId] == nil {
		return nil, status.Error(codes.AlreadyExists, "full pool")
	}
	a.active[in.TaskId] = proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding)
	out := &workspacev1.AdmitSharedDshTaskResponse{Admitted: true, Binding: proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding), TaskId: in.TaskId}
	switch a.admitMode.Load() {
	case 1:
		return &workspacev1.AdmitSharedDshTaskResponse{}, nil
	case 2:
		out.Binding.Revision++
	case 3:
		return nil, status.Error(codes.Unavailable, "unknown Admit")
	case 4:
		out.Admitted = false
	case 5:
		out.TaskId = in.Binding.SessionId
	}
	return out, nil
}

func (a *poolTaskControlFixture) ReleaseSharedDshTask(ctx context.Context, in *workspacev1.ReleaseSharedDshTaskRequest) (*workspacev1.ReleaseSharedDshTaskResponse, error) {
	a.releases.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkLocked(ctx); err != nil {
		return nil, err
	}
	if active := a.active[in.TaskId]; active != nil && !proto.Equal(active, in.Binding) {
		return nil, status.Error(codes.NotFound, "wrong binding")
	}
	_, fencedIAM := a.iam.stopped.Load(in.TaskId)
	activeIAM := !fencedIAM
	if a.active[in.TaskId] != nil {
		// Reserved roots also wait for their children. An absent rejected UUID
		// must not be confused with another live root of the same member.
		a.iam.running.Range(func(_, value any) bool {
			b := value.(*nodev1.SharedDshSessionBinding)
			activeIAM = activeIAM || proto.Equal(b, in.Binding)
			return true
		})
	}
	if activeIAM {
		return nil, status.Error(codes.FailedPrecondition, "IAM root/child not fenced")
	}
	out := &workspacev1.ReleaseSharedDshTaskResponse{Fenced: true, Binding: proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding), TaskId: in.TaskId}
	switch a.releaseMode.Load() {
	case 1:
		return &workspacev1.ReleaseSharedDshTaskResponse{}, nil
	case 2:
		out.Binding.AuthGeneration++
	case 3:
		return nil, status.Error(codes.Unavailable, "unknown Release")
	case 4:
		out.Fenced = false
	case 5:
		out.TaskId = in.Binding.SessionId
	}
	a.ended[in.TaskId] = proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding)
	delete(a.active, in.TaskId)
	return out, nil
}

func startPoolTaskControl(t *testing.T, m *agentruntime.PersistentPools, gate *agentruntime.PoolTaskAdmission, iam *admissionAuthority) *poolTaskControlFixture {
	t.Helper()
	a := &poolTaskControlFixture{t: t, iam: iam, memory: map[string]*nodev1.SharedDshPoolMemorySample{}, notify: make(chan struct{}), active: map[string]*nodev1.SharedDshSessionBinding{}, ended: map[string]*nodev1.SharedDshSessionBinding{}}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{{Kid: "synthetic-task-root", PublicKey: publicKey, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}}, RegisterServices: func(server grpc.ServiceRegistrar) { workspacev1.RegisterSharedDshServiceServer(server, a) }, SessionInterceptor: func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != nodev1.NodeControlService_Session_FullMethodName {
			return handler(srv, stream)
		}
		p, ok := peer.FromContext(stream.Context())
		if !ok {
			return fmt.Errorf("missing Session peer")
		}
		a.mu.Lock()
		a.address, a.live = p.Addr.String(), stream.Context()
		a.mu.Unlock()
		return handler(srv, &poolControlStream{ServerStream: stream, authority: a})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fw.Close)
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err = enroll.Join(t.Context(), enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: dir}); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m.TaskAdmission = gate
	m.Memory = func(ctx context.Context, pool string) (*nodev1.SharedDshPoolMemorySample, error) {
		sample, err := m.Driver.PoolMemory(ctx, pool)
		if err == nil {
			sample.TenantId = gate.TenantID
		}
		return sample, err
	}
	session := conn.New(conn.Config{AgentDir: dir, Store: store, SharedRuntime: m, SharedPools: m.Pools, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MinBackoff: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("unexpected NodeControl shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("NodeControl did not stop")
		}
	})
	if err = testutil.WaitFor(3*time.Second, session.Connected); err != nil {
		t.Fatal(err)
	}
	return a
}
