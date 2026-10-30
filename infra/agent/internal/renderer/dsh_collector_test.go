package renderer_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in probe uses the actual DSH packages installed from the real-dsh
// lockfile. Ordinary Go tests do not download or start a DSH installation.
func TestCordisRealDSHBrowserCollector(t *testing.T) {
	runtimeRoot := os.Getenv("AURIGA_DSH_COLLECTOR_RUNTIME")
	if runtimeRoot == "" {
		t.Skip("set AURIGA_DSH_COLLECTOR_RUNTIME to an isolated real-dsh installation")
	}
	raw, err := renderCordis(t, baseSpec(), "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	patch := filepath.Join(dir, "cordis.patch.yml")
	if err := os.WriteFile(patch, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/dsh-collector-probe.mjs", runtimeRoot, patch, dir)
	// The probe generates its own managed identity files and must not inherit
	// installation credentials, platform endpoints or the user's DSH profile.
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		name = strings.ToUpper(name)
		if !strings.HasPrefix(name, "AURIGA_") && !strings.HasPrefix(name, "DSH_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	out, err := cmd.CombinedOutput()
	t.Logf("real DSH collector:\n%s", out)
	if err != nil {
		t.Fatalf("DSH collector probe: %v", err)
	}
}
