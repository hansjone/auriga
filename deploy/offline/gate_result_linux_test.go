package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestGateDigestCancellation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "digest-stream")
	if err := syscall.Mkfifo(file, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := digestContext(ctx, file); done <- err }()
	writer, err := os.OpenFile(file, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("digest lost cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("digest did not stop until input was closed")
	}
}

func TestGateSupervisorCancellationReapsNamespace(t *testing.T) {
	exe := gateFixtureCLI(t)
	root := t.TempDir()
	key, public := testSigningKeys(t)
	source := filepath.Join(root, "source")
	bin := filepath.Join(root, "bin")
	scratch := filepath.Join(root, "scratch")
	for _, path := range []string{source, bin, scratch} {
		if err := os.Mkdir(path, 0700); err != nil {
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
	writeGateProgram(t, filepath.Join(bin, "docker"), "touch \"$GATE_SUPERVISOR_MARKER\"\nexec sleep 120\n")
	marker := filepath.Join(root, "ready")
	t.Setenv("GATE_SUPERVISOR_MARKER", marker)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TMPDIR", scratch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		proof, err := offlineBuildGate(gateRequest{Context: ctx, Source: source, Kit: kit, Out: filepath.Join(root, "out.tar"), Tag: "1.0", Key: key, Executable: exe})
		if proof.Passed {
			err = errors.New("cancelled supervisor recorded a passing gate")
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
			t.Fatalf("supervisor exited before probe: %v", err)
		case <-deadline:
			t.Fatal("supervisor did not reach readiness")
		case <-time.After(10 * time.Millisecond):
		}
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	namespace := ""
	for _, process := range processes {
		args, _ := os.ReadFile(filepath.Join("/proc", process.Name(), "cmdline"))
		if strings.Split(string(args), "\x00")[0] == exe && strings.Contains(string(args), source) && strings.Contains(string(args), "internal-offline-build") {
			namespace, _ = os.Readlink(filepath.Join("/proc", process.Name(), "ns/pid"))
			break
		}
	}
	if namespace == "" {
		t.Fatal("isolated PID namespace not found")
	}
	current, err := os.Readlink("/proc/self/ns/pid")
	if err != nil || namespace == current {
		t.Fatal("gate shares supervisor PID namespace", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("supervisor lost cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop its namespace")
	}
	processes, err = os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		pid, _ := strconv.Atoi(process.Name())
		owned, _ := os.Readlink(filepath.Join("/proc", process.Name(), "ns/pid"))
		if owned == namespace && gateProcessRunning(pid) {
			t.Errorf("active process left in gate namespace: %d", pid)
		}
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("supervisor left temporary resources: %v %v", entries, err)
	}
}

func TestGateCancellationDuringResultExtraction(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run as root for the gate supervisor's private /run workspace")
	}
	for _, interrupted := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(interrupted.String(), func(t *testing.T) {
			root := t.TempDir()
			key, public := testSigningKeys(t)
			source := t.TempDir()
			if err := copyFile(public, filepath.Join(source, "release-public.pem"), 0644); err != nil {
				t.Fatal(err)
			}
			kit := filepath.Join(root, "kit")
			if _, err := composeDevkit("", fixtureSingleKit(t, "1.0", "a"), "1.0", strings.Repeat("a", 40), kit, key, 1<<30, true); err != nil {
				t.Fatal(err)
			}
			payload := t.TempDir()
			if err := os.WriteFile(filepath.Join(payload, "payload"), []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := writeSums(payload); err != nil {
				t.Fatal(err)
			}
			if err := signSums(payload, key); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(root, "result.tar")
			if err := pack(payload, archive); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(root, "bin")
			scratch := filepath.Join(root, "scratch")
			for _, directory := range []string{bin, scratch} {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			out := filepath.Join(root, "gate.tar")
			writeGateProgram(t, filepath.Join(bin, "unshare"), "mkfifo \"$GATE_RESULT_FIFO\"\n")
			t.Setenv("GATE_RESULT_FIFO", out)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			t.Setenv("TMPDIR", scratch)
			observed := make(chan os.Signal, 1)
			signal.Notify(observed, interrupted)
			defer signal.Stop(observed)
			type outcome struct {
				proof buildGate
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				proof, err := offlineBuildGate(gateRequest{Source: source, Kit: kit, Out: out, Tag: "1.0", Commit: strings.Repeat("a", 40), Key: key, Executable: "unused-fixture"})
				done <- outcome{proof, err}
			}()
			deadline := time.After(5 * time.Second)
			for {
				if _, err := os.Stat(out); err == nil {
					break
				}
				select {
				case result := <-done:
					t.Fatalf("gate failed before child output: %v", result.err)
				case <-deadline:
					t.Fatal("build child did not finish")
				case <-time.After(10 * time.Millisecond):
				}
			}
			started := make(chan error, 1)
			release := make(chan struct{})
			releaseWriter := sync.OnceFunc(func() { close(release) })
			defer releaseWriter()
			go func() {
				file, err := os.OpenFile(out, os.O_WRONLY, 0600)
				if err != nil {
					started <- err
					return
				}
				_, err = file.Write(raw[:512])
				started <- err
				<-release
				_ = file.Close()
			}()
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(os.Getpid(), interrupted.(syscall.Signal)); err != nil {
				t.Fatal(err)
			}
			<-observed
			var result outcome
			select {
			case result = <-done:
			case <-time.After(2 * time.Second):
				t.Error("result extraction ignored cancellation after build success")
				releaseWriter()
				result = <-done
			}
			if result.proof.Passed || !errors.Is(result.err, context.Canceled) {
				t.Fatalf("cancelled validation proof=%+v error=%v", result.proof, result.err)
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatalf("validation left scratch data %v %v", entries, err)
			}
		})
	}
}
