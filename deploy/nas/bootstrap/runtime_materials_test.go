package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestNASActivationPlatformBoundary(t *testing.T) {
	if runtime.GOOS == "linux" {
		return // Linux's real fixed-uid boundary is covered by the root integration parent.
	}
	if err := runAsInstaller("unused", nil, "", "", 15000, "", "", 512, runtimeInputs{}, false); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("unsupported platform activation accepted: %v", err)
	}
	if err := runAsInstaller("", nil, "", "", 15000, "", "", 512, runtimeInputs{}, true); err != nil {
		t.Fatalf("read-only validation denied on this platform: %v", err)
	}
}

func TestRuntimeInputsAllOrNothing(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		r := runtimeInputs{}
		if mask&1 != 0 {
			r.bundle = "bundle"
		}
		if mask&2 != 0 {
			r.root = "operator-root.pb"
		}
		if mask&4 != 0 {
			r.image = "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("a", 64)
		}
		if err := r.validate(512); (err == nil) != (mask == 0 || mask == 7) {
			t.Fatalf("mask %d: %v", mask, err)
		}
		if mask == 7 && r.validate(255) == nil {
			t.Fatal("unconsumable runtime memory accepted")
		}
		if mask == 0 && r.validate(1) != nil {
			t.Fatal("base preparation minimum changed")
		}
	}
}

func TestNetworkWSSReceiptVersion(t *testing.T) {
	raw := []byte("original access bytes")
	old := sha256.Sum256(append(append([]byte{}, raw...), []byte("\n192.0.2.138\n192.0.2.128/26\n15000\n192.0.2.10\n192.0.2.0/26\n512")...))
	got := nasInputDigest(raw, "192.0.2.138", "192.0.2.128/26", 15000, "192.0.2.10", "192.0.2.0/26", 512, runtimeInputs{}, nil)
	if got == fmt.Sprintf("%x", old) {
		t.Fatal("pre-WSS receipt input accepted")
	}
	current := sha256.Sum256(append(append([]byte{}, raw...), []byte("\n192.0.2.138\n192.0.2.128/26\n15000\n192.0.2.10\n192.0.2.0/26\n512\nnetwork-wss-v2-https-litellm-v1\nhttp-or-proxy")...))
	if got != fmt.Sprintf("%x", current) {
		t.Fatal("unexpected WSS receipt input")
	}
}

func TestNASRuntimeConsumerWiring(t *testing.T) {
	checks := map[string][]string{
		"../docker-compose.yml": {
			"/config/materials/templates:/etc/auriga/templates:ro",
			"/config/materials/runtime-keys:/etc/auriga/runtime-keys:ro",
			"/config/materials/runtime-proofs:/etc/auriga/runtime-proofs:ro",
		},
		"../scripts/render.sh": {
			"AURIGA_WORKSPACE_ARTIFACT_KEYS_DIRECTORY=/etc/auriga/runtime-keys",
			"AURIGA_AGENT_RUNTIME_PROOFS_DIR=/etc/auriga/runtime-proofs",
		},
		"../scripts/lib.sh": {"-runtime-bundle", "-runtime-root", "-runtime-image"},
	}
	for path, needles := range checks {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if !strings.Contains(string(raw), needle) {
				t.Errorf("%s missing %s", path, needle)
			}
		}
		if strings.Contains(string(raw), "$CONFIG/templates") || strings.Contains(string(raw), "/config/templates:") {
			t.Errorf("%s uses unreceipted outer templates", path)
		}
	}
}
