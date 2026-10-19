package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
)

func TestAllowArtifactURL(t *testing.T) {
	p := policy.Default()
	p.AllowedArtifactURLPrefixes = []string{"https://artifacts.example.com/"}
	if err := p.AllowArtifactURL("https://artifacts.example.com/a.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := p.AllowArtifactURL("http://artifacts.example.com/a"); err == nil {
		t.Fatal("http should fail")
	}
	if err := p.AllowArtifactURL("https://evil.example.com/a"); err == nil {
		t.Fatal("other host should fail")
	}
}

func TestAllowImageAndClamp(t *testing.T) {
	p := policy.Default()
	p.AllowedImageRepos = []string{"registry.example.com/auriga/"}
	p.MaxMemoryMB = 1024
	if err := p.AllowImage("registry.example.com/auriga/dsh@sha256:" + hex64()); err != nil {
		t.Fatal(err)
	}
	if err := p.AllowImage("evil.example.com/x@sha256:" + hex64()); err == nil {
		t.Fatal("evil registry")
	}
	cpu, mem, pids := p.ClampResources(99999, 99999, 99999)
	if cpu != p.MaxCPUMillis || mem != 1024 || pids != p.MaxPIDs {
		t.Fatalf("%d %d %d", cpu, mem, pids)
	}
}

func TestLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-policy.yaml")
	raw := []byte("allowedImageRepos:\n  - registry.example.com/auriga/\nmaxMemoryMB: 512\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := policy.Load(path)
	if err != nil || p.MaxMemoryMB != 512 || len(p.AllowedImageRepos) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func hex64() string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
