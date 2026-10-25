package network

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const testTenant = "01900000-0000-7000-8000-000000000001"
const testDevice = "01900000-0000-7000-8000-000000000002"
const connectTicket = `{"tenant":"` + testTenant + `","node":"1","device":"` + testDevice + `","ticket":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`

type fakeIAM struct {
	iam.UnimplementedNetworkGatewayServiceServer
	mu          sync.Mutex
	used        bool
	delay       time.Duration
	failure     error
	active      bool
	calls       int
	mutate      func(*iam.RedeemNetworkTicketResponse)
	mutateBatch func(*iam.BatchNetworkNodesActiveResponse)
}

func (f *fakeIAM) Invoke(ctx context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	switch method {
	case iam.NetworkGatewayService_RedeemNetworkTicket_FullMethodName:
		resp, err := f.RedeemNetworkTicket(ctx, args.(*iam.RedeemNetworkTicketRequest))
		if err == nil {
			proto.Merge(reply.(*iam.RedeemNetworkTicketResponse), resp)
		}
		return err
	case iam.NetworkGatewayService_NetworkNodeActive_FullMethodName:
		resp, err := f.NetworkNodeActive(ctx, args.(*iam.NetworkNodeActiveRequest))
		if err == nil {
			proto.Merge(reply.(*iam.NetworkNodeActiveResponse), resp)
		}
		return err
	case iam.NetworkGatewayService_BatchNetworkNodesActive_FullMethodName:
		resp, err := f.BatchNetworkNodesActive(ctx, args.(*iam.BatchNetworkNodesActiveRequest))
		if err == nil {
			proto.Merge(reply.(*iam.BatchNetworkNodesActiveResponse), resp)
		}
		return err
	default:
		return status.Error(codes.Unimplemented, "unknown RPC")
	}
}

func (f *fakeIAM) beforeCall(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	delay, failure := f.delay, f.failure
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	if failure != nil {
		return failure
	}
	return nil
}

func (f *fakeIAM) NetworkNodeActive(ctx context.Context, _ *iam.NetworkNodeActiveRequest) (*iam.NetworkNodeActiveResponse, error) {
	if err := f.beforeCall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &iam.NetworkNodeActiveResponse{Active: f.active}, nil
}

func (f *fakeIAM) BatchNetworkNodesActive(ctx context.Context, req *iam.BatchNetworkNodesActiveRequest) (*iam.BatchNetworkNodesActiveResponse, error) {
	if err := f.beforeCall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &iam.BatchNetworkNodesActiveResponse{}
	for _, id := range req.Nodes {
		resp.Nodes = append(resp.Nodes, &iam.NetworkNodeStatus{Identity: id, Active: f.active})
	}
	if f.mutateBatch != nil {
		f.mutateBatch(resp)
	}
	return resp, nil
}

func (f *fakeIAM) RedeemNetworkTicket(ctx context.Context, req *iam.RedeemNetworkTicketRequest) (*iam.RedeemNetworkTicketResponse, error) {
	if err := f.beforeCall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.used {
		return nil, status.Error(codes.PermissionDenied, "common.forbidden")
	}
	f.used = true
	resp := &iam.RedeemNetworkTicketResponse{
		Realm: "9223372036854775809", Node: req.GetNode(), Device: req.GetDevice(),
		ServerAudience: req.GetServerAudience(), Ingress: req.GetIngress(),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}
	if f.mutate != nil {
		f.mutate(resp)
	}
	return resp, nil
}

func (*fakeIAM) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("unused")
}

func testRedeemer(t *testing.T, f *fakeIAM) *IAMRedeemer {
	t.Helper()
	r, err := NewIAMRedeemer(f, IAMConfig{ServerID: "server-id", ServerAudience: "server-a", Account: clientAccount, RedeemTimeout: 30 * time.Millisecond, AuthTimeout: time.Second, MaxConnections: 100})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testAttempt() Attempt {
	return Attempt{Ticket: connectTicket, ServerID: "server-id", ClientID: 10, UserNkey: "user", At: time.Now()}
}

func TestIAMRedeemerSuccessAndReplay(t *testing.T) {
	r := testRedeemer(t, &fakeIAM{})
	g, err := r.Redeem(context.Background(), testAttempt())
	if err != nil || !ValidIngressID(g.Ingress) || !g.NotAfter.After(time.Now()) {
		t.Fatalf("grant=%+v err=%v", g, err)
	}
	if _, err = r.Redeem(context.Background(), testAttempt()); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("replay=%v", err)
	}
	if len(r.snapshot()) != 1 {
		t.Fatal("replay changed bindings")
	}
}

func TestIAMRedeemerUnavailableAndTimeoutFailClosed(t *testing.T) {
	for _, f := range []*fakeIAM{{failure: status.Error(codes.Unavailable, "unavailable")}, {delay: time.Second}} {
		r := testRedeemer(t, f)
		started := time.Now()
		g, err := r.Redeem(context.Background(), testAttempt())
		if err == nil || g.Ingress != "" || len(r.snapshot()) != 0 {
			t.Fatalf("failure authorized: %+v %v", g, err)
		}
		if time.Since(started) > time.Second/2 {
			t.Fatal("redeem timeout not enforced")
		}
	}
}

func TestIAMRedeemerRejectsUntrustedServerAndMalformedTicket(t *testing.T) {
	f := &fakeIAM{}
	r := testRedeemer(t, f)
	for _, a := range []Attempt{{Ticket: connectTicket, ServerID: "other", ClientID: 1}, {Ticket: `{"tenant":"bad"}`, ServerID: "server-id", ClientID: 1}} {
		if _, err := r.Redeem(context.Background(), a); err == nil {
			t.Fatal("untrusted attempt authorized")
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid attempt reached IAM")
	}
}

func TestIAMGeneratedMessagesRoundTrip(t *testing.T) {
	msg := &iam.NetworkNodeActiveRequest{Realm: "18446744073709551615"}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	result := new(iam.NetworkNodeActiveRequest)
	if err = proto.Unmarshal(raw, result); err != nil || result.GetRealm() != "18446744073709551615" {
		t.Fatalf("roundtrip: %v", err)
	}
}

func TestTicketDerivedIngressMatchesSharedVector(t *testing.T) {
	if got := ingressForTicket("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); got != "b4ahhbnw7hklp3vsosdall7bve" {
		t.Fatalf("ingress=%s", got)
	}
}

func TestIAMRedeemerRejectsMismatchedOrExpiredAuthorityResponse(t *testing.T) {
	for field, mutate := range map[string]func(*iam.RedeemNetworkTicketResponse){
		"realm":           func(resp *iam.RedeemNetworkTicketResponse) { resp.Realm = "0" },
		"node":            func(resp *iam.RedeemNetworkTicketResponse) { resp.Node = "2" },
		"device":          func(resp *iam.RedeemNetworkTicketResponse) { resp.Device = "other" },
		"server_audience": func(resp *iam.RedeemNetworkTicketResponse) { resp.ServerAudience = "other" },
		"ingress":         func(resp *iam.RedeemNetworkTicketResponse) { resp.Ingress = "foreign" },
		"expires_at":      func(resp *iam.RedeemNetworkTicketResponse) { resp.ExpiresAt = "2000-01-01T00:00:00Z" },
	} {
		t.Run(field, func(t *testing.T) {
			r := testRedeemer(t, &fakeIAM{mutate: mutate})
			if g, err := r.Redeem(context.Background(), testAttempt()); err == nil || g.Ingress != "" || len(r.snapshot()) != 0 {
				t.Fatalf("invalid response authorized: %+v %v", g, err)
			}
		})
	}
}

func TestIAMRedeemerReservesCapacityBeforeConsumingTicket(t *testing.T) {
	f := &fakeIAM{}
	r := testRedeemer(t, f)
	r.cfg.MaxConnections = 1
	if _, err := r.Redeem(context.Background(), testAttempt()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Redeem(context.Background(), testAttempt()); err == nil {
		t.Fatal("connection capacity exceeded")
	}
	if f.calls != 1 {
		t.Fatal("full registry consumed another ticket")
	}
}
