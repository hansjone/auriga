package policy_test

import (
	"os"
	"path/filepath"
	"strings"
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

func TestArtifactURLPrefixNeedsTrailingSlash(t *testing.T) {
	p := policy.Default()
	// Without trailing slash normalization, https://a.example.com would match a.example.com.evil.
	p.AllowedArtifactURLPrefixes = []string{"https://a.example.com"}
	if err := p.AllowArtifactURL("https://a.example.com/pkg"); err != nil {
		t.Fatal(err)
	}
	if err := p.AllowArtifactURL("https://a.example.com.evil/pkg"); err == nil {
		t.Fatal("host suffix bypass")
	}
	if err := p.AllowArtifactURL("https://a.example.comx/pkg"); err == nil {
		t.Fatal("path prefix bypass")
	}
}

func TestLoadNormalizesPrefixesAndClearsAllowAny(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-policy.yaml")
	raw := []byte("allowedArtifactURLPrefixes:\n  - https://cdn.example.com\nallowAnyImage: true\nallowAnyArtifact: true\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := policy.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AllowedArtifactURLPrefixes) != 1 || p.AllowedArtifactURLPrefixes[0] != "https://cdn.example.com/" {
		t.Fatalf("prefixes=%v", p.AllowedArtifactURLPrefixes)
	}
	if p.AllowAnyImage || p.AllowAnyArtifact {
		t.Fatal("AllowAny* must be cleared on Load")
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

func TestAllowImageExactReferences(t *testing.T) {
	const repo = "registry.example.com/auriga/dsh"
	image := repo + "@sha256:" + hex64()
	for _, tc := range []struct {
		name  string
		image string
		want  string
	}{
		{name: "approved_locator", image: image},
		{name: "other_digest", image: repo + "@sha256:" + strings.Repeat("b", 64), want: "policy: image reference not allowlisted"},
		{name: "repository_suffix", image: repo + "-extra@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "subrepository", image: repo + "/child@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "other_registry", image: "other.example.com/auriga/dsh@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "registry_case", image: "Registry.example.com/auriga/dsh@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "repository_case", image: "registry.example.com/auriga/DSH@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "digest_case", image: repo + "@sha256:" + strings.Repeat("A", 64), want: "policy: image reference not allowlisted"},
		{name: "registry_alias", image: "registry.example.com:443/auriga/dsh@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "tag", image: repo + ":latest", want: "policy: image reference not allowlisted"},
		{name: "tag_with_digest", image: repo + ":latest@sha256:" + hex64(), want: "policy: image reference not allowlisted"},
		{name: "short_digest", image: repo + "@sha256:aaaa", want: "policy: image reference not allowlisted"},
		{name: "empty", want: "policy: image reference not allowlisted"},
		{name: "whitespace", image: " " + image, want: "policy: image reference not allowlisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy.Default()
			p.AllowedImageRepos = []string{"registry.example.com/auriga/"}
			p.AllowedImageRefs = []string{image}
			err := p.AllowImage(tc.image)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("AllowImage(%q) = %v, want %s", tc.image, err, tc.want)
			}
		})
	}
}

func TestAllowImageExactReferencesAreAdditionalConstraint(t *testing.T) {
	image := "registry.example.com/auriga/dsh@sha256:" + hex64()
	for _, tc := range []struct {
		name  string
		refs  []string
		repos []string
		want  string
	}{
		{name: "nil_keeps_prefix_behavior", repos: []string{"registry.example.com/auriga/d"}},
		{name: "empty_denies_all", refs: []string{}, repos: []string{"registry.example.com/auriga/"}, want: "policy: image reference not allowlisted"},
		{name: "empty_entry_is_not_wildcard", refs: []string{""}, repos: []string{"registry.example.com/auriga/"}, want: "policy: image reference not allowlisted"},
		{name: "match_among_multiple", refs: []string{"", "other.example.com/dsh@sha256:" + hex64(), image}, repos: []string{"registry.example.com/auriga/"}},
		{name: "match_cannot_bypass_empty_prefixes", refs: []string{image}, want: "policy: no allowedImageRepos configured"},
		{name: "match_cannot_bypass_empty_prefix", refs: []string{image}, repos: []string{""}, want: "policy: image repository not allowlisted"},
		{name: "match_cannot_bypass_other_prefix", refs: []string{image}, repos: []string{"other.example.com/"}, want: "policy: image repository not allowlisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy.Default()
			p.AllowedImageRepos = tc.repos
			p.AllowedImageRefs = tc.refs
			err := p.AllowImage(image)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("AllowImage = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestAllowImageExactMatchStillRequiresDigestShape(t *testing.T) {
	for _, image := range []string{"", "registry.example.com/auriga/dsh:latest", "registry.example.com/auriga/dsh@sha256:aaaa"} {
		t.Run(image, func(t *testing.T) {
			p := policy.Default()
			p.AllowedImageRepos = []string{"registry.example.com/auriga/"}
			p.AllowedImageRefs = []string{image}
			err := p.AllowImage(image)
			if err == nil || err.Error() != "policy: image must be repo@sha256:<64 hex>" {
				t.Fatalf("AllowImage = %v, want digest shape rejection", err)
			}
		})
	}
}

func TestAllowAnyImageCannotBypassExactReferences(t *testing.T) {
	image := "registry.example.com/auriga/dsh@sha256:" + hex64()
	for _, tc := range []struct {
		name string
		refs []string
		want string
	}{
		{name: "nil_preserves_test_switch"},
		{name: "match", refs: []string{image}},
		{name: "empty", refs: []string{}, want: "policy: image reference not allowlisted"},
		{name: "mismatch", refs: []string{"other.example.com/dsh@sha256:" + hex64()}, want: "policy: image reference not allowlisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy.Default()
			p.AllowAnyImage = true
			p.AllowedImageRefs = tc.refs
			err := p.AllowImage(image)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("AllowImage = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestLoadImageExactReferences(t *testing.T) {
	image := "registry.example.com/auriga/dsh@sha256:" + hex64()
	for _, tc := range []struct {
		name    string
		yaml    string
		nilRefs bool
		want    string
	}{
		{name: "omitted", nilRefs: true},
		{name: "empty", yaml: "allowedImageRefs: []\n", want: "policy: image reference not allowlisted"},
		{name: "approved", yaml: "allowedImageRefs:\n  - " + image + "\n"},
		{name: "mismatch", yaml: "allowedImageRefs:\n  - other.example.com/dsh@sha256:" + hex64() + "\n", want: "policy: image reference not allowlisted"},
		{name: "empty_entry", yaml: "allowedImageRefs: [\"\"]\n", want: "policy: image reference not allowlisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-policy.yaml")
			raw := "allowedImageRepos:\n  - registry.example.com/auriga/\nallowAnyImage: true\nallowAnyArtifact: true\n" + tc.yaml
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			p, err := policy.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if (p.AllowedImageRefs == nil) != tc.nilRefs {
				t.Fatalf("AllowedImageRefs = %#v, want nil=%v", p.AllowedImageRefs, tc.nilRefs)
			}
			if p.AllowAnyImage || p.AllowAnyArtifact {
				t.Fatal("YAML AllowAny* must remain disabled")
			}
			err = p.AllowImage(image)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("AllowImage = %v, want %s", err, tc.want)
			}
		})
	}
}
