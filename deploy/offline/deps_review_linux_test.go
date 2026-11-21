//go:build linux

package main

// 审查返工的 Linux 负例：真实的属主/权限校验（可注入 stat 属主）、导入发行开发套件时
// 替换 recipes.tar、fetch 的报错条件与权限归一化、归档导入的来源摘要。

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectOwner 让 siteOwnerOf 对真实文件返回指定 uid（祖先目录校验仍跳过）。
func injectOwner(t *testing.T, uid uint32) {
	t.Helper()
	stubSiteOwner(t, nil)
	siteOwnerOf = func(os.FileInfo) (uint32, error) { return uid, nil }
}

// M2：deps.kits 目录要求 root 所有、组和其他用户不可写。
func TestKitDirOwnershipRequired(t *testing.T) {
	stubSiteOwner(t, nil)
	kit, cfg, _ := signedCacheLayoutKit(t)
	if err := os.Chmod(kit, 0755); err != nil {
		t.Fatal(err)
	}
	if k := newDepsResolver(cfg).kits[0]; k.Err != nil {
		t.Fatalf("root 所有、0755 的套件应可用：%v", k.Err)
	}
	if err := os.Chmod(kit, 0775); err != nil {
		t.Fatal(err)
	}
	if k := newDepsResolver(cfg).kits[0]; k.Err == nil || !strings.Contains(k.Err.Error(), "不能被组或其他用户写") {
		t.Fatalf("组可写的套件目录应拒绝：%v", k.Err)
	}
	if err := os.Chmod(kit, 0757); err != nil {
		t.Fatal(err)
	}
	if k := newDepsResolver(cfg).kits[0]; k.Err == nil || !strings.Contains(k.Err.Error(), "不能被组或其他用户写") {
		t.Fatalf("他人可写的套件目录应拒绝：%v", k.Err)
	}
	if err := os.Chmod(kit, 0755); err != nil {
		t.Fatal(err)
	}
	injectOwner(t, 1000)
	if k := newDepsResolver(cfg).kits[0]; k.Err == nil || !strings.Contains(k.Err.Error(), "root 所有") {
		t.Fatalf("非 root 所有的套件目录应拒绝：%v", k.Err)
	}
	stubSiteOwner(t, errors.New("祖先可写"))
	if k := newDepsResolver(cfg).kits[0]; k.Err == nil || !strings.Contains(k.Err.Error(), "祖先可写") {
		t.Fatalf("祖先目录不受信应拒绝：%v", k.Err)
	}
	if os.Geteuid() != 0 {
		stubSiteOwner(t, nil)
		siteOwnerOf = fileOwnerUID
		if k := newDepsResolver(cfg).kits[0]; k.Err == nil || !strings.Contains(k.Err.Error(), "root 所有") {
			t.Fatalf("真实属主非 root 时应拒绝：%v", k.Err)
		}
	}
}

// M2：导入源（目录或归档）同样要求 root 所有、组和其他用户不可写；L7：归档导入的来源
// 摘要等于被解包的归档字节的摘要。
func TestDepsImportSourceOwnershipRequired(t *testing.T) {
	stubSiteOwner(t, nil)
	kit, _, lock := signedCacheLayoutKit(t)
	cache := filepath.Join(t.TempDir(), "cache")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache}}
	ctx := context.Background()
	var out bytes.Buffer
	if err := os.Chmod(kit, 0777); err != nil {
		t.Fatal(err)
	}
	if err := depsImport(ctx, cfg, kit, depsImportOptions{Out: &out}); err == nil || !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("他人可写的导入目录应拒绝：%v", err)
	}
	if err := os.Chmod(kit, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "kit.tar")
	if err := pack(kit, archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(archive, 0666); err != nil {
		t.Fatal(err)
	}
	if err := depsImport(ctx, cfg, archive, depsImportOptions{Out: &out}); err == nil || !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("他人可写的导入归档应拒绝：%v", err)
	}
	if err := os.Chmod(archive, 0644); err != nil {
		t.Fatal(err)
	}
	injectOwner(t, 1000)
	if err := depsImport(ctx, cfg, archive, depsImportOptions{Out: &out}); err == nil || !strings.Contains(err.Error(), "root 所有") {
		t.Fatalf("非 root 所有的导入归档应拒绝：%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "inputs")); len(entries) != 0 {
		t.Fatal("被拒绝的导入不应写入条目")
	}
	stubSiteOwner(t, nil)
	if err := depsImport(ctx, cfg, archive, depsImportOptions{Out: &out}); err != nil {
		t.Fatal(err)
	}
	sum, err := digest(archive)
	if err != nil {
		t.Fatal(err)
	}
	var origin depsOrigin
	if err := readJSON(filepath.Join(cache, "inputs", "go", lock.Digest, depsOriginFile), &origin); err != nil || !strings.Contains(origin.From[0], "sha256 "+sum) {
		t.Fatalf("归档导入的来源摘要应是归档摘要：%+v %v", origin, err)
	}
}

// L6：deps.caFile 要求 root 所有、组和其他用户不可写。
func TestCAFileOwnershipRequired(t *testing.T) {
	stubSiteOwner(t, nil)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	writeTestFileAt(t, ca, "pem")
	if err := os.Chmod(ca, 0644); err != nil {
		t.Fatal(err)
	}
	d := &siteDeps{Cache: "/srv/auriga/cache", CAFile: &ca, Sources: siteSources{Go: []string{"https://goproxy.internal.example"}}}
	if _, _, err := depsPrepareSettings(d, "go", fakeEnv(nil)); err != nil {
		t.Fatalf("root 所有、0644 的 caFile 应接受：%v", err)
	}
	if err := os.Chmod(ca, 0666); err != nil {
		t.Fatal(err)
	}
	if _, _, err := depsPrepareSettings(d, "go", fakeEnv(nil)); err == nil || !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("他人可写的 caFile 应拒绝：%v", err)
	}
	if err := os.Chmod(ca, 0644); err != nil {
		t.Fatal(err)
	}
	injectOwner(t, 1000)
	if _, _, err := depsPrepareSettings(d, "go", fakeEnv(nil)); err == nil || !strings.Contains(err.Error(), "root 所有") {
		t.Fatalf("非 root 所有的 caFile 应拒绝：%v", err)
	}
}

// M3：导入发行开发套件时替换 recipes.tar（保留真实签名）必须失败，不写任何条目。
func TestDepsImportDevkitReplacedRecipesRejected(t *testing.T) {
	stubSiteOwner(t, nil)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	recipes := filepath.Join(t.TempDir(), "recipes")
	writeTestFileAt(t, filepath.Join(recipes, "deploy", "nas", "offline-build", "system-inputs.sh"), "#!/bin/sh\necho forged\n")
	path := filepath.Join(fx.Root, "cache", "recipes.tar")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := pack(recipes, path); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache}}
	source := func() (string, error) { return src, nil }
	var out bytes.Buffer
	err := depsImport(context.Background(), cfg, fx.Root, depsImportOptions{Fingerprint: devkitTrust(t, fx.Root), Source: source, Out: &out})
	if err == nil || !strings.Contains(err.Error(), "摘要与签名清单不符") {
		t.Fatalf("替换 recipes.tar 后导入应失败：%v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "inputs", "*", "*")); len(left) != 0 {
		t.Fatalf("失败的导入不应写入条目：%v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "images", "*", "*")); len(left) != 0 {
		t.Fatalf("失败的导入不应写入镜像：%v", left)
	}
}

// L4：只缺不能自动补的生态时，不提示 acceptNewDeps，直接报不能自动补。
func TestDepsFetchOnlyUnfetchableMissing(t *testing.T) {
	fx := setupDepsFetch(t)
	for _, eco := range []string{"go", "web"} {
		makeInputEntry(t, fx.cache, eco, fx.locks[eco].Raw, map[string]string{"x": eco})
	}
	err := depsFetch(context.Background(), fx.cfg, "site.json", fx.src, depsFetchOptions{Out: &bytes.Buffer{}})
	if err == nil || errors.Is(err, errNewDepsRejected) || !strings.Contains(err.Error(), "不能自动补") {
		t.Fatalf("只缺 Java/system 时应直接报不能自动补：%v", err)
	}
	if len(fx.calls) != 0 {
		t.Fatalf("不应运行任何准备：%v", fx.calls)
	}
}

// L5：准备容器按自己的 umask 写出的文件，进缓存前去掉组和其他用户的写位。
func TestDepsFetchNormalizesPermissions(t *testing.T) {
	fx := setupDepsFetch(t)
	inner := runOnlineDocker
	runOnlineDocker = func(ctx context.Context, env []string, args ...string) error {
		if err := inner(ctx, env, args...); err != nil {
			return err
		}
		out := mountsOf(args)["/output"]
		return filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0666)
			if d.IsDir() {
				mode = 0777
			}
			return os.Chmod(path, mode)
		})
	}
	_ = depsFetch(context.Background(), fx.cfg, "site.json", fx.src, depsFetchOptions{Accept: true, Out: &bytes.Buffer{}, Budget: buildBudget{MemoryMiB: 4096, CPUs: 2}})
	dir := filepath.Join(fx.cache, "inputs", "go", fx.locks["go"].Digest)
	if err := verifyInputEntry(dir, "go", fx.locks["go"].Digest); err != nil {
		t.Fatalf("go 条目应写入缓存：%v", err)
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0022 != 0 {
			t.Errorf("缓存条目不应组/他人可写 %s %04o", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
