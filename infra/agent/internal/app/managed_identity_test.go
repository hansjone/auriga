package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"gopkg.in/yaml.v3"
)

func managedInstallationFixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed-network.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstanceSurfaceCombinesIPAccessInternalModelsAndIndependentIdentity(t *testing.T) {
	for _, access := range []string{"ip-http.json", "domain-proxy.json"} {
		t.Run(access, func(t *testing.T) {
			clearSurfaceEnv(t)
			path, err := filepath.Abs(filepath.Join("../../../../contracts/deployment/fixtures", access))
			if err != nil {
				t.Fatal(err)
			}
			const identity = "https://native.example.com"
			const models = "https://auriga-instance.internal:18446"
			t.Setenv("AURIGA_DEPLOYMENT_ACCESS_FILE", path)
			t.Setenv("AURIGA_AGENT_MANAGED_PLATFORM_ORIGIN", models)
			t.Setenv("AURIGA_AGENT_MANAGED_INSTALLATION_FILE", managedInstallationFixture(t, fmt.Sprintf(`{"platformOrigin":%q,"issuer":%q,"network":{"realm":"1"}}`, identity, identity)))
			t.Setenv("AURIGA_LINKS_ALLOWED_ORIGINS", strings.Join([]string{"http://192.0.2.10:8080", identity, models, "https://admin.example.com", "https://relay.example.com"}, ","))
			got, err := instanceSurfaceFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			cfg := got.dockerConfig(runtime.DockerConfig{Host: "http://127.0.0.1:1"})
			if cfg.PlatformHost != "auriga-instance.internal" || !reflect.DeepEqual(cfg.ManagedHosts, []string{"auriga-instance.internal", "native.example.com"}) {
				t.Fatalf("identity issuance was replaced by portal/model or all allowlisted hosts: %+v", cfg)
			}
			if access == "ip-http.json" && (cfg.PortalHost != "" || got.Access.PortalURL() != "http://192.0.2.10:8080" || got.Access.Issuer() != "http://192.0.2.10:8080") {
				t.Fatal("IP browser/issuer access was changed to supply a DNS alias")
			}
			if access == "domain-proxy.json" && cfg.PortalHost != "portal.example.com" {
				t.Fatal("independent managed issuance removed the browser portal alias")
			}
			driver, err := runtime.NewDocker(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ManagedHosts[1] = "relay.example.com"
			if driver.ManagedHosts[1] != "native.example.com" {
				t.Fatal("mutable caller config changed installation aliases after validation")
			}
		})
	}
}

func TestManagedIdentityInstallationRejectsUnavailableOrUnboundSource(t *testing.T) {
	const origin = "https://native.example.com"
	for name, raw := range map[string]string{
		"empty":        "",
		"invalid":      "{",
		"mismatch":     `{"platformOrigin":"https://native.example.com","issuer":"https://other.example.com","network":{}}`,
		"unapproved":   `{"platformOrigin":"https://other.example.com","issuer":"https://other.example.com","network":{}}`,
		"duplicate":    `{"platformOrigin":"https://native.example.com","platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{}}`,
		"credential":   `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{},"credential":"do-not-echo-this"}`,
		"null-network": `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":null}`,
		"trailing":     `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{}} {}`,
		"oversized":    strings.Repeat(" ", 65537),
	} {
		t.Run(name, func(t *testing.T) {
			path := managedInstallationFixture(t, raw)
			if _, err := managedIdentityHost(path, origin); err == nil || strings.Contains(err.Error(), "do-not-echo-this") {
				t.Fatal("invalid static issuance source accepted or echoed")
			}
		})
	}
	for _, path := range []string{"relative.json", filepath.Join(t.TempDir(), "missing.json"), t.TempDir()} {
		if _, err := managedIdentityHost(path, origin); err == nil {
			t.Fatal("configured unavailable static source accepted")
		}
	}
}

func TestManagedOriginRequiresExactApprovedCanonicalDNSOrigin(t *testing.T) {
	for _, origin := range []string{"http://native.example.com", "https://192.0.2.10", "https://native.example.com/", "https://native.example.com?", "https://native.example.com#x", "https://user@native.example.com", "https://Native.example.com", "https://native.example.com:", "https://native.example.com:0", "https://native.example.com:65536", "https://native.example.com:0443", "https://*.example.com", "https://native.example.com."} {
		if _, err := managedOriginHost(origin, origin); err == nil {
			t.Fatal("unsafe managed origin accepted")
		}
	}
	for _, origin := range []string{"https://native.example.com", "https://native.example.com:443", "https://auriga-instance.internal:18446"} {
		if host, err := managedOriginHost(origin, origin); err != nil || host == "" {
			t.Fatalf("approved HTTPS origin rejected: %v", err)
		}
	}
}

func TestNASManagedIdentityProjectionReachesAgentInBothCheckedModes(t *testing.T) {
	root := filepath.Join("../../../../deploy/nas")
	type mount struct {
		Type, Source, Target string
		ReadOnly             bool `yaml:"read_only"`
		Bind                 struct {
			CreateHostPath *bool `yaml:"create_host_path"`
		}
	}
	readAgent := func(name string) (map[string]string, []mount) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Services map[string]yaml.Node }
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		var agent struct {
			Environment map[string]string
			Volumes     []mount
		}
		node := doc.Services["agent"]
		if err := node.Decode(&agent); err != nil {
			t.Fatal(err)
		}
		return agent.Environment, agent.Volumes
	}
	// Only decode env in the base recipe: its existing short-form mounts remain
	// outside this single-file projection contract.
	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var base struct {
		Services map[string]struct{ Environment map[string]string }
	}
	if err := yaml.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	if env := base.Services["agent"].Environment; env["AURIGA_AGENT_MANAGED_PLATFORM_ORIGIN"] != "${AURIGA_MANAGED_PLATFORM_ORIGIN:-}" || env["AURIGA_LINKS_ALLOWED_ORIGINS"] != "${AURIGA_LINKS_ALLOWED_ORIGINS:-}" {
		t.Fatal("agent lost the same approved installation inputs used by workspace and edge")
	}
	for _, name := range []string{"collab-compose.yml", "managed-identity-compose.yml"} {
		env, mounts := readAgent(name)
		if env["AURIGA_AGENT_MANAGED_INSTALLATION_FILE"] != "/etc/auriga/managed-identity.json" || len(mounts) != 1 {
			t.Fatal("checked identity flow did not project one static file to agent")
		}
		m := mounts[0]
		if m.Type != "bind" || m.Source != "${AURIGA_DATA_ROOT}/config/collab-runtime/platform/managed-network.json" || m.Target != env["AURIGA_AGENT_MANAGED_INSTALLATION_FILE"] || !m.ReadOnly || m.Bind.CreateHostPath == nil || *m.Bind.CreateHostPath {
			t.Fatal("agent static source was not the existing read-only checked projection")
		}
	}
}
