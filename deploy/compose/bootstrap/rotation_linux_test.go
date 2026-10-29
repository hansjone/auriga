package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test uses only temporary files and an injected executor, never Docker/PG/WSL.
func TestRotationLinuxRootProjectionAndReadOnlyReentry(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Linux root DAC projection; no real deployment")
	}
	f := activationFixture(t)
	if err := runActivation(f.out, f.input, f.base, f.identity, false, false, true); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(f.out, activationDirectory)
	runtimeRoot := filepath.Join(f.out, "collab-activation-runtime")
	if raw, err := exec.Command("sh", "../scripts/collab-materials.sh", source, runtimeRoot).CombinedOutput(); err != nil {
		t.Fatalf("temporary projection: %v %s", err, raw)
	}
	state := filepath.Join(runtimeRoot, "collab-runtime", "state", "state.json")
	if err := os.WriteFile(state+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(state+".lock", 10002, 10002); err != nil {
		t.Fatal(err)
	}
	keys, _ := os.ReadFile(filepath.Join(f.out, "iam-installation", "compose.keys"))
	access, _ := os.ReadFile(filepath.Join(f.out, "access.env"))
	ca, _ := os.ReadFile(filepath.Join(f.out, "ca-sha256.txt"))
	composeEnv := append(append(append([]byte{}, keys...), []byte("AURIGA_AGENT_CA_SHA256="+strings.TrimSpace(string(ca))+"\n")...), access...)
	if err := os.WriteFile(filepath.Join(f.out, "compose.env"), composeEnv, 0600); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(filepath.Dir(f.out), "fixture.jar")
	jarBytes := []byte("injected executor fixture; not a runnable Java artifact")
	if err := os.WriteFile(jar, jarBytes, 0600); err != nil {
		t.Fatal(err)
	}
	r := &rotation{compose: filepath.Dir(f.out), out: f.out, jar: jar, input: rotationInput{Base: f.base, Identity: f.identity, Jar: digestBytes(jarBytes)}}
	if err := r.binding(); err != nil {
		t.Fatal(err)
	}
	initial, _ := rotationRead(filepath.Join(source, "collab-installation", "signed.json"))
	var signed map[string]any
	_ = json.Unmarshal(initial, &signed)
	signed["keyset"].(map[string]any)["keyset_version"] = "8"
	candidate, _ := json.Marshal(signed)
	inputDir := filepath.Dir(f.input)
	r.input.SignedFile = filepath.Join(inputDir, "rotation-signed.json")
	r.input.AnnouncementFile = filepath.Join(inputDir, "rotation-announcement.json")
	if err := os.WriteFile(r.input.SignedFile, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.input.AnnouncementFile, []byte(`{"fixture":"pair"}`), 0600); err != nil {
		t.Fatal(err)
	}
	sealed := map[string]string{}
	for _, name := range activationFiles() {
		raw, err := rotationRead(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		sealed[name] = digestBytes(raw)
	}
	var instances []rotationContainer
	var recipe []string
	args := r.composeArgs()
	for i, arg := range args {
		if arg == "-f" {
			recipe = append(recipe, args[i+1])
		}
	}
	for i, service := range []string{"platform", "edge", "network", "collab"} {
		var c rotationContainer
		c.ID = strings.Repeat(string(rune('a'+i)), 64)
		c.Image = "sha256:" + strings.Repeat("e", 64)
		c.State.FinishedAt = "2026-10-29T00:00:00Z"
		c.Config.Labels = map[string]string{"com.docker.compose.service": service, "com.docker.compose.project": r.project, "com.docker.compose.project.working_dir": r.compose, "com.docker.compose.project.config_files": strings.Join(recipe, ",")}
		if service == "collab" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{r.config, "/etc/auriga/collab"}, struct{ Source, Destination string }{filepath.Dir(r.state), "/var/lib/auriga/collab"})
		}
		if service == "network" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{filepath.Join(r.network, "runtime.json"), "/etc/auriga/network/runtime.json"}, struct{ Source, Destination string }{filepath.Join(r.network, "protocol.key"), "/etc/auriga/network/protocol.key"})
		}
		instances = append(instances, c)
	}
	javaMutations := 0
	r.run = func(program string, args ...string) ([]byte, error) {
		if program == "/usr/bin/docker" {
			if args[0] == "inspect" {
				return json.Marshal(instances)
			}
			if strings.Contains(strings.Join(args, " "), " stop ") {
				return nil, nil
			}
			var ids []string
			for _, c := range instances {
				ids = append(ids, c.ID)
			}
			return []byte(strings.Join(ids, "\n")), nil
		}
		if program != "/usr/bin/java" {
			return nil, fmt.Errorf("unexpected executor")
		}
		action := args[len(args)-2]
		var plan operatorPlan
		raw, err := rotationRead(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		if err := rotationJSON(raw, &plan); err != nil {
			return nil, err
		}
		if action == "publish" {
			javaMutations++
		}
		if action == "install" {
			javaMutations++
			var stateBody map[string]any
			old, err := os.ReadFile(r.state)
			if err != nil {
				return nil, err
			}
			_ = json.Unmarshal(old, &stateBody)
			stateBody["version"] = "8"
			body, _ := json.Marshal(stateBody)
			if err := os.WriteFile(r.state, body, 0600); err != nil {
				return nil, err
			}
		}
		if action == "status" || action == "publish" || action == "install" {
			_, entries, _ := rotationPayload(candidate)
			history := map[string]string{}
			for _, entry := range entries {
				history[entry["kid"]] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			return json.Marshal(map[string]any{"version": "8", "databaseTime": time.Now().UTC().Format(time.RFC3339Nano), "manifestDigest": "fixture", "pendingPush": true, "firstPublished": history})
		}
		return []byte(`{}`), nil
	}
	if err := r.publish(); err != nil {
		t.Fatal(err)
	}
	if err := r.publish(); err != nil {
		t.Fatalf("same-byte publication reentry: %v", err)
	}
	var originalInput collabInput
	inputBytes, _ := rotationRead(f.input)
	_ = json.Unmarshal(inputBytes, &originalInput)
	r.input.PrivateFiles = originalInput.PrivateFiles
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if err := r.install(); err != nil {
		t.Fatalf("same-byte installation reentry: %v", err)
	}
	if javaMutations != 2 {
		t.Fatalf("reentry performed mutations: %d", javaMutations)
	}
	for name, hash := range sealed {
		raw, err := rotationRead(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil || digestBytes(raw) != hash {
			t.Fatalf("original source changed: %s %v", name, err)
		}
	}
	if _, err := checkActivation(f.out); err != nil {
		t.Fatalf("original seal no longer valid: %v", err)
	}
	if err := os.Chmod(filepath.Join(r.root(), "installs", "8", "network", "protocol.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.install(); err == nil {
		t.Fatal("exposed runtime private accepted on reentry")
	}
}

func TestRotationLinuxRejectsSourceLinksAndWritableAncestors(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Linux root source metadata fixture")
	}
	for _, probe := range []string{"hardlink", "symlink", "parent-symlink", "parent-writable", "private-readable"} {
		t.Run(probe, func(t *testing.T) {
			dir := t.TempDir()
			private := filepath.Join(dir, "private.der")
			if err := os.WriteFile(private, []byte("test-only secret bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := rotationRead(private); err != nil {
				t.Fatal(err)
			}
			switch probe {
			case "hardlink":
				if err := os.Link(private, filepath.Join(dir, "linked.der")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(dir, "symlink.der")
				if err := os.Symlink(private, link); err != nil {
					t.Fatal(err)
				}
				private = link
			case "parent-symlink":
				link := dir + "-linked"
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				private = filepath.Join(link, "private.der")
			case "parent-writable":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
			case "private-readable":
				if err := os.Chmod(private, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rotationRead(private); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestRotationLinuxSystemAncestorsDoNotGrantOperations(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture uid=%d euid=%d supported=%t", os.Getuid(), os.Geteuid(), rotationSupported())
	// A setuid context is only a gate test: its created leaf has the effective
	// owner's identity, so it must not stand in for a caller-owned fixture.
	if os.Getuid() == os.Geteuid() {
		t.Run("protected-caller-leaf-under-system-ancestors", func(t *testing.T) {
			if raw, err := rotationRead(input); err != nil || string(raw) != `{}` {
				t.Fatalf("protected fixture refused: %v", err)
			}
		})
	}
	t.Run("writable-ancestor-rejected", func(t *testing.T) {
		parent := filepath.Join(dir, "writable")
		if err := os.Mkdir(parent, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0777); err != nil {
			t.Fatal(err)
		}
		if err := rotationInputParents(filepath.Join(parent, "input.json")); err == nil || !strings.Contains(err.Error(), "ancestor DAC refused") {
			t.Fatalf("writable ancestor accepted: %v", err)
		}
	})
	t.Run("symlink-ancestor-rejected", func(t *testing.T) {
		link := filepath.Join(dir, "linked")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if err := rotationInputParents(filepath.Join(link, "input.json")); err == nil {
			t.Fatal("symlink ancestor accepted")
		}
	})
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Run("public-cli-still-requires-real-and-effective-root", func(t *testing.T) {
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "LOADER_") || key == "JAVA_TOOL_OPTIONS" || key == "JDK_JAVA_OPTIONS" || key == "_JAVA_OPTIONS" || key == "CLASSPATH" {
					t.Setenv(key, "")
				}
			}
			err := keyRotationCLI([]string{"status", input})
			if err == nil || !strings.Contains(err.Error(), "requires Linux installer root") {
				t.Fatalf("unprivileged or setuid operation passed the root gate: %v", err)
			}
		})
	}
}
