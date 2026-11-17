package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const gateNetwork = "unshare-net: isolated dockerd; containers --network none; --pull never"
const gateBuildTimeout = 2 * time.Hour
const gateReadinessTimeout = 30 * time.Second
const gateProbeTimeout = 2 * time.Second

func gateDockerEnv(socket string) []string {
	env := []string{}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		switch name {
		case "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "BUILDX_BUILDER":
			continue
		}
		env = append(env, value)
	}
	return append(env, "DOCKER_HOST=unix://"+socket, "BUILDX_GIT_INFO=false", "BUILDX_GIT_LABELS=false")
}

// Invoked only inside unshare --net. The dedicated daemon starts empty, so
// neither the host's online registry access nor its image cache can mask a
// missing dependency. No existing Docker service or network is modified.
func isolatedGateBuild(args []string) (result error) {
	f := flags("internal-offline-build")
	source := f.String("source", "", "")
	out := f.String("out", "", "")
	version := f.String("version", "", "")
	key := f.String("sign-key", "", "")
	workdir := f.String("workdir", "", "")
	runtimeDir := f.String("runtime-dir", "", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *source == "" || *out == "" || *version == "" || *key == "" {
		return errors.New("断网门禁参数无效")
	}
	if os.Geteuid() != 0 {
		return errors.New("断网发行门禁需要本机root运行临时独立Docker引擎")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, device := range interfaces {
		if device.Name != "lo" {
			return errors.New("门禁必须位于仅有loopback的独立断网命名空间")
		}
	}
	workspace := &gateWorkspace{root: *workdir, runtime: *runtimeDir}
	if *workdir == "" && *runtimeDir == "" {
		workspace, err = newGateWorkspace()
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, workspace.close()) }()
	} else if *workdir == "" || *runtimeDir == "" {
		return errors.New("门禁工作目录和运行目录必须一起指定")
	}
	root, runtimeRoot := workspace.root, workspace.runtime
	for _, path := range []string{root, runtimeRoot} {
		if err := noSymlinks(path); err != nil {
			return err
		}
	}
	buildTemp := filepath.Join(root, "build-tmp")
	if err := os.Mkdir(buildTemp, 0700); err != nil {
		return err
	}
	config := filepath.Join(root, "daemon.json")
	if err := os.WriteFile(config, []byte("{}\n"), 0600); err != nil {
		return err
	}
	logPath := filepath.Join(root, "daemon.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, gateBuildTimeout)
	defer cancel()
	socket := filepath.Join(runtimeRoot, "docker.sock")
	env := append(gateDockerEnv(socket), "TMPDIR="+buildTemp)
	// Keep the daemon alive until task containers have been stopped, including
	// when the build context has already been cancelled.
	daemon := exec.CommandContext(context.Background(), "dockerd", "--config-file", config, "--host", "unix://"+socket, "--data-root", filepath.Join(root, "data"), "--exec-root", filepath.Join(runtimeRoot, "exec"), "--pidfile", filepath.Join(runtimeRoot, "pid"), "--bridge=none", "--iptables=false", "--ip6tables=false", "--ip-forward=false", "--ip-masq=false", "--userland-proxy=false", "--storage-driver=vfs", "--log-level=error")
	daemon.Stdout = log
	daemon.Stderr = log
	daemon.Env = env
	setGateProcessGroup(daemon)
	if err := daemon.Start(); err != nil {
		return fmt.Errorf("门禁需要本机dockerd: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = daemon.Wait(); close(done) }()
	ready := false
	defer func() {
		if ready {
			result = errors.Join(result, removeGateContainers(env))
		}
		stopGateProcessGroup(daemon, false)
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			stopGateProcessGroup(daemon, true)
			<-done
		}
		stopGateProcessGroup(daemon, true)
		result = errors.Join(result, unmountGatePaths(root, runtimeRoot))
	}()
	if err := waitGateEngine(ctx, env, daemon, done, logPath); err != nil {
		return err
	}
	ready = true
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	build := exec.CommandContext(ctx, exe, "bundle", "--build", "--offline", "--version", *version, "--out", *out, "--sign-key", *key)
	build.Dir = *source
	build.Env = env
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	setGateProcessGroup(build)
	err = build.Run()
	if build.Process != nil {
		stopGateProcessGroup(build, true)
	}
	return err
}

func waitGateEngine(ctx context.Context, env []string, daemon *exec.Cmd, stopped <-chan struct{}, logPath string) error {
	ready, cancel := context.WithTimeout(ctx, gateReadinessTimeout)
	defer cancel()
	var lastErr error
	diagnostic := func(cause error) error {
		raw, _ := smallFile(logPath, 4<<20)
		return fmt.Errorf("独立Docker引擎未就绪（总预算%s，单次探针%s）: %w\n最后探针: %v\n%s", gateReadinessTimeout, gateProbeTimeout, cause, lastErr, raw)
	}
	for {
		probeCtx, stop := context.WithTimeout(ready, gateProbeTimeout)
		probe := exec.CommandContext(probeCtx, "docker", "info", "--format", "{{.ServerVersion}}")
		probe.Env = env
		setGateProcessGroup(probe)
		probe.WaitDelay = time.Second
		probeDone := make(chan error, 1)
		go func() { probeDone <- probe.Run() }()
		var startupErr error
		select {
		case <-stopped:
			stop()
			lastErr = <-probeDone
			startupErr = fmt.Errorf("daemon提前退出: %v", daemon.ProcessState)
		case <-ready.Done():
			stop()
			lastErr = <-probeDone
			startupErr = ready.Err()
		case lastErr = <-probeDone:
		}
		stop()
		if probe.Process != nil {
			stopGateProcessGroup(probe, true)
		}
		if startupErr != nil {
			return diagnostic(startupErr)
		}
		select {
		case <-stopped:
			return diagnostic(fmt.Errorf("daemon提前退出: %v", daemon.ProcessState))
		case <-ready.Done():
			return diagnostic(ready.Err())
		default:
		}
		if lastErr == nil {
			return nil
		}
		select {
		case <-ready.Done():
			return diagnostic(ready.Err())
		case <-stopped:
			return diagnostic(fmt.Errorf("daemon提前退出: %v", daemon.ProcessState))
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func removeGateContainers(env []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list := exec.CommandContext(ctx, "docker", "ps", "--all", "--quiet", "--no-trunc")
	list.Env = env
	setGateProcessGroup(list)
	raw, err := list.Output()
	if err != nil {
		return fmt.Errorf("列出门禁容器失败: %w", err)
	}
	ids := strings.Fields(string(raw))
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if !hex64.MatchString(id) {
			return errors.New("门禁容器ID无效")
		}
	}
	remove := exec.CommandContext(ctx, "docker", append([]string{"rm", "--force", "--volumes"}, ids...)...)
	remove.Env = env
	setGateProcessGroup(remove)
	if err := remove.Run(); err != nil {
		return fmt.Errorf("停止门禁容器失败: %w", err)
	}
	return nil
}
