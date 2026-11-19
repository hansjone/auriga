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

func TestPoolProductionPortsAndDedicatedModeAreImmutable(t *testing.T) {
	profile := strings.Replace(runnablePoolProfile, "- id: auriga-dsh\n  name: auriga-dsh/shared\n- id: ports\n  name: file:///run/auriga/shared-ports.mjs", "- id: ports\n  name: auriga-dsh/shared-ports\n- id: auriga-dsh\n  name: auriga-dsh/shared", 1)
	if e := ValidateProductionPoolProfile([]byte(profile)); e != nil {
		t.Fatal(e)
	}
	if ValidateProductionPoolProfile([]byte(runnablePoolProfile)) == nil {
		t.Fatal("external handwritten ports accepted")
	}
	meta := t.TempDir()
	if _, e := RenderPoolMode([]byte(profile), nil, "pool", "tenant", "dedicated", meta, 7860); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(meta, "pool-binding.json"))
	if e != nil || !strings.Contains(string(raw), `"mode":"dedicated"`) {
		t.Fatal("mode not recorded", e)
	}
	if _, e = RenderPoolMode([]byte(profile), nil, "pool", "tenant", "shared", meta, 7860); e == nil {
		t.Fatal("running mode mapping changed")
	}
	raw, e = os.ReadFile(filepath.Join(meta, "pool-profile.yml"))
	if e != nil || !strings.Contains(string(raw), "/var/lib/auriga/pools/pool/control/shared-host.json") {
		t.Fatal("Host config not wired", e)
	}
}

func TestProductionBrowserProfileUsesFixedModuleAndMode(t *testing.T) {
	profile := strings.Replace(runnablePoolProfile, "- id: auriga-dsh\n  name: auriga-dsh/shared\n- id: ports\n  name: file:///run/auriga/shared-ports.mjs", "- id: ports\n  name: auriga-dsh/shared-ports\n- id: auriga-dsh\n  name: auriga-dsh/shared", 1)
	browser := "- id: shared-browser\n  name: auriga-dsh/shared-browser\n  config: {distIndex: /opt/dsh/public/index.html}\n"
	profile += browser
	if err := ValidateProductionPoolProfile([]byte(profile)); err != nil {
		t.Fatal("fixed native browser export rejected", err)
	}
	meta := t.TempDir()
	if _, err := RenderPoolMode([]byte(profile), nil, "pool", "tenant", "shared", meta, 7860); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(meta, "pool-profile.yml"))
	if err != nil || !strings.Contains(string(raw), "auriga-dsh/shared-browser") || !strings.Contains(string(raw), "/opt/dsh/public/index.html") {
		t.Fatal("native browser not materialized", err)
	}
	if _, err := RenderPoolMode([]byte(profile), nil, "pool", "tenant", "dedicated", t.TempDir(), 7860); err == nil {
		t.Fatal("dedicated loaded shared browser")
	}
	for _, invalid := range []string{strings.Replace(profile, "/opt/dsh/public/index.html", "../index.html", 1), strings.Replace(profile, "/opt/dsh/public/index.html", "https://example.com/index.html", 1), profile + browser, strings.Replace(profile, "distIndex: /opt/dsh/public/index.html", "distIndex: /opt/dsh/public/index.html, owner: true", 1)} {
		if err := ValidateProductionPoolProfile([]byte(invalid)); err == nil {
			t.Fatal("unapproved browser profile accepted")
		}
	}
}
