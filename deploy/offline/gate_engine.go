package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const gateNetwork = "unshare-net: isolated dockerd; containers --network none; --pull never"

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
func isolatedGateBuild(args []string) error {
	f := flags("internal-offline-build")
	source := f.String("source", "", "")
	out := f.String("out", "", "")
	version := f.String("version", "", "")
	key := f.String("sign-key", "", "")
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
	root, err := os.MkdirTemp("", "auriga-gate-engine-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	// Unix socket paths are limited to roughly 104 bytes. Keep runtime sockets
	// separate from TMPDIR, which may point deep into a large data disk.
	runtimeRoot, err := os.MkdirTemp("/run", "auriga-gate-")
	if err != nil {
		return fmt.Errorf("创建临时Docker运行目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(runtimeRoot) }()
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	socket := filepath.Join(runtimeRoot, "docker.sock")
	daemon := exec.CommandContext(ctx, "dockerd", "--config-file", config, "--host", "unix://"+socket, "--data-root", filepath.Join(root, "data"), "--exec-root", filepath.Join(runtimeRoot, "exec"), "--pidfile", filepath.Join(runtimeRoot, "pid"), "--bridge=none", "--iptables=false", "--ip6tables=false", "--ip-forward=false", "--ip-masq=false", "--userland-proxy=false", "--storage-driver=vfs", "--log-level=error")
	daemon.Stdout = log
	daemon.Stderr = log
	setGateProcessGroup(daemon)
	if err := daemon.Start(); err != nil {
		return fmt.Errorf("门禁需要本机dockerd: %w", err)
	}
	type daemonExit struct{ err error }
	done := make(chan daemonExit, 1)
	go func() { done <- daemonExit{err: daemon.Wait()} }()
	defer func() {
		stopGateProcessGroup(daemon, false)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			stopGateProcessGroup(daemon, true)
			<-done
		}
		stopGateProcessGroup(daemon, true)
	}()
	env := gateDockerEnv(socket)
	ready := false
	for attempt := 0; attempt < 150; attempt++ {
		probe := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
		probe.Env = env
		if err := probe.Run(); err == nil {
			ready = true
			break
		}
		select {
		case result := <-done:
			done <- result
			raw, _ := smallFile(logPath, 4<<20)
			return fmt.Errorf("独立Docker引擎启动失败: %v\n%s", result.err, raw)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !ready {
		return errors.New("独立Docker引擎30秒未就绪")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	build := exec.CommandContext(ctx, exe, "bundle", "--build", "--offline", "--version", *version, "--out", *out, "--sign-key", *key)
	build.Dir = *source
	build.Env = env
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	return build.Run()
}
