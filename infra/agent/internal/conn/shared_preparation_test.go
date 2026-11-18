package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type preparationQuota struct{ calls atomic.Int32 }

func (q *preparationQuota) Ensure(_ context.Context, _ string, bytes, inodes int64) error {
	if bytes != storage.MemberBytes || inodes != storage.MemberInodes {
		return fmt.Errorf("unexpected quota")
	}
	q.calls.Add(1)
	return nil
}

func preparationIntent(member int) *nodev1.SharedDshRuntimeIntent {
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	return &nodev1.SharedDshRuntimeIntent{SchemaVersion: 1, OperationId: id(member),
		Binding: &nodev1.SharedDshBinding{TenantId: id(1), MemberId: id(member + 10),
			PlacementId: id(member), PhysicalInstanceId: id(2),
			Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1},
		Action: "prepare-empty", CancelWaitSeconds: 30, ArchiveRetentionSeconds: 2592000,
		WorkspacePolicy: "empty", SessionPolicy: "discard"}
}

type preparationStream struct {
	grpc.ServerStream
	commands []*nodev1.SharedDshRuntimeIntent
	receipts chan *nodev1.SharedDshRuntimeReceipt
	hellos   chan *nodev1.Hello
}

func (s *preparationStream) SendMsg(msg any) error {
	if err := s.ServerStream.SendMsg(msg); err != nil {
		return err
	}
	if frame, ok := msg.(*nodev1.SessionResponse); ok && frame.GetWelcome() != nil {
		for _, command := range s.commands {
			if err := s.ServerStream.SendMsg(&nodev1.SessionResponse{
				Body: &nodev1.SessionResponse_SharedDshRuntimeIntent{SharedDshRuntimeIntent: command},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *preparationStream) RecvMsg(msg any) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}
	if frame, ok := msg.(*nodev1.SessionRequest); ok {
		if receipt := frame.GetSharedDshRuntimeReceipt(); receipt != nil {
			select {
			case s.receipts <- proto.Clone(receipt).(*nodev1.SharedDshRuntimeReceipt):
			case <-s.Context().Done():
				return s.Context().Err()
			}
		}
		if hello := frame.GetHello(); hello != nil {
			s.hellos <- hello
		}
	}
	return nil
}

func TestSharedPreparationMTLSRoundTripAndReconnectReplaysDurableReceipts(t *testing.T) {
	commands := []*nodev1.SharedDshRuntimeIntent{preparationIntent(3), preparationIntent(4)}
	receipts := make(chan *nodev1.SharedDshRuntimeReceipt, 8)
	hellos := make(chan *nodev1.Hello, 4)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true,
		ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{{Kid: "synthetic-root", PublicKey: publicKey,
			NotAfter: timestamppb.New(time.Now().Add(time.Hour))}},
		SessionInterceptor: func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			p, ok := peer.FromContext(stream.Context())
			if !ok {
				return fmt.Errorf("missing TLS peer")
			}
			info, ok := p.AuthInfo.(credentials.TLSInfo)
			if !ok || info.State.Version < tls.VersionTLS12 || len(info.State.PeerCertificates) == 0 {
				return fmt.Errorf("missing mutual TLS")
			}
			if _, err := identity.NodeIDFromCert(info.State.PeerCertificates[0]); err != nil {
				return err
			}
			return handler(srv, &preparationStream{ServerStream: stream, commands: commands, receipts: receipts, hellos: hellos})
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if _, err := enroll.Join(ctx, enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: agentDir}); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	quota := &preparationQuota{}
	layout := &storage.PoolLayout{Root: t.TempDir(), Quota: quota}
	first := map[string]*nodev1.SharedDshRuntimeReceipt{}
	for round := 0; round < 2; round++ {
		session := New(Config{AgentDir: agentDir, Store: store,
			SharedPools: &agentruntime.SharedPools{Layout: layout},
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			Drivers:     []nodev1.RuntimeKind{nodev1.RuntimeKind_RUNTIME_KIND_DOCKER}})
		connected, disconnect := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- session.runOnce(connected) }()
		select {
		case hello := <-hellos:
			if hello.Labels["auriga.shared-dsh-runtime"] != "prepare-v1" {
				t.Fatal("missing preparation capability")
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		for range commands {
			select {
			case receipt := <-receipts:
				if receipt.Outcome != "confirmed" || !receipt.WorkspaceEmpty {
					t.Fatal("preparation not confirmed")
				}
				if round == 0 {
					first[receipt.OperationId] = receipt
				} else if !proto.Equal(first[receipt.OperationId], receipt) {
					t.Fatal("reconnect replaced durable receipt")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		disconnect()
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("session did not stop")
		}
		// Existing content must survive the replay, even after rebuilding the controller.
		path, err := layout.Workspace(commands[0].Binding.PhysicalInstanceId, commands[0].Binding.TenantId, commands[0].Binding.MemberId, 1)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(path, "retained.txt")
		if round == 0 {
			if err := os.WriteFile(file, []byte("synthetic member content"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if content, err := os.ReadFile(file); err != nil || string(content) != "synthetic member content" {
			t.Fatal("replay changed member workspace")
		}
	}
	if quota.calls.Load() != 2 {
		t.Fatal("reconnect repeated member preparation")
	}
}

func TestSharedPreparationRejectsUnavailableNonInitialAndInvalidIntents(t *testing.T) {
	command := preparationIntent(3)
	frame := func(command *nodev1.SharedDshRuntimeIntent) *nodev1.SessionResponse {
		return &nodev1.SessionResponse{Body: &nodev1.SessionResponse_SharedDshRuntimeIntent{SharedDshRuntimeIntent: command}}
	}
	session := New(Config{})
	if session.handleControl(context.Background(), frame(command)) == nil {
		t.Fatal("unavailable preparation accepted")
	}
	quota := &preparationQuota{}
	session.cfg.SharedPools = &agentruntime.SharedPools{Layout: &storage.PoolLayout{Root: t.TempDir(), Quota: quota}}
	for _, mutate := range []func(*nodev1.SharedDshRuntimeIntent){
		func(c *nodev1.SharedDshRuntimeIntent) { c.Action = "stop-pool" },
		func(c *nodev1.SharedDshRuntimeIntent) { c.OperationId = preparationIntent(4).OperationId },
		func(c *nodev1.SharedDshRuntimeIntent) {
			c.Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
		},
		func(c *nodev1.SharedDshRuntimeIntent) { c.CancelWaitSeconds = 1 },
	} {
		bad := proto.Clone(command).(*nodev1.SharedDshRuntimeIntent)
		mutate(bad)
		if session.handleControl(context.Background(), frame(bad)) == nil {
			t.Fatal("invalid preparation accepted")
		}
	}
	if quota.calls.Load() != 0 || len(session.sharedReceiptCh) != 0 {
		t.Fatal("rejected intent changed runtime or emitted receipt")
	}
}

func TestSharedPreparationQuotaFailureReturnsOnlyAnUnconfirmedReceipt(t *testing.T) {
	session := New(Config{SharedPools: &agentruntime.SharedPools{
		Layout: &storage.PoolLayout{Root: t.TempDir()},
	}})
	command := preparationIntent(3)
	if err := session.handleControl(context.Background(), &nodev1.SessionResponse{
		Body: &nodev1.SessionResponse_SharedDshRuntimeIntent{SharedDshRuntimeIntent: command},
	}); err != nil {
		t.Fatal(err)
	}
	receipt := <-session.sharedReceiptCh
	if receipt.Outcome != "unconfirmed" || receipt.WorkspaceEmpty || receipt.ExecutionTreeStopped {
		t.Fatal("unavailable hard quota reported preparation success")
	}
}
