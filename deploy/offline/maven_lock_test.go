package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMavenDepsLock(t *testing.T) {
	raw := []byte(`# 注释
a.b:c:jar:1.0 ` + strings.Repeat("a", 64) + `
a.b:c:sources:jar:1.0 ` + strings.Repeat("b", 64) + `
`)
	ents, err := parseMavenDepsLock(raw)
	if err != nil || len(ents) != 2 {
		t.Fatalf("解析失败：%v %#v", err, ents)
	}
	if _, err := parseMavenDepsLock([]byte("bad line\n")); err == nil {
		t.Fatal("格式错应失败")
	}
	if _, err := parseMavenDepsLock([]byte("a:b:jar:1 nothex\n")); err == nil {
		t.Fatal("非 64 位摘要应失败")
	}
	dup := "a:b:jar:1 " + strings.Repeat("c", 64) + "\na:b:jar:1 " + strings.Repeat("d", 64) + "\n"
	if _, err := parseMavenDepsLock([]byte(dup)); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复坐标应失败：%v", err)
	}
}

func TestMavenCoordFromRepoPath(t *testing.T) {
	cases := map[string]string{
		"org/example/foo/1.0/foo-1.0.jar":                              "org.example:foo:jar:1.0",
		"org/example/foo/1.0/foo-1.0.pom":                              "org.example:foo:pom:1.0",
		"org/example/foo/1.0/foo-1.0-sources.jar":                      "org.example:foo:sources:jar:1.0",
		"com/github/eirslett/node/20.0.0/node-20.0.0-linux-x64.tar.gz": "com.github.eirslett:node:linux-x64:tar.gz:20.0.0",
	}
	for rel, want := range cases {
		got, ok := mavenCoordFromRepoPath(rel)
		if !ok || got != want {
			t.Errorf("%s → %q（期望 %q）", rel, got, want)
		}
	}
}

func TestVerifyMavenRepoAgainstLock(t *testing.T) {
	repo := t.TempDir()
	art := filepath.Join(repo, "org", "ex", "lib", "1.0")
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	body := []byte("jar-bytes")
	putFile(t, filepath.Join(art, "lib-1.0.jar"), string(body), 0644)
	putFile(t, filepath.Join(art, "_remote.repositories"), "central\n", 0644)
	putFile(t, filepath.Join(art, "lib-1.0.jar.lastUpdated"), "x", 0644)
	sum, err := digest(filepath.Join(art, "lib-1.0.jar"))
	if err != nil {
		t.Fatal(err)
	}
	lock := []mavenLockEntry{{Coord: "org.ex:lib:jar:1.0", SHA256: sum}}
	if err := verifyMavenRepoAgainstLock(repo, lock); err != nil {
		t.Fatal(err)
	}
	// 多出
	putFile(t, filepath.Join(art, "lib-1.0.pom"), "<pom/>", 0644)
	if err := verifyMavenRepoAgainstLock(repo, lock); err == nil || !strings.Contains(err.Error(), "多出") {
		t.Fatalf("多出应失败：%v", err)
	}
	// 缺少
	_ = os.Remove(filepath.Join(art, "lib-1.0.pom"))
	_ = os.Remove(filepath.Join(art, "lib-1.0.jar"))
	if err := verifyMavenRepoAgainstLock(repo, lock); err == nil || !strings.Contains(err.Error(), "缺少") {
		t.Fatalf("缺少应失败：%v", err)
	}
	// 摘要不符
	putFile(t, filepath.Join(art, "lib-1.0.jar"), "other", 0644)
	if err := verifyMavenRepoAgainstLock(repo, lock); err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("摘要不符应失败：%v", err)
	}
}

func TestVerifyImportedJavaRejectsExtra(t *testing.T) {
	src := t.TempDir()
	sum := strings.Repeat("a", 64)
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(mavenDepsLockRel)), "org.ex:lib:jar:1.0 "+sum+"\n")
	entry := t.TempDir()
	art := filepath.Join(entry, "repository", "org", "ex", "lib", "1.0")
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(art, "lib-1.0.jar"), "x", 0644)
	putFile(t, filepath.Join(art, "lib-1.0.pom"), "y", 0644)
	err := verifyImportedJavaAgainstLock(src, entry)
	if err == nil || !strings.Contains(err.Error(), "多出") && !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("不在锁内或摘要不符的构件应拒绝：%v", err)
	}
}

func TestGenerateMavenDepsLockStable(t *testing.T) {
	repo := t.TempDir()
	art := filepath.Join(repo, "a", "b", "1.0")
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(art, "b-1.0.jar"), "x", 0644)
	putFile(t, filepath.Join(art, "b-1.0.pom"), "y", 0644)
	a, err := generateMavenDepsLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateMavenDepsLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("生成结果不稳定")
	}
	ents, err := parseMavenDepsLock(a)
	if err != nil || len(ents) != 2 {
		t.Fatalf("生成后无法解析：%v %d", err, len(ents))
	}
}

func TestCheckImportedJavaEntries(t *testing.T) {
	src := fakeSourceTree(t)
	sum := lockDigestOf([]byte("jar")) // sha256("jar")
	writeTestFileAt(t, filepath.Join(src, filepath.FromSlash(mavenDepsLockRel)), "org.ex:lib:jar:1.0 "+sum+"\n")
	locks, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	for _, l := range locks {
		if l.Ecosystem == "java" {
			current = l.Digest
		}
	}
	root, cache := t.TempDir(), t.TempDir()
	rel := "inputs/java/" + current
	putFile(t, filepath.Join(root, filepath.FromSlash(rel), "repository/org/ex/lib/1.0/lib-1.0.jar"), "jar", 0644)
	opts := depsImportOptions{Source: func() (string, error) { return src, nil }}
	entries := []importEntry{{Rel: rel, Ecosystem: "java", LockDigest: current}}
	if err := checkImportedJavaEntries(cache, entries, root, opts); err != nil {
		t.Fatalf("与锁一致的 Java 条目应通过：%v", err)
	}
	if err := checkImportedJavaEntries(cache, entries, root, depsImportOptions{}); err == nil {
		t.Fatal("没有源码时导入 Java 应拒绝")
	}
	other := []importEntry{{Rel: "inputs/java/" + strings.Repeat("0", 64), Ecosystem: "java", LockDigest: strings.Repeat("0", 64)}}
	if err := checkImportedJavaEntries(cache, other, root, opts); err == nil || !strings.Contains(err.Error(), "锁摘要") {
		t.Fatalf("锁摘要与当前源码不同的 Java 条目应拒绝：%v", err)
	}
	putFile(t, filepath.Join(root, filepath.FromSlash(rel), "repository/org/ex/extra/1/extra-1.jar"), "x", 0644)
	if err := checkImportedJavaEntries(cache, entries, root, opts); err == nil || !strings.Contains(err.Error(), "多出") {
		t.Fatalf("不在锁里的构件应拒绝：%v", err)
	}
	// 缓存里已有同名条目时不导入也不核对。
	if err := os.MkdirAll(filepath.Join(cache, filepath.FromSlash(rel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := checkImportedJavaEntries(cache, entries, root, opts); err != nil {
		t.Fatalf("缓存已有的条目应跳过核对：%v", err)
	}
}

func TestListMavenRepoRejectsUnknownFiles(t *testing.T) {
	repo := t.TempDir()
	putFile(t, filepath.Join(repo, "org/ex/lib/1.0/lib-1.0.jar"), "x", 0644)
	putFile(t, filepath.Join(repo, "org/ex/lib/1.0/notes.txt"), "x", 0644)
	if _, err := listMavenRepoArtifacts(repo); err == nil || !strings.Contains(err.Error(), "无法识别") {
		t.Fatalf("推不出坐标的文件不能被跳过：%v", err)
	}
	_ = os.Remove(filepath.Join(repo, "org/ex/lib/1.0/notes.txt"))
	putFile(t, filepath.Join(repo, "README"), "x", 0644)
	if _, err := listMavenRepoArtifacts(repo); err == nil {
		t.Fatal("仓库根下的杂项文件不能被跳过")
	}
}

// TestSiteConfigPyPISource：deps.sources.pypi 与 npm/maven 同规则——单个地址、只允许 https:// 或 file://、不能含凭据。
func TestSiteConfigPyPISource(t *testing.T) {
	cfg := func(v string) string {
		return `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache","sources":{"pypi":` + v + `}}}`
	}
	for _, ok := range []string{`"https://pypi.internal.example/simple"`, `"file:///srv/auriga/pypi"`} {
		c, err := parseSiteConfig([]byte(cfg(ok)))
		if err != nil || c.Deps.Sources.PyPI == nil {
			t.Fatalf("%s 应接受：%v", ok, err)
		}
	}
	for v, want := range map[string]string{
		`["https://a.example/simple"]`:          "只能是单个地址",
		`"http://pypi.internal.example/simple"`: "只允许 https:// 或 file://",
		`"https://u:p@pypi.internal.example/s"`: "不能内嵌",
	} {
		if _, err := parseSiteConfig([]byte(cfg(v))); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("deps.sources.pypi %s 应拒绝（%s）：%v", v, want, err)
		}
	}
	src := "https://pypi.internal.example/simple"
	lookup, _, err := depsPrepareSettings(&siteDeps{Cache: "/srv/auriga/cache", Sources: siteSources{PyPI: &src}}, "python", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := lookup("PIP_INDEX_URL"); got != src {
		t.Fatalf("PIP_INDEX_URL 应为配置的 pypi 源：%q", got)
	}
}
