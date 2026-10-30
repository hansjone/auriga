package modelgate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/litellm"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const tenant = "01900000-0000-7000-8000-000000000001"
const account = "01900000-0000-7000-8000-000000000002"
const device = "01900000-0000-7000-8000-000000000003"

// This server is a control-plane port fixture, NOT an IAM token signer/verifier.
type control struct {
	iam.UnimplementedDeviceAccessServiceServer
	org.UnimplementedMemberServiceServer
	org.UnimplementedAuthorizationServiceServer
	org.UnimplementedQuotaServiceServer
	mu                 sync.Mutex
	now                time.Time
	verify             *iam.VerifyDeviceAiAccessResponse
	member             *org.GetMemberResponse
	permissions        *org.GetEffectivePermissionsResponse
	fail               string
	hook               func(string)
	calls              []string
	raw, authorization string
}

func (s *control) before(ctx context.Context, method string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	if got := md.Get("authorization"); len(got) != 1 || got[0] != s.authorization {
		return status.Error(codes.Unauthenticated, "fixture service identity mismatch")
	}
	if got := md.Get("auriga-tenant-id"); len(got) != 1 || got[0] != tenant {
		return status.Error(codes.PermissionDenied, "fixture tenant mismatch")
	}
	s.calls = append(s.calls, method)
	if s.hook != nil {
		s.hook(method)
	}
	if s.fail == method {
		return status.Error(codes.Unavailable, "secret internal failure")
	}
	return nil
}
func (s *control) VerifyDeviceAiAccess(ctx context.Context, in *iam.VerifyDeviceAiAccessRequest) (*iam.VerifyDeviceAiAccessResponse, error) {
	if e := s.before(ctx, "iam"); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.AccessToken != s.raw {
		return nil, status.Error(codes.Unauthenticated, "wrong raw")
	}
	return proto.Clone(s.verify).(*iam.VerifyDeviceAiAccessResponse), nil
}
func (s *control) GetMember(ctx context.Context, in *org.GetMemberRequest) (*org.GetMemberResponse, error) {
	if e := s.before(ctx, "member"); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.TenantId != tenant || in.AccountId != account {
		return nil, status.Error(codes.PermissionDenied, "wrong subject")
	}
	return proto.Clone(s.member).(*org.GetMemberResponse), nil
}
func (s *control) GetEffectivePermissions(ctx context.Context, in *org.GetEffectivePermissionsRequest) (*org.GetEffectivePermissionsResponse, error) {
	if e := s.before(ctx, "permissions"); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.TenantId != tenant || in.AccountId != account {
		return nil, status.Error(codes.PermissionDenied, "wrong subject")
	}
	return proto.Clone(s.permissions).(*org.GetEffectivePermissionsResponse), nil
}
func (s *control) GetEffectiveQuota(ctx context.Context, in *org.GetEffectiveQuotaRequest) (*org.GetEffectiveQuotaResponse, error) {
	if e := s.before(ctx, "quota"); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.TenantId != tenant || in.AccountId != account {
		return nil, status.Error(codes.PermissionDenied, "wrong subject")
	}
	return nil, status.Error(codes.Unavailable, "obsolete quota service must not be called")
}
func routeToken(t *testing.T, now time.Time, change func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{"tid": tenant, "sub": account, "did": device, "exp": now.Add(2 * time.Minute).Unix()}
	if change != nil {
		change(claims)
	}
	b, e := json.Marshal(claims)
	if e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString(b) + ".ZmFrZQ"
}
func controlFixture(t *testing.T) (*control, *Authority, string) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	s := &control{now: now, authorization: "Bearer fixture-service-token", verify: &iam.VerifyDeviceAiAccessResponse{Active: true, TenantId: tenant, AccountId: account, DeviceId: device, ExpiresAt: timestamppb.New(now.Add(time.Minute))}, member: &org.GetMemberResponse{Member: &org.Member{TenantId: tenant, AccountId: account, Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER}}, permissions: &org.GetEffectivePermissionsResponse{Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, MemberType: org.MemberType_MEMBER_TYPE_MEMBER, Permissions: []string{"auriga.connect.models.invoke"}}}
	s.raw = routeToken(t, now, nil)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	iam.RegisterDeviceAccessServiceServer(server, s)
	org.RegisterMemberServiceServer(server, s)
	org.RegisterAuthorizationServiceServer(server, s)
	org.RegisterQuotaServiceServer(server, s)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	tokenFile := filepath.Join(t.TempDir(), "service.jwt")
	if e = os.WriteFile(tokenFile, []byte("fixture-service-token"), 0600); e != nil {
		t.Fatal(e)
	}
	conn, e := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := conn.Close(); e != nil {
			t.Error(e)
		}
	})
	a, e := NewAuthority(iam.NewDeviceAccessServiceClient(conn), org.NewMemberServiceClient(conn), org.NewAuthorizationServiceClient(conn), func() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.now })
	if e != nil {
		t.Fatal(e)
	}
	return s, a, tokenFile
}
func wantCode(t *testing.T, e error, want string) {
	t.Helper()
	if e == nil || e.Error() != want {
		t.Fatalf("error=%v want=%s", e, want)
	}
}

func TestDeviceOnlineAuthority(t *testing.T) {
	s, a, tokenFile := controlFixture(t)
	id, e := a.Check(t.Context(), s.raw)
	if e != nil {
		t.Fatal(e)
	}
	if id.Tenant() != tenant || id.Account() != account || id.Device() != device || id.ExpiresAt() != s.verify.ExpiresAt.AsTime() {
		t.Fatal("identity mismatch")
	}
	s.mu.Lock()
	s.authorization = "Bearer rotated-service-token"
	s.mu.Unlock()
	if e = os.WriteFile(tokenFile, []byte("rotated-service-token"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Check(t.Context(), s.raw); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	n := len(s.calls)
	s.permissions.Permissions = []string{"ai.models.invoke", "collab.messages.use", "ai.providers.write"}
	s.mu.Unlock()
	_, e = a.Check(t.Context(), s.raw)
	wantCode(t, e, "common.forbidden")
	s.mu.Lock()
	defer s.mu.Unlock()
	if n != 6 || len(s.calls) <= n {
		t.Fatal("authority was cached")
	}
}

func TestManagedModelKeyUsesExactOnlineAuthorityAndMemberAccounting(t *testing.T) {
	s, a, _ := controlFixture(t)
	s.raw = "amc." + tenant + "." + account + "." + account + "." + device + "." + strings.Repeat("a", 43)
	id, err := a.Check(t.Context(), s.raw)
	if err != nil || id.Tenant() != tenant || id.Account() != account || id.Device() != device {
		t.Fatalf("managed identity: %v", err)
	}
	s.mu.Lock()
	s.verify.Active = false
	s.mu.Unlock()
	_, err = a.Check(t.Context(), s.raw)
	wantCode(t, err, "common.unauthenticated")
	s.mu.Lock()
	s.verify.Active = true
	s.verify.ExpiresAt = timestamppb.New(s.now.Add(301 * time.Second))
	s.mu.Unlock()
	_, err = a.Check(t.Context(), s.raw)
	wantCode(t, err, "common.unauthenticated")
}

func TestManagedModelRelayForcesDesktopMemberUsageIdentity(t *testing.T) {
	s, a, _ := controlFixture(t)
	s.raw = "amc." + tenant + "." + account + "." + account + "." + device + "." + strings.Repeat("a", 43)
	serviceKey := "sk-" + rand.Text()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User     string            `json:"user"`
			Metadata map[string]string `json:"metadata"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.User != tenant+":"+account || body.Metadata["account_id"] != account || body.Metadata["device_id"] != device || r.Header.Get("Authorization") != "Bearer "+serviceKey {
			t.Error("managed usage or service key mismatch")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	key := filepath.Join(t.TempDir(), "team-key")
	if err := os.WriteFile(key, []byte(serviceKey), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := litellm.New(upstream.URL, key, func(ctx context.Context, raw string) (litellm.Principal, error) {
		id, e := a.Check(ctx, raw)
		if e != nil {
			return litellm.Principal{}, e
		}
		return litellm.Principal{Tenant: id.Tenant(), Account: id.Account(), Device: id.Device(), Expires: id.ExpiresAt()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	request := httptest.NewRequest(http.MethodPost, "/ai/v1/chat/completions", strings.NewReader(`{"model":"pool-a","messages":[],"user":"attacker","metadata":{"account_id":"other"}}`))
	request.Header.Set("Authorization", "Bearer "+s.raw)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("relay status %d", recorder.Code)
	}
}
func TestDeviceAuthorityRejects(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*control)
	}{
		{"inactive", "common.unauthenticated", func(s *control) { s.verify.Active = false }},
		{"empty-identity", "common.unauthenticated", func(s *control) { s.verify = &iam.VerifyDeviceAiAccessResponse{Active: true} }},
		{"foreign-tenant", "common.unauthenticated", func(s *control) { s.verify.TenantId = device }},
		{"foreign-account", "common.unauthenticated", func(s *control) { s.verify.AccountId = device }},
		{"foreign-device", "common.unauthenticated", func(s *control) { s.verify.DeviceId = account }},
		{"invalid-expiry", "common.unauthenticated", func(s *control) { s.verify.ExpiresAt = &timestamppb.Timestamp{Seconds: 253402300800} }},
		{"extended-expiry", "common.unauthenticated", func(s *control) { s.verify.ExpiresAt = timestamppb.New(s.now.Add(3 * time.Minute)) }},
		{"empty-member", "ai.budget_unavailable", func(s *control) { s.member = &org.GetMemberResponse{} }},
		{"foreign-member", "common.forbidden", func(s *control) { s.member.Member.TenantId = device }},
		{"offboarded", "common.forbidden", func(s *control) { s.member.Member.Status = org.MemberStatus_MEMBER_STATUS_OFFBOARDED }},
		{"permissions-disabled", "common.forbidden", func(s *control) { s.permissions.Status = org.MemberStatus_MEMBER_STATUS_DISABLED }},
		{"missing-permission-status", "ai.budget_unavailable", func(s *control) { s.permissions = &org.GetEffectivePermissionsResponse{} }},
		{"missing-member-type", "ai.budget_unavailable", func(s *control) { s.member.Member.MemberType = org.MemberType_MEMBER_TYPE_UNSPECIFIED }},
		{"expired-member-lease", "common.unauthenticated", func(s *control) { s.verify.ExpiresAt = timestamppb.New(s.now) }},
		{"disabled-member", "common.forbidden", func(s *control) { s.member.Member.Status = org.MemberStatus_MEMBER_STATUS_DISABLED }},
		{"foreign-permission-member-type", "common.forbidden", func(s *control) { s.permissions.MemberType = org.MemberType_MEMBER_TYPE_EXTERNAL }},
		{"missing-model-permission", "common.forbidden", func(s *control) { s.permissions.Permissions = nil }},
	}
	for _, method := range []string{"iam", "member", "permissions"} {
		tests = append(tests, struct {
			name, code string
			change     func(*control)
		}{"failure-" + method, "ai.budget_unavailable", func(s *control) { s.fail = method }})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, a, _ := controlFixture(t)
			tc.change(s)
			_, e := a.Check(t.Context(), s.raw)
			wantCode(t, e, tc.code)
		})
	}
}
func TestDeviceExpiryDuringRPC(t *testing.T) {
	for _, method := range []string{"iam", "member", "permissions"} {
		t.Run(method, func(t *testing.T) {
			s, a, _ := controlFixture(t)
			s.hook = func(m string) {
				if m == method {
					s.now = s.now.Add(time.Minute)
				}
			}
			_, e := a.Check(t.Context(), s.raw)
			wantCode(t, e, "common.unauthenticated")
		})
	}
}
func TestDeviceRawRoutingBounds(t *testing.T) {
	s, a, _ := controlFixture(t)
	invalid := []string{"", "ait_example", strings.Repeat("x", 16385), s.raw + "\n", routeToken(t, s.now, func(m map[string]any) { m["tid"] = "01900000-0000-7000-8000-00000000000A" }), routeToken(t, s.now, func(m map[string]any) { m["tid"] = "00000000-0000-0000-0000-000000000000" }), routeToken(t, s.now, func(m map[string]any) { delete(m, "did") })}
	for _, raw := range invalid {
		_, e := a.Check(t.Context(), raw)
		wantCode(t, e, "common.unauthenticated")
	}
	if len(s.calls) != 0 {
		t.Fatal("invalid route contacted IAM")
	}
}
func TestDeviceAuthorityDoesNotFetchQuotas(t *testing.T) {
	s, a, _ := controlFixture(t)
	s.fail = "quota"
	if _, err := a.Check(t.Context(), s.raw); err != nil {
		t.Fatalf("LiteLLM member access depends on obsolete org model quotas: %v", err)
	}
	for _, call := range s.calls {
		if call == "quota" {
			t.Fatal("member access fetched obsolete org model quotas")
		}
	}
}
