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
