package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeDockerSave 写一个最小的 docker save 归档（manifest.json + 配置块），返回镜像 ID。
func writeFakeDockerSave(t *testing.T, path, role string) string {
	t.Helper()
	config := []byte(`{"role":"` + role + `"}`)
	hexID := lockDigestOf(config)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, m := range []struct {
		name string
		body []byte
	}{
		{"blobs/sha256/" + hexID, config},
		{"manifest.json", []byte(`[{"Config":"blobs/sha256/` + hexID + `","RepoTags":["auriga/devkit-` + role + `:x"],"Layers":[]}]`)},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0644, Size: int64(len(m.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + hexID
}

type devkitDepsFixture struct {
	Root     string // 单版本发行开发套件
	Key      string // 签名私钥
	Locks    map[string]ecosystemLock
	ToolGoID string
}

// fixtureDevkitDeps 生成与源码 src 的 go、system 锁摘要匹配的单版本发行开发套件
// （devkit.json 布局，签名）；web 输入的锁清单故意不同，java 不提供。
func fixtureDevkitDeps(t *testing.T, src, version string) devkitDepsFixture {
	t.Helper()
	locks, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	byEco := map[string]ecosystemLock{}
	for _, l := range locks {
		byEco[l.Ecosystem] = l
	}
	stage := t.TempDir()
	// inputs.tar：go/ 与 web/ 是 prepare 输出布局；system/<服务>/ 没有锁清单。
	inputs := filepath.Join(stage, "inputs")
	writeEntry := func(eco string, lockRaw []byte, files map[string]string) {
		dir := filepath.Join(inputs, eco)
		writeTestFileAt(t, filepath.Join(dir, "source-locks.sha256"), string(lockRaw))
		var sums strings.Builder
		for name, body := range files {
			writeTestFileAt(t, filepath.Join(dir, filepath.FromSlash(name)), body)
			sums.WriteString(lockDigestOf([]byte(body)) + "  " + name + "\n")
		}
		sums.WriteString(lockDigestOf(lockRaw) + "  source-locks.sha256\n")
		writeTestFileAt(t, filepath.Join(dir, "SHA256SUMS"), sums.String())
	}
	writeEntry("go", byEco["go"].Raw, map[string]string{"download/m/@v/v1.zip": "zip"})
	writeEntry("web", []byte("other web locks\n"), map[string]string{"web-cache.tar.gz": "w"})
	writeTestFileAt(t, filepath.Join(inputs, "system", "agent", "packages.txt"), "pkg 1\n")
	writeTestFileAt(t, filepath.Join(inputs, "system", "agent", "packages", "a.apk"), "apk")
	// 第 8 步前的套件：operator 的 pip 包在 system/operator/（布局同 python 条目），没有 python/。
	writeEntry("system/operator", byEco["python"].Raw, map[string]string{"packages/a.whl": "whl", "packages.txt": "./a.whl\n"})
	recipes := filepath.Join(stage, "recipes")
	raw, err := os.ReadFile(filepath.Join(src, "deploy", "nas", "offline-build", "system-inputs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFileAt(t, filepath.Join(recipes, "deploy", "nas", "offline-build", "system-inputs.sh"), string(raw))
	writeTestFileAt(t, filepath.Join(recipes, "deploy", "nas", "offline-build", "go-inputs.sh"), "#!/bin/sh\n")

	root := filepath.Join(t.TempDir(), "auriga-devkit-"+version)
	var r release
	if err := readJSON("release.example.json", &r); err != nil {
		t.Fatal(err)
	}
	r.Version = version
	r.NAS.URL, r.Build.Inputs.URL, r.Build.Recipes.URL = "nas.tar", "inputs.tar", "recipes.tar"
	req, err := digest(filepath.Join(src, "deploy", "nas", "test", "admin-bootstrap-requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	kit := devkitManifest{Schema: 1, Release: r, Source: &provenance{Commit: strings.Repeat("a", 40), Version: version, UpstreamVersion: version},
		Locks: &lockInventory{Digest: strings.Repeat("a", 64), Files: map[string]string{"deploy/nas/test/admin-bootstrap-requirements.txt": req}}}
	refs, err := buildImageRefs(r)
	if err != nil {
		t.Fatal(err)
	}
	fx := devkitDepsFixture{Root: root, Locks: byEco}
	for role, ref := range refs {
		file := "images/" + role + ".tar"
		kit.Images = append(kit.Images, kitImage{Role: role, Ref: ref, Tag: kitImageTag(role, ref), File: file})
		id := writeFakeDockerSave(t, filepath.Join(root, "cache", filepath.FromSlash(file)), role)
		if role == "tool-go" {
			fx.ToolGoID = id
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := pack(inputs, filepath.Join(root, "cache", "inputs.tar")); err != nil {
		t.Fatal(err)
	}
	if err := pack(recipes, filepath.Join(root, "cache", "recipes.tar")); err != nil {
		t.Fatal(err)
	}
	writeTestFileAt(t, filepath.Join(root, "cache", "nas.tar"), "nas")
	writeTestFileAt(t, filepath.Join(root, "source", "README"), "可在现场修改的源码")
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	fx.Key, _ = testSigningKeys(t)
	signTestRoot(t, root, fx.Key)
	return fx
}

func devkitTrust(t *testing.T, root string) string {
	t.Helper()
	fp, err := publicFingerprint(filepath.Join(root, "release-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	return "SHA256:" + fp
}

func TestDevkitLayoutKitResolvesByLockDigest(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cache := filepath.Join(t.TempDir(), "cache")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: fx.Root, TrustFingerprint: devkitTrust(t, fx.Root)}}}}
	r := newDepsResolver(cfg)
	k := r.kits[0]
	if k.Err != nil || k.devkit == nil || !strings.Contains(k.devkit.describe(), "0.2.0") {
		t.Fatalf("发行开发套件应可直接使用：%v", k.Err)
	}
	for _, eco := range []string{"go", "system", "python"} {
		hit, ok := r.findInput(eco, fx.Locks[eco].Digest)
		if !ok || hit.release == nil || !strings.Contains(hit.Location, "版本 0.2.0") {
			t.Fatalf("%s 应按锁摘要命中套件：%+v %v", eco, hit, ok)
		}
		if err := r.verifyInput(hit, eco, fx.Locks[eco].Digest); err != nil {
			t.Fatal(err)
		}
	}
	for _, eco := range []string{"web", "java"} {
		if _, ok := r.findInput(eco, fx.Locks[eco].Digest); ok {
			t.Fatalf("%s 锁摘要不同或套件不提供，不应命中", eco)
		}
	}
	// 解出的 go/system/python 条目符合缓存条目规则（system 补写锁清单与条目 SHA256SUMS；
	// python 取自旧布局的 system/operator/）。
	for _, eco := range []string{"go", "system", "python"} {
		dst := filepath.Join(t.TempDir(), eco)
		if err := os.Mkdir(dst, 0755); err != nil {
			t.Fatal(err)
		}
		if err := k.devkit.extractInput(k.devkit.Latest, fx.Locks[eco], dst); err != nil {
			t.Fatalf("%s 解出失败：%v", eco, err)
		}
	}
	tool, err := r.toolImage("go", t.TempDir())
	if err != nil || tool.kit != k || tool.Image.ID != fx.ToolGoID || tool.Image.runtimeRef() != fx.ToolGoID {
		t.Fatalf("工具镜像应来自套件并带镜像 ID：%+v %v", tool, err)
	}
	// status 列出套件里的镜像。
	stubSiteOwner(t, nil)
	rep, _, _, err := computeDepsStatus(cfg, "site.json", src)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ecosystems[0].Location == "" || len(rep.Images[0].Available) != 1 || !strings.Contains(rep.Kits[0].Layout, "单版本") {
		t.Fatalf("status 应显示套件命中与镜像：%+v", rep)
	}
	// 篡改 inputs.tar：快速命中仍可能在，但使用前的完整校验失败。
	tampered := filepath.Join(fx.Root, "cache", "inputs.tar")
	f, err := os.OpenFile(tampered, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(make([]byte, 1024))
	_ = f.Close()
	// 打开之后才被篡改：命中索引仍在，但使用前的完整校验与解出都失败。
	hit, ok := r.findInput("go", fx.Locks["go"].Digest)
	if !ok {
		t.Fatal("已打开的套件索引应仍在")
	}
	if err := r.verifyInput(hit, "go", fx.Locks["go"].Digest); err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("篡改的套件输入应校验失败：%v", err)
	}
	if err := r.kits[0].devkit.extractInput(r.kits[0].devkit.Latest, fx.Locks["go"], t.TempDir()); err == nil {
		t.Fatal("篡改的套件输入解出时应校验失败")
	}
	// 重新打开：锁扫描整档核对 inputs.tar 摘要，套件不可用、不再命中。
	r = newDepsResolver(cfg)
	if err := r.kits[0].Err; err == nil || !strings.Contains(err.Error(), "摘要与签名清单不符") {
		t.Fatalf("inputs.tar 摘要不符时套件应不可用：%v", err)
	}
	if _, ok := r.findInput("go", fx.Locks["go"].Digest); ok {
		t.Fatal("inputs.tar 摘要不符时不应命中")
	}
}

func TestDevkitLayoutKitRequiresIndependentTrust(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cache := filepath.Join(t.TempDir(), "cache")
	_, otherPublic := testSigningKeys(t)
	otherFP, _ := publicFingerprint(otherPublic)
	r := newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: fx.Root, TrustFingerprint: otherFP}}}})
	if r.kits[0].Err == nil || !strings.Contains(r.kits[0].Err.Error(), "自证") {
		t.Fatalf("发行开发套件自带公钥不能自证：%v", r.kits[0].Err)
	}
	if _, ok := r.findInput("go", fx.Locks["go"].Digest); ok {
		t.Fatal("不受信套件不应命中")
	}
	old := depsStateKeys
	defer func() { depsStateKeys = old }()
	statePub := filepath.Join(t.TempDir(), "state.pem")
	if err := signingPublic(fx.Key, statePub); err != nil {
		t.Fatal(err)
	}
	depsStateKeys = func(*siteConfig) ([]string, error) { return []string{otherPublic, statePub}, nil }
	r = newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: fx.Root, TrustState: true}}}})
	if r.kits[0].Err != nil || !strings.Contains(r.kits[0].Trust, "state") {
		t.Fatalf("trustState 应可用：%v", r.kits[0].Err)
	}
	// 清单被改动（签名仍是旧清单）：拒绝。
	writeTestFileAt(t, filepath.Join(fx.Root, "devkit.json"), "{}")
	r = newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: fx.Root, TrustState: true}}}})
	if r.kits[0].Err == nil || !strings.Contains(r.kits[0].Err.Error(), "摘要不符") {
		t.Fatalf("被改动的 devkit.json 应拒绝：%v", r.kits[0].Err)
	}
}

func TestCumulativeDevkitKitResolvesAcrossVersions(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	full := filepath.Join(t.TempDir(), "full")
	if _, err := composeDevkit("", fx.Root, "0.2.0", strings.Repeat("a", 40), full, fx.Key, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	// 新版本的 go 锁不同：旧版本的 go 输入仍可按锁摘要命中。
	src2 := fakeSourceTree(t)
	writeTestFileAt(t, filepath.Join(src2, "infra", "edge", "go.sum"), "changed\n")
	fx2 := fixtureDevkitDeps(t, src2, "0.2.1")
	next := filepath.Join(t.TempDir(), "full")
	signTestRoot(t, fx2.Root, fx.Key)
	if _, err := composeDevkit(full, fx2.Root, "0.2.1", strings.Repeat("b", 40), next, fx.Key, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache"), Kits: []siteKit{{Path: next, TrustFingerprint: devkitTrust(t, next)}}}}
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil || r.kits[0].devkit.Kind != devkitCumulativeFile || r.kits[0].devkit.Latest.Version != "0.2.1" {
		t.Fatalf("累积套件应可直接使用：%v", r.kits[0].Err)
	}
	hit, ok := r.findInput("go", fx.Locks["go"].Digest)
	if !ok || hit.release.Version != "0.2.0" {
		t.Fatalf("旧版本的 go 输入应命中：%+v %v", hit, ok)
	}
	hit, ok = r.findInput("go", fx2.Locks["go"].Digest)
	if !ok || hit.release.Version != "0.2.1" {
		t.Fatalf("新版本的 go 输入应命中：%+v %v", hit, ok)
	}
	if err := r.verifyInput(hit, "go", fx2.Locks["go"].Digest); err != nil {
		t.Fatal(err)
	}
	if tool, err := r.toolImage("go", t.TempDir()); err != nil || !strings.Contains(tool.Location, "0.2.1") {
		t.Fatalf("镜像取最新版本：%+v %v", tool, err)
	}
}

func TestDockerSaveImageIDRejectsBadArchives(t *testing.T) {
	good := filepath.Join(t.TempDir(), "img.tar")
	id := writeFakeDockerSave(t, good, "tool-go")
	if got, err := dockerSaveImageID(good); err != nil || got != id {
		t.Fatalf("镜像 ID：%s %v", got, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.tar")
	writeTestFileAt(t, filepath.Join(t.TempDir(), "x"), "x")
	if err := os.WriteFile(bad, []byte("not a tar"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerSaveImageID(bad); err == nil {
		t.Fatal("非归档应拒绝")
	}
}
