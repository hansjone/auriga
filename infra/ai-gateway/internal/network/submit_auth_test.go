package network

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type submitDeviceRPC struct {
	iam.UnimplementedDeviceAccessServiceServer
	response *iam.VerifyDeviceAccessResponse
	called   chan string
}

func (f *submitDeviceRPC) VerifyDeviceAccess(_ context.Context, q *iam.VerifyDeviceAccessRequest) (*iam.VerifyDeviceAccessResponse, error) {
	f.called <- q.AccessToken
	return f.response, nil
}

func TestSubmitDeviceVerifierRequiresAuthorityEcho(t *testing.T) {
	f := &submitDeviceRPC{response: &iam.VerifyDeviceAccessResponse{Active: true, TenantId: testTenant, DeviceId: testDevice, AccountId: testTenant, ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}, called: make(chan string, 10)}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, q any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if v := md.Get("auriga-tenant-id"); len(v) != 1 || v[0] != testTenant {
			t.Error("unverified claims escaped routing tenant")
		}
		if v := md.Get("authorization"); len(v) != 1 || v[0] != "Bearer service-token" {
			t.Error("device token replaced service identity")
		}
		return next(ctx, q)
	}))
	iam.RegisterDeviceAccessServiceServer(srv, f)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(l) }()
	defer func() { srv.Stop(); _ = l.Close(); <-done }()
	file := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(file, []byte("service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := team.DialRPC(team.RPCConfig{Target: l.Addr().String(), ServiceTokenFile: file})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	verify := submitDeviceVerifier(iam.NewDeviceAccessServiceClient(conn))
	for _, tc := range []struct {
		name, sub, device, tenant string
		want                      bool
	}{
		{"valid", testTenant, testDevice, testTenant, true},
		{"owner", testDevice, testDevice, testTenant, false},
		{"device", testTenant, testTenant, testTenant, false},
		{"bad tenant", testTenant, testDevice, "not-uuid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": tc.sub, "did": tc.device, "tid": tc.tenant}).SignedString([]byte("fixture-token-signing-material"))
			if e != nil {
				t.Fatal(e)
			}
			v, e := verify(context.Background(), raw)
			if (e == nil) != tc.want || tc.want && !v.GetActive() {
				t.Fatalf("unverified identity accepted: %v", e)
			}
		})
	}
}

func TestSubmitRuntimeRequiresTLS(t *testing.T) {
	for _, c := range []*SubmitHTTPConfig{{Listen: "127.0.0.1:0"}, {Listen: "127.0.0.1:0", TLSCertFile: "missing", TLSKeyFile: "missing"}} {
		r := &Runtime{}
		if err := r.startSubmitHTTP(context.Background(), c); err == nil {
			t.Fatal("HTTPS without TLS started")
		}
	}
}
