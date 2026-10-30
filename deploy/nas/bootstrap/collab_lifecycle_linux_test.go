package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/testfixture"
)

func TestNASCollabScriptLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("NAS installer lifecycle requires Linux root")
	}
	root, err := os.MkdirTemp("", "auriga-collab-script-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build %v: %s", err, b)
	}
	nas, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			_, cfg, err := loadNASAccess("../access-"+mode+".example.json", "192.0.2.138", "192.0.2.128/26", 15000)
			if err != nil {
				t.Fatal(err)
			}
			input := testfixture.Write(t, cfg.TenantID())
			cmd := exec.Command("sh", filepath.Join(nas, "test", "collab.sh"), binary, root, input, mode)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script lifecycle %v: %s", err, b)
			} else {
				t.Log(string(b))
			}
		})
	}
}
