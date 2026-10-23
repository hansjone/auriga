//go:build docker

package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/releaseproof"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

// Integration test against a real Docker Engine. Skipped in default CI
// (build tag docker). Run: go test -tags docker ./internal/runtime/ -count=1
func TestDockerIntegrationSmoke(t *testing.T) {
	pol := policy.Default()
	pol.AllowAnyImage = true // integration only
	d, err := runtime.NewDocker(runtime.DockerConfig{Timeout: 30 * time.Second, Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Client.Ping(ctx); err != nil {
		t.Skip("docker engine not available:", err)
	}
	image := os.Getenv("AURIGA_TEST_IMAGE")
	if image == "" || !containsDigest(image) {
		t.Skip("set AURIGA_TEST_IMAGE=repo@sha256:… to run")
	}
	proofs := os.Getenv("AURIGA_TEST_RUNTIME_PROOFS_DIR")
	if proofs == "" {
		t.Skip("set AURIGA_TEST_RUNTIME_PROOFS_DIR to original public OCI evidence for AURIGA_TEST_IMAGE")
	}
	_, digest, _ := strings.Cut(image, "@")
	m, c, err := releaseproof.Read(proofs, digest)
	if err != nil {
		t.Fatal(err)
	}
	fixture := dockerFixture(t, d)
	entry := filepath.Join(fixture.proofs, digest[7:])
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "manifest.json"), m, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "config.json"), c, 0600); err != nil {
		t.Fatal(err)
	}
	full := releaseSpec(fixture, "itest", 7)
	full.Dsh.Image = image
	full.Dsh.ReleaseEnvelope.ImageDigest = digest
	signRelease(t, full.Dsh, fixture.key)
	home := t.TempDir()
	meta := t.TempDir()
	spec := &runtime.Spec{
		Full:       full,
		InstanceID: "itest",
		Generation: 1,
		Image:      image,
		HomeBind:   home,
		MetaBind:   meta,
		HostUID:    200001,
		CPUMillis:  500,
		MemoryMB:   256,
		PIDs:       64,
		ListenPort: 7860,
	}
	if err := d.Activation.Activate(ctx, full, func(ctx context.Context) error {
		if err := d.Ensure(ctx, spec, &runtime.Rendered{}); err != nil {
			return err
		}
		return d.Start(ctx, "itest")
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Remove(context.Background(), "itest") })
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := d.Inspect(ctx, "itest")
		if err == nil && st.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container not running: %+v %v", st, err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func containsDigest(image string) bool {
	return len(image) > 0 && (len(image) > 8) && (indexOf(image, "@sha256:") >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
