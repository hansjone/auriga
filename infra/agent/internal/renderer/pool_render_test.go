package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const renderedPool = "00000000-0000-4000-8000-000000000002"
const renderedTenant = "00000000-0000-4000-8000-000000000001"

func TestPoolRenderKeepsExpandedProfileAndImmutableSharedEntry(t *testing.T) {
	profile, meta := completePoolProfile(t), t.TempDir()
	digest, err := RenderPool(profile, nil, renderedPool, renderedTenant, meta, 7860)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RenderPool(profile, nil, renderedPool, renderedTenant, meta, 7860)
	if err != nil || digest != again {
		t.Fatal("reopen changed immutable profile", err)
	}
	raw, err := os.ReadFile(filepath.Join(meta, "pool-profile.yml"))
	if err != nil || ValidateProductionPoolProfile(raw) != nil || !strings.Contains(string(raw), SharedBrowserModule) || strings.Contains(string(raw), "!!js") {
		t.Fatal("complete literal profile not rendered", err)
	}
	entry, err := os.ReadFile(filepath.Join(meta, "pool-entry.yml"))
	if err != nil || string(entry) != "[]\n" {
		t.Fatal("readonly boot root not empty", err)
	}
	if _, err := os.Stat(filepath.Join(meta, "shared-ports.mjs")); !os.IsNotExist(err) {
		t.Fatal("handwritten ports module materialized", err)
	}
	if _, err := RenderPool(profile, []byte("// unapproved ports"), renderedPool, renderedTenant, meta, 7860); err == nil {
		t.Fatal("handwritten ports accepted")
	}
	if _, err := RenderPool(profile, nil, renderedPool, renderedTenant, meta, 7861); err == nil {
		t.Fatal("live profile replaced")
	}
	for _, invalid := range []string{
		strings.Replace(string(profile), "name: auriga-dsh/shared\n", "name: auriga-dsh\n", 1),
		strings.Replace(string(profile), "mode: native", "mode: code", 1),
		string(profile) + "- id: shell\n  name: '@deepseek-ai/dsh-tool-bash'\n",
	} {
		if _, err := RenderPool([]byte(invalid), nil, renderedPool, renderedTenant, t.TempDir(), 7860); err == nil {
			t.Fatal("forbidden profile accepted")
		}
	}
}

func TestPoolProductionPortsAndDedicatedModeAreImmutable(t *testing.T) {
	profile, meta := completePoolProfile(t), t.TempDir()
	if err := ValidateProductionPoolProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProductionPoolProfile([]byte(strings.Replace(string(profile), "auriga-dsh/shared-ports", "file:///run/auriga/shared-ports.mjs", 1))); err == nil {
		t.Fatal("external handwritten ports accepted")
	}
	if _, err := RenderPoolMode(profile, nil, renderedPool, renderedTenant, "dedicated", meta, 7860); err == nil {
		t.Fatal("dedicated borrowed the shared launcher")
	}
	if _, err := RenderPoolMode(profile, nil, renderedPool, renderedTenant, "shared", meta, 7860); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(meta, "pool-binding.json"))
	if err != nil || !strings.Contains(string(raw), `"mode":"shared"`) {
		t.Fatal("mode not recorded", err)
	}
	raw, err = os.ReadFile(filepath.Join(meta, "pool-profile.yml"))
	if err != nil || !strings.Contains(string(raw), "/var/lib/auriga/pools/"+renderedPool+"/control/shared-host.json") {
		t.Fatal("fixed Host config missing", err)
	}
}

func TestProductionBrowserProfileUsesFixedModuleAndMode(t *testing.T) {
	profile := string(completePoolProfile(t))
	for _, invalid := range []string{
		strings.Replace(profile, SharedBrowserModule, "auriga-dsh/shared-browser", 1),
		strings.Replace(profile, SharedBrowserModule, "file:///run/auriga/browser.mjs", 1),
		strings.Replace(profile, SharedBrowserModule, SharedBrowserModule+"?owner=1", 1),
		strings.Replace(profile, SharedFrontendIndex, "https://example.com/index.html", 1),
		strings.Replace(profile, SharedFrontendIndex, "/opt/dsh/public/index.html", 1),
	} {
		if ValidateProductionPoolProfile([]byte(invalid)) == nil {
			t.Fatal("unapproved browser config accepted")
		}
	}
}
