package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Run the public prepare script and actual bootstrap with synthetic registry
// proofs and build metadata matching the shape and size of the real output.
func TestPrepareRealFixturePreservesFullBuildMetadata(t *testing.T) {
	root := t.TempDir()
	compose := filepath.Join(root, "deploy", "compose")
	scripts, bin := filepath.Join(compose, "scripts"), filepath.Join(root, "bin")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{scripts, bin, tmp, filepath.Join(compose, "bootstrap")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prepare, err := os.ReadFile("../scripts/prepare.sh")
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(scripts, "prepare.sh"), string(prepare))
	for _, name := range []string{
		"compose.sh", "network-permissions.sh", "https-materials.sh",
		"collab-materials.sh", "renew-panel.sh", "bifrost-permissions.sh",
	} {
		write(filepath.Join(scripts, name), "#!/bin/sh\nexit 0\n")
	}
	for _, name := range []string{"sleep", "chgrp", "chown"} {
		write(filepath.Join(bin, name), "#!/bin/sh\nexit 0\n")
	}
	write(filepath.Join(bin, "id"), "#!/bin/sh\necho 0\n")
	write(filepath.Join(bin, "go"), `#!/bin/sh
[ "$1" = run ] && [ "$2" = . ] || exit 1
shift 2
exec "$PREPARE_TEST_BOOTSTRAP" "$@"
`)
	write(filepath.Join(scripts, "build-real-dsh.mjs"), `
import { copyFileSync } from 'node:fs';
if (process.argv[2] !== '--metadata-out') throw new Error('expected metadata output');
copyFileSync(process.env.PREPARE_TEST_METADATA, process.argv[3]);
`)
	write(filepath.Join(scripts, "collect-runtime-proofs.mjs"), `
import { cpSync } from 'node:fs';
if (process.argv[2] !== process.env.PREPARE_TEST_IMAGE) throw new Error('wrong collected image');
cpSync(process.env.PREPARE_TEST_PROOFS, process.argv[3], { recursive: true });
`)

	proofs, image := runtimeProofFixture(t, "real")
	files := make([]struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}, 64202)
	sha := strings.Repeat("a", 64)
	for i := range files {
		files[i].Path = fmt.Sprintf("dsh/node_modules/@deepseek-ai/dsh/testdata/runtime/package-inputs/%05d.json", i)
		files[i].SHA256 = sha
	}
	metadata := filepath.Join(root, "build-metadata.json")
	build := map[string]any{
		"image": image, "localOnly": false,
		"target": "linux/amd64", "files": files,
	}

	bootstrap := filepath.Join(root, "bootstrap")
	bash := "bash"
	if runtime.GOOS == "windows" {
		bootstrap += ".exe"
		bash = `C:\Program Files\Git\bin\bash.exe`
	}
	if output, err := exec.Command("go", "build", "-o", bootstrap, ".").CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v: %s", err, output)
	}
	bashPath := func(path string) string {
		path = filepath.ToSlash(path)
		if runtime.GOOS == "windows" {
			return "/" + strings.ToLower(path[:1]) + path[2:]
		}
		return path
	}
	run := func(version string) (string, error) {
		build["dshVersion"] = version
		writeJSON(metadata, build, 0o600)
		cmd := exec.Command(bash, "--noprofile", "--norc", "-c",
			`export PATH="$PREPARE_TEST_BIN:/usr/bin:/bin:$PATH"; exec bash --noprofile --norc "$1"`,
			"prepare-fixture", bashPath(filepath.Join(scripts, "prepare.sh")))
		cmd.Env = append(os.Environ(),
			"DSH_FIXTURE=real", "COMPOSE_PROJECT_NAME=prepare-fixture", "TMPDIR="+bashPath(tmp),
			"PREPARE_TEST_BIN="+bashPath(bin), "PREPARE_TEST_BOOTSTRAP="+bashPath(bootstrap),
			"PREPARE_TEST_METADATA="+metadata, "PREPARE_TEST_PROOFS="+proofs, "PREPARE_TEST_IMAGE="+image,
		)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	out := filepath.Join(compose, "var")
	savedPath := filepath.Join(out, "real-dsh.json")
	t.Run("rejects-stale-version", func(t *testing.T) {
		output, err := run("0.0.0-stale")
		if err == nil || !strings.Contains(output, "real DSH build metadata must match the selected image and pinned version") {
			t.Fatalf("expected bootstrap version rejection: %v\n%s", err, output)
		}
		if _, err := os.Stat(savedPath); !os.IsNotExist(err) {
			t.Fatalf("rejected build published metadata: %v", err)
		}
	})
	t.Run("publishes-full-metadata", func(t *testing.T) {
		if output, err := run(lockedDSHVersion(t)); err != nil {
			t.Fatalf("public prepare: %v\n%s", err, output)
		}
		original, err := os.ReadFile(metadata)
		if err != nil {
			t.Fatal(err)
		}
		if len(original) <= maxProofBytes {
			t.Fatalf("build metadata must exceed the proof limit, got %d bytes", len(original))
		}
		saved, err := os.ReadFile(savedPath)
		if err != nil || !bytes.Equal(saved, original) {
			t.Fatalf("prepare must preserve the full build metadata byte-for-byte: saved %d bytes: %v", len(saved), err)
		}
		if err := validateRuntimeTemplate(out); err != nil {
			t.Fatal(err)
		}
		t.Logf("public prepare signed the real template and preserved %d bytes with %d file entries", len(saved), len(files))
	})
}
