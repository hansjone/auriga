package main

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const resourcePlatformOrigin = "https://host.docker.internal:8081"

type resourceRouteConfig struct {
	Upstreams map[string]string `json:"upstreams"`
	IAMHop    map[string]string `json:"iamHop"`
}

func readResourceRouteConfig(t *testing.T, out string) resourceRouteConfig {
	t.Helper()
	var edge resourceRouteConfig
	raw, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &edge); err != nil {
		t.Fatal(err)
	}
	return edge
}

func TestDevResourceRouteInstallationSeal(t *testing.T) {
	const option = "-dev-resource-route"
	base := activationFixtureWithCA(t, false, "-heartbeat-observer")
	enabled := activationFixtureWithCA(t, false, "-heartbeat-observer", option)
	baseEdge := readResourceRouteConfig(t, base.out)
	enabledEdge := readResourceRouteConfig(t, enabled.out)
	if _, present := baseEdge.Upstreams["resource"]; present {
		t.Fatal("default installation acquired a resource route")
	}
	if enabledEdge.Upstreams["resource"] != resourcePlatformOrigin || enabledEdge.Upstreams["iam"] != "https://host.docker.internal:8082" {
		t.Fatal("resource must reach platform directly while IAM still uses its observer")
	}
	delete(enabledEdge.Upstreams, "resource")
	if !maps.Equal(baseEdge.Upstreams, enabledEdge.Upstreams) || !maps.Equal(baseEdge.IAMHop, enabledEdge.IAMHop) {
		t.Fatal("opting in changed other routes or the authenticated hop")
	}
	for _, tc := range []struct {
		name    string
		fixture activationFixtureData
		changed []string
	}{
		{"refuse-add", base, append(base.installArgs, option)},
		{"refuse-remove", enabled, enabled.installArgs[:len(enabled.installArgs)-1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fixture
			before := originalBase(t, f.out)
			if b, err := exec.Command(f.binary, f.installArgs...).CombinedOutput(); err != nil || !strings.Contains(string(b), "(existing installation)") {
				t.Fatalf("same-input prepare did not reuse installation: %v %s", err, b)
			}
			if b, err := exec.Command(f.binary, tc.changed...).CombinedOutput(); err == nil || !strings.Contains(string(b), "installation inputs or identity materials changed") {
				t.Fatalf("changed resource route selection was not refused: %v %s", err, b)
			}
			if b, err := exec.Command(f.binary, "-out", f.out, "-print-compose-recipe").CombinedOutput(); err != nil || strings.TrimSpace(string(b)) != "public-test" {
				t.Fatalf("normal lifecycle refused the unchanged seal: %v %s", err, b)
			}
			assertBase(t, f.out, before)
		})
	}
	path := filepath.Join(enabled.out, "edge.config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), `"resource": "`+resourcePlatformOrigin+`"`, `"resource": "https://other.example.com:8081"`, 1)
	if changed == string(raw) {
		t.Fatal("failed to construct resource origin drift fixture")
	}
	if err := os.WriteFile(path, []byte(changed), 0644); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(enabled.binary, "-out", enabled.out, "-print-compose-recipe").CombinedOutput(); err == nil || !strings.Contains(string(b), "seal invalid") {
		t.Fatalf("normal lifecycle accepted resource origin drift: %v %s", err, b)
	}
}
