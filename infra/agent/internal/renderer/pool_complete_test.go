package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func completePoolProfile(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "nas", "shared-runtime-profile.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCompleteProfileRejectsConfigExtensionsForEveryProvider(t *testing.T) {
	var baseline []map[string]any
	if err := yaml.Unmarshal(completePoolProfile(t), &baseline); err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 60 {
		t.Fatal("frozen profile drift")
	}
	for i := range baseline {
		t.Run(baseline[i]["id"].(string), func(t *testing.T) {
			var rows []map[string]any
			if err := yaml.Unmarshal(completePoolProfile(t), &rows); err != nil {
				t.Fatal(err)
			}
			cfg, _ := rows[i]["config"].(map[string]any)
			if cfg == nil {
				cfg = map[string]any{}
				rows[i]["config"] = cfg
			}
			cfg["unapproved"] = map[string]any{"plugins": []any{"@deepseek-ai/dsh-shell"}}
			raw, err := yaml.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProductionPoolProfile(raw); err == nil {
				t.Fatal("provider accepted unreviewed config")
			}
		})
	}
}

func TestCompleteProfileRejectsMissingRolesAndUnsafeNestedValues(t *testing.T) {
	for _, scenario := range []string{"partial", "duplicate-name", "wrong-order", "wrong-host-id", "inject", "preset", "agent-loop", "credentials", "skill-root", "sdk-file", "owner-root"} {
		t.Run(scenario, func(t *testing.T) {
			var rows []map[string]any
			if err := yaml.Unmarshal(completePoolProfile(t), &rows); err != nil {
				t.Fatal(err)
			}
			for i, row := range rows {
				switch scenario {
				case "duplicate-name":
					if i == 0 {
						row["name"] = rows[1]["name"]
					}
				case "wrong-host-id":
					if row["name"] == "auriga-dsh/shared" {
						row["id"] = "owner"
					}
				case "inject":
					if i == 0 {
						row["inject"] = []any{"shell"}
					}
				case "preset":
					if row["name"] == "@deepseek-ai/dsh-agent-preset" {
						row["config"].(map[string]any)["plugins"] = []any{map[string]any{"name": "@deepseek-ai/dsh-shell"}}
					}
				case "agent-loop":
					if row["name"] == "@deepseek-ai/dsh-agent-loop" {
						row["config"].(map[string]any)["agents"] = []any{map[string]any{"plugins": []any{"@deepseek-ai/dsh-subagent"}}}
					}
				case "credentials":
					if row["name"] == "@deepseek-ai/dsh-credentials-local" {
						row["config"].(map[string]any)["path"] = "/tmp/owner.yaml"
					}
				case "skill-root":
					if row["name"] == "@deepseek-ai/dsh-skill-filesystem" {
						row["config"].(map[string]any)["includeDefaultRoots"] = true
					}
				case "sdk-file":
					if i == 0 {
						row["name"] = "file:///opt/dsh/node_modules/@deepseek-ai/dsh-fs-local/lib/index.js"
					}
				case "owner-root":
					if row["name"] == "auriga-dsh/shared" {
						row["name"] = "auriga-dsh"
					}
				}
			}
			if scenario == "partial" {
				rows = rows[:len(rows)-1]
			}
			if scenario == "wrong-order" {
				rows[len(rows)-2], rows[len(rows)-3] = rows[len(rows)-3], rows[len(rows)-2]
			}
			raw, err := yaml.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProductionPoolProfile(raw); err == nil {
				t.Fatal("unsafe shared profile accepted")
			}
		})
	}
}

func TestCompleteFrozenSharedProfilePassesProductionValidation(t *testing.T) {
	if err := ValidateProductionPoolProfile(completePoolProfile(t)); err != nil {
		t.Fatal("frozen 60-row shared rc.3 closure rejected", err)
	}
}
