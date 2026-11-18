package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A separate test process reads its mount table through an inherited host proc
// directory, even after the isolated PID namespace's init has exited.
func TestGateMountInspectorProcess(t *testing.T) {
	if os.Getenv("AURIGA_MOUNT_INSPECTOR") != "1" {
		return
	}
	fd, err := syscall.Openat(4, "self/mountinfo", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "mountinfo")
	defer file.Close()
	if _, err := io.Copy(os.Stdout, file); err != nil {
		t.Fatal(err)
	}
}

func TestGateForcedCancellationWaitsForNamespace(t *testing.T) {
	exe := gateFixtureCLI(t)
	root := t.TempDir()
	key, public := testSigningKeys(t)
	source := filepath.Join(root, "source")
	bin := filepath.Join(root, "bin")
	scratch := filepath.Join(root, "scratch")
	for _, dir := range []string{source, bin, scratch} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyFile(public, filepath.Join(source, "release-public.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	kit := filepath.Join(root, "kit")
	if _, err := composeDevkit("", fixtureSingleKit(t, "1.0", "a"), "1.0", strings.Repeat("a", 40), kit, key, 1<<30, true); err != nil {
		t.Fatal(err)
	}
	writeGateProgram(t, filepath.Join(bin, "dockerd"), "exec sleep 120\n")
	writeGateProgram(t, filepath.Join(bin, "docker"), `mkdir -p "$TMPDIR/forced-mount"
mount -t tmpfs -o size=1m gate-forced-fixture "$TMPDIR/forced-mount"
touch "$GATE_FORCE_MARKER"
sleep 120 &
wait
`)
	marker := filepath.Join(root, "ready")
	t.Setenv("GATE_FORCE_MARKER", marker)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TMPDIR", scratch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		proof, err := offlineBuildGate(gateRequest{Context: ctx, Source: source, Kit: kit, Out: filepath.Join(root, "out.tar"), Tag: "1.0", Key: key, Executable: exe})
		if proof.Passed {
			err = errors.New("forced cancellation passed the gate")
		}
		done <- err
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("gate exited before forced fixture: %v", err)
		case <-deadline:
			t.Fatal("forced fixture did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	initPID := 0
	for _, process := range processes {
		args, _ := os.ReadFile(filepath.Join("/proc", process.Name(), "cmdline"))
		if strings.Split(string(args), "\x00")[0] == exe && strings.Contains(string(args), source) {
			initPID, _ = strconv.Atoi(process.Name())
			break
		}
	}
	if initPID == 0 {
		t.Fatal("isolated init not found")
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", initPID))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") && !strings.HasSuffix(line, "\t1") {
			t.Fatalf("gate is not namespace PID 1: %s", line)
		}
		if strings.HasPrefix(line, "PPid:") && strings.TrimSpace(strings.TrimPrefix(line, "PPid:")) != strconv.Itoa(os.Getpid()) {
			t.Fatalf("gate init is not the supervisor's direct child: %s", line)
		}
	}
	mountNS, err := os.Open(fmt.Sprintf("/proc/%d/ns/mnt", initPID))
	if err != nil {
		t.Fatal(err)
	}
	defer mountNS.Close()
	proc, err := os.Open("/proc")
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	namespace, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", initPID))
	if err != nil {
		t.Fatal(err)
	}
	// Stop PID 1 so it cannot run cooperative cleanup. The outer force deadline
	// must kill it, await namespace teardown, then remove this held mount.
	if err := syscall.Kill(initPID, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(initPID, syscall.SIGKILL) })
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("forced cancellation lost its cause: %v", err)
		}
	case <-time.After(70 * time.Second):
		t.Fatal("forced supervisor did not return")
	}
	if time.Since(started) < 40*time.Second {
		t.Error("fixture did not reach the production force deadline")
	}
	processes, err = os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		pid, _ := strconv.Atoi(process.Name())
		owned, _ := os.Readlink(filepath.Join("/proc", process.Name(), "ns/pid"))
		if owned == namespace && gateProcessRunning(pid) {
			t.Errorf("forced cancellation returned before process %d exited", pid)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	inspect := exec.Command("nsenter", "--mount=/proc/self/fd/3", "--", self, "-test.run=^TestGateMountInspectorProcess$")
	inspect.ExtraFiles = []*os.File{mountNS, proc}
	inspect.Env = append(os.Environ(), "AURIGA_MOUNT_INSPECTOR=1")
	raw, err := inspect.CombinedOutput()
	if err != nil || bytes.Contains(raw, []byte("gate-forced-fixture")) {
		t.Errorf("forced cancellation left its held namespace mount: %v\n%s", err, raw)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Errorf("forced cancellation left temporary directories: %v %v", entries, err)
	}
}

func TestGateUnconfirmedCleanupRetainsDiagnostics(t *testing.T) {
	gateFixtureCLI(t)
	workspace, err := newGateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(workspace.root)
		_ = os.RemoveAll(workspace.runtime)
	})
	bin := t.TempDir()
	writeGateProgram(t, filepath.Join(bin, "nsenter"), "exit 23\n")
	child := filepath.Join(bin, "gate")
	writeGateProgram(t, child, `touch "$GATE_RETAIN_ROOT/supervisor-ready"
while ! test -f "$GATE_RETAIN_ROOT/supervisor-ack"; do sleep .01; done
`)
	if err := os.WriteFile(filepath.Join(workspace.root, "daemon.log"), []byte("preserved daemon diagnostic"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "--net", "--mount", "--propagation", "private", "--mount-proc", child)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "GATE_RETAIN_ROOT="+workspace.root)
	err = runGateProcess(ctx, cmd, workspace, child)
	if err == nil || !strings.Contains(err.Error(), "preserved daemon diagnostic") || !strings.Contains(err.Error(), workspace.root) || !strings.Contains(err.Error(), workspace.runtime) {
		t.Fatalf("missing cleanup diagnostics: %v", err)
	}
	if !workspace.retained {
		t.Fatal("unconfirmed cleanup was not retained")
	}
	if err := workspace.close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{workspace.root, workspace.runtime} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("unconfirmed resource was released: %s %v", path, err)
		}
	}
}
