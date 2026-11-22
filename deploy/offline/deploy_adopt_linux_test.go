//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adopt 与 deploy 等共用同一把 flock 站点锁：别的操作持锁时 adopt 直接失败且什么都不写。
func TestAdoptLockConflictRealFlock(t *testing.T) {
	stubSiteOwner(t, nil)
	if os.Geteuid() != 0 {
		t.Skip("trustedParent 需要 root 所有的祖先目录")
	}
	parent, err := os.MkdirTemp("/root", "sd2e-lock-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(parent)
	stubTrustedSource(t)
	data := filepath.Join(parent, "auriga-data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	deployNas := filepath.Join(parent, "bundle", "deploy-nas")
	putFile(t, filepath.Join(deployNas, "scripts/start.sh"), "#!/bin/sh\n", 0755)
	stubDeployRun(t, func(r runSpec) (string, error) {
		t.Fatalf("持锁冲突时不应执行任何命令：%v", r)
		return "", nil
	})
	run, _ := testRun()
	err = withLock(data, "deploy", func() error {
		e := adoptLocked(context.Background(), data, deployNas, map[string]string{}, false, run)
		if e == nil || !strings.Contains(e.Error(), "deploy") {
			t.Fatalf("持 deploy 锁时 adopt 应失败并说明正在进行的操作：%v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(data + ".oneclick"); !os.IsNotExist(err) {
		t.Fatal("锁冲突不应写 .oneclick")
	}
}
