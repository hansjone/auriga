package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deploy/offline 必须只依赖标准库：发行在 unshare --net 下交叉编译 auriga.exe，
// ./auriga 引导也可能在 --network none 容器里编译，任何外部模块都会取不到。
func TestWindowsCLICrossCompilesWithoutModules(t *testing.T) {
	if _, err := os.Stat("go.sum"); err == nil {
		t.Fatal("deploy/offline 不得出现 go.sum（不得引入外部模块）")
	}
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "require") {
		t.Fatalf("deploy/offline/go.mod 不得 require 外部模块：\n%s", raw)
	}
	if testing.Short() {
		t.Skip("-short 跳过真实交叉编译")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("没有 go 工具链")
	}
	out := filepath.Join(t.TempDir(), "auriga.exe")
	// GOPROXY=off 等由 buildWindowsAurigaCLI 固定；源码根为仓库根。
	if err := buildWindowsAurigaCLI(context.Background(), filepath.Join("..", ".."), out); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("未产出 auriga.exe：%v", err)
	}
}

func TestWindowsCLIRejectsGoSum(t *testing.T) {
	src := t.TempDir()
	dir := filepath.Join(src, "deploy", "offline")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := buildWindowsAurigaCLI(context.Background(), src, filepath.Join(t.TempDir(), "auriga.exe"))
	if err == nil || !strings.Contains(err.Error(), "go.sum") {
		t.Fatalf("出现 go.sum 应拒绝：%v", err)
	}
}
