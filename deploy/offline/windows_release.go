package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func compileWindowsCLI(single, source, target string) error {
	kit, err := readDevkit(single)
	if err != nil {
		return err
	}
	foundRunner := false
	for _, image := range kit.Images {
		if image.Role != "tool-runner" {
			continue
		}
		foundRunner = true
		if err := command(nil, os.Stdout, nil, "docker", "load", "-i", filepath.Join(single, "cache", image.File)); err != nil {
			return err
		}
		if err := prepareBuildImage(image.Tag, true); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "docker", "run", "--rm", "--pull", "never", "--network", "none", "--memory", "512m", "--cpus", "1", "--entrypoint", "sh", image.Tag, "-ec", "docker --version && docker compose version --short && docker buildx version && tar --help >/dev/null"); err != nil {
			return fmt.Errorf("当前Windows runner缺少Docker CLI/Compose/buildx/sh/tar: %w", err)
		}
	}
	if !foundRunner {
		return fmt.Errorf("devkit缺少Windows Docker Desktop的tool-runner")
	}
	for _, image := range kit.Images {
		if image.Role != "tool-go" {
			continue
		}
		if err := command(nil, os.Stdout, nil, "docker", "load", "-i", filepath.Join(single, "cache", image.File)); err != nil {
			return err
		}
		if err := prepareBuildImage(image.Tag, true); err != nil {
			return err
		}
		parent := filepath.Dir(target)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}
		args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64", "--memory", "4096m", "--memory-swap", "4096m", "--cpus", "2", "--mount", "type=bind,src=" + source + ",dst=/source,readonly", "--mount", "type=bind,src=" + parent + ",dst=/output", "--workdir", "/source/deploy/offline", "--env", "CGO_ENABLED=0", "--env", "GOOS=windows", "--env", "GOARCH=amd64", "--env", "GOTOOLCHAIN=local", "--env", "GOPROXY=off", "--env", "GOSUMDB=off", image.Tag, "go", "build", "-mod=readonly", "-buildvcs=false", "-o", "/output/" + filepath.Base(target), "."}
		return command(nil, os.Stdout, nil, "docker", args...)
	}
	return fmt.Errorf("devkit缺少Windows CLI交叉编译所需tool-go")
}
