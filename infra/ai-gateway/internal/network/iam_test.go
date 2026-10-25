package network

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const testTenant = "01900000-0000-7000-8000-000000000001"
const testDevice = "01900000-0000-7000-8000-000000000002"
const connectTicket = `{"tenant":"` + testTenant + `","node":"1","device":"` + testDevice + `","ticket":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`

type fakeIAM struct {
	mu      sync.Mutex
	used    bool
	delay   time.Duration
	failure error
	active  bool
	calls   int
	mutate  func(*dynamicpb.Message)
}

func (f *fakeIAM) Invoke(ctx context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
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
	req := args.(*dynamicpb.Message)
	resp := reply.(*dynamicpb.Message)
	f.mu.Lock()
	defer f.mu.Unlock()
	if method == activeMethod {
		resp.Set(resp.Descriptor().Fields().ByName("active"), protoreflect.ValueOfBool(f.active))
		return nil
	}
	if f.used {
		return status.Error(codes.PermissionDenied, "common.forbidden")
	}
	f.used = true
	setString(resp, "realm", "9223372036854775809")
	setString(resp, "node", getString(req, "node"))
	setString(resp, "device", getString(req, "device"))
	setString(resp, "server_audience", getString(req, "server_audience"))
	setString(resp, "ingress", getString(req, "ingress"))
	setString(resp, "expires_at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if f.mutate != nil {
		f.mutate(resp)
	}
	return nil
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

func TestIAMDescriptorMatchesContract(t *testing.T) {
	// Round-trip proves this transport uses protobuf descriptors, not a JSON codec.
	msg := dynamicpb.NewMessage(iamFile.Messages().ByName("NetworkNodeActiveRequest"))
	setString(msg, "realm", "18446744073709551615")
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	result := dynamicpb.NewMessage(msg.Descriptor())
	if err = proto.Unmarshal(raw, result); err != nil || getString(result, "realm") != "18446744073709551615" {
		t.Fatalf("roundtrip: %v", err)
	}
}

func TestIAMRedeemerRejectsMismatchedOrExpiredAuthorityResponse(t *testing.T) {
	for field, value := range map[protoreflect.Name]string{"realm": "0", "node": "2", "device": "other", "server_audience": "other", "ingress": "foreign", "expires_at": "2000-01-01T00:00:00Z"} {
		t.Run(string(field), func(t *testing.T) {
			r := testRedeemer(t, &fakeIAM{mutate: func(resp *dynamicpb.Message) { setString(resp, field, value) }})
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
