package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const runnablePoolProfile = `- id: auriga-dsh
  name: auriga-dsh/shared
- id: ports
  name: file:///run/auriga/shared-ports.mjs
- id: listener
  name: '@deepseek-ai/dsh-host-webserver'
- id: tools
  name: '@deepseek-ai/dsh-tools'
  config: {mode: native}
- id: skills
  name: '@deepseek-ai/dsh-skill-filesystem'
  config: {providerName: auriga-shared, includeDefaultRoots: false, customSkillDirs: [/run/auriga/skills-active/current]}
`

func TestPoolRenderKeepsExpandedProfileAndImmutableSharedEntry(t *testing.T) {
	meta := t.TempDir()
	digest, err := RenderPool([]byte(runnablePoolProfile), []byte("// fixture ports\n"), "pool", "tenant", meta, 7860)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RenderPool([]byte(runnablePoolProfile), []byte("// fixture ports\n"), "pool", "tenant", meta, 7860)
	if err != nil || digest != again {
		t.Fatal("reopen changed immutable profile")
	}
	raw, err := os.ReadFile(filepath.Join(meta, "pool-profile.yml"))
	if err != nil || !strings.Contains(string(raw), "auriga-dsh/shared") || strings.Contains(string(raw), "dsh-base") {
		t.Fatal("shared profile was not rendered independently")
	}
	if _, err := RenderPool([]byte(runnablePoolProfile), []byte("// changed module\n"), "pool", "tenant", meta, 7860); err == nil {
		t.Fatal("live pool module was replaced")
	}
	for _, profile := range []string{
		strings.Replace(runnablePoolProfile, "auriga-dsh/shared", "auriga-dsh", 1),
		strings.Replace(runnablePoolProfile, "mode: native", "mode: code", 1),
		runnablePoolProfile + "- id: shell\n  name: '@deepseek-ai/dsh-tool-bash'\n",
	} {
		if _, err := RenderPool([]byte(profile), []byte("// fixture\n"), "pool", "tenant", t.TempDir(), 7860); err == nil {
			t.Fatal("forbidden shared host profile accepted")
		}
	}
}
