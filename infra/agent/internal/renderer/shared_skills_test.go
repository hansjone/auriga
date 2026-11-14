package renderer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedSkillContractFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "contracts", "resource")
	raw, err := os.ReadFile(filepath.Join(root, "cordis-upgrade-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID       string `json:"id"`
			Input    string `json:"inputPath"`
			Expected string `json:"expectedPath"`
			Shared   bool   `json:"expectShared"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join(root, tc.Input))
			if err != nil {
				t.Fatal(err)
			}
			expected, err := os.ReadFile(filepath.Join(root, tc.Expected))
			if err != nil {
				t.Fatal(err)
			}
			actual, err := MergeSharedSkills(input)
			if (err == nil) != tc.Shared {
				t.Fatalf("shared=%v, error=%v", tc.Shared, err)
			}
			if !bytes.Equal(actual, expected) {
				t.Fatalf("fixture differs:\n%s", actual)
			}
			if tc.Shared {
				again, err := MergeSharedSkills(actual)
				if err != nil || !bytes.Equal(again, actual) {
					t.Fatalf("not idempotent: %v", err)
				}
			}
		})
	}
}

func TestSharedSkillReservedPatch(t *testing.T) {
	if _, _, err := splitPatchPath("plugins.auriga-shared-skills.customSkillDirs"); err == nil {
		t.Fatal("managed row accepted an override")
	}
}

func TestSharedSkillAmbiguousYAMLPreservesInput(t *testing.T) {
	for _, input := range []string{
		"- &provider {id: other, config: {providerName: auriga-shared}}\n- *provider\n",
		"- id: other\n  config: {providerName: other, providerName: auriga-shared}\n",
		"- insert: {id: other}\n",
		"- id: other\n---\n- id: second\n",
		"- insert:\n    - id: other\n      config: {providerName: auriga-shared}\n",
	} {
		got, err := MergeSharedSkills([]byte(input))
		if err == nil || string(got) != input {
			t.Fatalf("ambiguous document changed or shared: %v", err)
		}
	}
}
