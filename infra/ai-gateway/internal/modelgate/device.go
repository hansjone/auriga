// Package modelgate verifies device identity and current member model ACL.
// Bifrost owns model budgets and usage admission.
package modelgate

import (
	"context"
	"fmt"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"log/slog"
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
func (a *Authority) Check(ctx context.Context, raw string) (_ *Identity, resultErr error) {
	started := time.Now()
	stage, reason, rpcStatus, detail := "credential", "invalid_credential", "not_called", ""
	active, leaseValid := false, false
	tenant, account, device, instance := "", "", "", ""
	defer func() {
		if raw != "" {
			detail = strings.ReplaceAll(detail, raw, "****")
		}
		detail = observability.Mask(detail)
		code := "ok"
		if resultErr != nil {
			code = resultErr.Error()
		}
		slog.InfoContext(ctx, "Managed model access check", "stage", stage, "reason", reason,
			"code", code, "grpc_status", rpcStatus, "error", detail, "active", active,
			"lease_valid", leaseValid, "tenant_id", tenant, "account_id", account,
			"device_id", device, "instance_id", instance, "elapsed_ms", time.Since(started).Milliseconds())
	}()

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
		instance = parts[3]
	} else if _, _, e := jwt.NewParser().ParseUnverified(raw, claims); e != nil || claims.ExpiresAt == nil {
		return nil, fault("common.unauthenticated")
	}
	if !uuid.MatchString(claims.Tenant) || claims.Tenant == platformTenant || !uuid.MatchString(claims.Subject) || !uuid.MatchString(claims.Device) {
		return nil, fault("common.unauthenticated")
	}
	tenant, account, device = claims.Tenant, claims.Subject, claims.Device
	stage, reason = "iam", "iam_rpc_failed"
	ctx = team.WithActor(ctx, &collab.Actor{TenantId: claims.Tenant})
	verified, e := a.devices.VerifyDeviceAiAccess(ctx, &iam.VerifyDeviceAiAccessRequest{AccessToken: raw})
	rpcStatus = status.Code(e).String()
	if e != nil {
		detail = fmt.Sprintf("%T: %v", e, e)
		if status.Code(e) == codes.PermissionDenied {
			return nil, fault("common.forbidden")
		}
		return nil, fault("ai.budget_unavailable")
	}
	reason = "iam_response_missing"
	if verified == nil {
		return nil, fault("ai.budget_unavailable")
	}
	active = verified.Active
	reason = "iam_identity_or_lease_invalid"
	end := verified.GetExpiresAt()
	if !active {
		reason = "device_inactive"
	} else if end == nil || !end.IsValid() {
		reason = "lease_invalid"
	} else if !end.AsTime().After(a.now()) {
		reason = "lease_expired"
	}
	if !verified.Active || verified.TenantId != claims.Tenant || verified.AccountId != claims.Subject || verified.DeviceId != claims.Device || end == nil || !end.IsValid() || !end.AsTime().After(a.now()) {
		return nil, fault("common.unauthenticated")
	}
	reason = "lease_window_invalid"
	if managed && end.AsTime().After(a.now().Add(300*time.Second+managedLeaseClockSkew)) || !managed && end.AsTime().After(claims.ExpiresAt.Time) {
		return nil, fault("common.unauthenticated")
	}
	leaseValid = true
	stage, reason = "member", "member_rpc_failed"
	member, e := a.members.GetMember(ctx, &org.GetMemberRequest{TenantId: verified.TenantId, AccountId: verified.AccountId})
	rpcStatus = status.Code(e).String()
	if e != nil {
		detail = fmt.Sprintf("%T: %v", e, e)
		if status.Code(e) == codes.NotFound {
			return nil, fault("common.forbidden")
		}
		return nil, fault("ai.budget_unavailable")
	}
	reason = "member_missing"
	m := member.GetMember()
	if m == nil {
		return nil, fault("ai.budget_unavailable")
	}
	reason = "member_state_invalid"
	if m.Status == org.MemberStatus_MEMBER_STATUS_UNSPECIFIED || !memberType(m.MemberType) {
		return nil, fault("ai.budget_unavailable")
	}
	reason = "member_inactive_or_identity_mismatch"
	if m.TenantId != verified.TenantId || m.AccountId != verified.AccountId || m.Status != org.MemberStatus_MEMBER_STATUS_ACTIVE || !memberType(m.MemberType) {
		return nil, fault("common.forbidden")
	}
	stage, reason = "permission", "permission_rpc_failed"
	permissions, e := a.permissions.GetEffectivePermissions(ctx, &org.GetEffectivePermissionsRequest{TenantId: verified.TenantId, AccountId: verified.AccountId})
	rpcStatus = status.Code(e).String()
	if e != nil {
		detail = fmt.Sprintf("%T: %v", e, e)
	}
	if e != nil || permissions == nil {
		return nil, fault("ai.budget_unavailable")
	}
	reason = "permission_state_invalid"
	if permissions.Status == org.MemberStatus_MEMBER_STATUS_UNSPECIFIED || !memberType(permissions.MemberType) {
		return nil, fault("ai.budget_unavailable")
	}
	reason = "invoke_not_authorized"
	if permissions.Status != org.MemberStatus_MEMBER_STATUS_ACTIVE {
		reason = "permission_member_inactive"
	} else if !memberType(permissions.MemberType) || permissions.MemberType != m.MemberType {
		reason = "permission_type_mismatch"
	}
	if permissions.Status != org.MemberStatus_MEMBER_STATUS_ACTIVE || !memberType(permissions.MemberType) || permissions.MemberType != m.MemberType || !slices.Contains(permissions.Permissions, "auriga.connect.models.invoke") {
		return nil, fault("common.forbidden")
	}
	// Remote IAM/org work can consume the remainder of the backing/token lease.
	stage, reason = "completion", "cancelled"
	if ctx.Err() != nil {
		return nil, fault("ai.budget_unavailable")
	}
	reason = "lease_expired_after_authority"
	if !end.AsTime().After(a.now()) {
		return nil, fault("common.unauthenticated")
	}
	reason = "authorized"
	return &Identity{tenant: verified.TenantId, account: verified.AccountId, device: verified.DeviceId, expires: end.AsTime()}, nil
}
