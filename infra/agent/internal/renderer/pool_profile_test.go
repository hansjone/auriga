package renderer

import (
	"strings"
	"testing"
)

func TestPoolProfileAllowlist(t *testing.T) {
	validPoolProfile := string(completePoolProfile(t))
	if e := ValidatePoolProfile([]byte(validPoolProfile)); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"tool-bash", "tool-pwsh", "tool-terminal", "tool-jobs", "tool-subagent", "mcp-client", "api-terminal-controller", "api-job-controller", "api-settings-controller", "base", "web-app", "tool-cordis"} {
		raw := validPoolProfile + "- id: forbidden\n  name: '@deepseek-ai/dsh-" + name + "'\n  config: {}\n"
		if e := ValidatePoolProfile([]byte(raw)); e == nil {
			t.Fatal(name)
		}
	}
	for _, raw := range []string{strings.Replace(validPoolProfile, "includeDefaultRoots: false", "includeDefaultRoots: true", 1), strings.Replace(validPoolProfile, "/run/auriga/skills-active/current", "/home/private", 1), validPoolProfile + "---\n[]\n", strings.Replace(validPoolProfile, "cwd: /home/dsh/.host", "cwd: !!js evil", 1)} {
		if e := ValidatePoolProfile([]byte(raw)); e == nil {
			t.Fatal("bad profile accepted")
		}
	}
}

// Nested preset tools that can read or execute outside the member directory
// must never ride a shared preset (fs-local accepts absolute paths).
func TestPoolProfileRejectsEscapingPresetTools(t *testing.T) {
	valid := string(completePoolProfile(t))
	anchor := "      - id: tool-skill\n        name: '@deepseek-ai/dsh-tool-skill'\n"
	if !strings.Contains(valid, anchor) {
		t.Fatal("preset anchor missing from complete profile")
	}
	for _, name := range []string{"tool-present", "tool-fs", "tool-fs-search", "tool-bash", "tool-pwsh", "terminal-bash", "tool-bash-persistent", "tool-jobs", "tool-schedule", "tool-subagent", "workflow-ptc", "tool-workflow", "tool-cordis"} {
		raw := strings.Replace(valid, anchor, anchor+"      - id: escape\n        name: '@deepseek-ai/dsh-"+name+"'\n", 1)
		if e := ValidatePoolProfile([]byte(raw)); e == nil {
			t.Fatal(name)
		}
	}
}
