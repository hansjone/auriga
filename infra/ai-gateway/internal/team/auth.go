package team

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

// DeviceAuthenticator delegates signature, device, owner and backing session checks to IAM;
// unverified claims are used only to choose the delegated tenant, then compared to authority.
func DeviceAuthenticator(devices iam.DeviceAccessServiceClient, permissions org.AuthorizationServiceClient) Authenticate {
	return func(ctx context.Context, raw string) (*collab.Actor, error) {
		claims := jwt.MapClaims{}
		if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
			return nil, status.Error(codes.Unauthenticated, "common.unauthenticated")
		}
		tid, _ := claims["tid"].(string)
		account, _ := claims["sub"].(string)
		device, _ := claims["did"].(string)
		if !uuidPattern.MatchString(tid) || !uuidPattern.MatchString(account) || !uuidPattern.MatchString(device) {
			return nil, status.Error(codes.Unauthenticated, "common.unauthenticated")
		}
		route := &collab.Actor{TenantId: tid}
		ctx = WithActor(ctx, route)
		verified, err := devices.VerifyDeviceAccess(ctx, &iam.VerifyDeviceAccessRequest{AccessToken: raw})
		if err != nil {
			return nil, status.Error(codes.Unavailable, "common.unavailable")
		}
		if !verified.GetActive() || verified.GetTenantId() != tid || verified.GetAccountId() != account || verified.GetDeviceId() != device || verified.GetExpiresAt() == nil || !verified.GetExpiresAt().IsValid() || !verified.GetExpiresAt().AsTime().After(time.Now()) {
			return nil, status.Error(codes.Unauthenticated, "common.unauthenticated")
		}
		current, err := permissions.GetEffectivePermissions(ctx, &org.GetEffectivePermissionsRequest{TenantId: tid, AccountId: account})
		if err != nil {
			return nil, status.Error(codes.Unavailable, "common.unavailable")
		}
		allowed := false
		for _, permission := range current.GetPermissions() {
			if permission == "collab.messages.use" {
				allowed = true
			}
		}
		if current.GetStatus() != org.MemberStatus_MEMBER_STATUS_ACTIVE || !allowed {
			return nil, status.Error(codes.PermissionDenied, "common.forbidden")
		}
		return &collab.Actor{TenantId: tid, AccountId: account, CredentialKind: "DEVICE", CredentialSubject: device}, nil
	}
}
