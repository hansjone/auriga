package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestManagedPlatformPermissionProjection(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root for runtime UID test")
	}
	root := t.TempDir()
	for _, dir := range []string{"network", "network-platform", "collab-installation", "pki"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"network/server.pem", "pki/gateway.key", "network-platform/gateway.secret", "network-platform/platform.properties", "network-platform/collab.secret", "network-platform/managed-network.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func() error {
		t.Helper()
		b, e := exec.Command("sh", "../scripts/network-permissions.sh", root).CombinedOutput()
		if e != nil {
			t.Logf("projection: %v %s", e, b)
		}
		return e
	}
	for i := 0; i < 2; i++ {
		if run() != nil {
			t.Fatal("managed material projection/reentry failed")
		}
	}
	info, err := os.Stat(filepath.Join(root, "network-platform/managed-network.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("managed material must remain owner-only")
	}
	if err := os.WriteFile(filepath.Join(root, "network-platform/unknown"), []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Fatal("unknown material accepted")
	}
	if err := os.Remove(filepath.Join(root, "network-platform/unknown")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "network-platform/managed-network.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "network-platform/collab.secret"), filepath.Join(root, "network-platform/managed-network.json")); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Fatal("symlink managed material accepted")
	}
}
