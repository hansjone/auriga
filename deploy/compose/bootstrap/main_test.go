package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// Exercise the actual CLI and protobuf for each fixture in a fresh installation.
func TestFixtureTemplates(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v: %s", err, output)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "127.0.0.1:5000/auriga/real-dsh@" + digest
	for _, fixture := range []string{"fake", "real"} {
		out := filepath.Join(root, fixture)
		if err := os.MkdirAll(filepath.Join(out, "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-fixture", fixture, "-image", image, "-out", out}
		if fixture == "real" {
			args = append(args, "-heartbeat-observer")
		}
		if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("bootstrap %s: %v: %s", fixture, err, output)
		}
		assertAccessMaterials(t, out, fixture == "real")
		name, version := fixture+"-dsh", "0.0.0-fake"
		if fixture == "real" {
			version = "0.2.0-rc.2"
		}
		entries, err := os.ReadDir(filepath.Join(out, "templates"))
		if err != nil || len(entries) != 1 || entries[0].Name() != name+".pb" {
			t.Fatalf("stale or missing template after %s: %v %v", fixture, entries, err)
		}
		raw, err := os.ReadFile(filepath.Join(out, "templates", name+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		var spec nodev1.InstanceSpec
		if err := proto.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		if spec.GetDsh().GetImage() != image || spec.GetDsh().GetDigest() != digest ||
			spec.GetDsh().GetVersion() != version || spec.GetDsh().GetArtifactId() != name ||
			spec.GetLabels()["template"] != name+"-e2e" || spec.GetProfile().GetListenPort() != 7860 {
			t.Fatalf("wrong runtime binding in %s template", fixture)
		}
		patches := spec.GetProfile().GetPatches()
		if fixture == "real" {
			if len(patches) != 2 || patches[0].GetPath() != "plugins.auriga-dsh.imBots" || patches[0].GetValueJson() != `"none"` ||
				patches[1].GetPath() != "plugins.auriga-dsh.terminalSources" || patches[1].GetValueJson() != `"browser-only"` {
				t.Fatalf("real template must declare no IM and browser-only terminal sources: %v", patches)
			}
		} else if len(patches) != 0 {
			t.Fatalf("fake template must not declare plugin config: %v", patches)
		}
		statement := spec.GetDsh().GetSigned().GetStatement()
		if statement.GetName() != name || statement.GetVersion() != version || statement.GetDigest() != digest {
			t.Fatalf("runtime statement differs from selected %s image", fixture)
		}
	}
}
