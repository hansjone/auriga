package renderer

import (
	"strings"
	"testing"
)

const validPoolProfile = `- id: auriga-dsh
  name: auriga-dsh
  config: {}
- id: skills
  name: '@deepseek-ai/dsh-skill-filesystem'
  config:
    providerName: auriga-shared
    includeDefaultRoots: false
    customSkillDirs: [/run/auriga/skills-active/current]
`

func TestPoolProfileAllowlist(t *testing.T) {
	if e := ValidatePoolProfile([]byte(validPoolProfile)); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"tool-bash", "tool-pwsh", "tool-terminal", "tool-jobs", "tool-subagent", "mcp-client", "api-terminal-controller", "api-job-controller", "api-settings-controller", "base", "web-app", "tool-cordis"} {
		raw := validPoolProfile + "- id: forbidden\n  name: '@deepseek-ai/dsh-" + name + "'\n  config: {}\n"
		if e := ValidatePoolProfile([]byte(raw)); e == nil {
			t.Fatal(name)
		}
	}
	for _, raw := range []string{strings.Replace(validPoolProfile, "includeDefaultRoots: false", "includeDefaultRoots: true", 1), strings.Replace(validPoolProfile, "/run/auriga/skills-active/current", "/home/private", 1), validPoolProfile + "---\n[]\n", strings.Replace(validPoolProfile, "config: {}", "config: {value: {__jsExpr: evil}}", 1)} {
		if e := ValidatePoolProfile([]byte(raw)); e == nil {
			t.Fatal("bad profile accepted")
		}
	}
}
