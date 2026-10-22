package app

import (
	"os"
	"path/filepath"
	"testing"
)

func clearSurfaceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AURIGA_DEPLOYMENT_ACCESS_FILE", "AURIGA_AGENT_INSTANCE_PLATFORM_URL", "AURIGA_AGENT_INSTANCE_PARENT_DOMAIN", "AURIGA_AGENT_DOCKER_EDGE_CONTAINER"} {
		t.Setenv(key, "")
	}
}
func TestInstanceSurfaceFromEnv(t *testing.T) {
	clearSurfaceEnv(t)
	t.Setenv("AURIGA_AGENT_SERVER", "https://192.0.2.10:7443")
	if _, err := instanceSurfaceFromEnv(); err == nil {
		t.Fatal("managed runtime accepted missing access file")
	}
	for _, name := range []string{"ip-http.json", "domain-proxy.json"} {
		t.Run(name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("../../../../contracts/deployment/fixtures", name))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AURIGA_DEPLOYMENT_ACCESS_FILE", path)
			got, err := instanceSurfaceFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if !got.Access.Valid() || got.EdgeContainer != "auriga-edge" || got.Access.InstanceControl().DnsAlias != "auriga-instance.internal" {
				t.Fatalf("surface: %+v", got)
			}
			for _, key := range []string{"AURIGA_AGENT_INSTANCE_PLATFORM_URL", "AURIGA_AGENT_INSTANCE_PARENT_DOMAIN", "AURIGA_AGENT_DOCKER_EDGE_CONTAINER"} {
				t.Setenv(key, "obsolete")
				if _, err := instanceSurfaceFromEnv(); err == nil {
					t.Fatalf("legacy override accepted: %s", key)
				}
				t.Setenv(key, "")
			}
		})
	}
}
func TestInstanceSurfaceRejectsUnreadableOrInvalidAccess(t *testing.T) {
	clearSurfaceEnv(t)
	p := filepath.Join(t.TempDir(), "access.json")
	t.Setenv("AURIGA_DEPLOYMENT_ACCESS_FILE", p)
	if _, err := instanceSurfaceFromEnv(); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, raw := range []string{`null`, `{}`, `{"portalOrigin":null}`, `{"mode":"ip","mode":"domain"}`} {
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := instanceSurfaceFromEnv(); err == nil {
			t.Fatalf("invalid access accepted: %s", raw)
		}
	}
}
