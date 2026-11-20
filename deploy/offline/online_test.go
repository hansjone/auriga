package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOnlineFlags(t *testing.T) {
	cases := []struct {
		name                   string
		online, offline, build bool
		sign                   string
		wantErr                string
	}{
		{"未用 online", false, false, false, "", ""},
		{"互斥", true, true, true, "k", "互斥"},
		{"缺 build", true, false, false, "k", "--build"},
		{"缺签名", true, false, true, "", "--sign-key"},
		{"合法", true, false, true, "k", ""},
	}
	for _, c := range cases {
		err := validateOnlineFlags(c.online, c.offline, c.build, c.sign)
		if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// http 镜像必须在参数校验阶段就被拒绝（早于载入镜像和启动容器）。
func TestValidateOnlineFlagsRejectsHTTPMirrorEarly(t *testing.T) {
	old := lookupOnlineEnv
	defer func() { lookupOnlineEnv = old }()
	lookupOnlineEnv = func(name string) (string, bool) {
		if name == "AURIGA_MAVEN_MIRROR_URL" {
			return "http://repo.maven.apache.org/maven2", true
		}
		return "", false
	}
	if err := validateOnlineFlags(true, false, true, "k"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http 镜像应在参数校验阶段被拒绝: %v", err)
	}
	if err := validateOnlineFlags(false, false, false, ""); err != nil {
		t.Fatalf("未用 --online 时不检查镜像: %v", err)
	}
}

func TestBundleRejectsBadOnlineCombos(t *testing.T) {
	err := bundle([]string{"--build", "--online", "--offline", "--sign-key", "k"})
	if err == nil || !strings.Contains(err.Error(), "互斥") {
		t.Fatal(err)
	}
	if err := bundle([]string{"--online", "--sign-key", "k"}); err == nil || !strings.Contains(err.Error(), "--build") {
		t.Fatal(err)
	}
	if err := bundle([]string{"--build", "--online"}); err == nil || !strings.Contains(err.Error(), "--sign-key") {
		t.Fatal(err)
	}
}

func TestCheckOnlineScope(t *testing.T) {
	base := lockInventory{Files: map[string]string{"go.mod": "a", "web/package.json": "b", "deploy/nas/docker/Dockerfile.edge": "c", "@build-inputs": "d", "deploy/nas/test/admin-bootstrap-requirements.txt": "e"}}
	clone := func() lockInventory {
		out := lockInventory{Files: map[string]string{}}
		for k, v := range base.Files {
			out.Files[k] = v
		}
		return out
	}
	lang := clone()
	lang.Files["go.mod"] = "changed"
	lang.Files["services/new/pom.xml"] = "new"
	if err := checkOnlineScope(base, lang); err != nil {
		t.Fatal("语言依赖变化应允许", err)
	}
	for _, name := range []string{"deploy/nas/docker/Dockerfile.edge", "@build-inputs", "deploy/nas/test/admin-bootstrap-requirements.txt"} {
		bad := clone()
		bad.Files[name] = "changed"
		if err := checkOnlineScope(base, bad); err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "维护方") {
			t.Errorf("%s 未被拒绝: %v", name, err)
		}
	}
	added := clone()
	added.Files["deploy/other/Dockerfile.new"] = "x"
	if err := checkOnlineScope(base, added); err == nil {
		t.Error("新增 Dockerfile 未被拒绝")
	}
	got := changedLockFiles(base, lang)
	if strings.Join(got, ",") != "go.mod,services/new/pom.xml" {
		t.Fatal(got)
	}
}

func TestEcosystemLocksChanged(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write("old", "h1  go.mod\n")
	if changed, err := ecosystemLocksChanged(old, write("same", "h1  go.mod\n")); err != nil || changed {
		t.Fatal("相同锁被判变化", changed, err)
	}
	if changed, err := ecosystemLocksChanged(old, write("diff", "h2  go.mod\n")); err != nil || !changed {
		t.Fatal("不同锁未判变化", changed, err)
	}
	if _, err := ecosystemLocksChanged(filepath.Join(dir, "missing"), old); err == nil {
		t.Fatal("缺少套件锁应报错")
	}
	if _, err := ecosystemLocksChanged(old, write("empty", "")); err == nil {
		t.Fatal("空锁应报错")
	}
}

func fakeEnv(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { v, ok := values[name]; return v, ok }
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestPrepareDockerArgs(t *testing.T) {
	tool := "sha256:" + strings.Repeat("a", 64)
	budget := buildBudget{MemoryMiB: 4096, CPUs: 2}
	for _, kind := range onlineEcosystems {
		run := prepareRun{Kind: kind, Tool: tool, Source: "/stage/source", Out: "/stage/out", Cache: "/stage/cache", Budget: budget}
		args, env := prepareDockerArgs(run, fakeEnv(nil))
		joined := strings.Join(args, " ")
		if args[0] != "run" || !hasPair(args, "--pull", "never") || hasPair(args, "--network", "none") {
			t.Errorf("%s: 必须联网且不拉镜像: %s", kind, joined)
		}
		if !hasPair(args, "--mount", "type=bind,src=/stage/source,dst=/source,readonly") || !hasPair(args, "--mount", "type=bind,src=/stage/out,dst=/output") {
			t.Errorf("%s: 源码须只读、输出可写: %s", kind, joined)
		}
		if !strings.HasSuffix(joined, tool+" sh "+onlineScriptDir+kind+"-inputs.sh prepare /output") {
			t.Errorf("%s: 须使用套件固定的工具镜像: %s", kind, joined)
		}
		if strings.Contains(joined, "PROXY") || strings.Contains(joined, "registry") || strings.Contains(joined, "MIRROR") || len(env) != 0 {
			t.Errorf("%s: 未设置时不得透传代理/镜像: %s %v", kind, joined, env)
		}
		if strings.Contains(joined, "GOSUMDB") || strings.Contains(joined, "GOFLAGS") || strings.Contains(joined, "INSECURE") {
			t.Errorf("%s: 不得放宽校验: %s", kind, joined)
		}
		if !hasPair(args, "--memory", "4096m") {
			t.Errorf("%s: 缺少资源限制", kind)
		}
	}
}

func TestPrepareDockerArgsCachesAndPassthrough(t *testing.T) {
	tool := "sha256:" + strings.Repeat("b", 64)
	host := fakeEnv(map[string]string{"HTTPS_PROXY": "http://proxy:3128", "no_proxy": "localhost", "GOPROXY": "https://goproxy.cn", "npm_config_registry": "https://registry.npmmirror.com", "AURIGA_MAVEN_MIRROR_URL": "https://maven.example/repo", "GOSUMDB": "off", "GOFLAGS": "-insecure"})
	goArgs, goEnv := prepareDockerArgs(prepareRun{Kind: "go", Tool: tool, Source: "/s", Out: "/o", Cache: "/old/go/download"}, host)
	if !hasPair(goArgs, "--mount", "type=bind,src=/old/go/download,dst=/prepare-cache,readonly") || !hasPair(goArgs, "--env", "AURIGA_GO_PREPARE_CACHE=/prepare-cache") {
		t.Fatal("go 缓存应只读挂载", goArgs)
	}
	if !hasPair(goArgs, "--env", "AURIGA_GO_PREPARE_PROXY") || !hasPair(goArgs, "--env", "HTTPS_PROXY") || !hasPair(goArgs, "--env", "no_proxy") {
		t.Fatal("go 应透传代理与 GOPROXY", goArgs)
	}
	joined := strings.Join(goArgs, " ") + strings.Join(goEnv, " ")
	if strings.Contains(joined, "GOSUMDB") || strings.Contains(joined, "GOFLAGS") || strings.Contains(joined, "npm_config") || strings.Contains(joined, "MIRROR") {
		t.Fatal("go 不得带 GOSUMDB/GOFLAGS 或其他生态变量", joined)
	}
	if !contains(goEnv, "AURIGA_GO_PREPARE_PROXY=https://goproxy.cn") || !contains(goEnv, "HTTPS_PROXY=http://proxy:3128") {
		t.Fatal(goEnv)
	}
	webArgs, _ := prepareDockerArgs(prepareRun{Kind: "web", Tool: tool, Source: "/s", Out: "/o", Cache: "/old/web"}, host)
	if !hasPair(webArgs, "--env", "AURIGA_WEB_PREPARE_STORE_TAR=/prepare-cache/web-cache.tar.gz") || !hasPair(webArgs, "--env", "npm_config_registry") || strings.Contains(strings.Join(webArgs, " "), "GOPROXY") {
		t.Fatal("web 参数", webArgs)
	}
	javaArgs, _ := prepareDockerArgs(prepareRun{Kind: "java", Tool: tool, Source: "/s", Out: "/o", Cache: "/tmp/java-cache", CacheRW: true}, host)
	if !hasPair(javaArgs, "--mount", "type=bind,src=/tmp/java-cache,dst=/prepare-cache") || !hasPair(javaArgs, "--env", "AURIGA_JAVA_PREP_CACHE_DIR=/prepare-cache") || !hasPair(javaArgs, "--env", "AURIGA_MAVEN_MIRROR_URL") {
		t.Fatal("java 参数", javaArgs)
	}
	noCache, _ := prepareDockerArgs(prepareRun{Kind: "go", Tool: tool, Source: "/s", Out: "/o"}, fakeEnv(nil))
	if strings.Contains(strings.Join(noCache, " "), "prepare-cache") {
		t.Fatal("无缓存不应挂载")
	}
}

func TestLocksDockerArgsAreOffline(t *testing.T) {
	tool := "sha256:" + strings.Repeat("c", 64)
	args := locksDockerArgs("web", tool, "/s", "/o", buildBudget{MemoryMiB: 2048, CPUs: 1})
	if !hasPair(args, "--network", "none") || !hasPair(args, "--mount", "type=bind,src=/s,dst=/source,readonly") || !strings.HasSuffix(strings.Join(args, " "), tool+" sh "+onlineScriptDir+"web-inputs.sh locks /output") {
		t.Fatal(args)
	}
}

func TestInputScriptsStrictAndSumdb(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "nas", "offline-build", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	java := read("java-inputs.sh")
	prepare, build, ok := strings.Cut(java, "else\n    case \"${AURIGA_JAVA_INPUTS_DIR:-}\"")
	if !ok {
		t.Fatal("java 脚本结构变化")
	}
	if strings.Count(prepare, "mvnw -B -C ") != 2 || !strings.Contains(prepare, "AURIGA_MAVEN_MIRROR_URL") {
		t.Fatal("Java prepare 必须严格校验和并支持镜像")
	}
	if strings.Contains(build, " -C ") || strings.Contains(build, "--strict-checksums") {
		t.Fatal("Java build 模式不应改变")
	}
	goScript := read("go-inputs.sh")
	prepareGo, _, ok := strings.Cut(goScript, "\nelse\n    case \"${AURIGA_GO_PROXY_DIR:-}\"")
	if !ok {
		t.Fatal("go 脚本结构变化")
	}
	if !strings.Contains(prepareGo, "GOSUMDB=sum.golang.org") || strings.Contains(prepareGo, "GOSUMDB=off") {
		t.Fatal("Go prepare 必须保持 GOSUMDB 校验")
	}
	for _, name := range []string{"go-inputs.sh", "web-inputs.sh", "java-inputs.sh"} {
		text := read(name)
		if !strings.Contains(text, `"$mode" = locks`) && !strings.Contains(text, "locks)") {
			t.Errorf("%s 缺少 locks 模式", name)
		}
		if strings.Contains(text, "\r") {
			t.Errorf("%s 必须是 LF 换行", name)
		}
	}
}

func TestDiffAndListInputs(t *testing.T) {
	ctx := context.Background()
	mk := func(files ...string) string {
		root := t.TempDir()
		for _, f := range files {
			p := filepath.Join(root, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	oldGo := mk("download/github.com/!burnt!sushi/toml/@v/v1.2.0.zip", "download/github.com/!burnt!sushi/toml/@v/v1.2.0.mod", "download/golang.org/x/text/@v/v0.3.0.zip")
	newGo := mk("download/github.com/!burnt!sushi/toml/@v/v1.2.0.zip", "download/golang.org/x/text/@v/v0.4.0.zip", "download/github.com/new/lib/@v/v1.0.0.zip")
	a, err := listGoModules(ctx, oldGo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := listGoModules(ctx, newGo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a[depKey(depEntry{Ecosystem: "go", Name: "github.com/BurntSushi/toml", Version: "v1.2.0"})]; !ok {
		t.Fatal("未还原大写转义", a)
	}
	added, removed := diffDeps(a, b)
	if len(added) != 2 || added[0].Name != "github.com/new/lib" || added[1].Name != "golang.org/x/text" || added[1].Version != "v0.4.0" || len(removed) != 1 || removed[0].Version != "v0.3.0" {
		t.Fatal(added, removed)
	}

	repoOld := mk("repository/org/acme/lib/1.0/lib-1.0.pom", "repository/org/acme/lib/1.0/lib-1.0.pom.sha1", "repository/org/acme/lib/maven-metadata.xml")
	repoNew := mk("repository/org/acme/lib/1.0/lib-1.0.pom", "repository/org/acme/lib/1.1/lib-1.1.jar", "repository/com/x/y-z/2.0/y-z-2.0.pom")
	ma, err := listMavenArtifacts(ctx, filepath.Join(repoOld, "repository"))
	if err != nil {
		t.Fatal(err)
	}
	mb, err := listMavenArtifacts(ctx, filepath.Join(repoNew, "repository"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ma) != 1 {
		t.Fatal(ma)
	}
	added, removed = diffDeps(ma, mb)
	if len(added) != 2 || added[0].Name != "com.x:y-z" || added[1].Name != "org.acme:lib" || added[1].Version != "1.1" || len(removed) != 0 {
		t.Fatal(added, removed)
	}

}

func TestMavenMirrorURLValidation(t *testing.T) {
	for _, ok := range []string{"https://repo.example.com/maven/central", "https://nexus.corp:8443/repository/maven-public/?a=b%20c"} {
		if err := validateMavenMirrorURL(ok); err != nil {
			t.Errorf("%s 应接受: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://repo.example.com/x", "https://", "https://a b", "https://a&b", "https://a<b", "https://a>b", "https://a\"b", "https://a'b", "https://a\nb", "https://a\rb", "https://u:p@host/x", "ftp://x", "file:///x", "https://a`b", "https://a$b", "https://a;b"} {
		if err := validateMavenMirrorURL(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	// 脚本里的白名单与 Go 侧一致：https 前缀 + 同一字符集 + central 镜像 id。
	raw, err := os.ReadFile(filepath.Join("..", "nas", "offline-build", "java-inputs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"*[!A-Za-z0-9._~:/?%+=,-]*) exit 1", "https://?*) exit 0", "<id>central</id>", "<mirrorOf>central</mirrorOf>", "不能防镜像篡改"} {
		if want == "不能防镜像篡改" {
			want = "NOT protect against a malicious"
		}
		if !strings.Contains(text, want) {
			t.Errorf("java-inputs.sh 缺少 %s", want)
		}
	}
	if strings.Contains(text, "auriga-site-mirror") || strings.Contains(text, "<mirrorOf>*</mirrorOf>") {
		t.Error("镜像 id 必须是 central（M3）")
	}
}

// 真实 pnpm 11（lockfileVersion 9.0）锁文件片段：importers、packages、snapshots 三段。
const pnpmLockFragment = `lockfileVersion: '9.0'

settings:
  autoInstallPeers: true

importers:

  ..:
    devDependencies:
      '@eslint/js':
        specifier: 9.39.1
        version: 9.39.1
      '@vitejs/plugin-vue':
        specifier: 6.0.10
        version: 6.0.10(vite@7.3.1(@types/node@22.19.0)(yaml@2.9.1))(vue@3.5.44(typescript@5.9.3))

packages:

  '@apidevtools/json-schema-ref-parser@11.9.3':
    resolution: {integrity: sha512-60vepv88RwcJtSHrD6MjIL6Ta3SOYbgfnkHb+ppAVK+o9mXprRtulx7VlRl3lN3bbvysAfCS7WMVfhUYemB0IQ==}
    engines: {node: '>= 16'}

  '@babel/parser@7.29.9':
    resolution: {integrity: sha512-CjXrNHTnvqBVqHgdBysY3vk2T8tpJHb5/RMeHJBTyVa9xgugCB0CJTx/3oO8RV2QRQP391RWpB7D6hLjm8V9uA==}
    engines: {node: '>=6.0.0'}
    hasBin: true

  left-pad@1.3.0:
    resolution: {integrity: sha512-leftpadintegrity}

snapshots:

  '@babel/parser@7.29.9':
    dependencies:
      '@babel/types': 7.29.0

  '@vitejs/plugin-vue@6.0.10(vite@7.3.1(@types/node@22.19.0)(yaml@2.9.1))(vue@3.5.44(typescript@5.9.3))':
    dependencies:
      vue: 3.5.44(typescript@5.9.3)
`

func TestPnpmLockPackages(t *testing.T) {
	got := pnpmLockPackages([]byte(pnpmLockFragment))
	if len(got) != 3 {
		t.Fatalf("只应解析 packages 段 3 项: %v", got)
	}
	e, ok := got[depKey(depEntry{Ecosystem: "web", Name: "@babel/parser", Version: "7.29.9"})]
	if !ok || !strings.HasPrefix(e.Integrity, "sha512-CjXrNHTn") {
		t.Fatal("作用域包或 integrity 解析错误", got)
	}
	if e, ok := got[depKey(depEntry{Ecosystem: "web", Name: "left-pad", Version: "1.3.0"})]; !ok || e.Integrity != "sha512-leftpadintegrity" {
		t.Fatal(got)
	}
	// CRLF 与新增包的比较。
	newer := strings.ReplaceAll(pnpmLockFragment, "  left-pad@1.3.0:", "  is-odd@3.0.1:\n    resolution: {integrity: sha512-odd}\n\n  left-pad@1.3.0:")
	added, removed := diffDeps(got, pnpmLockPackages([]byte(strings.ReplaceAll(newer, "\n", "\r\n"))))
	if len(added) != 1 || added[0].Name != "is-odd" || added[0].Version != "3.0.1" || len(removed) != 0 {
		t.Fatal(added, removed)
	}
}

func TestPrepareAndProvenanceStayCompatibleWithOldCLI(t *testing.T) {
	// 旧版 CLI 用 DisallowUnknownFields 严格解码 offline-release.json / provenance.json。
	// 这里固定旧结构的字段集；provenance 增加任何字段都会让该测试失败。
	type legacyProvenance struct {
		Version         string `json:"version"`
		UpstreamVersion string `json:"upstreamVersion"`
		Commit          string `json:"commit"`
		Branch          string `json:"branch"`
		Dirty           bool   `json:"dirty"`
		SiteChanges     bool   `json:"siteChanges"`
		SnapshotSHA256  string `json:"snapshotSha256"`
		BuiltAt         string `json:"builtAt"`
	}
	dir := t.TempDir()
	info := &provenance{Version: "1.0-site.x", UpstreamVersion: "1.0", Commit: "c", Branch: "b", SnapshotSHA256: "s", BuiltAt: "t"}
	if err := jsonFile(filepath.Join(dir, "provenance.json"), info, 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	var legacy legacyProvenance
	if err := d.Decode(&legacy); err != nil {
		t.Fatal("旧 CLI 严格解码 provenance 失败", err)
	}
	// 整个 release 清单同样：旧 release 结构不认识任何 siteDependencies。
	if err := jsonFile(filepath.Join(dir, "offline-release.json"), release{Schema: 1, Version: "1.0", Source: info}, 0644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "offline-release.json"))
	if strings.Contains(string(raw), "siteDependencies") || strings.Contains(string(raw), "online") {
		t.Fatal("现场依赖标记不得写入 offline-release.json")
	}
}

func TestSiteDependenciesFileIsSignedAndDoctorReadsIt(t *testing.T) {
	root := t.TempDir()
	if got := siteDependenciesInfo(root); got != "" {
		t.Fatal("没有标记文件时不应输出", got)
	}
	report := &siteReport{Schema: 1, Online: true, Ecosystems: []string{"go", "java"}, LockDigest: strings.Repeat("a", 64)}
	if err := jsonFile(filepath.Join(root, "site-dependencies.json"), report, 0644); err != nil {
		t.Fatal(err)
	}
	line := siteDependenciesInfo(root)
	if !strings.HasPrefix(line, "INFO ") || !strings.Contains(line, "go/java") || !strings.Contains(line, strings.Repeat("a", 64)) {
		t.Fatal(line)
	}
	// bundle 在写 SHA256SUMS 并签名之前把文件放进包根；清单必须覆盖它。
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil || !strings.Contains(string(sums), "site-dependencies.json") {
		t.Fatal("SHA256SUMS 未覆盖 site-dependencies.json", err)
	}
	if err := jsonFile(filepath.Join(root, "site-dependencies.json"), &siteReport{Online: false}, 0644); err != nil {
		t.Fatal(err)
	}
	if siteDependenciesInfo(root) != "" {
		t.Fatal("online=false 不应输出")
	}
}

func TestSiteReportPrinted(t *testing.T) {
	var out bytes.Buffer
	printSiteReport(&out, &siteReport{Ecosystems: []string{"go"}, Added: []depEntry{{Ecosystem: "go", Name: "example.com/lib", Version: "v1.0.0", SHA256: "abc"}}, Warnings: []string{"w1"}, Notice: siteReportNotice})
	text := out.String()
	for _, want := range []string{"+ [go] example.com/lib v1.0.0 abc", "新增 1 项", "警告：w1", "镜像可信性由现场负责"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q: %s", want, text)
		}
	}
	if strings.Contains(text, "严格校验和仍启用") {
		t.Fatal("文案不得暗示 -C 能防镜像篡改")
	}
	if raw, err := os.ReadFile("help.go"); err != nil || strings.Contains(string(raw), "Maven 严格校验和仍启用") {
		t.Fatal("help 文案不得暗示 -C 能防镜像篡改", err)
	}
}

// ---- collectOnlineInputs 编排（假 docker 执行器）----

type fakeOnlineDocker struct {
	t         *testing.T
	locks     map[string]string // kind -> 当前源码锁内容
	failKinds map[string]bool   // prepare 失败的生态
	calls     []string          // "locks:go" / "prepare:web"
	prepEnv   map[string][]string
	cacheSeen map[string]string // kind -> 缓存挂载宿主路径
	cacheRW   map[string]bool
	cacheOK   map[string]bool
}

func mountsOf(args []string) map[string]string {
	out := map[string]string{} // dst -> src
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--mount" {
			continue
		}
		var src, dst string
		for _, part := range strings.Split(args[i+1], ",") {
			if v, ok := strings.CutPrefix(part, "src="); ok {
				src = v
			}
			if v, ok := strings.CutPrefix(part, "dst="); ok {
				dst = v
			}
		}
		out[dst] = src
	}
	return out
}

func (f *fakeOnlineDocker) run(_ context.Context, env []string, args ...string) error {
	t := f.t
	n := len(args)
	mode, script := args[n-2], args[n-3]
	kind := strings.TrimSuffix(filepath.Base(script), "-inputs.sh")
	mounts := mountsOf(args)
	out := mounts["/output"]
	if out == "" {
		t.Fatal("缺少 /output 挂载", args)
	}
	write := func(rel, body string) {
		p := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	f.calls = append(f.calls, mode+":"+kind)
	if mode == "locks" {
		write("source-locks.sha256", f.locks[kind])
		return nil
	}
	if f.failKinds[kind] {
		return errors.New("network down")
	}
	f.prepEnv[kind] = env
	cache := mounts["/prepare-cache"]
	f.cacheSeen[kind] = cache
	_, rw := mountsRW(args)
	f.cacheRW[kind] = rw
	switch kind {
	case "go":
		_, err := os.Stat(filepath.Join(cache, "example.com/old/@v/v1.0.0.zip"))
		f.cacheOK[kind] = err == nil
		write("download/example.com/old/@v/v1.0.0.zip", "old-zip")
		write("download/example.com/new/@v/v1.0.0.zip", "new-zip")
	case "java":
		_, err1 := os.Stat(filepath.Join(cache, "repository/org/old/lib/1.0/lib-1.0.pom"))
		_, err2 := os.Stat(filepath.Join(cache, "npm-cache/_cacache/x"))
		_, err3 := os.Stat(filepath.Join(cache, "maven/wrapper/dists/d"))
		f.cacheOK[kind] = err1 == nil && err2 == nil && err3 == nil
		write("repository/org/old/lib/1.0/lib-1.0.pom", "old-pom")
		write("repository/org/new/lib/2.0/lib-2.0.jar", "new-jar")
		write("repository/org/new/lib/2.0/lib-2.0.pom", "new-pom")
	case "web":
		_, err := os.Stat(filepath.Join(cache, "web-cache.tar.gz"))
		f.cacheOK[kind] = err == nil
		write("web-cache.tar.gz", "new-store")
	}
	write("source-locks.sha256", f.locks[kind])
	return nil
}

func mountsRW(args []string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--mount" && strings.Contains(args[i+1], "dst=/prepare-cache") {
			return args[i+1], !strings.HasSuffix(args[i+1], ",readonly")
		}
	}
	return "", false
}

type onlineFixture struct {
	stage, repo, commit string
	fake                *fakeOnlineDocker
	env                 map[string]string
	release             release
}

const webLockNew = "packages:\n\n  left-pad@1.3.0:\n    resolution: {integrity: sha512-leftpadintegrity}\n\n  is-odd@3.0.1:\n    resolution: {integrity: sha512-odd}\n"
const webLockOld = "packages:\n\n  left-pad@1.3.0:\n    resolution: {integrity: sha512-leftpadintegrity}\n"

func setupOnline(t *testing.T, withGit bool) *onlineFixture {
	t.Helper()
	stage := t.TempDir()
	put := func(rel, body string) {
		p := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range onlineEcosystems {
		put("inputs/"+kind+"/source-locks.sha256", "old-"+kind+"\n")
	}
	put("inputs/go/download/example.com/old/@v/v1.0.0.zip", "old-zip")
	put("inputs/java/repository/org/old/lib/1.0/lib-1.0.pom", "old-pom")
	put("inputs/java/npm-cacache/x", "n")
	put("inputs/java/wrapper/dists/d", "d")
	put("inputs/web/web-cache.tar.gz", "old-store")
	put("source/web/pnpm-lock.yaml", webLockNew)
	fx := &onlineFixture{stage: stage}
	if withGit {
		fx.repo = t.TempDir()
		for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "a"}, {"config", "user.email", "a@example.com"}} {
			if _, err := gitAt(fx.repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(fx.repo, "web"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.repo, "web/pnpm-lock.yaml"), []byte(webLockOld), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := gitAt(fx.repo, "add", "."); err != nil {
			t.Fatal(err)
		}
		if _, err := gitAt(fx.repo, "commit", "-m", "base"); err != nil {
			t.Fatal(err)
		}
		var err error
		if fx.commit, err = gitAt(fx.repo, "rev-parse", "HEAD"); err != nil {
			t.Fatal(err)
		}
	}
	tools := map[string]string{}
	for i, kind := range onlineEcosystems {
		tools[kind] = "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
	}
	fx.release = release{Build: &buildSpec{Tools: tools}}
	fx.fake = &fakeOnlineDocker{t: t, locks: map[string]string{"go": "old-go\n", "web": "old-web\n", "java": "old-java\n"}, failKinds: map[string]bool{}, prepEnv: map[string][]string{}, cacheSeen: map[string]string{}, cacheRW: map[string]bool{}, cacheOK: map[string]bool{}}
	fx.env = map[string]string{}
	oldRun, oldCheck, oldLookup := runOnlineDocker, checkOnlineTool, lookupOnlineEnv
	runOnlineDocker = fx.fake.run
	checkOnlineTool = func(context.Context, string) error { return nil }
	lookupOnlineEnv = func(name string) (string, bool) { v, ok := fx.env[name]; return v, ok }
	t.Cleanup(func() { runOnlineDocker, checkOnlineTool, lookupOnlineEnv = oldRun, oldCheck, oldLookup })
	return fx
}

func (fx *onlineFixture) collect(t *testing.T) (*siteReport, error) {
	t.Helper()
	return collectOnlineInputs(context.Background(), fx.stage, fx.release, buildOptions{Source: fx.repo, BaselineCommit: fx.commit, Budget: buildBudget{MemoryMiB: 2048, CPUs: 1}})
}

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCollectOnlineNoChangeKeepsKitInputs(t *testing.T) {
	fx := setupOnline(t, true)
	report, err := fx.collect(t)
	if err != nil || report != nil {
		t.Fatal(report, err)
	}
	if strings.Join(fx.fake.calls, ",") != "locks:go,locks:web,locks:java" {
		t.Fatal("未变化时只应运行 locks:", fx.fake.calls)
	}
	if readText(t, filepath.Join(fx.stage, "inputs/go/source-locks.sha256")) != "old-go\n" {
		t.Fatal("输入被改动")
	}
	if _, err := os.Stat(filepath.Join(fx.stage, "online")); !os.IsNotExist(err) {
		t.Fatal("scratch 应已清理")
	}
}

func TestCollectOnlineAllEcosystemsChanged(t *testing.T) {
	fx := setupOnline(t, true)
	for _, kind := range onlineEcosystems {
		fx.fake.locks[kind] = "new-" + kind + "\n"
	}
	fx.env["HTTPS_PROXY"] = "http://proxy:3128"
	fx.env["GOPROXY"] = "https://goproxy.example"
	report, err := fx.collect(t)
	if err != nil || report == nil {
		t.Fatal(report, err)
	}
	if strings.Join(report.Ecosystems, ",") != "go,web,java" || !report.Online {
		t.Fatal(report.Ecosystems)
	}
	for _, kind := range onlineEcosystems {
		if !fx.fake.cacheOK[kind] {
			t.Errorf("%s 的旧输入没有作为缓存准备好: %s", kind, fx.fake.cacheSeen[kind])
		}
		if readText(t, filepath.Join(fx.stage, "inputs", kind, "source-locks.sha256")) != "new-"+kind+"\n" {
			t.Errorf("%s 输入未被替换", kind)
		}
	}
	if !fx.fake.cacheRW["java"] || fx.fake.cacheRW["go"] || fx.fake.cacheRW["web"] {
		t.Fatal("只有 Java 缓存应可写", fx.fake.cacheRW)
	}
	if !contains(fx.fake.prepEnv["go"], "AURIGA_GO_PREPARE_PROXY=https://goproxy.example") || contains(fx.fake.prepEnv["web"], "AURIGA_GO_PREPARE_PROXY=https://goproxy.example") {
		t.Fatal("代理变量透传错误", fx.fake.prepEnv)
	}
	byName := map[string]depEntry{}
	for _, e := range report.Added {
		byName[e.Ecosystem+":"+e.Name] = e
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	if e := byName["go:example.com/new"]; e.SHA256 != sum("new-zip") || e.File != "v1.0.0.zip" {
		t.Fatal("Go 新增模块缺 SHA-256", byName)
	}
	if e := byName["java:org.new:lib"]; e.SHA256 != sum("new-jar") || e.File != "lib-2.0.jar" || e.Version != "2.0" {
		t.Fatal("Java 新增构件缺 SHA-256", byName)
	}
	if e := byName["web:is-odd"]; e.Integrity != "sha512-odd" || e.Version != "3.0.1" {
		t.Fatal("Web 新增包缺 integrity", byName)
	}
	if len(report.Added) != 3 || len(report.Warnings) != 0 {
		t.Fatal(report.Added, report.Warnings)
	}
	// 写出的清单可被 doctor 读取，且来源标记只在该文件里。
	root := t.TempDir()
	report.LockDigest = strings.Repeat("b", 64)
	if err := jsonFile(filepath.Join(root, "site-dependencies.json"), report, 0644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(siteDependenciesInfo(root), "go/web/java") {
		t.Fatal(siteDependenciesInfo(root))
	}
}

func TestCollectOnlineOnlyChangedEcosystemRerun(t *testing.T) {
	fx := setupOnline(t, true)
	fx.fake.locks["go"] = "new-go\n"
	report, err := fx.collect(t)
	if err != nil || report == nil || strings.Join(report.Ecosystems, ",") != "go" {
		t.Fatal(report, err)
	}
	if strings.Join(fx.fake.calls, ",") != "locks:go,locks:web,locks:java,prepare:go" {
		t.Fatal(fx.fake.calls)
	}
	if readText(t, filepath.Join(fx.stage, "inputs/web/web-cache.tar.gz")) != "old-store" || readText(t, filepath.Join(fx.stage, "inputs/java/source-locks.sha256")) != "old-java\n" {
		t.Fatal("未变化的生态必须沿用套件输入")
	}
}

func TestCollectOnlineFailureKeepsOldInputsAndCleansUp(t *testing.T) {
	fx := setupOnline(t, true)
	fx.fake.locks["java"] = "new-java\n"
	fx.fake.failKinds["java"] = true
	_, err := fx.collect(t)
	if err == nil || !strings.Contains(err.Error(), "java 依赖收集失败") || !strings.Contains(err.Error(), "不能防镜像篡改") {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(fx.stage, "inputs/java/source-locks.sha256")) != "old-java\n" || readText(t, filepath.Join(fx.stage, "inputs/java/repository/org/old/lib/1.0/lib-1.0.pom")) != "old-pom" {
		t.Fatal("失败后旧输入必须保持不变")
	}
	if _, err := os.Stat(filepath.Join(fx.stage, "online")); !os.IsNotExist(err) {
		t.Fatal("失败后应清理 scratch")
	}
}

func TestCollectOnlineWebWithoutBaselineWarns(t *testing.T) {
	fx := setupOnline(t, false)
	fx.fake.locks["web"] = "new-web\n"
	report, err := fx.collect(t)
	if err != nil || report == nil {
		t.Fatal(report, err)
	}
	if len(report.Added) != 0 || len(report.Warnings) == 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), "未能列出新增 Web 包") {
		t.Fatal("没有基线时必须警告而不是静默", report.Added, report.Warnings)
	}
}

func TestCollectOnlineWebZeroAddedWarns(t *testing.T) {
	fx := setupOnline(t, true)
	fx.fake.locks["web"] = "new-web\n"
	// 新锁与基线锁包集合相同：锁变化但解析出 0 项新增。
	if err := os.WriteFile(filepath.Join(fx.stage, "source/web/pnpm-lock.yaml"), []byte(webLockOld), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := fx.collect(t)
	if err != nil || report == nil || len(report.Added) != 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), "0 项") {
		t.Fatal(report, err)
	}
}

func TestCollectOnlineRejectsBadMirrorBeforeDocker(t *testing.T) {
	fx := setupOnline(t, true)
	fx.env["AURIGA_MAVEN_MIRROR_URL"] = "http://insecure.example/maven"
	if _, err := fx.collect(t); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatal(err)
	}
	if len(fx.fake.calls) != 0 {
		t.Fatal("不应启动容器", fx.fake.calls)
	}
}

func TestProxyWarnings(t *testing.T) {
	got := proxyWarnings(fakeEnv(map[string]string{"HTTP_PROXY": "http://127.0.0.1:7890", "https_proxy": "http://proxy.corp:3128"}))
	if len(got) != 1 || !strings.Contains(got[0], "HTTP_PROXY") {
		t.Fatal(got)
	}
}
