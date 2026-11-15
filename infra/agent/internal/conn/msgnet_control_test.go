package conn

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"path/filepath"
	"testing"
	"time"
)

type largePublicControlStream struct{ grpc.ServerStream }

func (s largePublicControlStream) SendMsg(value any) error {
	if e := s.ServerStream.SendMsg(value); e != nil {
		return e
	}
	if response, ok := value.(*nodev1.SessionResponse); ok && response.GetWelcome() != nil {
		return s.ServerStream.SendMsg(&nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetControlPackage{MsgnetControlPackage: &nodev1.MsgnetControlPackage{CanonicalJson: bytes.Repeat([]byte("x"), 5<<20)}}})
	}
	return nil
}

type largePublicInstaller struct{ received chan int }

func (i largePublicInstaller) InstallControl(_ context.Context, body []byte) ([]byte, error) {
	i.received <- len(body)
	return []byte("public test receipt"), nil
}
func (i largePublicInstaller) InstallTransport(context.Context, []byte) error { return nil }

func TestLargePublicControlUsesBoundedNativeGrpcLimit(t *testing.T) {
	public, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	fw, e := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{TLS: true, ArtifactSigningKeys: []*nodev1.ArtifactSigningKey{{Kid: "test-release-root", PublicKey: public, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}}, SessionInterceptor: func(server any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(server, largePublicControlStream{stream})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer fw.Close()
	directory := filepath.Join(t.TempDir(), "agent")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, e = enroll.Join(ctx, enroll.Options{Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AgentDir: directory})
	if e != nil {
		t.Fatal(e)
	}
	store, e := state.Open(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	installer := largePublicInstaller{received: make(chan int, 2)}
	session := New(Config{AgentDir: directory, Store: store, Msgnet: installer})
	stop := runSession(t, session)
	defer stop()
	select {
	case size := <-installer.received:
		if size != 5<<20 {
			t.Fatal("public control truncated")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native gRPC rejected a public package above its 4 MiB default")
	}
	if !session.Connected() {
		t.Fatal("message control closed native lifecycle session")
	}
}

type msgnetInstaller struct {
	fail            bool
	control, bundle []byte
}

type blockedMsgnet struct{ started chan struct{} }

func (b *blockedMsgnet) InstallControl(ctx context.Context, _ []byte) ([]byte, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockedMsgnet) InstallTransport(ctx context.Context, _ []byte) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestBlockedMsgnetDoesNotBlockLifecycleReceiveOrUnboundQueue(t *testing.T) {
	b := &blockedMsgnet{started: make(chan struct{}, 1)}
	s := New(Config{Msgnet: b})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.msgnetLoop(ctx) }()
	msg := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetControlPackage{MsgnetControlPackage: &nodev1.MsgnetControlPackage{CanonicalJson: []byte("public test control")}}}
	if e := s.handleReceivedControl(ctx, msg); e != nil {
		t.Fatal(e)
	}
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	for n := 0; n < cap(s.msgnetControlCh); n++ {
		if e := s.handleReceivedControl(ctx, msg); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.handleReceivedControl(ctx, msg); e == nil {
		t.Fatal("unbounded message control queue")
	}
	processed := make(chan error, 1)
	go func() { processed <- s.handleReceivedControl(ctx, &nodev1.SessionResponse{}) }()
	select {
	case e := <-processed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("message installer blocked lifecycle handling")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("message worker did not stop")
	}
}

func TestMsgnetReceiptBackpressureWaitsForRedelivery(t *testing.T) {
	s := New(Config{Msgnet: &msgnetInstaller{}})
	for n := 0; n < cap(s.msgnetReceiptCh); n++ {
		s.msgnetReceiptCh <- &nodev1.MsgnetControlReceipt{}
	}
	msg := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetControlPackage{MsgnetControlPackage: &nodev1.MsgnetControlPackage{CanonicalJson: []byte("public test control")}}}
	if e := s.handleControl(context.Background(), msg); e == nil {
		t.Fatal("full receipt queue silently acknowledged")
	}
}

func (i *msgnetInstaller) InstallControl(_ context.Context, b []byte) ([]byte, error) {
	i.control = append([]byte(nil), b...)
	if i.fail {
		return nil, errors.New("durability failed")
	}
	return []byte("signed durable receipt"), nil
}
func (i *msgnetInstaller) InstallTransport(_ context.Context, b []byte) error {
	i.bundle = append([]byte(nil), b...)
	return nil
}
func TestMsgnetControlIndependentOfReconciler(t *testing.T) {
	installer := &msgnetInstaller{}
	s := New(Config{Msgnet: installer})
	body := []byte("canonical signed public control")
	msg := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetControlPackage{MsgnetControlPackage: &nodev1.MsgnetControlPackage{CanonicalJson: body}}}
	if err := s.handleControl(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	body[0] = 'X'
	if string(installer.control) != "canonical signed public control" {
		t.Fatal("mutable transport buffer retained")
	}
	select {
	case r := <-s.msgnetReceiptCh:
		if string(r.CanonicalJson) != "signed durable receipt" {
			t.Fatal("receipt changed")
		}
	default:
		t.Fatal("missing receipt")
	}
	installer.fail = true
	if err := s.handleControl(context.Background(), msg); err == nil {
		t.Fatal("failed persistence acknowledged")
	}
	if len(s.msgnetReceiptCh) != 0 {
		t.Fatal("receipt emitted on failure")
	}
	s.cfg.Msgnet = nil
	if err := s.handleControl(context.Background(), msg); err == nil {
		t.Fatal("missing installer silently accepted")
	}
	s.cfg.Msgnet = installer
	bundle := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetTransportBundle{MsgnetTransportBundle: &nodev1.MsgnetTransportBundle{CanonicalJson: []byte("public bundle")}}}
	if err := s.handleControl(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	if string(installer.bundle) != "public bundle" {
		t.Fatal("bundle lost")
	}
}
