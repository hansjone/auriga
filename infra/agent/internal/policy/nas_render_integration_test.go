package policy_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
)

// Exercise the deployment producer and the agent consumer together. The Linux
// Go CI job runs this test; Windows cannot exercise the POSIX init/render chain.
func TestNASRenderedAgentPolicy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the real deployment init/render scripts require Linux/POSIX tools")
	}
	nasRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "deploy", "nas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"sh", "openssl"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("Linux deployment integration requires %s: %v", command, err)
		}
	}
	for _, tc := range []struct {
		name string
		port string
		env  string
	}{
		{name: "default_registry_port", port: "15000"},
		{name: "custom_registry_port", port: "25443", env: "AURIGA_HOST_PUBLISH_REGISTRY=25443\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot := filepath.Join(t.TempDir(), "srv", "container", "auriga-policy-test")
			if err := os.MkdirAll(dataRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			stackEnv := "AURIGA_DATA_ROOT=" + dataRoot + "\n" + tc.env
			if err := os.WriteFile(filepath.Join(dataRoot, "stack.env"), []byte(stackEnv), 0o600); err != nil {
				t.Fatal(err)
			}
			// Generate fresh credentials using the existing non-root test mode.
			// Ownership checks belong to the separate root shell regression suite.
			env := deploymentTestEnv(nasRoot, dataRoot)
			for _, script := range []string{"init.sh", "render.sh"} {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				cmd := exec.CommandContext(ctx, "sh", filepath.Join(nasRoot, "scripts", script))
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("real %s failed: %v\n%s", script, err, output)
				}
			}
			p, err := policy.Load(filepath.Join(dataRoot, "config", "agent-policy.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			t.Run("exact_repository_scope", func(t *testing.T) {
				want := []string{"127.0.0.1:" + tc.port + "/auriga/", "localhost:" + tc.port + "/auriga/"}
				if !reflect.DeepEqual(p.AllowedImageRepos, want) {
					t.Fatalf("real policy.Load repositories = %v, want %v", p.AllowedImageRepos, want)
				}
			})
			t.Run("unchanged_resource_limits", func(t *testing.T) {
				if p.MaxCPUMillis != 500 || p.MaxMemoryMB != 512 || p.MaxPIDs != 128 || p.MaxArtifactBytes != 2147483648 {
					t.Fatalf("rendered resource limits changed: cpu=%d memory=%d pids=%d artifact=%d",
						p.MaxCPUMillis, p.MaxMemoryMB, p.MaxPIDs, p.MaxArtifactBytes)
				}
				cpu, memory, pids := p.ClampResources(99999, 99999, 99999)
				if cpu != 500 || memory != 512 || pids != 128 {
					t.Fatalf("real clamp = %d/%d/%d, want 500/512/128", cpu, memory, pids)
				}
			})
			t.Run("no_allow_any_or_remote_artifacts", func(t *testing.T) {
				if p.AllowAnyImage || p.AllowAnyArtifact {
					t.Fatal("deployment must not bypass the production allowlists")
				}
				if err := p.AllowArtifactURL("https://artifacts.example.com/dsh.tar.gz"); err == nil || err.Error() != "policy: no allowedArtifactURLPrefixes configured" {
					t.Fatalf("unconfigured remote artifacts must remain denied: %v", err)
				}
			})
			digest := "@sha256:" + strings.Repeat("a", 64)
			repo := "127.0.0.1:" + tc.port + "/auriga/dsh"
			for _, image := range []struct {
				name string
				ref  string
				want string
			}{
				{name: "allow_loopback_digest", ref: repo + digest},
				{name: "allow_localhost_digest", ref: "localhost:" + tc.port + "/auriga/dsh" + digest},
				{name: "reject_other_registry", ref: "registry.example.com/auriga/dsh" + digest, want: "policy: image repository not allowlisted"},
				{name: "reject_other_port", ref: "127.0.0.1:25444/auriga/dsh" + digest, want: "policy: image repository not allowlisted"},
				{name: "reject_other_repository", ref: "127.0.0.1:" + tc.port + "/other/dsh" + digest, want: "policy: image repository not allowlisted"},
				{name: "reject_path_prefix_collision", ref: "127.0.0.1:" + tc.port + "/auriga-evil/dsh" + digest, want: "policy: image repository not allowlisted"},
				{name: "reject_host_prefix_collision", ref: "localhost.evil.example.com:" + tc.port + "/auriga/dsh" + digest, want: "policy: image repository not allowlisted"},
				{name: "reject_missing_digest", ref: repo, want: "policy: image must be repo@sha256:<64 hex>"},
				{name: "reject_mutable_tag", ref: repo + ":latest", want: "policy: image must be repo@sha256:<64 hex>"},
				{name: "reject_short_digest", ref: repo + "@sha256:aaaa", want: "policy: image must be repo@sha256:<64 hex>"},
				{name: "reject_other_digest_algorithm", ref: repo + "@sha512:" + strings.Repeat("a", 64), want: "policy: image must be repo@sha256:<64 hex>"},
			} {
				t.Run(image.name, func(t *testing.T) {
					err := p.AllowImage(image.ref)
					if image.want == "" {
						if err != nil {
							t.Fatalf("real policy.AllowImage(%q): %v", image.ref, err)
						}
					} else if err == nil || err.Error() != image.want {
						t.Fatalf("real policy.AllowImage(%q) = %v, want %s", image.ref, err, image.want)
					}
				})
			}
		})
	}
}

func deploymentTestEnv(nasRoot, dataRoot string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "AURIGA_") || strings.HasPrefix(value, "NAS_") || strings.HasPrefix(value, "GOWORK=") {
			continue
		}
		env = append(env, value)
	}
	return append(env, "NAS_ROOT="+nasRoot, "AURIGA_DATA_ROOT="+dataRoot, "NAS_ALLOW_NONROOT=1", "GOWORK=off")
}
