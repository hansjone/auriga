package admin

import (
	"context"
	"slices"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
)

// RPCGrantChecker reuses Team's authenticated service transport and tenant metadata.
func RPCGrantChecker(members org.MemberServiceClient, auth org.AuthorizationServiceClient) GrantChecker {
	return func(ctx context.Context, p *Principal, g api.ModelGrants) error {
		ctx = team.WithActor(ctx, &collab.Actor{TenantId: p.Tenant})
		current, err := auth.GetEffectivePermissions(ctx, &org.GetEffectivePermissionsRequest{TenantId: p.Tenant, AccountId: p.Subject})
		if err != nil {
			return failure("common.unavailable")
		}
		if current.GetStatus() != org.MemberStatus_MEMBER_STATUS_ACTIVE || !slices.Contains(current.GetPermissions(), "ai.models.grant") || (len(g.Items) > 0 && !slices.Contains(current.GetPermissions(), "ai.models.invoke")) {
			return failure("common.forbidden")
		}
		for _, grant := range g.Items {
			if grant.Kind != "member" {
				// CheckGrant treats missing/foreign roles and groups as empty permission
				// sets, so it cannot establish existence in this tenant. Fail closed until
				// org exposes an authoritative reference check (see ADMIN.zh.md).
				return failure("common.unavailable")
			}
			target, e := members.GetMember(ctx, &org.GetMemberRequest{TenantId: p.Tenant, AccountId: grant.Id})
			if e != nil {
				return failure("common.unavailable")
			}
			m := target.GetMember()
			if m.GetTenantId() != p.Tenant || m.GetAccountId() != grant.Id || m.GetStatus() != org.MemberStatus_MEMBER_STATUS_ACTIVE {
				return failure("common.forbidden")
			}
		}
		return nil
	}
}
