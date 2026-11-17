package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gateFixtureCLI(t *testing.T) string {
	t.Helper()
	exe := os.Getenv("AURIGA_OFFLINE_GATE_EXE")
	if exe == "" {
		t.Skip("set AURIGA_OFFLINE_GATE_EXE for short isolated CLI fixtures")
	}
	return exe
}

func writeGateProgram(t *testing.T, path, program string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+program), 0755); err != nil {
		t.Fatal(err)
	}
}

func startGateFixture(t *testing.T, exe, root, source, key string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	for _, directory := range []string{"bin", "scratch"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("unshare", "--net", "--mount", "--propagation", "private", exe, "internal-offline-build", "--source", source, "--version", "1.0", "--out", filepath.Join(root, "out.tar"), "--sign-key", key)
	cmd.Env = append(os.Environ(), "TMPDIR="+filepath.Join(root, "scratch"), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "GATE_FIXTURE_ROOT="+root)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		for _, name := range []string{"daemon.pid", "probe.pid", "probe-pids", "build.pid", "child.pid"} {
			raw, _ := os.ReadFile(filepath.Join(root, name))
			for _, value := range strings.Fields(string(raw)) {
				var pid int
				_, _ = fmt.Sscanf(value, "%d", &pid)
				if pid > 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
			t.Log(output.String())
		}
	})
	return cmd, &output
}

func waitGateFixture(t *testing.T, cmd *exec.Cmd, output *bytes.Buffer, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("gate exceeded %s\n%s", limit, output.String())
		return nil
	}
}

func TestGateReadinessHangAndDaemonExit(t *testing.T) {
	exe := gateFixtureCLI(t)
	for _, mode := range []string{"hang", "daemon-exit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			daemon := "printf '%s\\n' \"$$\" >\"$GATE_FIXTURE_ROOT/daemon.pid\"\nexec sleep 120\n"
			if mode == "daemon-exit" {
				daemon = "printf '%s\\n' \"$$\" >\"$GATE_FIXTURE_ROOT/daemon.pid\"\nwhile ! test -f \"$GATE_FIXTURE_ROOT/probe.pid\"; do sleep .02; done\nprintf 'fixture daemon stopped\\n' >&2\nexit 17\n"
			}
			writeGateProgram(t, filepath.Join(root, "bin/dockerd"), daemon)
			writeGateProgram(t, filepath.Join(root, "bin/docker"), "printf '%s\\n' \"$$\" >\"$GATE_FIXTURE_ROOT/probe.pid\"\nprintf '%s\\n' \"$$\" >>\"$GATE_FIXTURE_ROOT/probe-pids\"\nexec sleep 120\n")
			key, _ := testSigningKeys(t)
			cmd, output := startGateFixture(t, exe, root, root, key)
			probe := waitGatePID(t, filepath.Join(root, "probe.pid"))
			daemonPID := waitGatePID(t, filepath.Join(root, "daemon.pid"))
			limit := 35 * time.Second
			if mode == "daemon-exit" {
				limit = 5 * time.Second
			}
			if err := waitGateFixture(t, cmd, output, limit); err == nil {
				t.Fatal("failed readiness succeeded")
			}
			if mode == "daemon-exit" && !strings.Contains(output.String(), "fixture daemon stopped") {
				t.Fatal("daemon startup diagnostics lost", output.String())
			}
			if gateProcessRunning(probe) {
				t.Fatal("readiness left its probe running")
			}
			if gateProcessRunning(daemonPID) {
				t.Error("failed readiness left its daemon running")
			}
			raw, err := os.ReadFile(filepath.Join(root, "probe-pids"))
			if err != nil {
				t.Fatal(err)
			}
			pids := strings.Fields(string(raw))
			if mode == "hang" && len(pids) < 2 {
				t.Fatal("single-probe timeout did not retry")
			}
			for _, value := range pids {
				var pid int
				if _, err := fmt.Sscanf(value, "%d", &pid); err != nil || gateProcessRunning(pid) {
					t.Fatalf("probe not reaped: %s %v", value, err)
				}
			}
		})
	}
}

// Exercise the actual bundle --build entry point with signed tiny inputs. The
// language-tool Docker invocation blocks instead of compiling application code.
func gateBuildInputs(t *testing.T) (string, string) {
	t.Helper()
	kitRoot := fixtureSingleKit(t, "1.0", "a")
	kit, err := readDevkit(kitRoot)
	if err != nil {
		t.Fatal(err)
	}
	key, public := testSigningKeys(t)
	source := filepath.Join(kitRoot, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(public, filepath.Join(source, "release-public.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "deploy/nas"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "deploy/nas/fixture"), []byte("fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}, {"add", "."}, {"commit", "-m", "fixture"}, {"tag", "1.0"}} {
		if _, err := gitAt(source, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, err := gitAt(source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	kit.Source = &provenance{Commit: commit, Version: "1.0", UpstreamVersion: "1.0"}
	kit.Locks = nil
	for name, target := range map[string]*artifact{"nas": &kit.Release.NAS, "inputs": &kit.Release.Build.Inputs, "recipes": &kit.Release.Build.Recipes} {
		payload := t.TempDir()
		if name == "nas" {
			bin := filepath.Join(payload, "deploy-nas/bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			writeGateProgram(t, filepath.Join(bin, "auriga-nas-bootstrap"), "if test \"$1\" = image-extra-resolve; then printf 'sha256:"+strings.Repeat("0", 64)+"\\n'; fi\n")
			if err := writeSums(payload); err != nil {
				t.Fatal(err)
			}
		}
		if name == "recipes" {
			if err := os.MkdirAll(filepath.Join(payload, "deploy/nas/offline-build"), 0755); err != nil {
				t.Fatal(err)
			}
			writeGateProgram(t, filepath.Join(payload, "deploy/nas/offline-build/go-inputs.sh"), "exit 0\n")
		}
		file := filepath.Join(kitRoot, "cache", target.URL)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := pack(payload, file); err != nil {
			t.Fatal(err)
		}
		target.SHA256, err = digest(file)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := jsonFile(filepath.Join(kitRoot, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	signTestRoot(t, kitRoot, key)
	return source, key
}

func TestGateCancellationDuringBundleBuild(t *testing.T) {
	exe := gateFixtureCLI(t)
	for _, interrupted := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(interrupted.String(), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			source, key := gateBuildInputs(t)
			container := ""
			if image := os.Getenv("AURIGA_GATE_TEST_CONTAINER_IMAGE"); image != "" {
				create := exec.Command("/usr/bin/docker", "--host=unix:///var/run/docker.sock", "run", "--pull=never", "--detach", "--rm", "--network=none", "--memory=32m", "--cpus=.25", "--label=auriga-test=pr388", image, "sleep", "120")
				create.Env = gateDockerEnv("/var/run/docker.sock")
				raw, err := create.CombinedOutput()
				if err != nil {
					t.Fatalf("create short local container: %v %s", err, raw)
				}
				container = strings.TrimSpace(string(raw))
				if !hex64.MatchString(container) {
					t.Fatal("invalid fixture container ID", container)
				}
				t.Cleanup(func() {
					_ = exec.Command("/usr/bin/docker", "--host=unix:///var/run/docker.sock", "rm", "--force", container).Run()
				})
				if err := os.WriteFile(filepath.Join(root, "container.id"), []byte(container), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeGateProgram(t, filepath.Join(root, "bin/dockerd"), "printf '%s\\n' \"$$\" >\"$GATE_FIXTURE_ROOT/daemon.pid\"\nexec sleep 120\n")
			writeGateProgram(t, filepath.Join(root, "bin/docker"), `case "$1" in
info)
  if test "$3" = '{{json .}}'; then
    printf '{"MemTotal":17179869184,"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true}\n'
  else printf '29.0\n'; fi ;;
compose) printf '2.29.2\n' ;;
image) printf 'linux/amd64\n' ;;
load) exit 0 ;;
ps)
  if test -f "$GATE_FIXTURE_ROOT/container.id" && ! test -f "$GATE_FIXTURE_ROOT/container-removed"; then cat "$GATE_FIXTURE_ROOT/container.id"; fi ;;
rm)
  id=$(cat "$GATE_FIXTURE_ROOT/container.id")
  /usr/bin/docker --host=unix:///var/run/docker.sock rm --force "$id"
  touch "$GATE_FIXTURE_ROOT/container-removed" ;;
run)
  mkdir -p "$TMPDIR/fixture-mount"
  mount -t tmpfs -o size=1m gate-fixture "$TMPDIR/fixture-mount"
  printf '%s\n' "$$" >"$GATE_FIXTURE_ROOT/build.pid"
  sleep 120 & printf '%s\n' "$!" >"$GATE_FIXTURE_ROOT/child.pid"
  wait ;;
*) exit 1 ;;
esac
`)
			cmd, output := startGateFixture(t, exe, root, source, key)
			build := waitGatePID(t, filepath.Join(root, "build.pid"))
			child := waitGatePID(t, filepath.Join(root, "child.pid"))
			daemonPID := waitGatePID(t, filepath.Join(root, "daemon.pid"))
			mountNS, err := os.Open(fmt.Sprintf("/proc/%d/ns/mnt", cmd.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer mountNS.Close()
			if err := cmd.Process.Signal(interrupted); err != nil {
				t.Fatal(err)
			}
			if err := waitGateFixture(t, cmd, output, 8*time.Second); err == nil {
				t.Fatal("cancelled build succeeded")
			}
			if gateProcessRunning(build) || gateProcessRunning(child) {
				t.Error("cancelled build left descendants")
			}
			if gateProcessRunning(daemonPID) {
				t.Error("cancelled build left its daemon running")
			}
			if container != "" {
				check := exec.Command("/usr/bin/docker", "--host=unix:///var/run/docker.sock", "ps", "--all", "--quiet", "--no-trunc", "--filter", "id="+container)
				check.Env = gateDockerEnv("/var/run/docker.sock")
				raw, err := check.CombinedOutput()
				if err != nil || len(bytes.TrimSpace(raw)) != 0 {
					t.Errorf("gate left its task container: %v %s", err, raw)
				}
			}
			entries, err := os.ReadDir(filepath.Join(root, "scratch"))
			if err != nil || len(entries) != 0 {
				t.Errorf("cancelled bundle left temporary directories: %v %v\n%s", entries, err, output.String())
			}
			probe := exec.Command("nsenter", "--mount=/proc/self/fd/3", "--", "cat", "/proc/self/mountinfo")
			probe.ExtraFiles = []*os.File{mountNS}
			raw, err := probe.CombinedOutput()
			if err != nil || bytes.Contains(raw, []byte(root+"/scratch/")) {
				t.Errorf("cancelled build left mounts: %v\n%s", err, raw)
			}
		})
	}
}
