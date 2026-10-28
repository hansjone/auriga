package network

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestComposeNetworkPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root to set runtime gid10001")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "network")
	if err := Generate(dir, testOptions()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "pki"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pki", "gateway.key"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "compose", "scripts", "network-permissions.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if out, err := exec.Command("sh", script, root).CombinedOutput(); err != nil {
			t.Fatalf("permissions: %v %s", err, out)
		}
	}
	for _, name := range append(append([]string{}, Files...), CredentialFiles...) {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		mode := os.FileMode(0640)
		credential := name == "gateway.secret" || name == "platform.properties"
		if credential {
			mode = 0600
		}
		if info.Mode().Perm() != mode || stat.Uid != 0 || !credential && stat.Gid != 10001 || stat.Nlink != 1 {
			t.Fatalf("wrong metadata for %s: %+v", name, stat)
		}
	}
	// Prove a real uid10001 reader gets runtime material but no platform secret.
	if err := os.Chmod(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for name, allowed := range map[string]bool{"submit.key": true, "broker.conf": true, "gateway.secret": false, "platform.properties": false} {
		cmd := exec.Command("sh", "-c", `test -r "$1"`, "sh", filepath.Join(dir, name))
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001}}
		if err := cmd.Run(); (err == nil) != allowed {
			t.Fatalf("uid10001 read %s: %v", name, err)
		}
	}
	// A symlink must fail before permissions on any target can be changed.
	path := filepath.Join(dir, "unexpected")
	if err := os.Symlink(filepath.Join(dir, "gateway.secret"), path); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", script, root).Run(); err == nil {
		t.Fatal("symlink accepted")
	}
}
