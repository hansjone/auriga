package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollabRuntimeProjection(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root for runtime UID test")
	}
	root, err := os.MkdirTemp("", "auriga-collab-projection-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "collab-installation")
	pki := filepath.Join(root, "pki")
	for _, dir := range []string{source, pki} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range collabLeaves {
		if err := os.WriteFile(filepath.Join(source, name), []byte("trusted "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"edge-collab.crt", "edge-collab.key", "ca.crt"} {
		if err := os.WriteFile(filepath.Join(pki, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "collab-materials.sh"))
	if err != nil {
		t.Fatal(err)
	}
	run := func() error {
		t.Helper()
		b, e := exec.Command("sh", script, root).CombinedOutput()
		if e != nil {
			t.Logf("projection: %v %s", e, b)
		}
		return e
	}
	for i := 0; i < 2; i++ {
		if run() != nil {
			t.Fatal("initial/reentry projection failed")
		}
	}
	probe := `umask 077; test -r "$1/collab-runtime/config/business.der" && ! test -r "$1/collab-installation/business.der" && ! test -r "$1/collab-edge/edge-collab.key" && : > "$1/collab-runtime/state/state.json.lock"`
	if b, e := exec.Command("setpriv", "--reuid=10002", "--regid=10002", "--clear-groups", "sh", "-c", probe, "sh", root).CombinedOutput(); e != nil {
		t.Fatalf("collab UID separation: %v %s", e, b)
	}
	if run() != nil {
		t.Fatal("runtime lock polluted source receipt")
	}
	for _, name := range []string{"runtime-mismatch", "source-hardlink", "source-symlink", "runtime-permissions"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "collab-runtime", "config", "protocol.der")
			original, _ := os.ReadFile(path)
			switch name {
			case "runtime-mismatch":
				if e := os.WriteFile(path, []byte("mismatch"), 0600); e != nil {
					t.Fatal(e)
				}
			case "runtime-permissions":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			default:
				path = filepath.Join(source, "protocol.der")
				original, _ = os.ReadFile(path)
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if name == "source-hardlink" {
					err = os.Link(filepath.Join(source, "business.der"), path)
				} else {
					err = os.Symlink(filepath.Join(source, "business.der"), path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if run() == nil {
				t.Fatal("invalid projection accepted")
			}
			if name == "runtime-permissions" {
				if e := os.Chmod(path, 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, original, 0600); e != nil {
					t.Fatal(e)
				}
				if strings.Contains(path, "collab-runtime") {
					if e := os.Chown(path, 10002, 10002); e != nil {
						t.Fatal(e)
					}
				}
			}
		})
	}
}

func TestCollabLifecycleRecipe(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	bin := filepath.Join(root, "bin")
	vars := filepath.Join(root, "var")
	for _, dir := range []string{scripts, bin, vars} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../scripts/compose.sh")
	if e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(scripts, "compose.sh")
	if e = os.WriteFile(script, raw, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		b, e := cmd.CombinedOutput()
		return string(b), e
	}
	if b, e := run("ps"); e == nil {
		t.Fatalf("status accepted missing env: %s", b)
	}
	if b, e := run("bootstrap-registry"); e != nil || !strings.Contains(b, "registry") {
		t.Fatalf("bootstrap registry: %v %s", e, b)
	}
	if e = os.WriteFile(filepath.Join(vars, "compose.env"), []byte("COMPOSE_FILE=must-never-select.yml\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := run("ps"); e != nil || strings.Contains(b, "collab-required") {
		t.Fatalf("default recipe: %v %s", e, b)
	}
	if e = os.Mkdir(filepath.Join(vars, "collab-installation"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"ps", "up", "start", "stop", "bootstrap-registry"} {
		b, e := run(action)
		if e != nil || !strings.Contains(b, "collab-required.yml") || !strings.Contains(b, "network-collab") || !strings.Contains(b, "compose.env") {
			t.Fatalf("enabled lifecycle %s: %v %s", action, e, b)
		}
	}
	if b, e := run("up", "--file", "foreign.yml"); e == nil {
		t.Fatalf("recipe override accepted: %s", b)
	}
}
