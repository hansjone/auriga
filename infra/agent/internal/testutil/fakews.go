package testutil

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FakeWorkspace is an insecure gRPC server (grpc://) for tests.
// TLS pin checks are covered separately via net.Pipe (localhost TLS may be MITMed by AV).
type FakeWorkspace struct {
	CA *TestCA

	mu            sync.Mutex
	token         string
	tokenUsed     bool
	heartbeat     time.Duration
	rejectExpired bool
	renew         func(context.Context, *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error)

	HelloCount     int
	InventoryCount int
	UsageCount     int
	LastHello      *nodev1.Hello
	LastInventory  *nodev1.Inventory

	DropNextSession bool

	applyCh  chan *nodev1.InstanceSpec
	deleteCh chan *nodev1.DeleteInstance

	lis    net.Listener
	server *grpc.Server
	addr   string

	nodev1.UnimplementedNodeEnrollmentServiceServer
	nodev1.UnimplementedNodeControlServiceServer
}

// FakeWorkspaceOpts configures the fake platform.
type FakeWorkspaceOpts struct {
	Token             string
	HeartbeatInterval time.Duration
	RejectExpired     bool
	Renew             func(context.Context, *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error)
}

// StartFakeWorkspace listens on 127.0.0.1:0 without TLS.
func StartFakeWorkspace(opts FakeWorkspaceOpts) (*FakeWorkspace, error) {
	ca, err := NewTestCA()
	if err != nil {
		return nil, err
	}
	hb := opts.HeartbeatInterval
	if hb <= 0 {
		hb = 50 * time.Millisecond
	}
	token := opts.Token
	if token == "" {
		token = "tok1.secret-value"
	}

	fw := &FakeWorkspace{
		CA:            ca,
		token:         token,
		heartbeat:     hb,
		rejectExpired: opts.RejectExpired,
		renew:         opts.Renew,
		applyCh:       make(chan *nodev1.InstanceSpec, 8),
		deleteCh:      make(chan *nodev1.DeleteInstance, 8),
	}

	fw.server = grpc.NewServer()
	nodev1.RegisterNodeEnrollmentServiceServer(fw.server, fw)
	nodev1.RegisterNodeControlServiceServer(fw.server, fw)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fw.lis = lis
	fw.addr = lis.Addr().String()
	go func() { _ = fw.server.Serve(lis) }()
	return fw, nil
}

// Addr returns host:port.
func (f *FakeWorkspace) Addr() string { return f.addr }

// URL returns grpc://127.0.0.1:port (insecure; for tests).
func (f *FakeWorkspace) URL() string { return "grpc://" + f.addr }

// Token returns the valid join token.
func (f *FakeWorkspace) Token() string { return f.token }

// Close stops the server.
func (f *FakeWorkspace) Close() {
	if f.server != nil {
		f.server.Stop()
	}
	if f.lis != nil {
		_ = f.lis.Close()
	}
}

// PushApply queues an ApplySpec for the active session.
func (f *FakeWorkspace) PushApply(spec *nodev1.InstanceSpec) {
	f.applyCh <- spec
}

// PushDelete queues a DeleteInstance.
func (f *FakeWorkspace) PushDelete(d *nodev1.DeleteInstance) {
	f.deleteCh <- d
}

// Stats returns counters for assertions.
func (f *FakeWorkspace) Stats() (hello, inventory, usage int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.HelloCount, f.InventoryCount, f.UsageCount
}

// SetDropNextSession forces the next Session RPC to fail once.
func (f *FakeWorkspace) SetDropNextSession(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DropNextSession = v
}

func (f *FakeWorkspace) Join(_ context.Context, req *nodev1.JoinRequest) (*nodev1.JoinResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.GetToken() != f.token {
		return nil, status.Error(codes.Unauthenticated, "invalid enrollment token")
	}
	if f.tokenUsed {
		return nil, status.Error(codes.Unauthenticated, "token already used")
	}
	if len(req.GetCsr()) == 0 || len(req.GetSealingPublicKey()) != 32 {
		return nil, status.Error(codes.InvalidArgument, "csr/sealing key required")
	}
	nodeID := "n_test01"
	certPEM, err := f.CA.IssueNodeFromCSR(nodeID, req.GetCsr(), time.Now().Add(30*24*time.Hour))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue cert: %v", err)
	}
	f.tokenUsed = true
	expires := time.Now().Add(30 * 24 * time.Hour)
	return &nodev1.JoinResponse{
		NodeId:               nodeID,
		CertificateChain:     certPEM,
		CaBundle:             f.CA.PEM,
		ControlEndpoint:      f.URL(),
		CertificateExpiresAt: timestamppb.New(expires),
	}, nil
}

func (f *FakeWorkspace) RenewCertificate(ctx context.Context, req *nodev1.RenewCertificateRequest) (*nodev1.RenewCertificateResponse, error) {
	if f.renew != nil {
		return f.renew(ctx, req)
	}
	// Insecure test server cannot require mTLS; issue against default node id when no peer cert.
	nodeID := "n_test01"
	if id, err := peerNodeID(ctx); err == nil {
		nodeID = id
	}
	certPEM, err := f.CA.IssueNodeFromCSR(nodeID, req.GetCsr(), time.Now().Add(30*24*time.Hour))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue cert: %v", err)
	}
	return &nodev1.RenewCertificateResponse{
		CertificateChain:     certPEM,
		CertificateExpiresAt: timestamppb.New(time.Now().Add(30 * 24 * time.Hour)),
	}, nil
}

func (f *FakeWorkspace) Session(stream nodev1.NodeControlService_SessionServer) error {
	ctx := stream.Context()
	if f.rejectExpired {
		if p, ok := peer.FromContext(ctx); ok {
			if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				if len(ti.State.PeerCertificates) > 0 && time.Now().After(ti.State.PeerCertificates[0].NotAfter) {
					return status.Error(codes.Unauthenticated, "expired client certificate")
				}
			}
		}
	}

	f.mu.Lock()
	drop := f.DropNextSession
	if drop {
		f.DropNextSession = false
	}
	f.mu.Unlock()
	if drop {
		return status.Error(codes.Unavailable, "forced drop")
	}

	var gotHello, gotInv bool
	for !gotHello || !gotInv {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch body := msg.Body.(type) {
		case *nodev1.SessionRequest_Hello:
			f.mu.Lock()
			f.HelloCount++
			f.LastHello = body.Hello
			f.mu.Unlock()
			gotHello = true
		case *nodev1.SessionRequest_Inventory:
			f.mu.Lock()
			f.InventoryCount++
			f.LastInventory = body.Inventory
			f.mu.Unlock()
			gotInv = true
		}
	}

	f.mu.Lock()
	nodeID := ""
	if f.LastHello != nil {
		nodeID = f.LastHello.GetNodeId()
	}
	hb := f.heartbeat
	f.mu.Unlock()

	if err := stream.Send(&nodev1.SessionResponse{
		Body: &nodev1.SessionResponse_Welcome{Welcome: &nodev1.Welcome{
			NodeId:            nodeID,
			ServerTime:        timestamppb.Now(),
			ProtocolVersion:   1,
			HeartbeatInterval: durationpb.New(hb),
		}},
	}); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			if msg.GetUsage() != nil {
				f.mu.Lock()
				f.UsageCount++
				f.mu.Unlock()
			}
			if msg.GetInventory() != nil {
				f.mu.Lock()
				f.InventoryCount++
				f.LastInventory = msg.GetInventory()
				f.mu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case spec := <-f.applyCh:
			if err := stream.Send(&nodev1.SessionResponse{
				Body: &nodev1.SessionResponse_Apply{Apply: &nodev1.ApplySpec{Spec: spec}},
			}); err != nil {
				return err
			}
		case d := <-f.deleteCh:
			if err := stream.Send(&nodev1.SessionResponse{
				Body: &nodev1.SessionResponse_Delete{Delete: d},
			}); err != nil {
				return err
			}
		}
	}
}

func peerNodeID(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.PeerCertificates) == 0 {
		return "", status.Error(codes.Unauthenticated, "mTLS required")
	}
	id, err := identity.NodeIDFromCert(ti.State.PeerCertificates[0])
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	return id, nil
}
