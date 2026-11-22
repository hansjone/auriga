package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRequirementsFullyHashed(t *testing.T) {
	ok, err := requirementsFullyHashed([]byte("pkg==1.0 --hash=sha256:" + strings.Repeat("a", 64) + "\n"))
	if err != nil || !ok {
		t.Fatalf("应通过：%v %v", ok, err)
	}
	ok, err = requirementsFullyHashed([]byte(`pkg==1.0 \
    --hash=sha256:` + strings.Repeat("b", 64) + "\n"))
	if err != nil || !ok {
		t.Fatalf("续行应通过：%v %v", ok, err)
	}
	if ok, err := requirementsFullyHashed([]byte("pkg==1.0\n")); ok || err == nil {
		t.Fatalf("缺哈希应失败：%v %v", ok, err)
	}
	if ok, err := requirementsFullyHashed([]byte("pkg>=1.0 --hash=sha256:" + strings.Repeat("c", 64) + "\n")); ok || err == nil {
		t.Fatalf("未钉 == 应失败：%v %v", ok, err)
	}
	if ok, err := requirementsFullyHashed([]byte("# only comment\n")); ok || err != nil {
		t.Fatalf("空文件应返回 false,nil：%v %v", ok, err)
	}
}

func TestPythonLockDigestChangesWithRequirements(t *testing.T) {
	src := t.TempDir()
	writeTestFileAt(t, filepath.Join(src, "deploy/nas/offline-build/system-inputs.sh"), "#!/bin/sh\n")
	req := filepath.Join(src, filepath.FromSlash(pythonRequirementsRel))
	writeTestFileAt(t, req, "a==1 --hash=sha256:"+strings.Repeat("1", 64)+"\n")
	// 最小 java/web/go 固定文件，避免 ecosystemLocks 失败——只测 python。
	raw1, err := ecosystemLocks(src, "python")
	if err != nil {
		t.Fatal(err)
	}
	d1 := lockDigestOf(raw1)
	writeTestFileAt(t, req, "a==2 --hash=sha256:"+strings.Repeat("2", 64)+"\n")
	raw2, err := ecosystemLocks(src, "python")
	if err != nil {
		t.Fatal(err)
	}
	if lockDigestOf(raw2) == d1 {
		t.Fatal("requirements 变化后 python 锁摘要应变化")
	}
}
