//go:build linux

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDepsImportDevkitLayout(t *testing.T) {
	stubSiteOwner(t, nil)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cache := filepath.Join(t.TempDir(), "cache")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache}}
	source := func() (string, error) { return src, nil }
	ctx := context.Background()
	var out bytes.Buffer
	// 没有独立信任：拒绝（不接受未签名或自证）。
	if err := depsImport(ctx, cfg, fx.Root, depsImportOptions{Source: source, Out: &out}); err == nil || !strings.Contains(err.Error(), "--trust-fingerprint") {
		t.Fatalf("无独立信任的发行开发套件导入应拒绝：%v", err)
	}
	_, otherPublic := testSigningKeys(t)
	otherFP, _ := publicFingerprint(otherPublic)
	if err := depsImport(ctx, cfg, fx.Root, depsImportOptions{Fingerprint: otherFP, Source: source, Out: &out}); err == nil || !strings.Contains(err.Error(), "自证") {
		t.Fatalf("指纹不符应拒绝：%v", err)
	}
	out.Reset()
	if err := depsImport(ctx, cfg, fx.Root, depsImportOptions{Fingerprint: devkitTrust(t, fx.Root), Source: source, Out: &out}); err != nil {
		t.Fatalf("导入发行开发套件：%v\n%s", err, out.String())
	}
	for _, eco := range []string{"go", "system"} {
		dir := filepath.Join(cache, "inputs", eco, fx.Locks[eco].Digest)
		if err := verifyInputEntry(dir, eco, fx.Locks[eco].Digest); err != nil {
			t.Fatalf("%s 导入条目应符合缓存规则：%v", eco, err)
		}
		var origin depsOrigin
		if err := readJSON(filepath.Join(dir, depsOriginFile), &origin); err != nil || origin.Kind != "import" || !origin.Signed || !strings.Contains(origin.From[0], "版本 0.2.0") {
			t.Fatalf("%s 来源记录：%+v %v", eco, origin, err)
		}
	}
	if !strings.Contains(out.String(), "未覆盖当前源码的生态：web、java") {
		t.Fatalf("应列出套件未覆盖的生态：\n%s", out.String())
	}
	hits, err := readImageRecords(cache, "tool-go")
	if err != nil || len(hits) != 1 || hits[0].Image.ID != fx.ToolGoID || hits[0].Tar == "" {
		t.Fatalf("工具镜像应带归档与镜像 ID 导入：%+v %v", hits, err)
	}
	// 导入后缓存直接命中，status 不再依赖套件。
	r := newDepsResolver(cfg)
	if hit, ok := r.findInput("go", fx.Locks["go"].Digest); !ok || hit.Location != "缓存" {
		t.Fatalf("导入后应命中缓存：%+v", hit)
	}
	// 归档形式：重复导入全部跳过。
	archive := filepath.Join(t.TempDir(), "devkit.tar")
	if err := pack(fx.Root, archive); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := depsImport(ctx, cfg, archive, depsImportOptions{Fingerprint: devkitTrust(t, fx.Root), Source: source, Out: &out}); err != nil {
		t.Fatalf("归档导入：%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "新增 0 个依赖输入、0 个镜像") {
		t.Fatalf("重复导入应跳过：\n%s", out.String())
	}
	// 篡改套件输入归档：拒绝且不留暂存。
	cache2 := filepath.Join(t.TempDir(), "cache")
	writeTestFileAt(t, filepath.Join(fx.Root, "cache", "inputs.tar"), "evil")
	err = depsImport(ctx, &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache2}}, fx.Root, depsImportOptions{Fingerprint: devkitTrust(t, fx.Root), Source: source, Out: &out})
	if err == nil {
		t.Fatal("篡改的发行开发套件应拒绝")
	}
	if left, _ := filepath.Glob(filepath.Join(cache2, ".import-*")); len(left) != 0 {
		t.Fatalf("导入暂存应清理：%v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(cache2, "inputs", "*", "*")); len(left) != 0 {
		t.Fatalf("失败的导入不应写入条目：%v", left)
	}
}
