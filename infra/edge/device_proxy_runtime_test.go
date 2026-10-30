package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceNetworkSubmitRuntimeSeparation(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{
		filepath.Join(root, "ai-gateway", "internal", "network", "edge_submit_fixture_test.go"): filepath.Join(root, "edge", "testdata", "device_submit_runtime_test.go"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "network-fixture.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	build := exec.CommandContext(ctx, "go", "test", "-c", "-overlay", overlayPath, "-o", binary, "./internal/network")
	build.Dir = filepath.Join(root, "ai-gateway")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production runtime fixture: %v\n%s", err, output)
	}
	pki := createAccessPKI(t)
	pair := writeAccessServerTLS(t, pki)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestEdgeSubmitRuntimeFixture$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "AURIGA_EDGE_FIXTURE_CERT="+pair.CertFile, "AURIGA_EDGE_FIXTURE_KEY="+pair.KeyFile, "AURIGA_EDGE_FIXTURE_CA="+pki.hop.CAFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	t.Cleanup(func() {
		_ = stdin.Close()
		remaining, readErr := io.ReadAll(reader)
		if err := cmd.Wait(); err != nil {
			t.Errorf("runtime fixture did not stop cleanly: %v (read=%v)\n%s\n%s", err, readErr, remaining, stderr.String())
		}
	})
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("runtime fixture startup: %v", err)
	}
	var origins map[string]string
	if err := json.Unmarshal(line, &origins); err != nil || origins["network"] == "" || origins["device"] == "" {
		t.Fatalf("invalid runtime origins: %s (%v)", line, err)
	}
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("device request reached platform")
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["ai-device"], c.Upstreams["network-submit"] = origins["device"], origins["network"]
				// JSON also verifies the public config spelling. Before the fix the
				// unknown field is ignored and the real model handler returns 404.
				raw, err := json.Marshal(map[string]any{"networkSubmitHop": pki.hop})
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, c); err != nil {
					t.Fatal(err)
				}
			})
			closeAIAdminTransports(t, g)
			for _, operation := range []struct {
				method, path, media, code string
				status                    int
			}{
				{"POST", "/api/ai/v1/network/submit", "application/json", "network.unauthorized", 401},
				{"GET", "/ai/v1/models", "text/plain", "common.unauthenticated", 401},
				{"POST", "/ai/v1/chat/completions", "text/plain", "common.unauthenticated", 401},
				{"GET", "/api/ai/v1/me/usage", "text/plain", "common.not_found", 404},
			} {
				status, _, body := aiAdminRequest(t, g, addresses, "portal", operation.method, operation.path, http.Header{
					"Authorization": {"Bearer not-a-jwt"}, "Content-Type": {operation.media}, "Sec-Fetch-Mode": {"cors"},
				})
				if status != operation.status || !strings.Contains(body, `"code":"`+operation.code+`"`) {
					t.Errorf("real runtime %s: status=%d body=%s, want %d %s", operation.path, status, body, operation.status, operation.code)
				}
			}
		})
	}
}
