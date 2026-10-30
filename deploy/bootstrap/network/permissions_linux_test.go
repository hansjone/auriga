package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestComposeNetworkPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root to set runtime gid10001")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "network")
	options := testOptions()
	options.PlatformDirectory = filepath.Join(root, "network-platform")
	if err := Generate(dir, options); err != nil {
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
		credential := name == "gateway.secret" || name == "platform.properties"
		parent := dir
		if credential {
			parent = options.PlatformDirectory
		}
		info, err := os.Stat(filepath.Join(parent, name))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		mode := os.FileMode(0640)
		if credential {
			mode = 0600
		}
		if info.Mode().Perm() != mode || stat.Uid != 0 || !credential && stat.Gid != 10001 || stat.Nlink != 1 {
			t.Fatalf("wrong metadata for %s: %+v", name, stat)
		}
	}
	platform, err := os.Stat(options.PlatformDirectory)
	if err != nil {
		t.Fatal(err)
	}
	actualOwner := platform.Sys().(*syscall.Stat_t).Uid
	properties, err := os.ReadFile(filepath.Join(options.PlatformDirectory, "platform.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if platform.Mode().Perm() != 0700 || !strings.Contains(string(properties), fmt.Sprintf("installation-owner=%d\n", actualOwner)) {
		t.Fatal("private mounted parent must be 0700 and match generated installation-owner")
	}
	t.Logf("platform bind source uid=%d mode=%04o; generated installation-owner matches", actualOwner, platform.Mode().Perm())
	// Prove a real uid10001 reader gets runtime material but no platform secret.
	if err := os.Chmod(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for name, allowed := range map[string]bool{"network/submit.key": true, "network/broker.conf": true, "network-platform/gateway.secret": false, "network-platform/platform.properties": false} {
		cmd := exec.Command("sh", "-c", `test -r "$1"`, "sh", filepath.Join(root, name))
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001}}
		if err := cmd.Run(); (err == nil) != allowed {
			t.Fatalf("uid10001 read %s: %v", name, err)
		}
	}
	// A symlink must fail before permissions on any target can be changed.
	path := filepath.Join(dir, "unexpected")
	if err := os.Symlink(filepath.Join(options.PlatformDirectory, "gateway.secret"), path); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", script, root).Run(); err == nil {
		t.Fatal("symlink accepted")
	}
}
