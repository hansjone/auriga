package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitGatePID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(file)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process did not record its PID in %s", file)
	return 0
}

func gateProcessRunning(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	return len(fields) > 0 && fields[0] != "Z"
}

func TestGateContextCancellationStopsDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `sleep 30 & printf '%s\n' "$!" >"$1"; wait`, "sh", marker)
	setGateProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGateProcessGroup(cmd, true) })
	child := waitGatePID(t, marker)
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	deadline := time.Now().Add(time.Second)
	for gateProcessRunning(child) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gateProcessRunning(child) {
		t.Fatal("cancelled gate left its child running")
	}
}

func TestIsolatedGateInterruptCleansStartup(t *testing.T) {
	exe := os.Getenv("AURIGA_OFFLINE_GATE_EXE")
	if exe == "" {
		t.Skip("set the locally built CLI to exercise interrupted gate startup")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	scratch := filepath.Join(root, "scratch")
	source := filepath.Join(root, "source")
	for _, directory := range []string{bin, scratch, source} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	daemonFile := filepath.Join(root, "daemon.pid")
	probeFile := filepath.Join(root, "probe.pid")
	for name, variable := range map[string]string{"dockerd": "AURIGA_GATE_FIXTURE_DAEMON", "docker": "AURIGA_GATE_FIXTURE_PROBE"} {
		program := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$$\" >\"$%s\"\nexec sleep 30\n", variable)
		if err := os.WriteFile(filepath.Join(bin, name), []byte(program), 0755); err != nil {
			t.Fatal(err)
		}
	}
	key, _ := testSigningKeys(t)
	cmd := exec.Command("unshare", "--net", exe, "internal-offline-build", "--source", source, "--version", "1.0", "--out", filepath.Join(root, "out.tar"), "--sign-key", key)
	cmd.Env = append(os.Environ(), "TMPDIR="+scratch, "PATH="+bin+":"+os.Getenv("PATH"), "AURIGA_GATE_FIXTURE_DAEMON="+daemonFile, "AURIGA_GATE_FIXTURE_PROBE="+probeFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		for _, file := range []string{daemonFile, probeFile} {
			raw, _ := os.ReadFile(file)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	daemon := waitGatePID(t, daemonFile)
	probe := waitGatePID(t, probeFile)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted gate succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted gate did not finish cleanup")
	}
	if gateProcessRunning(daemon) || gateProcessRunning(probe) {
		t.Fatal("interrupted gate left its daemon or readiness probe running")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("interrupted gate left temporary data: %v %v", entries, err)
	}
}
