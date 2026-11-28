package bifrostbackend

import (
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestNormalizePoolSaveAllowsEmptyAurigaRoutes(t *testing.T) {
	pool := placeholderAurigaPool()
	if err := normalizePoolSave(&pool); err != nil {
		t.Fatal(err)
	}
	if pool.GetAudience() != "member" {
		t.Fatalf("audience=%s", pool.GetAudience())
	}
	other := pool
	other.Id = "company-coder"
	if err := normalizePoolSave(&other); err == nil {
		t.Fatal("non-auriga empty routes accepted")
	}
}

func TestNormalizePoolSaveRejectsLegacyAutoID(t *testing.T) {
	pool := placeholderAurigaPool()
	pool.Id = "auto"
	pool.Routes = []api.RelayRoute{{ProviderId: "019a0000-0000-7000-8000-000000000001", Model: "m", Weight: 1}}
	if err := normalizePoolSave(&pool); err == nil {
		t.Fatal("legacy auto id accepted")
	}
}

func TestCatalogAllowsFiltersExternalForMemberWildcard(t *testing.T) {
	member := api.RelayKey{Source: "member", Models: []string{"*"}}
	external := api.RelayKey{Source: "external", Models: []string{"*"}}
	aud := "external"
	pool := api.RelayPool{Id: "ext-only", Enabled: true, Audience: &aud}
	if catalogAllows(member, pool) {
		t.Fatal("member * saw external-only pool")
	}
	if !catalogAllows(external, pool) {
		t.Fatal("external * missed external-only pool")
	}
	if !catalogAllows(member, placeholderAurigaPool()) {
		t.Fatal("member missed auriga")
	}
	if !modelGranted(api.RelayKey{Models: []string{"auto"}}, "auriga") {
		t.Fatal("legacy auto grant missed auriga")
	}
}

func TestChatAllowsExplicitExternalGrant(t *testing.T) {
	aud := "external"
	pool := api.RelayPool{Id: "ext-only", Enabled: true, Audience: &aud}
	key := api.RelayKey{Source: "member", Models: []string{"ext-only"}}
	if !chatAllows(key, pool) {
		t.Fatal("explicit grant denied")
	}
}
