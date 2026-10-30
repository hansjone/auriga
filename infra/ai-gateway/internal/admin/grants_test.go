package admin

import (
	"context"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc"
)

type authority struct {
	org.AuthorizationServiceClient
	org.MemberServiceClient
	member      *org.Member
	permissions []string
}

func (a authority) GetEffectivePermissions(context.Context, *org.GetEffectivePermissionsRequest, ...grpc.CallOption) (*org.GetEffectivePermissionsResponse, error) {
	return &org.GetEffectivePermissionsResponse{Status: org.MemberStatus_MEMBER_STATUS_ACTIVE, Permissions: a.permissions}, nil
}
func (a authority) GetMember(context.Context, *org.GetMemberRequest, ...grpc.CallOption) (*org.GetMemberResponse, error) {
	return &org.GetMemberResponse{Member: a.member}, nil
}
func TestGrantAuthority(t *testing.T) {
	p := &Principal{Tenant: testUUID}
	p.Subject = testUUID
	a := authority{permissions: []string{"ai.models.grant", "ai.models.invoke"}, member: &org.Member{TenantId: testUUID, AccountId: testUUID, Status: org.MemberStatus_MEMBER_STATUS_ACTIVE}}
	g := api.ModelGrants{Items: []api.Grant{{Kind: "member", Id: testUUID}}}
	if err := RPCGrantChecker(a, a)(context.Background(), p, g); err != nil {
		t.Fatal(err)
	}
	a.member.TenantId = "00000000-0000-7000-8000-000000000099"
	if err := RPCGrantChecker(a, a)(context.Background(), p, g); err != failure("common.forbidden") {
		t.Fatal("foreign member accepted", err)
	}
	a.permissions = []string{"ai.models.grant"}
	if err := RPCGrantChecker(a, a)(context.Background(), p, g); err != failure("common.forbidden") {
		t.Fatal("ceiling bypass", err)
	}
	a.permissions = []string{"ai.models.grant", "ai.models.invoke"}
	for _, kind := range []string{"group", "role"} {
		g.Items[0].Kind = kind
		if err := RPCGrantChecker(a, a)(context.Background(), p, g); err != failure("common.unavailable") {
			t.Fatal("unverifiable reference accepted", err)
		}
	}
}
