package main

// 发行附件中的 Windows 薄壳 auriga.exe（设计 D13 / L8）。
//
// deploy/offline 只依赖 Go 标准库（无 go.sum），交叉编译不需要取任何模块：
// 固定 GOFLAGS=-mod=readonly、GOPROXY=off、GOWORK=off、GOTOOLCHAIN=local，
// 在 unshare --net / --network none 下同样可编。与 ./auriga 引导脚本一致：本机有 go
// 就直接用；没有 go 时用 AURIGA_GO_IMAGE（须为 @sha256 digest）在断网容器里编。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var windowsCLIEnv = []string{
	"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0",
	"GOFLAGS=-mod=readonly", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local",
}

// buildWindowsAurigaCLI 交叉编译 source（发行源码快照根）里的 deploy/offline 为 auriga.exe。
func buildWindowsAurigaCLI(ctx context.Context, source, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	dir := filepath.Join(source, "deploy", "offline")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("找不到 deploy/offline 源码目录 %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); err == nil {
		return errors.New("deploy/offline 出现 go.sum：发行 CLI 必须只依赖标准库，才能在断网环境交叉编译")
	}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("go"); err == nil {
		cmd = exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", out, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), windowsCLIEnv...)
	} else {
		image := os.Getenv("AURIGA_GO_IMAGE")
		if !strings.Contains(image, "@sha256:") {
			return errors.New("交叉编译 auriga.exe 需要 Go 1.27.3，或设置 AURIGA_GO_IMAGE 为已批准的 Go 工具镜像 digest")
		}
		outDir, err := filepath.Abs(filepath.Dir(out))
		if err != nil {
			return err
		}
		src, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		args := []string{"run", "--rm", "--platform", "linux/amd64", "--network", "none",
			"--mount", "type=bind,src=" + src + ",dst=/src,readonly",
			"--mount", "type=bind,src=" + outDir + ",dst=/out", "-w", "/src",
			"-e", "GOCACHE=/tmp/go-build", "-e", "GOMODCACHE=/tmp/go-mod"}
		for _, kv := range windowsCLIEnv {
			args = append(args, "-e", kv)
		}
		args = append(args, image, "go", "build", "-trimpath", "-buildvcs=false", "-o", "/out/"+filepath.Base(out), ".")
		cmd = exec.CommandContext(ctx, "docker", args...)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build GOOS=windows：%w", err)
	}
	return nil
}
