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
	f := activationFixture(t)
	root := filepath.Dir(f.out)
	// The generated executable must not occupy the recipe's bootstrap directory.
	if f.binary == filepath.Join(root, "bootstrap") {
		binary := filepath.Join(root, "fixture-bootstrap")
		if e := os.Rename(f.binary, binary); e != nil {
			t.Fatal(e)
		}
		f.binary = binary
	}
	scripts := filepath.Join(root, "scripts")
	bin := filepath.Join(root, "bin")
	vars := f.out
	for _, dir := range []string{scripts, bin, filepath.Join(root, "bootstrap")} {
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
	if e = os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf 'env-project=%s\\n' \"$COMPOSE_PROJECT_NAME\"\nprintf '%s\\n' \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	// The copied recipe has no source module of its own. Relay the exact Go-run
	// shape to the real binary compiled by activationFixture, preserving errors.
	goRelay := `#!/bin/sh
[ "$#" -ge 4 ] && [ "$1" = -C ] && [ -d "$2" ] && [ "$2" = "$AURIGA_LIFECYCLE_TEST_ROOT/bootstrap" ] && [ "$3" = run ] && [ "$4" = . ] || exit 2
shift 4
exec "$AURIGA_LIFECYCLE_TEST_BOOTSTRAP" "$@"
`
	if e = os.WriteFile(filepath.Join(bin, "go"), []byte(goRelay), 0700); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "AURIGA_LIFECYCLE_TEST_ROOT="+filepath.Dir(filepath.Dir(script)), "AURIGA_LIFECYCLE_TEST_BOOTSTRAP="+f.binary, "COMPOSE_PROJECT_NAME=foreign-example")
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
	assertProject := func(b string) {
		t.Helper()
		if !strings.Contains(b, "--project-name\npublic-test\n") || !strings.Contains(b, "env-project=public-test\n") || strings.Contains(b, "foreign-example") {
			t.Fatalf("lifecycle escaped sealed project: %s", b)
		}
	}
	if b, e := run("ps"); e != nil || strings.Contains(b, "collab-required") {
		t.Fatalf("default recipe: %v %s", e, b)
	} else {
		assertProject(b)
	}
	// A failed real query must propagate, never become a successful fake project.
	access := filepath.Join(vars, "access.json")
	accessRaw, e := os.ReadFile(access)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(access, append(accessRaw, '\n'), 0644); e != nil {
		t.Fatal(e)
	}
	if b, e := run("ps"); e == nil || !strings.Contains(b, "seal invalid") || strings.Contains(b, "env-project=") {
		t.Fatalf("query failure swallowed: %v %s", e, b)
	}
	if e = os.WriteFile(access, accessRaw, 0644); e != nil {
		t.Fatal(e)
	}
	// An empty collab-installation directory cannot stand in for a sealed N1
	// installation. Build a separate first-install fixture; do not upgrade base.
	n1Root := filepath.Join(root, "n1")
	for _, dir := range []string{filepath.Join(n1Root, "scripts"), filepath.Join(n1Root, "bootstrap")} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	script = filepath.Join(n1Root, "scripts", "compose.sh")
	if e = os.WriteFile(script, raw, 0700); e != nil {
		t.Fatal(e)
	}
	vars = filepath.Join(n1Root, "var")
	proofs, image := runtimeProofFixture(t, "real")
	args := []string{"-out", vars, "-access-file", access, "-edge-container", "public-test-edge", "-image", image, "-fixture", "real", "-runtime-proofs", proofs,
		"-https-cert-file", filepath.Join(f.out, "browser-tls", "browser.crt"), "-https-key-file", filepath.Join(f.out, "browser-tls", "browser.key"), "-network-collab-file", f.input}
	if b, e := exec.Command(f.binary, args...).CombinedOutput(); e != nil {
		t.Fatalf("sealed N1 fixture: %v %s", e, b)
	}
	if e = os.WriteFile(filepath.Join(vars, "compose.env"), []byte("COMPOSE_FILE=must-never-select.yml\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"ps", "up", "start", "stop", "bootstrap-registry"} {
		b, e := run(action)
		if e != nil || !strings.Contains(b, "collab-required.yml") || !strings.Contains(b, "network-collab") || !strings.Contains(b, "compose.env") {
			t.Fatalf("enabled lifecycle %s: %v %s", action, e, b)
		}
		assertProject(b)
	}
	if b, e := run("up", "--file", "foreign.yml"); e == nil {
		t.Fatalf("recipe override accepted: %s", b)
	}
}
