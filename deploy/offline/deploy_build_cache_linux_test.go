//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGoBuildCachePermissionsLinux(t *testing.T) {
	stubSiteOwner(t, nil)
	cache := filepath.Join(t.TempDir(), "cache")
	if _, err := ensureDepsCache(cache, true); err != nil {
		t.Fatal(err)
	}
	hexA := strings.Repeat("a", 64)
	dir, err := ensureGoBuildCache(cache, hexA)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cache, "build", "go", hexA)
	if dir != want {
		t.Fatalf("路径：got %s want %s", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("权限应为 0700，得到 %04o", info.Mode().Perm())
	}
	for _, parent := range []string{filepath.Join(cache, "build"), filepath.Join(cache, "build", "go")} {
		pi, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if pi.Mode().Perm() != 0700 {
			t.Fatalf("%s 权限应为 0700，得到 %04o", parent, pi.Mode().Perm())
		}
	}
	hexB := strings.Repeat("b", 64)
	dirB, err := ensureGoBuildCache(cache, hexB)
	if err != nil {
		t.Fatal(err)
	}
	if dirB == dir {
		t.Fatal("不同工具镜像 digest 应分目录")
	}
	if _, err := ensureGoBuildCache(cache, "short"); err == nil {
		t.Fatal("非 64 hex 应拒绝")
	}

	linkName := strings.Repeat("c", 64)
	link := filepath.Join(cache, "build", "go", linkName)
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureGoBuildCache(cache, linkName); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("符号链接缓存应拒绝：%v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	// 组可写叶子：Mkdir 受 umask 影响，须 Chmod 后才能测权限拒绝。
	// ensureGoBuildCache 必须直接拒绝组/其他可写目录，绝不静默 chmod 掩盖篡改窗口。
	hexD := strings.Repeat("d", 64)
	leaf := filepath.Join(cache, "build", "go", hexD)
	if err := os.Mkdir(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaf, 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureGoBuildCache(cache, hexD); err == nil || !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("组可写目录应由 ensureGoBuildCache 直接拒绝：%v", err)
	}
	// root 所有且组/其他不可写（0755）时允许收紧到 0700 后通过。
	if err := os.Chmod(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureGoBuildCache(cache, hexD); err != nil {
		t.Fatalf("0755 应安全收紧为 0700 后成功：%v", err)
	}
	if info, err := os.Stat(leaf); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("收紧后应为 0700：%v", err)
	}

	if os.Geteuid() != 0 {
		siteOwnerOf = fileOwnerUID
		hexE := strings.Repeat("e", 64)
		if _, err := ensureGoBuildCache(cache, hexE); err == nil || !strings.Contains(err.Error(), "root 所有") {
			t.Fatalf("非 root 属主应拒绝：%v", err)
		}
	}

	stubSiteOwner(t, errors.New("父目录可写"))
	if _, err := ensureGoBuildCache(cache, strings.Repeat("f", 64)); err == nil || !strings.Contains(err.Error(), "父目录可写") {
		t.Fatalf("祖先不受信应拒绝：%v", err)
	}
}
