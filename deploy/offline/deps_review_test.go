package main

// 审查返工（M1–M3 与低危）的负例测试，各平台都跑。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M1：依赖源地址里的 GOPROXY 列表分隔符不能注入 direct/off 或公网源。
func TestDepsSourceSeparatorInjectionRejected(t *testing.T) {
	const ok = `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache","sources":{"go":["https://goproxy.internal.example"]}}}`
	if _, err := parseSiteConfig([]byte(ok)); err != nil {
		t.Fatalf("对照配置应能解析：%v", err)
	}
	bad := []string{
		"https://goproxy.internal.example,direct",
		"https://goproxy.internal.example/x,direct",
		"https://goproxy.internal.example/,https://proxy.golang.org",
		"https://goproxy.internal.example|direct",
		"https://goproxy.internal.example/x|off",
		"https://goproxy.internal.example/?a=b",
		"https://goproxy.internal.example/?",
	}
	for _, v := range bad {
		raw := strings.Replace(ok, "https://goproxy.internal.example", v, 1)
		if _, err := parseSiteConfig([]byte(raw)); err == nil {
			t.Errorf("deps.sources.go %q 应被配置校验拒绝", v)
		}
		npm := `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache","sources":{"npm":"` + v + `"}}}`
		if _, err := parseSiteConfig([]byte(npm)); err == nil {
			t.Errorf("deps.sources.npm %q 应被配置校验拒绝", v)
		}
		// 绕过配置校验直接构造：准备环境时同样拒绝，GOPROXY 不会带上 direct。
		for _, public := range []bool{false, true} {
			d := &siteDeps{Cache: "/srv/auriga/cache", AllowPublic: public, Sources: siteSources{Go: []string{v}}}
			if lookup, _, err := depsPrepareSettings(d, "go", fakeEnv(nil)); err == nil {
				got, _ := lookup("GOPROXY")
				t.Errorf("allowPublic=%v 时 %q 应拒绝，得到 GOPROXY=%q", public, v, got)
			}
		}
	}
	for _, item := range []string{"direct", "off", "DIRECT", " off", "", "https://a.example,direct", "http://a.example"} {
		if checkGoproxyItem(item) == nil {
			t.Errorf("GOPROXY 项 %q 应拒绝", item)
		}
	}
	for _, item := range []string{"https://goproxy.internal.example", "file:///auriga-deps-sources/go-0"} {
		if err := checkGoproxyItem(item); err != nil {
			t.Errorf("GOPROXY 项 %q 应接受：%v", item, err)
		}
	}
	// 多个源仍按数组元素拼接；allowPublic=true 时未配置源保持不设 GOPROXY（沿用默认）。
	d := &siteDeps{Cache: "/srv/auriga/cache", Sources: siteSources{Go: []string{"https://a.example", "https://b.example/"}}}
	lookup, _, err := depsPrepareSettings(d, "go", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := lookup("GOPROXY"); got != "https://a.example,https://b.example" {
		t.Fatalf("GOPROXY=%q", got)
	}
	lookup, _, err = depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache", AllowPublic: true}, "go", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := lookup("GOPROXY"); ok {
		t.Fatalf("allowPublic=true 且未配置源时不应设置 GOPROXY：%q", got)
	}
}

func signedCacheLayoutKit(t *testing.T) (string, *siteConfig, ecosystemLock) {
	t.Helper()
	src := fakeSourceTree(t)
	lock := goLocksOf(t, src)
	kit := filepath.Join(t.TempDir(), "kit")
	key, _ := testSigningKeys(t)
	makeInputEntry(t, kit, "go", lock.Raw, map[string]string{"download/m/@v/v1.zip": "zip"})
	makeImageRecord(t, kit, "tool-go", true)
	signTestRoot(t, kit, key)
	fp, err := publicFingerprint(filepath.Join(kit, "release-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache"), Kits: []siteKit{{Path: kit, TrustFingerprint: "SHA256:" + fp}}}}
	return kit, cfg, lock
}

// M2：缓存布局套件的 image.json 用同一份字节核对签名清单与解析；镜像归档先复制到
// 暂存目录、在副本上核对后，返回的 Tar 指向副本。
func TestKitImageRecordAndArchiveVerifiedOnStagedBytes(t *testing.T) {
	stubTrustedSource(t)
	kit, cfg, _ := signedCacheLayoutKit(t)
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	stage := t.TempDir()
	tool, err := r.toolImage("go", stage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tool.Tar, stage+string(filepath.Separator)) || tool.Image.ID == "" {
		t.Fatalf("套件镜像归档应在暂存副本上核对并带镜像 ID：%+v", tool)
	}
	hexDir := tool.Image.digestHex()
	// 打开后替换归档：toolImage 在副本上核对摘要，拒绝。
	archive := filepath.Join(kit, "images", "tool-go", hexDir, "image.tar")
	writeFakeDockerSave(t, archive, "evil")
	if _, err := r.toolImage("go", t.TempDir()); err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("被替换的套件镜像归档应拒绝：%v", err)
	}
	// 替换 image.json：读入的字节摘要与签名清单不符，拒绝。
	img := tool.Image
	img.ID = "sha256:" + strings.Repeat("e", 64)
	writeTestFileAt(t, filepath.Join(kit, "images", "tool-go", hexDir, "image.json"), mustJSON(t, img))
	if _, err := r.findImages("tool-go"); err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("被替换的 image.json 应拒绝：%v", err)
	}
}

func TestDevkitImageStagedBeforeLoad(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache"), Kits: []siteKit{{Path: fx.Root, TrustFingerprint: devkitTrust(t, fx.Root)}}}}
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	stage := t.TempDir()
	tool, err := r.toolImage("go", stage)
	if err != nil || !strings.HasPrefix(tool.Tar, stage+string(filepath.Separator)) || tool.Image.ID != fx.ToolGoID {
		t.Fatalf("发行开发套件镜像应复制到暂存目录后核对：%+v %v", tool, err)
	}
	writeFakeDockerSave(t, filepath.Join(fx.Root, "cache", "images", "tool-go.tar"), "evil")
	if _, err := r.toolImage("go", t.TempDir()); err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("被替换的镜像归档应拒绝：%v", err)
	}
}

// M3：recipes.tar 整档摘要必须等于已验签清单，替换后 system 锁摘要不能被伪造。
func TestDevkitReplacedRecipesRejected(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache"), Kits: []siteKit{{Path: fx.Root, TrustFingerprint: devkitTrust(t, fx.Root)}}}}
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	if _, ok := r.findInput("system", fx.Locks["system"].Digest); !ok {
		t.Fatal("未篡改时 system 应命中")
	}
	// 攻击者保留真实签名，只换 recipes.tar（放进别的 system-inputs.sh）。
	recipes := filepath.Join(t.TempDir(), "recipes")
	writeTestFileAt(t, filepath.Join(recipes, "deploy", "nas", "offline-build", "system-inputs.sh"), "#!/bin/sh\necho forged\n")
	path := filepath.Join(fx.Root, "cache", "recipes.tar")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := pack(recipes, path); err != nil {
		t.Fatal(err)
	}
	r = newDepsResolver(cfg)
	if err := r.kits[0].Err; err == nil || !strings.Contains(err.Error(), "摘要与签名清单不符") || !strings.Contains(err.Error(), "recipes.tar") {
		t.Fatalf("替换 recipes.tar 后套件应不可用：%v", err)
	}
	if _, ok := r.findInput("system", fx.Locks["system"].Digest); ok {
		t.Fatal("替换 recipes.tar 后 system 不应命中")
	}
}

// L1：解出套件输入有总字节上限，超出即拒绝。
func TestDevkitExtractInputSizeLimit(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	fx := fixtureDevkitDeps(t, src, "0.2.0")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache"), Kits: []siteKit{{Path: fx.Root, TrustFingerprint: devkitTrust(t, fx.Root)}}}}
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	old := devkitExtractMaxBytes
	devkitExtractMaxBytes = 2
	defer func() { devkitExtractMaxBytes = old }()
	if err := r.kits[0].devkit.extractInput(r.kits[0].devkit.Latest, fx.Locks["go"], t.TempDir()); err == nil || !strings.Contains(err.Error(), "大小上限") {
		t.Fatalf("超出解出大小上限应拒绝：%v", err)
	}
	devkitExtractMaxBytes = old
	oldEntries := devkitExtractMaxEntries
	devkitExtractMaxEntries = 1
	defer func() { devkitExtractMaxEntries = oldEntries }()
	if err := r.kits[0].devkit.extractInput(r.kits[0].devkit.Latest, fx.Locks["go"], t.TempDir()); err == nil || !strings.Contains(err.Error(), "条目数上限") {
		t.Fatalf("超出条目数上限应拒绝：%v", err)
	}
}

// L3：累积套件版本按语义版本排序（新版本优先）。
func TestCompareDevkitVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.10.0", "0.9.0", 1},
		{"0.9.0", "0.10.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"v0.2.1", "0.2.0", 1},
		{"0.2.0", "0.2.0", 0},
		{"0.2", "0.2.1", -1},
	} {
		if got := compareDevkitVersion(c.a, c.b); got != c.want {
			t.Errorf("compare(%s, %s) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}
