//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadSiteConfigRootDirect(t *testing.T) {
	stubSiteOwner(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "site.json")
	writeTestFileAt(t, path, designSiteJSON)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, got, err := loadSiteConfig(path, siteInvocation{Root: true})
	if err != nil || got != path || cfg.Deps.Cache != "/srv/auriga/cache" {
		t.Fatalf("root 直接运行可用 --config：%v", err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSiteConfig(path, siteInvocation{Root: true}); err == nil || !strings.Contains(err.Error(), "0600 或 0644") {
		t.Fatalf("组/其他可写的配置应拒绝：%v", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSiteConfig(link, siteInvocation{Root: true}); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("符号链接配置应拒绝：%v", err)
	}
	// 真实属主：非 root 运行测试时文件不属于 root，应拒绝。
	if os.Geteuid() != 0 {
		siteOwnerOf = fileOwnerUID
		if _, _, err := loadSiteConfig(path, siteInvocation{Root: true}); err == nil || !strings.Contains(err.Error(), "root 所有") {
			t.Fatalf("非 root 所有的配置应拒绝：%v", err)
		}
	}
}

func TestDepsCachePermissions(t *testing.T) {
	stubSiteOwner(t, nil)
	cache := filepath.Join(t.TempDir(), "cache")
	if ok, err := ensureDepsCache(cache, false); ok || err != nil {
		t.Fatalf("不存在的缓存只报告不存在：%v %v", ok, err)
	}
	if ok, err := ensureDepsCache(cache, true); !ok || err != nil {
		t.Fatalf("应创建缓存：%v", err)
	}
	if err := os.Chmod(cache, 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDepsCache(cache, false); err == nil || !strings.Contains(err.Error(), "不能被组或其他用户写") {
		t.Fatalf("组可写缓存应拒绝：%v", err)
	}
	if err := os.Chmod(cache, 0755); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		siteOwnerOf = fileOwnerUID
		if _, err := ensureDepsCache(cache, false); err == nil || !strings.Contains(err.Error(), "root 所有") {
			t.Fatalf("非 root 所有的缓存应拒绝：%v", err)
		}
	}
	stubSiteOwner(t, errors.New("父目录可写"))
	if _, err := ensureDepsCache(cache, false); err == nil || !strings.Contains(err.Error(), "父目录可写") {
		t.Fatalf("祖先不受信应拒绝：%v", err)
	}
}

type depsFetchFixture struct {
	src, cache string
	cfg        *siteConfig
	calls      []string
	envs       [][]string
	locks      map[string]ecosystemLock
	tamper     bool
}

func setupDepsFetch(t *testing.T) *depsFetchFixture {
	t.Helper()
	stubSiteOwner(t, nil)
	fx := &depsFetchFixture{src: fakeSourceTree(t), cache: filepath.Join(t.TempDir(), "cache"), locks: map[string]ecosystemLock{}}
	npm := "https://npm.internal.example"
	fx.cfg = &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: fx.cache, Sources: siteSources{Go: []string{"https://goproxy.internal.example"}, NPM: &npm}}}
	locks, err := sourceEcosystemLocks(fx.src)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locks {
		fx.locks[l.Ecosystem] = l
	}
	if _, err := ensureDepsCache(fx.cache, true); err != nil {
		t.Fatal(err)
	}
	makeImageRecord(t, fx.cache, "tool-go", false)
	makeImageRecord(t, fx.cache, "tool-web", false)
	oldRun, oldImage, oldReady, oldLookup := runOnlineDocker, ensureDepsImage, depsDockerReady, lookupOnlineEnv
	t.Cleanup(func() {
		runOnlineDocker, ensureDepsImage, depsDockerReady, lookupOnlineEnv = oldRun, oldImage, oldReady, oldLookup
	})
	lookupOnlineEnv = fakeEnv(map[string]string{"GOPROXY": "https://proxy.golang.org"})
	ensureDepsImage = func(_ context.Context, img imageHit) (string, error) { return img.Image.runtimeRef(), nil }
	depsDockerReady = func(context.Context, buildBudget) error { return nil }
	runOnlineDocker = func(_ context.Context, env []string, args ...string) error {
		kind := strings.TrimSuffix(filepath.Base(args[len(args)-3]), "-inputs.sh")
		fx.calls = append(fx.calls, kind)
		fx.envs = append(fx.envs, env)
		out := mountsOf(args)["/output"]
		files := map[string]string{}
		switch kind {
		case "go":
			files["download/example.com/m/@v/v1.0.0.zip"] = "zip"
			files["download/example.com/m/@v/v1.0.0.mod"] = "mod"
		case "web":
			files["web-cache.tar.gz"] = "store"
			files["corepack.tgz"] = "pnpm"
		}
		locks := fx.locks[kind].Raw
		if fx.tamper {
			locks = []byte("different\n")
		}
		writeTestFileAt(t, filepath.Join(out, "source-locks.sha256"), string(locks))
		var sums strings.Builder
		for name, body := range files {
			writeTestFileAt(t, filepath.Join(out, filepath.FromSlash(name)), body)
			sum, _ := digest(filepath.Join(out, filepath.FromSlash(name)))
			fmt.Fprintf(&sums, "%s  %s\n", sum, name)
		}
		fmt.Fprintf(&sums, "%s  source-locks.sha256\n", lockDigestOf(locks))
		writeTestFileAt(t, filepath.Join(out, "SHA256SUMS"), sums.String())
		return nil
	}
	oldLock := depsCacheLock
	t.Cleanup(func() { depsCacheLock = oldLock })
	return fx
}

func TestDepsFetchWithoutAcceptOnlyPrintsList(t *testing.T) {
	fx := setupDepsFetch(t)
	var out bytes.Buffer
	err := depsFetch(context.Background(), fx.cfg, "site.json", fx.src, depsFetchOptions{Out: &out, Budget: buildBudget{MemoryMiB: 4096, CPUs: 2}})
	if !errors.Is(err, errNewDepsRejected) {
		t.Fatalf("acceptNewDeps=false 应以错误（退出码 1）结束：%v", err)
	}
	if len(fx.calls) != 0 {
		t.Fatalf("不应运行任何准备：%v", fx.calls)
	}
	for _, want := range []string{"[go]", "可自动补", "[java]", "不能自动补", "[system]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("清单缺少 %q：\n%s", want, out.String())
		}
	}
}

func TestDepsFetchWritesCacheAndRefusesJava(t *testing.T) {
	fx := setupDepsFetch(t)
	var out bytes.Buffer
	err := depsFetch(context.Background(), fx.cfg, "site.json", fx.src, depsFetchOptions{Accept: true, Out: &out, Budget: buildBudget{MemoryMiB: 4096, CPUs: 2}})
	if err == nil || !strings.Contains(err.Error(), "java") || !strings.Contains(err.Error(), "system") || !strings.Contains(err.Error(), "python") {
		t.Fatalf("java/python/system 缺失应报不能自动补：%v", err)
	}
	if strings.Join(fx.calls, ",") != "go,web" {
		t.Fatalf("只应准备 Go 与 Web：%v", fx.calls)
	}
	if !contains(fx.envs[0], "AURIGA_GO_PREPARE_PROXY=https://goproxy.internal.example") {
		t.Fatalf("Go 代理应来自配置而不是主机 GOPROXY：%v", fx.envs[0])
	}
	for _, eco := range []string{"go", "web"} {
		dir := filepath.Join(fx.cache, "inputs", eco, fx.locks[eco].Digest)
		if err := verifyInputEntry(dir, eco, fx.locks[eco].Digest); err != nil {
			t.Fatalf("%s 缓存条目无效：%v", eco, err)
		}
		var origin depsOrigin
		if err := readJSON(filepath.Join(dir, depsOriginFile), &origin); err != nil || origin.Kind != "fetch" || origin.SumsSHA256 == "" {
			t.Fatalf("%s 来源记录：%+v %v", eco, origin, err)
		}
		var report siteReport
		if err := readJSON(filepath.Join(dir, depsReportFile), &report); err != nil || report.LockDigest != fx.locks[eco].Digest {
			t.Fatalf("%s 新增依赖清单：%v", eco, err)
		}
		if eco == "go" && (len(report.Added) != 1 || report.Added[0].Name != "example.com/m" || report.Added[0].SHA256 == "") {
			t.Fatalf("Go 新增清单应含模块与 SHA-256：%+v", report.Added)
		}
		if eco == "web" && len(report.Added) != 2 {
			t.Fatalf("Web 新增清单：%+v", report.Added)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.cache, "inputs", "web", fx.locks["web"].Digest, depsWebLockFile)); err != nil {
		t.Fatal("Web 条目应保存 pnpm-lock 副本")
	}
	// 再次 status：go/web 命中缓存。
	rep, _, _, err := computeDepsStatus(fx.cfg, "site.json", fx.src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Missing, ",") != "java,python,system" {
		t.Fatalf("status 缺失：%v", rep.Missing)
	}
	var jsonOut bytes.Buffer
	printDepsStatus(&jsonOut, rep)
	if !strings.Contains(jsonOut.String(), "命中 缓存") {
		t.Fatalf("status 输出：\n%s", jsonOut.String())
	}
}

func TestDepsFetchRejectsLockMismatch(t *testing.T) {
	fx := setupDepsFetch(t)
	fx.tamper = true
	err := depsFetch(context.Background(), fx.cfg, "site.json", fx.src, depsFetchOptions{Accept: true, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("锁清单不一致应拒绝：%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fx.cache, "inputs", "go")); len(entries) != 0 {
		t.Fatal("不应写入缓存")
	}
	leftovers, _ := filepath.Glob(filepath.Join(fx.cache, ".staging-*"))
	if len(leftovers) != 0 {
		t.Fatalf("暂存目录应清理：%v", leftovers)
	}
}

func TestDepsFetchCacheLockBlocksConcurrentWriter(t *testing.T) {
	fx := setupDepsFetch(t)
	unlock, err := lockDepsCache(fx.cache)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// 另一个打开的文件描述符拿不到 flock。
	if _, err := lockDepsCache(fx.cache); err == nil || !strings.Contains(err.Error(), "正在写缓存") {
		t.Fatalf("并发写缓存应失败：%v", err)
	}
}

func TestDepsImportIntoCache(t *testing.T) {
	stubSiteOwner(t, nil)
	src := fakeSourceTree(t)
	lock := goLocksOf(t, src)
	kit := filepath.Join(t.TempDir(), "kit")
	makeInputEntry(t, kit, "go", lock.Raw, map[string]string{"download/a.zip": "a"})
	makeImageRecord(t, kit, "base-edge", true)
	key, _ := testSigningKeys(t)
	signTestRoot(t, kit, key)
	fp, _ := publicFingerprint(filepath.Join(kit, "release-public.pem"))
	cache := filepath.Join(t.TempDir(), "cache")
	cfg := &siteConfig{SchemaVersion: 1, Deps: &siteDeps{Cache: cache}}
	var out bytes.Buffer
	if err := depsImport(context.Background(), cfg, kit, depsImportOptions{Fingerprint: "SHA256:" + fp, Out: &out}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "inputs", "go", lock.Digest)
	if err := verifyInputEntry(dir, "go", lock.Digest); err != nil {
		t.Fatal(err)
	}
	var origin depsOrigin
	if err := readJSON(filepath.Join(dir, depsOriginFile), &origin); err != nil || origin.Kind != "import" || !origin.Signed || origin.Fingerprint != fp || origin.From[0] != kit {
		t.Fatalf("导入来源记录：%+v %v", origin, err)
	}
	hits, err := readImageRecords(cache, "base-edge")
	if err != nil || len(hits) != 1 {
		t.Fatalf("镜像记录应导入：%v %d", err, len(hits))
	}
	// 再导入一次：跳过已有条目；归档形式同样可导入。
	archive := filepath.Join(t.TempDir(), "kit.tar")
	if err := pack(kit, archive); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := depsImport(context.Background(), cfg, archive, depsImportOptions{Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "跳过 2 个") || !strings.Contains(out.String(), "未用 --trust-fingerprint") {
		t.Fatalf("重复导入应跳过并提示未核验：\n%s", out.String())
	}
	// 篡改后拒绝，缓存不变。
	writeTestFileAt(t, filepath.Join(kit, "inputs", "go", lock.Digest, "download", "a.zip"), "evil")
	if err := depsImport(context.Background(), cfg, kit, depsImportOptions{Out: &out}); err == nil {
		t.Fatal("篡改的导入内容应拒绝")
	}
	leftovers, _ := filepath.Glob(filepath.Join(cache, ".import-*"))
	if len(leftovers) != 0 {
		t.Fatalf("导入暂存应清理：%v", leftovers)
	}
}

// TestDepsLockScriptParity 直接拿配方脚本里的锁清单片段在真实源码上运行，
// 与 CLI 的 Go 实现逐字节比较，防止两者不同步。
func TestDepsLockScriptParity(t *testing.T) {
	for _, tool := range []string{"sh", "sha256sum", "tar", "find", "sort", "xargs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("缺少 " + tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	recipe := filepath.Join(root, "deploy/nas/offline-build")
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(recipe, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	snippets := map[string]string{}
	goScript := read("go-inputs.sh")
	m := regexp.MustCompile(`(?s)\nsnapshot\(\) \(\n.*?\n\)\n`).FindString(goScript)
	if m == "" {
		t.Fatal("go-inputs.sh 中找不到 snapshot()")
	}
	snippets["go"] = m + "snapshot\n"
	for _, eco := range []string{"web", "java"} {
		s := read(eco + "-inputs.sh")
		start := strings.Index(s, "workspace=\"$scratch/source\"")
		end := strings.Index(s, "# locks:")
		if start < 0 || end < start {
			t.Fatalf("%s-inputs.sh 锁清单片段定位失败", eco)
		}
		snippets[eco] = s[start:end] + "cat \"$scratch/source-locks\"\n"
	}
	for eco, body := range snippets {
		scratch := t.TempDir()
		script := "set -eu\nexport LC_ALL=C\nsrc='" + root + "'\nscratch='" + scratch + "'\n" + body
		cmd := exec.Command("sh", "-c", script)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s 脚本片段失败：%v\n%s", eco, err, stderr.String())
		}
		got, err := ecosystemLocks(root, eco)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s 锁清单与脚本不一致：\nCLI:\n%s\n脚本:\n%s", eco, got, want)
		}
	}
}

func TestDepsStatusJSONShape(t *testing.T) {
	fx := setupDepsFetch(t)
	rep, _, _, err := computeDepsStatus(fx.cfg, "site.json", fx.src)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"ecosystem":"go"`, `"present":false`, `"autoFetch":true`, `"role":"tool-go"`, `"missing":["go","web","java","python","system"]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON 缺少 %s：%s", want, raw)
		}
	}
}

func TestDepsFetchJavaSeedCacheRWAndIsolation(t *testing.T) {
	stubSiteOwner(t, nil)
	src := fakeSourceTree(t)

	oldPomBody := "<project><artifactId>old</artifactId></project>\n"
	newJarBody := "new-reactor-common-jar"
	oldPomHash := lockDigestOf([]byte(oldPomBody))
	newJarHash := lockDigestOf([]byte(newJarBody))

	// 模拟新增 POM/构件后的新源码树与锁文件。
	writeTestFileAt(t, filepath.Join(src, "services", "iam", "api", "pom.xml"), "<project><artifactId>iam-api</artifactId></project>\n")
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(mavenDepsLockRel)),
		"org.old:lib:pom:1.0 "+oldPomHash+"\n"+
			"org.new:reactor-common:jar:2.0 "+newJarHash+"\n")

	locksSlice, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	locks := map[string]ecosystemLock{}
	for _, l := range locksSlice {
		locks[l.Ecosystem] = l
	}

	cache := filepath.Join(t.TempDir(), "cache")
	if _, err := ensureDepsCache(cache, true); err != nil {
		t.Fatal(err)
	}
	npm := "https://npm.internal.example"
	maven := "https://maven.internal.example/repo"
	cfg := &siteConfig{
		SchemaVersion: 1,
		Deps: &siteDeps{
			Cache: cache,
			Sources: siteSources{
				Go:    []string{"https://goproxy.internal.example"},
				NPM:   &npm,
				Maven: &maven,
			},
		},
	}

	makeImageRecord(t, cache, "tool-go", false)
	makeImageRecord(t, cache, "tool-web", false)
	makeImageRecord(t, cache, "tool-java", false)

	// 写入旧缓存条目作为增量 seed（锁内容与当前源码不同）。
	oldGoLock := []byte("old-go-lock\n")
	oldWebLock := []byte("old-web-lock\n")
	oldJavaLock := []byte("old-java-lock\n")
	oldGoSeed := makeInputEntry(t, cache, "go", oldGoLock, map[string]string{
		"download/example.com/old/@v/v1.0.0.zip": "old-zip",
	})
	oldWebSeed := makeInputEntry(t, cache, "web", oldWebLock, map[string]string{
		"web-cache.tar.gz": "old-store",
		"corepack.tgz":     "old-pnpm",
	})
	oldJavaSeed := makeInputEntry(t, cache, "java", oldJavaLock, map[string]string{
		"repository/org/old/lib/1.0/lib-1.0.pom": oldPomBody,
		"npm-cacache/x":                          "old-npm",
		"wrapper/dists/d":                        "old-wrapper",
	})

	oldRun, oldImage, oldReady, oldLookup := runOnlineDocker, ensureDepsImage, depsDockerReady, lookupOnlineEnv
	t.Cleanup(func() {
		runOnlineDocker, ensureDepsImage, depsDockerReady, lookupOnlineEnv = oldRun, oldImage, oldReady, oldLookup
	})
	lookupOnlineEnv = fakeEnv(nil)
	ensureDepsImage = func(_ context.Context, img imageHit) (string, error) { return img.Image.runtimeRef(), nil }
	depsDockerReady = func(context.Context, buildBudget) error { return nil }

	seenRW := map[string]bool{}
	seenCache := map[string]string{}
	runOnlineDocker = func(_ context.Context, _ []string, args ...string) error {
		kind := strings.TrimSuffix(filepath.Base(args[len(args)-3]), "-inputs.sh")
		mounts := mountsOf(args)
		out := mounts["/output"]
		cacheDir := mounts["/prepare-cache"]
		_, rw := mountsRW(args)
		seenCache[kind] = cacheDir
		seenRW[kind] = rw

		files := map[string]string{}
		switch kind {
		case "go":
			if cacheDir == "" || rw {
				return fmt.Errorf("go 缓存挂载异常：cache=%q rw=%v", cacheDir, rw)
			}
			files["download/example.com/m/@v/v1.0.0.zip"] = "zip"
		case "web":
			if cacheDir == "" || rw {
				return fmt.Errorf("web 缓存挂载异常：cache=%q rw=%v", cacheDir, rw)
			}
			files["web-cache.tar.gz"] = "store"
			files["corepack.tgz"] = "pnpm"
		case "java":
			if cacheDir == "" {
				return errors.New("java 缺少 /prepare-cache 挂载")
			}
			// 复现容器只读挂载时的真实失败行为。
			if !rw {
				return errors.New("cp: cannot create regular file '/prepare-cache/source-locks': Read-only file system")
			}
			// 验证 prepareCache 布局已到位。
			for _, rel := range []string{"repository/org/old/lib/1.0/lib-1.0.pom", "npm-cache/_cacache/x", "maven/wrapper/dists/d"} {
				if _, err := os.Stat(filepath.Join(cacheDir, filepath.FromSlash(rel))); err != nil {
					return fmt.Errorf("java seed 副本缺少 %s：%w", rel, err)
				}
			}
			// 模拟 java-inputs.sh:278-282 写入 /prepare-cache/source-locks 与新构件缓存。
			if err := os.WriteFile(filepath.Join(cacheDir, "source-locks"), locks["java"].Raw, 0644); err != nil {
				return err
			}
			writeTestFileAt(t, filepath.Join(cacheDir, "repository", "org", "new", "reactor-common", "2.0", "reactor-common-2.0.jar"), newJarBody)

			files["repository/org/old/lib/1.0/lib-1.0.pom"] = oldPomBody
			files["repository/org/new/reactor-common/2.0/reactor-common-2.0.jar"] = newJarBody
			files["npm-cacache/x"] = "old-npm"
			files["wrapper/dists/d"] = "old-wrapper"
		}

		rawLock := locks[kind].Raw
		writeTestFileAt(t, filepath.Join(out, "source-locks.sha256"), string(rawLock))
		var sums strings.Builder
		for name, body := range files {
			writeTestFileAt(t, filepath.Join(out, filepath.FromSlash(name)), body)
			sum, _ := digest(filepath.Join(out, filepath.FromSlash(name)))
			fmt.Fprintf(&sums, "%s  %s\n", sum, name)
		}
		fmt.Fprintf(&sums, "%s  source-locks.sha256\n", lockDigestOf(rawLock))
		writeTestFileAt(t, filepath.Join(out, "SHA256SUMS"), sums.String())
		return nil
	}

	var out bytes.Buffer
	err = depsFetch(context.Background(), cfg, "site.json", src, depsFetchOptions{
		Accept: true,
		Out:    &out,
		Budget: buildBudget{MemoryMiB: 4096, CPUs: 2},
	})
	// python 和 system 未配置/不可自动补，期望只报这两项；go/web/java 应全部成功。
	if err == nil || strings.Contains(err.Error(), "java") || !strings.Contains(err.Error(), "system") {
		t.Fatalf("期望 go/web/java 成功，仅拒绝 python/system，得到：%v", err)
	}
	if !seenRW["java"] || seenRW["go"] || seenRW["web"] {
		t.Fatalf("只有 Java seed 副本应可写，其余保持只读：%+v", seenRW)
	}
	if seenCache["go"] != filepath.Join(oldGoSeed, "download") {
		t.Fatalf("go 应只读挂载旧 seed download：%q", seenCache["go"])
	}
	if seenCache["web"] != oldWebSeed {
		t.Fatalf("web 应只读挂载旧 seed：%q", seenCache["web"])
	}
	if seenCache["java"] == oldJavaSeed || strings.HasPrefix(seenCache["java"], oldJavaSeed+string(filepath.Separator)) {
		t.Fatalf("java 不可把旧只读正式输入直接挂可写：mount=%q oldSeed=%q", seenCache["java"], oldJavaSeed)
	}
	if !strings.Contains(seenCache["java"], ".staging-") {
		t.Fatalf("java 可写缓存应位于暂存隔离目录：%q", seenCache["java"])
	}

	// 新 Java 缓存条目应通过完整性与新源码 Maven 锁校验。
	newJavaDir := filepath.Join(cache, "inputs", "java", locks["java"].Digest)
	if err := verifyInputEntry(newJavaDir, "java", locks["java"].Digest); err != nil {
		t.Fatalf("新 Java 缓存条目校验失败：%v", err)
	}
	if err := verifyImportedJavaAgainstLock(src, newJavaDir); err != nil {
		t.Fatalf("新 Java 缓存条目未通过源码锁校验：%v", err)
	}

	// 旧只读正式输入条目必须保持原样、未被写入 source-locks 或新构件。
	if _, err := os.Stat(filepath.Join(oldJavaSeed, "source-locks")); !os.IsNotExist(err) {
		t.Fatalf("旧 Java 正式输入不应被写入 source-locks：%v", err)
	}
	if err := verifyInputEntry(oldJavaSeed, "java", lockDigestOf(oldJavaLock)); err != nil {
		t.Fatalf("旧 Java 正式输入应保持完整未改动：%v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(cache, ".staging-*")); len(leftovers) != 0 {
		t.Fatalf("暂存目录应清理：%v", leftovers)
	}

	// 容器失败时错误提示不应仅归因于网络。
	runOnlineDocker = func(_ context.Context, _ []string, _ ...string) error {
		return errors.New("docker 执行失败 exit status 1")
	}
	err = fetchEcosystem(context.Background(), cfg, newDepsResolver(cfg), src, locks["java"], buildBudget{MemoryMiB: 4096, CPUs: 2}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "准备容器执行失败") {
		t.Fatalf("容器失败应给出准确错误提示：%v", err)
	}
}
