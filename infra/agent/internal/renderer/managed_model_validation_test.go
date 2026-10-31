package renderer

import (
	"encoding/json"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestManagedModelMaterialRejectsForeignBindingsAndUnsafeConfiguration(t *testing.T) {
	spec := &nodev1.InstanceSpec{TenantId: "tenant", OwnerId: "owner", InstanceId: "instance"}
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"tenant", func(m map[string]any) { m["tenantId"] = "other" }},
		{"member", func(m map[string]any) { m["ownerId"] = "other" }},
		{"instance", func(m map[string]any) { m["instanceId"] = "other" }},
		{"version", func(m map[string]any) { m["credentialVersion"] = 0 }},
		{"credential-purpose", func(m map[string]any) { m["credential"] = "ait_control" }},
		{"url-userinfo", func(m map[string]any) { m["baseUrl"] = "https://user:password@example.com/ai/v1" }},
		{"url-query", func(m map[string]any) { m["baseUrl"] = "https://example.com/ai/v1?key=x" }},
		{"url-path", func(m map[string]any) { m["baseUrl"] = "https://example.com/admin" }},
		{"empty-pool", func(m map[string]any) { m["models"] = []any{} }},
		{"duplicate-model", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a"}, map[string]any{"id": "a"}} }},
		{"executable-object", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "__jsExpr": "process.exit()"}} }},
		{"control-character", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a\nb"}} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{"tenantId": "tenant", "ownerId": "owner", "instanceId": "instance", "credentialVersion": 1,
				"credential": "amc.tenant.owner.instance.01900000-0000-7000-8000-000000000003." + strings.Repeat("a", 43), "baseUrl": "https://example.com/ai/v1", "models": []any{map[string]any{"id": "a"}}}
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeManagedModel(raw, spec); err != nil {
				t.Fatal("valid control rejected")
			}
			tc.change(m)
			raw, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeManagedModel(raw, spec); err == nil {
				t.Fatal("invalid material accepted")
			}
		})
	}
}
