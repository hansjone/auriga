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
		{"zero-limit", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 0}}
		}},
		{"inconsistent-limit", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": 4096, "maxOutputTokens": 4096}}
		}},
		{"oversized-limit", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": int64(9007199254740992)}}
		}},
		{"unsafe-output", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "maxOutputTokens": int64(9007199254740992)}}
		}},
		{"context-one", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "contextWindow": 1}} }},
		{"negative-context", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "contextWindow": -1}} }},
		{"negative-output", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "maxOutputTokens": -1}} }},
		{"null-context", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "contextWindow": nil}} }},
		{"null-output", func(m map[string]any) { m["models"] = []any{map[string]any{"id": "a", "maxOutputTokens": nil}} }},
		{"fractional-limit", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 1.5}}
		}},
		{"empty-pool", func(m map[string]any) { m["models"] = []any{} }},
		{"duplicate-model", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 4096}, map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 4096}}
		}},
		{"executable-object", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 4096, "__jsExpr": "process.exit()"}}
		}},
		{"control-character", func(m map[string]any) {
			m["models"] = []any{map[string]any{"id": "a\nb", "contextWindow": 32768, "maxOutputTokens": 4096}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{"tenantId": "tenant", "ownerId": "owner", "instanceId": "instance", "credentialVersion": 1,
				"credential": "amc.tenant.owner.instance.01900000-0000-7000-8000-000000000003." + strings.Repeat("a", 43), "baseUrl": "https://example.com/ai/v1", "models": []any{map[string]any{"id": "a", "contextWindow": 32768, "maxOutputTokens": 4096}}}
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

func TestManagedModelMaterialPreservesUnknownAndLargeCapabilities(t *testing.T) {
	spec := &nodev1.InstanceSpec{TenantId: "tenant", OwnerId: "owner", InstanceId: "instance"}
	m := map[string]any{"tenantId": "tenant", "ownerId": "owner", "instanceId": "instance", "credentialVersion": 1,
		"credential": "amc.tenant.owner.instance.01900000-0000-7000-8000-000000000003." + strings.Repeat("a", 43), "baseUrl": "https://example.com/ai/v1",
		"models": []any{map[string]any{"id": "unknown"}, map[string]any{"id": "context", "contextWindow": 2000000}, map[string]any{"id": "output", "maxOutputTokens": 131072}, map[string]any{"id": "large", "contextWindow": 2000000, "maxOutputTokens": 131072}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeManagedModel(raw, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Models) != 4 {
		t.Fatal("a model with unknown capabilities invalidated the catalog")
	}
	if len(decoded.Models[0].ContextWindow) != 0 || len(decoded.Models[0].MaxOutputTokens) != 0 || string(decoded.Models[3].ContextWindow) != "2000000" || string(decoded.Models[3].MaxOutputTokens) != "131072" {
		t.Fatal("capabilities were lost or fabricated")
	}
}
