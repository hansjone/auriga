package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const designSiteJSON = `{
  "schemaVersion": 1,
  "source": {
    "git": {
      "url": "https://git.internal.example/team/auriga.git", "allowedRefs": ["refs/heads/main"],
      "requireSignedCommits": false, "trustedSigners": []
    },
    "dir": null
  },
  "deps": {
    "cache": "/srv/auriga/cache",
    "kits": [{ "path": "/srv/auriga/kits/auriga-devkit-0.2.0", "trustFingerprint": "SHA256:` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `" }],
    "sources": {
      "go": ["https://goproxy.internal.example"],
      "npm": "https://npm.internal.example",
      "maven": "https://maven.internal.example/repository/public",
      "files": "https://files.internal.example/auriga-inputs"
    },
    "registry": "registry.internal.example",
    "caFile": "/etc/auriga/internal-ca.pem",
    "allowPublic": false,
    "acceptNewDeps": false
  },
  "server": {
    "data": "/srv/auriga/data",
    "install": {
      "address": "192.0.2.10", "domain": "auriga.example", "proxy": null,
      "desktopPublicKey": null, "desktopPublisherTenant": null, "desktopConfirmFingerprint": null
    },
    "healthTimeout": "10m",
    "smoke": true,
    "keepHistory": 3
  },
  "sign": { "keyFile": "/etc/auriga/site-sign.key" },
  "client": {
    "out": "D:\\auriga-client-out",
    "platformOrigin": "https://auriga.example",
    "verifyKey": null, "trustFingerprint": "SHA256:` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `",
    "releasePublicKey": "D:\\keys\\release-public.pem", "resourceTrustJson": "D:\\keys\\resource-roots.json"
  },
  "logs": { "dir": "/var/log/auriga" }
}`

func TestSiteConfigDesignExampleParses(t *testing.T) {
	cfg, err := parseSiteConfig([]byte(designSiteJSON))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Deps.Cache != "/srv/auriga/cache" || len(cfg.Deps.Sources.Go) != 1 || *cfg.Deps.Sources.NPM != "https://npm.internal.example" || cfg.Client == nil {
		t.Fatalf("字段未解析：%+v", cfg.Deps)
	}
	minimal := `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"}}`
	if _, err := parseSiteConfig([]byte(minimal)); err != nil {
		t.Fatal(err)
	}
}

func TestSiteConfigRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name, from, to, want string
	}{
		{"顶层未知字段", `"schemaVersion": 1,`, `"schemaVersion": 1, "extra": true,`, "未知字段"},
		{"嵌套未知字段", `"allowPublic": false,`, `"allowPublic": false, "allowpublicx": true,`, "未知字段"},
		{"schema 版本", `"schemaVersion": 1,`, `"schemaVersion": 2,`, "schemaVersion 必须是 1"},
		{"重复键", `"allowPublic": false,`, `"allowPublic": false, "allowPublic": true,`, "重复"},
		{"http 依赖源", `"https://npm.internal.example"`, `"http://npm.internal.example"`, "只允许 https:// 或 file://"},
		{"内嵌凭据", `"https://npm.internal.example"`, `"https://u:p@npm.internal.example"`, "不能内嵌"},
		{"npm 不能是数组", `"npm": "https://npm.internal.example"`, `"npm": ["https://npm.internal.example"]`, "只能是单个地址"},
		{"maven 不能是数组", `"maven": "https://maven.internal.example/repository/public"`, `"maven": ["https://a.example","https://b.example"]`, "只能是单个地址"},
		{"go 必须是数组", `"go": ["https://goproxy.internal.example"]`, `"go": "https://goproxy.internal.example"`, "必须是地址数组"},
		{"go 源 ftp", `"go": ["https://goproxy.internal.example"]`, `"go": ["ftp://goproxy.internal.example"]`, "只允许 https:// 或 file://"},
		{"file 带主机", `"files": "https://files.internal.example/auriga-inputs"`, `"files": "file://host/srv/x"`, "file:///绝对路径"},
		{"registry 带协议", `"registry": "registry.internal.example"`, `"registry": "https://registry.internal.example"`, "主机名[:端口]"},
		{"registry 端口越界", `"registry": "registry.internal.example"`, `"registry": "registry.internal.example:70000"`, "端口无效"},
		{"套件无独立信任", `, "trustFingerprint": "SHA256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" }]`, ` }]`, "独立信任"},
		{"套件指纹格式", `"path": "/srv/auriga/kits/auriga-devkit-0.2.0", "trustFingerprint": "SHA256:0123`, `"path": "/srv/auriga/kits/auriga-devkit-0.2.0", "trustFingerprint": "SHA256:XYZ`, "64 位"},
		{"套件双信任", `, "trustFingerprint": "SHA256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" }]`, `, "trustFingerprint": "SHA256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "trustState": true }]`, "只能选一个"},
		{"缓存相对路径", `"cache": "/srv/auriga/cache"`, `"cache": "srv/cache"`, "规范绝对路径"},
		{"缓存不规范", `"cache": "/srv/auriga/cache"`, `"cache": "/srv/auriga/../cache"`, "规范绝对路径"},
		{"caFile 相对", `"caFile": "/etc/auriga/internal-ca.pem"`, `"caFile": "ca.pem"`, "deps.caFile"},
		{"健康超时", `"healthTimeout": "10m"`, `"healthTimeout": "ten"`, "healthTimeout"},
		{"保留份数", `"keepHistory": 3`, `"keepHistory": 0`, "keepHistory"},
		{"地址", `"address": "192.0.2.10"`, `"address": "::1"`, "IPv4"},
		{"客户端源", `"platformOrigin": "https://auriga.example"`, `"platformOrigin": "http://auriga.example/x"`, "platformOrigin"},
		{"客户端输出", `"out": "D:\\auriga-client-out"`, `"out": "relative"`, "Windows 绝对路径"},
		{"git 引用通配", `["refs/heads/main"]`, `["refs/heads/*"]`, "allowedRefs"},
		{"git 签名无签名者", `"requireSignedCommits": false`, `"requireSignedCommits": true`, "trustedSigners"},
		{"git 协议", `"https://git.internal.example/team/auriga.git"`, `"git://git.internal.example/team/auriga.git"`, "source.git.url"},
		{"类型不符", `"allowPublic": false`, `"allowPublic": "no"`, "类型不符"},
		{"尾部数据", `"logs": { "dir": "/var/log/auriga" }
}`, `"logs": { "dir": "/var/log/auriga" }
} {}`, "尾部"},
	}
	for _, c := range cases {
		raw := strings.Replace(designSiteJSON, c.from, c.to, 1)
		if raw == designSiteJSON {
			t.Fatalf("%s：替换没有生效", c.name)
		}
		_, err := parseSiteConfig([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：期望含 %q 的错误，得到 %v", c.name, c.want, err)
		}
	}
	if _, err := parseSiteConfig([]byte(`{"schemaVersion":1}`)); err == nil || !strings.Contains(err.Error(), "deps") {
		t.Errorf("缺少 deps 应报错：%v", err)
	}
}

type fakeInfo struct {
	mode os.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return "site.json" }
func (f fakeInfo) Size() int64        { return 1 }
func (f fakeInfo) Mode() os.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return f.uid }

func stubSiteOwner(t *testing.T, parentErr error) {
	t.Helper()
	oldOwner, oldParent := siteOwnerOf, siteParentCheck
	siteOwnerOf = func(info os.FileInfo) (uint32, error) {
		if fi, ok := info.(fakeInfo); ok {
			return fi.uid, nil
		}
		return 0, nil
	}
	siteParentCheck = func(string) error { return parentErr }
	t.Cleanup(func() { siteOwnerOf, siteParentCheck = oldOwner, oldParent })
}

// stubTrustedSource 跳过套件目录、导入源与 caFile 的属主校验（Windows 无此校验；
// Linux 上的真实校验见 deps_linux_test.go）。
func stubTrustedSource(t *testing.T) {
	t.Helper()
	old := depsTrustedSourceCheck
	depsTrustedSourceCheck = func(string, string) error { return nil }
	t.Cleanup(func() { depsTrustedSourceCheck = old })
}

func TestSiteConfigFilePermissions(t *testing.T) {
	stubSiteOwner(t, nil)
	// POSIX 权限规则在全平台单测；Windows ACL 见 TestWindowsSiteACLView。
	if err := checkUnixSiteConfigInfo("/etc/auriga/site.json", fakeInfo{mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if err := checkUnixSiteConfigInfo("/etc/auriga/site.json", fakeInfo{mode: 0600}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		info fakeInfo
		want string
	}{
		{fakeInfo{mode: 0644, uid: 1000}, "root 所有"},
		{fakeInfo{mode: 0664}, "0600 或 0644"},
		{fakeInfo{mode: 0640}, "0600 或 0644"},
		{fakeInfo{mode: 0755}, "0600 或 0644"},
		{fakeInfo{mode: os.ModeDir | 0755}, "普通文件"},
	} {
		check := checkUnixSiteConfigInfo
		if c.info.Mode().IsDir() {
			check = checkSiteConfigInfo
		}
		if err := check("/etc/auriga/site.json", c.info); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v：期望 %q，得到 %v", c.info, c.want, err)
		}
	}
	stubSiteOwner(t, errors.New("祖先可写"))
	if err := checkUnixSiteConfigInfo("/etc/auriga/site.json", fakeInfo{mode: 0644}); err == nil || !strings.Contains(err.Error(), "祖先可写") {
		t.Errorf("祖先目录不受信应拒绝：%v", err)
	}
}

func TestSudoRejectsConfigAndSource(t *testing.T) {
	if _, _, err := loadSiteConfig("/tmp/site.json", siteInvocation{Sudo: true, Root: true}); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("sudo 下 --config 应拒绝：%v", err)
	}
	if _, _, err := loadSiteConfig("/tmp/site.json", siteInvocation{}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("非 root 的 --config 应拒绝：%v", err)
	}
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: "/srv/auriga/cache"}}
	if _, err := resolveDepsSource("/src", cfg, siteInvocation{Sudo: true, Root: true}); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("sudo 下 --source 应拒绝：%v", err)
	}
	if _, err := resolveDepsSource("/src", cfg, siteInvocation{}); err == nil {
		t.Fatal("非 root 的 --source 应拒绝")
	}
	if _, err := resolveDepsSource("", cfg, siteInvocation{Root: true}); err == nil || !strings.Contains(err.Error(), "source.dir") {
		t.Fatalf("没有源码时应提示：%v", err)
	}
	old := lookupSudoUID
	lookupSudoUID = func(string) (string, bool) { return "1000", true }
	defer func() { lookupSudoUID = old }()
	if !currentInvocation().Sudo {
		t.Fatal("有 SUDO_UID 应判定为 sudo 调用")
	}
	// 命令入口同样拒绝。
	if err := depsCommandWith([]string{"status", "--config", "/tmp/x.json"}, siteInvocation{Sudo: true, Root: true}, os.Stdout); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("deps status 经 sudo 带 --config 应拒绝：%v", err)
	}
}

func TestAutoFetchRules(t *testing.T) {
	d := &siteDeps{Cache: "/srv/auriga/cache"}
	// 无源码时：system 仍不能自动补；java/python 在有公网/源时按配置放行（源码侧锁/哈希由调用方再核）。
	if ok, reason := autoFetchRule("system", &siteDeps{AllowPublic: true}, ""); ok || !strings.Contains(reason, "deps import") {
		t.Errorf("system 不应允许自动补：%v %s", ok, reason)
	}
	if ok, _ := autoFetchRule("go", d, ""); ok {
		t.Error("没有 Go 源且不允许公网时不能自动补")
	}
	d.Sources.Go = []string{"https://goproxy.internal.example"}
	if ok, _ := autoFetchRule("go", d, ""); !ok {
		t.Error("配置了 Go 源应允许自动补")
	}
	if ok, _ := autoFetchRule("web", d, ""); ok {
		t.Error("没有 npm 源且不允许公网时不能自动补")
	}
	d.AllowPublic = true
	if ok, _ := autoFetchRule("web", d, ""); !ok {
		t.Error("allowPublic 时 Web 应允许自动补")
	}
	if ok, _ := autoFetchRule("java", d, ""); !ok {
		t.Error("无源码路径且 allowPublic 时 Java 应允许自动补（锁文件由 status/fetch 再核）")
	}
	if ok, _ := autoFetchRule("python", d, ""); !ok {
		t.Error("无源码路径且 allowPublic 时 Python 应允许自动补（哈希由 status/fetch 再核）")
	}
	src := t.TempDir()
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(mavenDepsLockRel)), "# empty\n")
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(pythonRequirementsRel)), "cryptography==1\n")
	if ok, reason := autoFetchRule("java", d, src); ok || !strings.Contains(reason, mavenDepsLockRel) {
		t.Errorf("空 Maven 锁不应自动补：%v %s", ok, reason)
	}
	if ok, reason := autoFetchRule("python", d, src); ok || !strings.Contains(reason, "hash") {
		t.Errorf("无哈希的 requirements 不应自动补：%v %s", ok, reason)
	}
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(mavenDepsLockRel)), "a.b:c:jar:1 "+strings.Repeat("a", 64)+"\n")
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(pythonRequirementsRel)), "pkg==1.0 --hash=sha256:"+strings.Repeat("b", 64)+"\n")
	if ok, _ := autoFetchRule("java", d, src); !ok {
		t.Error("有非空 Maven 锁且 allowPublic 时应允许自动补")
	}
	if ok, _ := autoFetchRule("python", d, src); !ok {
		t.Error("requirements 带哈希且 allowPublic 时应允许自动补")
	}
	d.AllowPublic = false
	if ok, reason := autoFetchRule("java", d, src); ok || !strings.Contains(reason, "maven") {
		t.Errorf("无 maven 源且不允许公网时不能自动补：%v %s", ok, reason)
	}
	maven := "https://maven.internal.example/repo"
	d.Sources.Maven = &maven
	if ok, _ := autoFetchRule("java", d, src); !ok {
		t.Error("配置了 maven 源应允许自动补")
	}
	pypi := "https://pypi.internal.example/simple"
	d.Sources.PyPI = &pypi
	if ok, _ := autoFetchRule("python", d, src); !ok {
		t.Error("配置了 pypi 源应允许自动补")
	}
	if _, _, err := depsPrepareSettings(d, "java", fakeEnv(nil)); err != nil {
		t.Fatalf("Java 环境生成不应失败：%v", err)
	}
	if _, _, err := depsPrepareSettings(d, "python", fakeEnv(nil)); err != nil {
		t.Fatalf("Python 环境生成不应失败：%v", err)
	}
	if _, _, err := depsPrepareSettings(d, "system", fakeEnv(nil)); err == nil {
		t.Fatal("system 不能生成自动准备环境")
	}
}

func TestGoPrepareEnvWithoutPublic(t *testing.T) {
	d := &siteDeps{Cache: "/srv/auriga/cache", Sources: siteSources{Go: []string{"https://goproxy.internal.example/", "https://goproxy2.internal.example"}}}
	host := fakeEnv(map[string]string{"GOPROXY": "https://proxy.golang.org", "HTTPS_PROXY": "http://192.0.2.1:3128", "NO_PROXY": "192.0.2.0/24"})
	lookup, extra, err := depsPrepareSettings(d, "go", host)
	if err != nil {
		t.Fatal(err)
	}
	args, env := prepareDockerArgs(prepareRun{Kind: "go", Tool: "sha256:" + strings.Repeat("a", 64), Source: "/src", Out: "/out", Cache: "/seed", Budget: buildBudget{MemoryMiB: 4096, CPUs: 2}, Extra: extra}, lookup)
	if !contains(env, "AURIGA_GO_PREPARE_PROXY=https://goproxy.internal.example,https://goproxy2.internal.example") {
		t.Fatalf("GOPROXY 应来自配置：%v", env)
	}
	if !hasPair(args, "--env", "AURIGA_GO_PREPARE_CACHE=/prepare-cache") {
		t.Fatalf("种子缓存未接线：%v", args)
	}
	for _, v := range env {
		if strings.HasPrefix(v, "AURIGA_GO_PREPARE_PROXY=") && strings.Contains(v, "golang.org") {
			t.Fatalf("Go 代理不应含公网：%v", v)
		}
	}
	if !contains(env, "NO_PROXY=192.0.2.0/24,proxy.golang.org,sum.golang.org") {
		t.Fatalf("公网 Go 主机应加入 NO_PROXY：%v", env)
	}
	if !hasPair(args, "--add-host", "sum.golang.org:127.0.0.1") || !hasPair(args, "--add-host", "proxy.golang.org:127.0.0.1") {
		t.Fatalf("应屏蔽公网 Go 代理与校验库：%v", args)
	}
	toolAt := -1
	for i, a := range args {
		if strings.HasPrefix(a, "sha256:") {
			toolAt = i
		}
	}
	for i, a := range args {
		if a == "--add-host" && i > toolAt {
			t.Fatal("附加参数必须在工具镜像之前")
		}
	}
	// 没有 Go 源也不允许公网：不回落。
	if _, _, err := depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache"}, "go", host); err == nil || !strings.Contains(err.Error(), "proxy.golang.org") {
		t.Fatalf("应拒绝回落公网：%v", err)
	}
	// 把公网代理配成依赖源同样拒绝。
	if _, _, err := depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache", Sources: siteSources{Go: []string{"https://proxy.golang.org"}}}, "go", host); err == nil {
		t.Fatal("allowPublic=false 时不能把 proxy.golang.org 配为源")
	}
	// allowPublic=true 且没有源：不设置 GOPROXY（脚本默认公网），也不屏蔽。
	lookup, extra, err = depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache", AllowPublic: true}, "go", host)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookup("GOPROXY"); ok || len(extra) != 0 {
		t.Fatalf("allowPublic 时不应注入：%v", extra)
	}
}

func TestWebAndCAPrepareEnv(t *testing.T) {
	stubTrustedSource(t)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	npm := "https://npm.internal.example"
	d := &siteDeps{Cache: "/srv/auriga/cache", CAFile: &ca, Sources: siteSources{NPM: &npm}}
	lookup, extra, err := depsPrepareSettings(d, "web", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	args, env := prepareDockerArgs(prepareRun{Kind: "web", Tool: "tool@sha256:" + strings.Repeat("b", 64), Source: "/src", Out: "/out", Cache: "/seed", Extra: extra}, lookup)
	for _, want := range []string{"npm_config_registry=" + npm, "COREPACK_NPM_REGISTRY=" + npm} {
		if !contains(env, want) {
			t.Fatalf("缺少 %s：%v", want, env)
		}
	}
	if !hasPair(args, "--env", "AURIGA_WEB_PREPARE_STORE_TAR=/prepare-cache/web-cache.tar.gz") {
		t.Fatalf("Web 种子缓存未接线：%v", args)
	}
	if !hasPair(args, "--mount", "type=bind,src="+ca+",dst="+depsCAMount+"/ca.pem,readonly") {
		t.Fatalf("caFile 应只读挂载：%v", args)
	}
	if !hasPair(args, "--env", "NODE_EXTRA_CA_CERTS="+depsCAMount+"/ca.pem") || !hasPair(args, "--env", "SSL_CERT_DIR="+depsCAMount) {
		t.Fatalf("缺少 CA 环境：%v", args)
	}
	fileSrc := "file:///srv/npm"
	if _, _, err := depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache", Sources: siteSources{NPM: &fileSrc}}, "web", fakeEnv(nil)); err == nil {
		t.Fatal("npm file:// 暂不支持自动准备")
	}
	maven := "https://maven.internal.example/repo"
	files := "https://files.internal.example/node"
	lookup, extra, err = depsPrepareSettings(&siteDeps{
		Cache: "/srv/auriga/cache", CAFile: &ca, Sources: siteSources{Maven: &maven, NPM: &npm, Files: &files},
	}, "java", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup("AURIGA_MAVEN_MIRROR_URL"); v != maven {
		t.Fatalf("Maven 镜像应由配置生成：%q", v)
	}
	javaArgs, javaEnv := prepareDockerArgs(prepareRun{Kind: "java", Extra: extra}, lookup)
	for _, want := range []string{"AURIGA_JAVA_FILES=" + files, "npm_config_registry=" + npm, "AURIGA_JAVA_NO_PUBLIC=1"} {
		if !contains(javaEnv, want) {
			t.Errorf("Java prepare 缺少 %s：%v", want, javaEnv)
		}
	}
	if !hasPair(javaArgs, "--env", "AURIGA_JAVA_CA_FILE="+depsCAMount+"/ca.pem") ||
		!hasPair(javaArgs, "--add-host", "nodejs.org:127.0.0.1") ||
		!hasPair(javaArgs, "--add-host", "registry.npmjs.org:127.0.0.1") {
		t.Fatalf("Java prepare 未接线私有 CA 或默认公网源隔离：%v", javaArgs)
	}
}

const lockWithIntegrity = `lockfileVersion: '9.0'

packages:

  left-pad@1.3.0:
    resolution: {integrity: sha512-aaa}

  '@scope/x@2.0.0':
    resolution: {integrity: sha512-bbb}

snapshots:

  left-pad@1.3.0: {}
`

func TestWebLockWithoutIntegrityRejected(t *testing.T) {
	if bad := webLockMissingIntegrity([]byte(lockWithIntegrity)); len(bad) != 0 {
		t.Fatalf("不应报缺失：%v", bad)
	}
	raw := lockWithIntegrity + "" // 追加一个只有 tarball 的条目
	raw = strings.Replace(raw, "snapshots:", "  evil@1.0.0:\n    resolution: {tarball: https://x.example/evil.tgz}\n\nsnapshots:", 1)
	bad := webLockMissingIntegrity([]byte(raw))
	if len(bad) != 1 || bad[0] != "evil@1.0.0" {
		t.Fatalf("应找出无 integrity 条目：%v", bad)
	}
	src := fakeSourceTree(t)
	if err := os.WriteFile(filepath.Join(src, "web", "pnpm-lock.yaml"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkWebIntegrity(src); err == nil || !strings.Contains(err.Error(), "evil@1.0.0") {
		t.Fatalf("应拒绝：%v", err)
	}
}

// fakeSourceTree 生成覆盖四个生态锁规则的最小源码树。
func fakeSourceTree(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	files := map[string]string{
		"infra/go.work":                                    "go 1.27\n",
		"infra/edge/go.mod":                                "module edge\n",
		"infra/edge/go.sum":                                "",
		"deploy/bootstrap/go.mod":                          "module b\n",
		"web/package.json":                                 "{}",
		"web/pnpm-lock.yaml":                               lockWithIntegrity,
		"web/node_modules/a/package.json":                  "{\"ignored\":true}",
		"web/dist/package.json":                            "{}",
		"clients/desk/package.json":                        "{\"name\":\"desk\"}",
		"package.json":                                     "{\"packageManager\":\"pnpm@10.0.0\"}",
		"pnpm-workspace.yaml":                              "packages: []\n",
		".npmrc":                                           "\n",
		"pom.xml":                                          "<project/>",
		"mvnw":                                             "#!/bin/sh\n",
		".mvn/wrapper/maven-wrapper.properties":            "x=1\n",
		".mvn/wrapper/maven-wrapper.jar":                   "jar",
		"services/platform/pom.xml":                        "<project/>",
		"services/platform/target/pom.xml":                 "<ignored/>",
		"services/contracts/package.json":                  "{}",
		"services/contracts/package-lock.json":             "{}",
		"deploy/nas/offline-build/system-inputs.sh":        "#!/bin/sh\n",
		"deploy/nas/test/admin-bootstrap-requirements.txt": "pip==1.0 --hash=sha256:" + strings.Repeat("c", 64) + "\n",
		"services/maven-deps.sha256":                       "# empty\n",
	}
	for name, body := range files {
		writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(name)), body)
	}
	return src
}

func writeTestFileAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEcosystemLocksMirrorScripts(t *testing.T) {
	src := fakeSourceTree(t)
	h := func(name string) string {
		sum, err := digest(filepath.Join(src, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return sum + "  " + name + "\n"
	}
	want := map[string]string{
		"go":     h("deploy/bootstrap/go.mod") + h("infra/edge/go.mod") + h("infra/edge/go.sum") + h("infra/go.work"),
		"web":    h("clients/desk/package.json") + h("web/package.json") + h("package.json") + h("pnpm-workspace.yaml") + h(".npmrc") + h("web/pnpm-lock.yaml"),
		"java":   h("services/platform/pom.xml") + h("pom.xml") + h("mvnw") + h(".mvn/wrapper/maven-wrapper.properties") + h(".mvn/wrapper/maven-wrapper.jar") + h("services/contracts/package.json") + h("services/contracts/package-lock.json") + h("services/maven-deps.sha256"),
		"python": h("deploy/nas/test/admin-bootstrap-requirements.txt"),
		"system": h("deploy/nas/offline-build/system-inputs.sh"),
	}
	locks, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locks {
		if string(l.Raw) != want[l.Ecosystem] {
			t.Errorf("%s 锁清单不符：\n%s\n期望：\n%s", l.Ecosystem, l.Raw, want[l.Ecosystem])
		}
		if l.Digest != lockDigestOf([]byte(want[l.Ecosystem])) {
			t.Errorf("%s 锁摘要不符", l.Ecosystem)
		}
	}
	// go 没有任何锁文件时复现 xargs 空输入的输出；web 缺少固定文件时报错。
	empty := t.TempDir()
	raw, err := ecosystemLocks(empty, "go")
	if err != nil || string(raw) != emptySHA256Line {
		t.Fatalf("空 Go 锁：%q %v", raw, err)
	}
	if _, err := ecosystemLocks(empty, "web"); err == nil {
		t.Fatal("缺少 web 目录应报错")
	}
}

// makeInputEntry 在 root 下写一个依赖输入条目，返回条目目录。
func makeInputEntry(t *testing.T, root, eco string, locks []byte, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, "inputs", eco, lockDigestOf(locks))
	writeTestFileAt(t, filepath.Join(dir, "source-locks.sha256"), string(locks))
	var sums strings.Builder
	for name, body := range files {
		writeTestFileAt(t, filepath.Join(dir, filepath.FromSlash(name)), body)
		sum, _ := digest(filepath.Join(dir, filepath.FromSlash(name)))
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
	}
	fmt.Fprintf(&sums, "%s  source-locks.sha256\n", lockDigestOf(locks))
	writeTestFileAt(t, filepath.Join(dir, "SHA256SUMS"), sums.String())
	return dir
}

func makeImageRecord(t *testing.T, root, role string, withTar bool) depsImage {
	t.Helper()
	hexDigest := lockDigestOf([]byte(role + root))
	img := depsImage{Schema: 1, Role: role, Ref: "registry.example/" + role + "@sha256:" + hexDigest}
	dir := filepath.Join(root, "images", role, hexDigest)
	if withTar {
		img.ID = writeFakeDockerSave(t, filepath.Join(dir, "image.tar"), role)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := jsonFile(filepath.Join(dir, "image.json"), img, 0644); err != nil {
		t.Fatal(err)
	}
	return img
}

func goLocksOf(t *testing.T, src string) ecosystemLock {
	t.Helper()
	locks, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	return locks[0]
}

func TestDepsResolverCacheBeforeKitBeforeSource(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	lock := goLocksOf(t, src)
	cache := filepath.Join(t.TempDir(), "cache")
	kit := filepath.Join(t.TempDir(), "kit")
	key, _ := testSigningKeys(t)
	makeInputEntry(t, kit, "go", lock.Raw, map[string]string{"download/m/@v/v1.zip": "zip"})
	makeImageRecord(t, kit, "tool-go", true)
	signTestRoot(t, kit, key)
	fp, err := publicFingerprint(filepath.Join(kit, "release-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: kit, TrustFingerprint: "SHA256:" + fp}}}}
	r := newDepsResolver(cfg)
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	hit, ok := r.findInput("go", lock.Digest)
	if !ok || hit.kit == nil || !strings.Contains(hit.Location, kit) {
		t.Fatalf("缓存缺失时应命中套件：%+v %v", hit, ok)
	}
	if err := r.verifyInput(hit, "go", lock.Digest); err != nil {
		t.Fatal(err)
	}
	makeInputEntry(t, cache, "go", lock.Raw, map[string]string{"download/m/@v/v1.zip": "zip"})
	hit, ok = r.findInput("go", lock.Digest)
	if !ok || hit.Location != "缓存" {
		t.Fatalf("缓存应优先：%+v", hit)
	}
	if _, ok := r.findInput("web", lockDigestOf([]byte("other"))); ok {
		t.Fatal("没有的锁摘要不应命中")
	}
	tool, err := r.toolImage("go", t.TempDir())
	if err != nil || tool.kit == nil {
		t.Fatalf("工具镜像应来自套件：%+v %v", tool, err)
	}
	// 缓存里有同用途镜像时优先缓存；同一位置多个 digest 拒绝猜测。
	makeImageRecord(t, cache, "tool-go", false)
	if tool, err := r.toolImage("go", t.TempDir()); err != nil || tool.Location != "缓存" {
		t.Fatalf("缓存镜像应优先：%+v %v", tool, err)
	}
	second := depsImage{Schema: 1, Role: "tool-go", Ref: "x@sha256:" + strings.Repeat("c", 64)}
	writeTestFileAt(t, filepath.Join(cache, "images", "tool-go", strings.Repeat("c", 64), "image.json"), mustJSON(t, second))
	if _, err := r.toolImage("go", t.TempDir()); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("多个工具镜像应拒绝：%v", err)
	}
	// 篡改套件里的依赖文件：快速命中仍在，但完整校验失败。
	if err := os.RemoveAll(filepath.Join(cache, "inputs")); err != nil {
		t.Fatal(err)
	}
	writeTestFileAt(t, filepath.Join(kit, "inputs", "go", lock.Digest, "download", "m", "@v", "v1.zip"), "evil")
	hit, ok = r.findInput("go", lock.Digest)
	if !ok {
		t.Fatal("应快速命中套件")
	}
	if err := r.verifyInput(hit, "go", lock.Digest); err == nil {
		t.Fatal("篡改的套件条目应校验失败")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.json")
	if err := jsonFile(path, v, 0644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	return string(raw)
}

func TestKitWithoutIndependentTrustRejected(t *testing.T) {
	stubTrustedSource(t)
	src := fakeSourceTree(t)
	lock := goLocksOf(t, src)
	kit := filepath.Join(t.TempDir(), "kit")
	key, _ := testSigningKeys(t)
	_, otherPublic := testSigningKeys(t)
	makeInputEntry(t, kit, "go", lock.Raw, map[string]string{"download/a.zip": "a"})
	signTestRoot(t, kit, key)
	otherFP, err := publicFingerprint(otherPublic)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	// 指纹是另一把钥：套件自带公钥不能自证。
	r := newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: kit, TrustFingerprint: otherFP}}}})
	if r.kits[0].Err == nil || !strings.Contains(r.kits[0].Err.Error(), "自证") {
		t.Fatalf("指纹不符应拒绝：%v", r.kits[0].Err)
	}
	if _, ok := r.findInput("go", lock.Digest); ok {
		t.Fatal("不受信套件不应命中")
	}
	// 配置层面：没有任何信任来源直接拒绝。
	raw := `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache","kits":[{"path":"/srv/kit"}]}}`
	if _, err := parseSiteConfig([]byte(raw)); err == nil || !strings.Contains(err.Error(), "自证") {
		t.Fatalf("无独立信任的套件配置应拒绝：%v", err)
	}
	// trustState：只认 state 钉定的公钥。
	old := depsStateKeys
	defer func() { depsStateKeys = old }()
	depsStateKeys = func(*siteConfig) ([]string, error) { return []string{otherPublic}, nil }
	r = newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: kit, TrustState: true}}}})
	if r.kits[0].Err == nil {
		t.Fatal("签名不属于 state 钉定公钥时应拒绝")
	}
	statePub := filepath.Join(t.TempDir(), "state.pem")
	if err := signingPublic(key, statePub); err != nil {
		t.Fatal(err)
	}
	depsStateKeys = func(*siteConfig) ([]string, error) { return []string{otherPublic, statePub}, nil }
	r = newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: kit, TrustState: true}}}})
	if r.kits[0].Err != nil {
		t.Fatal(r.kits[0].Err)
	}
	if _, ok := r.findInput("go", lock.Digest); !ok {
		t.Fatal("state 钉定公钥签名的套件应可用")
	}
	// 未签名的套件即使指纹“匹配”自带公钥也必须有签名。
	if err := os.Remove(filepath.Join(kit, "SHA256SUMS.sig")); err != nil {
		t.Fatal(err)
	}
	fp, _ := publicFingerprint(filepath.Join(kit, "release-public.pem"))
	r = newDepsResolver(&siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache, Kits: []siteKit{{Path: kit, TrustFingerprint: fp}}}})
	if r.kits[0].Err == nil {
		t.Fatal("缺签名的套件应拒绝")
	}
}

func TestVerifyImportLayout(t *testing.T) {
	src := fakeSourceTree(t)
	lock := goLocksOf(t, src)
	build := func(t *testing.T) string {
		root := filepath.Join(t.TempDir(), "kit")
		makeInputEntry(t, root, "go", lock.Raw, map[string]string{"download/a.zip": "a"})
		makeInputEntry(t, root, "java", []byte("java locks\n"), map[string]string{"repository/x.jar": "jar"})
		makeImageRecord(t, root, "base-edge", true)
		if err := writeSums(root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	ctx := context.Background()
	root := build(t)
	entries, signed, _, err := verifyImportLayout(ctx, nil, root, siteKit{})
	if err != nil || signed || len(entries) != 3 {
		t.Fatalf("合法布局应通过：%v %v %d", err, signed, len(entries))
	}
	cases := []struct {
		name   string
		mutate func(root string)
		want   string
	}{
		{"篡改文件", func(root string) {
			writeTestFileAt(t, filepath.Join(root, "inputs", "go", lock.Digest, "download", "a.zip"), "evil")
		}, "摘要"},
		{"多余根文件", func(root string) {
			writeTestFileAt(t, filepath.Join(root, "README"), "x")
			_ = writeSums(root)
		}, "只允许"},
		{"未知生态", func(root string) {
			_ = os.Rename(filepath.Join(root, "inputs", "java"), filepath.Join(root, "inputs", "rust"))
			_ = writeSums(root)
		}, "生态目录"},
		{"目录名不是锁摘要", func(root string) {
			_ = os.Rename(filepath.Join(root, "inputs", "go", lock.Digest), filepath.Join(root, "inputs", "go", strings.Repeat("0", 64)))
			_ = writeSums(root)
		}, "目录名不符"},
		{"条目内未列入文件", func(root string) {
			writeTestFileAt(t, filepath.Join(root, "inputs", "go", lock.Digest, "download", "extra.zip"), "x")
			_ = writeSums(root)
		}, "未列入"},
		{"镜像记录 digest 不符", func(root string) {
			dirs, _ := os.ReadDir(filepath.Join(root, "images", "base-edge"))
			_ = os.Rename(filepath.Join(root, "images", "base-edge", dirs[0].Name()), filepath.Join(root, "images", "base-edge", strings.Repeat("d", 64)))
			_ = writeSums(root)
		}, "目录名一致"},
	}
	for _, c := range cases {
		root := build(t)
		c.mutate(root)
		if _, _, _, err := verifyImportLayout(ctx, nil, root, siteKit{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：期望 %q，得到 %v", c.name, c.want, err)
		}
	}
	// 指纹：签名有效且匹配才通过。
	root = build(t)
	key, _ := testSigningKeys(t)
	signTestRoot(t, root, key)
	fp, _ := publicFingerprint(filepath.Join(root, "release-public.pem"))
	if _, signed, got, err := verifyImportLayout(ctx, nil, root, siteKit{TrustFingerprint: "SHA256:" + fp}); err != nil || !signed || got != fp {
		t.Fatalf("签名导入应通过：%v %v", err, signed)
	}
	if _, _, _, err := verifyImportLayout(ctx, nil, root, siteKit{TrustFingerprint: strings.Repeat("e", 64)}); err == nil || !strings.Contains(err.Error(), "不符") {
		t.Fatalf("指纹不符应拒绝：%v", err)
	}
}

func TestDepsHelp(t *testing.T) {
	for _, args := range [][]string{{"deps", "--help"}, {"deps", "status", "--help"}, {"deps", "fetch", "--help"}, {"deps", "import", "x", "--help"}} {
		out, done, err := captureHelp(t, args)
		if !done || err != nil || !strings.HasPrefix(out, "用法：") || strings.Contains(out, "（无说明）") {
			t.Fatalf("%v：%v %v\n%s", args, done, err, out)
		}
	}
	out, _, _ := captureHelp(t, []string{"deps", "fetch", "--help"})
	for _, want := range []string{"--accept-new-deps", "--config", "--source", "allowPublic=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("deps fetch 帮助缺少 %q", want)
		}
	}
}
