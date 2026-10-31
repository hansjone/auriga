package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testNASRuntimeUpgradeLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root and fixed uid10001")
	}
	parent, err := os.MkdirTemp("", "auriga-runtime-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(parent) }()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(parent, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	r, _ := runtimeTestFixture(t, parent)
	base := filepath.Join(parent, "materials")
	args := []string{"-out", base, "-access-file", "../access-ip.example.json", "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26", "-runtime-bundle", r.bundle, "-runtime-root", r.root, "-runtime-image", r.image}
	if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("install: %v %s", err, b)
	}
	before := nasSnapshot(t, base)
	out := filepath.Join(parent, "runtime-source")
	currentBundle := ""
	run := func(action, bundle, image string, ok bool) string {
		t.Helper()
		a := []string{"runtime-upgrade", "-action", action, "-base", base, "-out", out}
		if bundle == "" && action != "path" {
			bundle = currentBundle
		}
		if bundle != "" {
			a = append(a, "-bundle", bundle)
		}
		if image != "" {
			a = append(a, "-image", image)
		}
		b, err := exec.Command("sh", append([]string{"-c", `umask 077; exec "$@"`, "sh", binary}, a...)...).CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("%s: %v %s", action, err, b)
		}
		if nasSnapshot(t, base) != before {
			t.Fatal("sealed base changed")
		}
		return strings.TrimSpace(string(b))
	}
	if got := run("path", "", "", true); got != base {
		t.Fatal(got)
	}
	makeRelease := func(name string, seq uint64, version string) string {
		t.Helper()
		dir := filepath.Dir(r.bundle)
		target := filepath.Join(dir, name)
		_, err := runtimebundle.Sign(runtimebundle.SignOptions{Image: r.image, ArtifactID: "dsh-test-linux-amd64", Version: version, OS: "linux", Arch: "amd64", Sequence: seq, IssuedAt: timestamppb.New(time.Now().Add(-time.Minute)), RootPath: r.root, PrivateKeyPath: filepath.Join(dir, "publisher.pem"), ManifestPath: filepath.Join(dir, "manifest.json"), ConfigPath: filepath.Join(dir, "config.json"), OutputDir: target})
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	newBundle := makeRelease("release8", 8, "1.2.4")
	currentBundle = newBundle
	run("stage", r.bundle, r.image, false) // rejects initial sequence replay
	run("stage", newBundle, r.image+"bad", false)
	run("stage", newBundle, r.image, true)
	active := run("path", "", "", true)
	if active == base || !strings.HasPrefix(active, out+"/") {
		t.Fatal(active)
	}
	snapshot := nasSnapshot(t, out)
	run("stage", newBundle, r.image, true)
	if nasSnapshot(t, out) != snapshot {
		t.Fatal("same pending candidate was not idempotent")
	}
	run("commit", "", r.image+"bad", false)
	run("rollback", "", r.image, true)
	if run("path", "", "", true) != base {
		t.Fatal("rollback did not restore base")
	}
	run("stage", r.bundle, r.image, false) // rollback does not lower high water
	conflict := makeRelease("conflict8", 8, "1.2.5")
	run("stage", conflict, r.image, false)
	run("stage", newBundle, r.image, true)
	run("commit", conflict, r.image, false)
	run("rollback", conflict, r.image, false)
	run("commit", "", r.image, true)
	run("commit", "", r.image, true)
	run("rollback", "", r.image, false) // cannot roll back committed activation
	if run("path", "", "", true) != active {
		t.Fatal("committed active changed")
	}
	// Byte, metadata and link tampering fail without repairing installation.
	leaf := filepath.Join(active, "templates", "real-dsh.pb")
	raw, err := os.ReadFile(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, append(raw, 'x'), 0644); err != nil {
		t.Fatal(err)
	}
	run("path", "", "", false)
	if err := os.WriteFile(leaf, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaf, 0666); err != nil {
		t.Fatal(err)
	}
	run("path", "", "", false)
	if err := os.Chmod(leaf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(leaf, filepath.Join(parent, "linked")); err != nil {
		t.Fatal(err)
	}
	run("path", "", "", false)
}
