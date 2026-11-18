package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedGateEngineFailureCleanup(t *testing.T) {
	exe := os.Getenv("AURIGA_OFFLINE_GATE_EXE")
	if exe == "" {
		t.Skip("set the locally built CLI to exercise an isolated temporary Docker engine")
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/var/tmp", "gate-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	source := filepath.Join(root, "source")
	// Containerd sockets must remain short even when the data disk's temporary
	// directory has a long absolute path.
	scratch := filepath.Join(root, strings.Repeat("long-temp-", 12))
	for _, path := range []string{source, scratch} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	key, _ := testSigningKeys(t)
	cmd := exec.Command("unshare", "--net", exe, "internal-offline-build", "--source", source, "--version", "1.0", "--out", filepath.Join(root, "out.tar"), "--sign-key", key)
	cmd.Env = append(os.Environ(), "TMPDIR="+scratch, "DOCKER_HOST=unix:///nonexistent", "DOCKER_CONTEXT=invalid-gate-fixture")
	raw, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(raw), "缺少签名公钥") {
		t.Fatalf("expected missing-kit rejection after starting isolated engine: %v\n%s", err, raw)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary engine/data were not cleaned", entries, err)
	}
}
