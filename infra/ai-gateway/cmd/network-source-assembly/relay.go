package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Relay uses generated services and preserves incoming service JWT/delegation,
// bytes, deadlines and cancellation. It never makes an authentication decision.
type relay struct {
	iam.UnimplementedNetworkGatewayServiceServer
	collab.UnimplementedNetworkMailboxServiceServer
	iamAddress, collabAddress string
	iam, collab               *grpc.ClientConn
	servers                   []*grpc.Server
	listeners                 []net.Listener
	mu                        sync.Mutex
	method, mode              string
	records                   []map[string]any
}

func startRelay(_ context.Context, iamTarget, collabTarget string) (*relay, error) {
	r := &relay{}
	var err error
	r.iam, err = grpc.NewClient(iamTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	r.collab, err = grpc.NewClient(collabTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		r.close()
		return nil, err
	}
	for i := range 2 {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			r.close()
			return nil, e
		}
		server := grpc.NewServer()
		r.listeners = append(r.listeners, listener)
		r.servers = append(r.servers, server)
		if i == 0 {
			iam.RegisterNetworkGatewayServiceServer(server, r)
			r.iamAddress = listener.Addr().String()
		} else {
			collab.RegisterNetworkMailboxServiceServer(server, r)
			r.collabAddress = listener.Addr().String()
		}
		go func() { _ = server.Serve(listener) }()
	}
	return r, nil
}
func (r *relay) close() {
	for _, s := range r.servers {
		s.Stop()
	}
	for _, l := range r.listeners {
		_ = l.Close()
	}
	if r.iam != nil {
		_ = r.iam.Close()
	}
	if r.collab != nil {
		_ = r.collab.Close()
	}
}
func (r *relay) actor(tenant string) *collab.Actor { return &collab.Actor{TenantId: tenant} }
func (r *relay) trace() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any{}, r.records...)
}
func (r *relay) fault(method, mode string) error {
	if method != "SourceRegister" && method != "SourceGet" {
		return errors.New("unsupported fault method")
	}
	if mode != "drop" && mode != "corrupt" && mode != "none" {
		return errors.New("unsupported fault mode")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method, r.mode = method, mode
	return nil
}
func (r *relay) forward(ctx context.Context, method string, req, reply proto.Message, connection *collab.NetworkMailboxConnection) error {
	incoming, _ := metadata.FromIncomingContext(ctx)
	// No headers other than the existing internal service identity and delegation.
	outgoing := metadata.MD{}
	for _, name := range []string{"authorization", "auriga-tenant-id"} {
		outgoing[name] = incoming.Get(name)
	}
	ctx = metadata.NewOutgoingContext(ctx, outgoing)
	target, service := r.collab, "/auriga.collab.v1.NetworkMailboxService/"
	if connection == nil {
		target, service = r.iam, "/auriga.iam.v1.NetworkGatewayService/"
	}
	started := time.Now()
	deadline, bounded := ctx.Deadline()
	err := target.Invoke(ctx, service+method, req, reply)
	contextCode := codes.OK
	if ctx.Err() != nil {
		contextCode = status.FromContextError(ctx.Err()).Code()
	}
	// Numeric loopback peer/method/status identify this hop without copying any
	// status description, metadata, service token or arbitrary transport details.
	event := map[string]any{
		"stage": "rpc", "method": method, "code": status.Code(err).String(),
		"peer": target.Target(), "rpc": service + method,
		"elapsedMillis": time.Since(started).Milliseconds(), "contextCode": contextCode.String(),
	}
	if bounded {
		event["remainingMillisAtStart"] = max(int64(0), deadline.Sub(started).Milliseconds())
		event["deadlineExpired"] = !time.Now().Before(deadline)
	}
	r.mu.Lock()
	mode := ""
	if err == nil && r.method == method {
		mode = r.mode
		if mode != "corrupt" {
			r.method, r.mode = "", ""
		}
	}
	r.mu.Unlock()
	if mode != "" {
		event["fault"] = mode
	}
	if connection != nil {
		event["connection"] = connection
		if frame, ok := req.(interface{ GetFrameJson() []byte }); ok {
			event["request"] = json.RawMessage(frame.GetFrameJson())
		}
		if err == nil {
			if frame, ok := reply.(interface{ GetFrameJson() []byte }); ok {
				event["response"] = json.RawMessage(frame.GetFrameJson())
			}
		}
	}
	if method != "BatchNetworkNodesActive" && method != "NetworkNodeActive" {
		r.mu.Lock()
		r.records = append(r.records, event)
		r.mu.Unlock()
		if e := emit(event); e != nil {
			return status.Error(codes.Unavailable, "assembly evidence output failed")
		}
	}
	if err != nil {
		return err
	}
	if mode == "drop" {
		return status.Error(codes.Unavailable, "assembly outer reply lost after downstream return")
	}
	if mode == "corrupt" {
		// Preserve the genuine upstream frame in evidence, mutate only transport bytes.
		response, ok := reply.(*collab.NetworkMailboxServiceSourceGetResponse)
		if !ok {
			return status.Error(codes.Internal, "invalid corruption boundary")
		}
		var body map[string]any
		if json.Unmarshal(response.FrameJson, &body) != nil {
			return status.Error(codes.Internal, "invalid signed frame")
		}
		auth, ok := body["server_auth"].(map[string]any)
		if !ok {
			return status.Error(codes.Internal, "missing signature")
		}
		sig, ok := auth["signature"].(string)
		if !ok || len(sig) < 2 {
			return status.Error(codes.Internal, "missing signature")
		}
		replacement := "A"
		if sig[0] == 'A' {
			replacement = "B"
		}
		auth["signature"] = replacement + sig[1:]
		response.FrameJson, err = json.Marshal(body)
	}
	return err
}

func (r *relay) RedeemNetworkTicket(c context.Context, q *iam.RedeemNetworkTicketRequest) (*iam.RedeemNetworkTicketResponse, error) {
	p := new(iam.RedeemNetworkTicketResponse)
	e := r.forward(c, "RedeemNetworkTicket", q, p, nil)
	return p, e
}
func (r *relay) NetworkNodeActive(c context.Context, q *iam.NetworkNodeActiveRequest) (*iam.NetworkNodeActiveResponse, error) {
	p := new(iam.NetworkNodeActiveResponse)
	e := r.forward(c, "NetworkNodeActive", q, p, nil)
	return p, e
}
func (r *relay) BatchNetworkNodesActive(c context.Context, q *iam.BatchNetworkNodesActiveRequest) (*iam.BatchNetworkNodesActiveResponse, error) {
	p := new(iam.BatchNetworkNodesActiveResponse)
	e := r.forward(c, "BatchNetworkNodesActive", q, p, nil)
	return p, e
}
func (r *relay) Ready(c context.Context, q *collab.NetworkMailboxServiceReadyRequest) (*collab.NetworkMailboxServiceReadyResponse, error) {
	p := new(collab.NetworkMailboxServiceReadyResponse)
	e := r.forward(c, "Ready", q, p, q.Connection)
	return p, e
}
func (r *relay) Head(c context.Context, q *collab.NetworkMailboxServiceHeadRequest) (*collab.NetworkMailboxServiceHeadResponse, error) {
	p := new(collab.NetworkMailboxServiceHeadResponse)
	e := r.forward(c, "Head", q, p, q.Connection)
	return p, e
}
func (r *relay) Fetch(c context.Context, q *collab.NetworkMailboxServiceFetchRequest) (*collab.NetworkMailboxServiceFetchResponse, error) {
	p := new(collab.NetworkMailboxServiceFetchResponse)
	e := r.forward(c, "Fetch", q, p, q.Connection)
	return p, e
}
func (r *relay) Receipt(c context.Context, q *collab.NetworkMailboxServiceReceiptRequest) (*collab.NetworkMailboxServiceReceiptResponse, error) {
	p := new(collab.NetworkMailboxServiceReceiptResponse)
	e := r.forward(c, "Receipt", q, p, q.Connection)
	return p, e
}
func (r *relay) LocalRegister(c context.Context, q *collab.NetworkMailboxServiceLocalRegisterRequest) (*collab.NetworkMailboxServiceLocalRegisterResponse, error) {
	p := new(collab.NetworkMailboxServiceLocalRegisterResponse)
	e := r.forward(c, "LocalRegister", q, p, q.Connection)
	return p, e
}
func (r *relay) SourceRegister(c context.Context, q *collab.NetworkMailboxServiceSourceRegisterRequest) (*collab.NetworkMailboxServiceSourceRegisterResponse, error) {
	p := new(collab.NetworkMailboxServiceSourceRegisterResponse)
	e := r.forward(c, "SourceRegister", q, p, q.Connection)
	return p, e
}
func (r *relay) SourceGet(c context.Context, q *collab.NetworkMailboxServiceSourceGetRequest) (*collab.NetworkMailboxServiceSourceGetResponse, error) {
	p := new(collab.NetworkMailboxServiceSourceGetResponse)
	e := r.forward(c, "SourceGet", q, p, q.Connection)
	return p, e
}
func (r *relay) SourceRevoke(c context.Context, q *collab.NetworkMailboxServiceSourceRevokeRequest) (*collab.NetworkMailboxServiceSourceRevokeResponse, error) {
	p := new(collab.NetworkMailboxServiceSourceRevokeResponse)
	e := r.forward(c, "SourceRevoke", q, p, q.Connection)
	return p, e
}
