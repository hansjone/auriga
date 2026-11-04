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

func TestInstanceSurfaceApprovedHTTPSIPIdentityDoesNotNeedDNSAlias(t *testing.T) {
	for _, tc := range []struct {
		name, identity, models string
		wantHosts              []string
	}{
		{"ipv4-identity", "https://192.0.2.10:8443", "https://auriga-instance.internal:18446", []string{"auriga-instance.internal"}},
		{"ipv6-identity", "https://[2001:db8::10]:8443", "https://auriga-instance.internal:18446", []string{"auriga-instance.internal"}},
		{"ip-models-and-identity", "https://192.0.2.10:8443", "https://192.0.2.10:8443", nil},
		{"ip-identity-only", "https://[2001:db8::10]", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearSurfaceEnv(t)
			path, err := filepath.Abs(filepath.Join("../../../../contracts/deployment/fixtures", "ip-http.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AURIGA_DEPLOYMENT_ACCESS_FILE", path)
			t.Setenv("AURIGA_AGENT_MANAGED_PLATFORM_ORIGIN", tc.models)
			t.Setenv("AURIGA_AGENT_MANAGED_INSTALLATION_FILE", managedInstallationFixture(t, fmt.Sprintf(`{"platformOrigin":%q,"issuer":%q,"network":{}}`, tc.identity, tc.identity)))
			t.Setenv("AURIGA_LINKS_ALLOWED_ORIGINS", strings.Join([]string{"http://192.0.2.10:8080", tc.identity, tc.models}, ","))
			surface, err := instanceSurfaceFromEnv()
			if err != nil {
				t.Fatalf("approved HTTPS/IP installation blocked agent configuration: %v", err)
			}
			cfg := surface.dockerConfig(runtime.DockerConfig{Host: "http://127.0.0.1:1"})
			if surface.ManagedIdentityHost != "" || cfg.PlatformHost != "auriga-instance.internal" || cfg.PortalHost != "" || !reflect.DeepEqual(cfg.ManagedHosts, tc.wantHosts) {
				t.Fatalf("IP source changed required DNS aliases: %+v", cfg)
			}
			if surface.Access.PortalURL() != "http://192.0.2.10:8080" || surface.Access.Issuer() != "http://192.0.2.10:8080" {
				t.Fatal("HTTPS/IP issuance rewrote the original HTTP/IP access")
			}
			if _, err := runtime.NewDocker(cfg); err != nil {
				t.Fatalf("approved IP installation cannot construct the runtime: %v", err)
			}
		})
	}
}

func TestInstanceSurfaceIdentityTrustsOnlyExactApprovedPortalWithoutExtraAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name, origin, allowed string
		wantOK                bool
	}{
		{"default-portal", "https://portal.example.com", "", true},
		{"default-portal-unrelated-list", "https://portal.example.com", "https://relay.example.com", true},
		{"admin-is-not-portal", "https://admin.example.com", "", false},
		{"other-domain", "https://other.example.com", "", false},
		{"same-host-other-port", "https://portal.example.com:8443", "", false},
		{"portal-suffix", "https://portal.example.com.evil.test", "", false},
		{"http-is-not-issuance", "http://portal.example.com", "http://portal.example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearSurfaceEnv(t)
			path, err := filepath.Abs(filepath.Join("../../../../contracts/deployment/fixtures", "domain-proxy.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AURIGA_DEPLOYMENT_ACCESS_FILE", path)
			t.Setenv("AURIGA_AGENT_MANAGED_INSTALLATION_FILE", managedInstallationFixture(t, fmt.Sprintf(`{"platformOrigin":%q,"issuer":%q,"network":{}}`, tc.origin, tc.origin)))
			t.Setenv("AURIGA_LINKS_ALLOWED_ORIGINS", tc.allowed)
			surface, err := instanceSurfaceFromEnv()
			if !tc.wantOK {
				if err == nil {
					t.Fatal("unapproved static identity origin inherited portal trust")
				}
				return
			}
			if err != nil {
				t.Fatalf("original approved portal required duplicate allowlist configuration: %v", err)
			}
			cfg := surface.dockerConfig(runtime.DockerConfig{Host: "http://127.0.0.1:1"})
			if surface.ManagedIdentityHost != "portal.example.com" || cfg.PortalHost != "portal.example.com" || !reflect.DeepEqual(cfg.ManagedHosts, []string{"portal.example.com"}) {
				t.Fatalf("default issuance portal was not preserved: %+v", cfg)
			}
			if _, err := runtime.NewDocker(cfg); err != nil {
				t.Fatalf("original portal installation cannot construct runtime: %v", err)
			}
		})
	}
}

func TestManagedIdentityInstallationRejectsUnavailableOrUnboundSource(t *testing.T) {
	const origin = "https://native.example.com"
	for name, raw := range map[string]string{
		"empty":         "",
		"invalid":       "{",
		"mismatch":      `{"platformOrigin":"https://native.example.com","issuer":"https://other.example.com","network":{}}`,
		"unapproved":    `{"platformOrigin":"https://other.example.com","issuer":"https://other.example.com","network":{}}`,
		"unapproved-ip": `{"platformOrigin":"https://192.0.2.10:8443","issuer":"https://192.0.2.10:8443","network":{}}`,
		"http-ip":       `{"platformOrigin":"http://192.0.2.10:8443","issuer":"http://192.0.2.10:8443","network":{}}`,
		"ip-mismatch":   `{"platformOrigin":"https://192.0.2.10:8443","issuer":"https://192.0.2.11:8443","network":{}}`,
		"duplicate":     `{"platformOrigin":"https://native.example.com","platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{}}`,
		"credential":    `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{},"credential":"do-not-echo-this"}`,
		"null-network":  `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":null}`,
		"trailing":      `{"platformOrigin":"https://native.example.com","issuer":"https://native.example.com","network":{}} {}`,
		"oversized":     strings.Repeat(" ", 65537),
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

func TestManagedOriginRequiresExactApprovedCanonicalHTTPSOrigin(t *testing.T) {
	for _, origin := range []string{"http://native.example.com", "http://192.0.2.10", "https://192.0.2.10:", "https://192.0.2.10:0443", "https://192.0.2.10:65536", "https://[2001:DB8::10]", "https://[2001:db8::10]:", "https://[2001:db8::10]:0", "https://[2001:db8::10%25eth0]", "https://native.example.com/", "https://native.example.com?", "https://native.example.com#x", "https://user@native.example.com", "https://Native.example.com", "https://native.example.com:", "https://native.example.com:0", "https://native.example.com:65536", "https://native.example.com:0443", "https://*.example.com", "https://native.example.com."} {
		if _, err := managedOriginHost(origin, origin); err == nil {
			t.Fatal("unsafe managed origin accepted")
		}
	}
	for origin, wantHost := range map[string]string{
		"https://native.example.com":             "native.example.com",
		"https://native.example.com:443":         "native.example.com",
		"https://auriga-instance.internal:18446": "auriga-instance.internal",
		"https://192.0.2.10":                     "",
		"https://192.0.2.10:8443":                "",
		"https://[2001:db8::10]":                 "",
		"https://[2001:db8::10]:8443":            "",
	} {
		if host, err := managedOriginHost(origin, origin); err != nil || host != wantHost {
			t.Fatalf("approved HTTPS origin host=%q, want=%q, err=%v", host, wantHost, err)
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
