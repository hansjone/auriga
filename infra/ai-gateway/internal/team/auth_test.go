package team

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const authTenant = "018f6f20-8f00-7000-8000-000000000001"
const authAccount = "018f6f20-8f00-7000-8000-000000000002"
const authDevice = "018f6f20-8f00-7000-8000-000000000003"

type wireDeviceAuthority struct {
	iam.UnimplementedDeviceAccessServiceServer
	active        atomic.Bool
	wrongIdentity atomic.Bool
	expired       atomic.Bool
	calls         atomic.Int32
	token         atomic.Value
}

func (a *wireDeviceAuthority) VerifyDeviceAccess(ctx context.Context, _ *iam.VerifyDeviceAccessRequest) (*iam.VerifyDeviceAccessResponse, error) {
	a.calls.Add(1)
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != authTenant {
		return nil, status.Error(codes.PermissionDenied, "wrong service context")
	}
	a.token.Store(md.Get("authorization")[0])
	if !a.active.Load() {
		return &iam.VerifyDeviceAccessResponse{}, nil
	}
	expiry := time.Now().Add(time.Minute)
	if a.expired.Load() {
		expiry = time.Now().Add(-time.Minute)
	}
	account := authAccount
	if a.wrongIdentity.Load() {
		account = authDevice
	}
	return &iam.VerifyDeviceAccessResponse{Active: true, TenantId: authTenant, AccountId: account, DeviceId: authDevice, ExpiresAt: timestamppb.New(expiry)}, nil
}

type wirePermissions struct {
	org.UnimplementedAuthorizationServiceServer
	allow    atomic.Bool
	disabled atomic.Bool
	calls    atomic.Int32
}

func (p *wirePermissions) GetEffectivePermissions(ctx context.Context, q *org.GetEffectivePermissionsRequest) (*org.GetEffectivePermissionsResponse, error) {
	p.calls.Add(1)
	md, _ := metadata.FromIncomingContext(ctx)
	if q.TenantId != authTenant || q.AccountId != authAccount || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != q.TenantId {
		return nil, status.Error(codes.PermissionDenied, "tenant mismatch")
	}
	current := &org.GetEffectivePermissionsResponse{Status: org.MemberStatus_MEMBER_STATUS_ACTIVE}
	if p.allow.Load() {
		current.Permissions = []string{"collab.messages.use"}
	}
	if p.disabled.Load() {
		current.Status = org.MemberStatus_MEMBER_STATUS_DISABLED
	}
	return current, nil
}
func TestDeviceAuthenticationRepeatsLiveRPCAndRotation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	devices := &wireDeviceAuthority{}
	devices.active.Store(true)
	permissions := &wirePermissions{}
	permissions.allow.Store(true)
	server := grpc.NewServer()
	iam.RegisterDeviceAccessServiceServer(server, devices)
	org.RegisterAuthorizationServiceServer(server, permissions)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	tokenFile := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(tokenFile, []byte("first-test-service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := DialRPC(RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	authenticate := DeviceAuthenticator(iam.NewDeviceAccessServiceClient(conn), org.NewAuthorizationServiceClient(conn))
	// Only IAM may validate a device token; the gateway uses these unverified claims for routing.
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"tid": authTenant, "sub": authAccount, "did": authDevice}).SignedString([]byte("test-only-unverified-routing-token"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if actor, err := authenticate(ctx, raw); err != nil || actor.AccountId != authAccount {
		t.Fatalf("verified authority failed: %v %v", actor, err)
	}
	if err = os.WriteFile(tokenFile, []byte("rotated-test-service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = authenticate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if devices.token.Load() != "Bearer rotated-test-service-token" {
		t.Fatal("service token was cached")
	}
	check := func(want codes.Code) {
		t.Helper()
		_, err := authenticate(ctx, raw)
		if status.Code(err) != want {
			t.Fatalf("want %s, got %v", want, err)
		}
	}
	devices.wrongIdentity.Store(true)
	check(codes.Unauthenticated)
	devices.wrongIdentity.Store(false)
	devices.expired.Store(true)
	check(codes.Unauthenticated)
	devices.expired.Store(false)
	permissions.disabled.Store(true)
	check(codes.PermissionDenied)
	permissions.disabled.Store(false)
	permissions.allow.Store(false)
	check(codes.PermissionDenied)
	permissions.allow.Store(true)
	devices.active.Store(false)
	check(codes.Unauthenticated)
	devices.active.Store(true)
	if devices.calls.Load() != 7 || permissions.calls.Load() != 4 {
		t.Fatalf("authorities not repeated: %d/%d", devices.calls.Load(), permissions.calls.Load())
	}
	if err = os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	check(codes.Unavailable)
	if _, err = DialRPC(RPCConfig{Target: "192.0.2.1:9090", ServiceTokenFile: tokenFile}); err == nil {
		t.Fatal("non-loopback plaintext accepted")
	}
	server.Stop()
	offline, cancelOffline := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelOffline()
	if err = os.WriteFile(tokenFile, []byte("rotated-test-service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = authenticate(offline, raw); status.Code(err) != codes.Unavailable {
		t.Fatal("offline authority admitted")
	}
	t.Log("生产认证适配器经真实gRPC反复调用外部控制面替身；身份篡改/到期/禁用/撤销/权限回收/断网/缺文件封闭，轮换即时读取")
}
