// Package modelgate verifies device identity and current member model ACL.
// LiteLLM owns model budgets and usage admission.
package modelgate

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var managedSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

const platformTenant = "00000000-0000-0000-0000-000000000000"

// IAM owns the 300s lease; permit bounded cross-host clock skew, never expired leases.
const managedLeaseClockSkew = 5 * time.Second

type fault string

func (e fault) Error() string { return string(e) }

// Identity is a single check result, not an allow cache. No public constructor
// accepts caller claims. All exposed data consists of independent value copies.
type Identity struct {
	tenant, account, device string
	expires                 time.Time
}

func (i *Identity) Tenant() string       { return i.tenant }
func (i *Identity) Account() string      { return i.account }
func (i *Identity) Device() string       { return i.device }
func (i *Identity) ExpiresAt() time.Time { return i.expires }

type Authority struct {
	devices     iam.DeviceAccessServiceClient
	members     org.MemberServiceClient
	permissions org.AuthorizationServiceClient
	now         func() time.Time
}

// Clients must be constructed with team.DialRPC, the existing authenticated
// service-JWT rotation/mTLS transport. User tokens belong only in the RPC body.
func NewAuthority(devices iam.DeviceAccessServiceClient, members org.MemberServiceClient, permissions org.AuthorizationServiceClient, now func() time.Time) (*Authority, error) {
	if devices == nil || members == nil || permissions == nil || now == nil {
		return nil, fault("common.validation_failed")
	}
	return &Authority{devices: devices, members: members, permissions: permissions, now: now}, nil
}

type routeClaims struct {
	jwt.RegisteredClaims
	Tenant string `json:"tid"`
	Device string `json:"did"`
}

func memberType(t org.MemberType) bool {
	return t == org.MemberType_MEMBER_TYPE_MEMBER || t == org.MemberType_MEMBER_TYPE_EXTERNAL
}

// Check forwards the EXACT bounded raw token to IAM on EVERY call. The parsed
// claims select the delegated route and expected identity only; IAM alone owns
// signature/issuer/typ/ES256/audience/300s/device/backing-session verification.
// No JWT roles, platform/admin/instance permissions or CheckPermission.resource
// can establish member model ACL. No idle-touch or refresh operation is invoked.
func (a *Authority) Check(ctx context.Context, raw string) (*Identity, error) {
	if len(raw) == 0 || len(raw) > 16384 || strings.ContainsAny(raw, " \t\r\n") {
		return nil, fault("common.unauthenticated")
	}
	claims := new(routeClaims)
	managed := strings.HasPrefix(raw, "amc.")
	if managed {
		parts := strings.Split(raw, ".")
		if len(parts) != 6 || !uuid.MatchString(parts[3]) || !managedSecret.MatchString(parts[5]) {
			return nil, fault("common.unauthenticated")
		}
		claims.Tenant, claims.Subject, claims.Device = parts[1], parts[2], parts[4]
	} else if _, _, e := jwt.NewParser().ParseUnverified(raw, claims); e != nil || claims.ExpiresAt == nil {
		return nil, fault("common.unauthenticated")
	}
	if !uuid.MatchString(claims.Tenant) || claims.Tenant == platformTenant || !uuid.MatchString(claims.Subject) || !uuid.MatchString(claims.Device) {
		return nil, fault("common.unauthenticated")
	}
	ctx = team.WithActor(ctx, &collab.Actor{TenantId: claims.Tenant})
	verified, e := a.devices.VerifyDeviceAiAccess(ctx, &iam.VerifyDeviceAiAccessRequest{AccessToken: raw})
	if e != nil {
		if status.Code(e) == codes.PermissionDenied {
			return nil, fault("common.forbidden")
		}
		return nil, fault("ai.budget_unavailable")
	}
	if verified == nil {
		return nil, fault("ai.budget_unavailable")
	}
	end := verified.GetExpiresAt()
	if !verified.Active || verified.TenantId != claims.Tenant || verified.AccountId != claims.Subject || verified.DeviceId != claims.Device || end == nil || !end.IsValid() || !end.AsTime().After(a.now()) {
		return nil, fault("common.unauthenticated")
	}
	if managed && end.AsTime().After(a.now().Add(300*time.Second+managedLeaseClockSkew)) || !managed && end.AsTime().After(claims.ExpiresAt.Time) {
		return nil, fault("common.unauthenticated")
	}
	member, e := a.members.GetMember(ctx, &org.GetMemberRequest{TenantId: verified.TenantId, AccountId: verified.AccountId})
	if e != nil {
		if status.Code(e) == codes.NotFound {
			return nil, fault("common.forbidden")
		}
		return nil, fault("ai.budget_unavailable")
	}
	m := member.GetMember()
	if m == nil {
		return nil, fault("ai.budget_unavailable")
	}
	if m.Status == org.MemberStatus_MEMBER_STATUS_UNSPECIFIED || !memberType(m.MemberType) {
		return nil, fault("ai.budget_unavailable")
	}
	if m.TenantId != verified.TenantId || m.AccountId != verified.AccountId || m.Status != org.MemberStatus_MEMBER_STATUS_ACTIVE || !memberType(m.MemberType) {
		return nil, fault("common.forbidden")
	}
	permissions, e := a.permissions.GetEffectivePermissions(ctx, &org.GetEffectivePermissionsRequest{TenantId: verified.TenantId, AccountId: verified.AccountId})
	if e != nil || permissions == nil {
		return nil, fault("ai.budget_unavailable")
	}
	if permissions.Status == org.MemberStatus_MEMBER_STATUS_UNSPECIFIED || !memberType(permissions.MemberType) {
		return nil, fault("ai.budget_unavailable")
	}
	if permissions.Status != org.MemberStatus_MEMBER_STATUS_ACTIVE || !memberType(permissions.MemberType) || permissions.MemberType != m.MemberType || !slices.Contains(permissions.Permissions, "auriga.connect.models.invoke") {
		return nil, fault("common.forbidden")
	}
	// Remote IAM/org work can consume the remainder of the backing/token lease.
	if ctx.Err() != nil {
		return nil, fault("ai.budget_unavailable")
	}
	if !end.AsTime().After(a.now()) {
		return nil, fault("common.unauthenticated")
	}
	return &Identity{tenant: verified.TenantId, account: verified.AccountId, device: verified.DeviceId, expires: end.AsTime()}, nil
}
