//go:build linux

package main

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mavenLockScript 取出 java-inputs.sh 里的锁文件函数（maven_lock_list / maven_lock_entries /
// verify_maven_lock），在真实 sh 下运行，与 Go 实现对照。
func mavenLockScript(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"sh", "sha256sum", "find", "sort", "xargs", "awk", "comm", "cut"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("缺少 " + tool)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "nas", "offline-build", "java-inputs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "# Maven 锁文件 services/maven-deps.sha256")
	end := strings.Index(s, "# hash-lock：从已")
	if start < 0 || end < start {
		t.Fatal("java-inputs.sh 锁文件函数定位失败")
	}
	return s[start:end]
}

func runMavenLockScript(t *testing.T, funcs, body string, env ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "set -eu\n"+funcs+body)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestMavenLockScriptParity(t *testing.T) {
	funcs := mavenLockScript(t)
	repo := t.TempDir()
	files := map[string]string{
		"org/ex/lib/1.0/lib-1.0.jar":                                   "jar",
		"org/ex/lib/1.0/lib-1.0.pom":                                   "pom",
		"org/ex/lib/1.0/lib-1.0-sources.jar":                           "src",
		"org/ex/lib/1.0/lib-1.0.jar.sha1":                              "x",
		"org/ex/lib/1.0/_remote.repositories":                          "central",
		"org/ex/lib/1.0/lib-1.0.pom.lastUpdated":                       "x",
		"org/ex/lib/maven-metadata-central.xml":                        "<m/>",
		"org/ex/lib/maven-metadata-central.xml.sha1":                   "x",
		"org/ex/lib/resolver-status.properties":                        "x",
		"com/github/eirslett/node/22.1.0/node-22.1.0-linux-x64.tar.gz": "node",
		"org/ex/parent/2/parent-2.pom":                                 "parent",
	}
	for rel, body := range files {
		putFile(t, filepath.Join(repo, filepath.FromSlash(rel)), body, 0644)
	}
	gen, err := generateMavenDepsLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	var goLines []string
	for _, line := range strings.Split(string(gen), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			goLines = append(goLines, line)
		}
	}
	out := filepath.Join(t.TempDir(), "list")
	_, stderr, err := runMavenLockScript(t, funcs, "maven_lock_list '"+repo+"' '"+out+"'\n")
	if err != nil {
		t.Fatalf("maven_lock_list 失败：%v\n%s", err, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != strings.Join(goLines, "\n") {
		t.Fatalf("脚本与 Go 生成的锁条目不一致：\n脚本:\n%s\nGo:\n%s", got, strings.Join(goLines, "\n"))
	}
	if len(goLines) != 5 {
		t.Fatalf("应有 5 个构件（非构件文件不参与）：%v", goLines)
	}

	lock := filepath.Join(t.TempDir(), "maven-deps.sha256")
	putFile(t, lock, string(gen), 0644)
	verify := func(when string, env ...string) (string, error) {
		_, stderr, err := runMavenLockScript(t, funcs, "verify_maven_lock '"+repo+"' '"+lock+"' "+when+"\n", env...)
		return stderr, err
	}
	if stderr, err := verify("build"); err != nil {
		t.Fatalf("锁与仓库一致时 build 核对应通过：%v\n%s", err, stderr)
	}
	// 非构件文件变化不影响核对。
	putFile(t, filepath.Join(repo, "org/ex/lib/1.0/_remote.repositories"), "other", 0644)
	if stderr, err := verify("build"); err != nil {
		t.Fatalf("非构件文件变化不应影响核对：%v\n%s", err, stderr)
	}
	// 摘要不符、多出、缺少。
	putFile(t, filepath.Join(repo, "org/ex/lib/1.0/lib-1.0.jar"), "tampered", 0644)
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "摘要不符") || !strings.Contains(stderr, "org.ex:lib:jar:1.0") {
		t.Fatalf("摘要不符应失败并列出：%v\n%s", err, stderr)
	}
	if stderr, err := verify("prepare"); err == nil {
		t.Fatalf("非空锁时 prepare 也应核对失败：%s", stderr)
	}
	putFile(t, filepath.Join(repo, "org/ex/lib/1.0/lib-1.0.jar"), "jar", 0644)
	putFile(t, filepath.Join(repo, "org/ex/new/1/new-1.jar"), "new", 0644)
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "多出") || !strings.Contains(stderr, "org.ex:new:jar:1") {
		t.Fatalf("多出应失败并列出：%v\n%s", err, stderr)
	}
	if stderr, err := verify("prepare", "AURIGA_MAVEN_LOCK_UPDATE=1"); err != nil || !strings.Contains(stderr, "警告") {
		t.Fatalf("AURIGA_MAVEN_LOCK_UPDATE=1 的 prepare 应只警告：%v\n%s", err, stderr)
	}
	if stderr, err := verify("build", "AURIGA_MAVEN_LOCK_UPDATE=1"); err == nil {
		t.Fatalf("AURIGA_MAVEN_LOCK_UPDATE 不能放过 build 核对：%s", stderr)
	}
	_ = os.Remove(filepath.Join(repo, "org/ex/new/1/new-1.jar"))
	_ = os.Remove(filepath.Join(repo, "org/ex/parent/2/parent-2.pom"))
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "缺少") || !strings.Contains(stderr, "org.ex:parent:pom:2") {
		t.Fatalf("缺少应失败并列出：%v\n%s", err, stderr)
	}
	putFile(t, filepath.Join(repo, "org/ex/parent/2/parent-2.pom"), "parent", 0644)
	// 推不出坐标的文件与符号链接不能被悄悄跳过（脚本与 Go 都拒绝）。
	odd := filepath.Join(repo, "org/ex/lib/1.0/evil.jar")
	putFile(t, odd, "x", 0644)
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "无法识别") {
		t.Fatalf("推不出坐标的文件应失败：%v\n%s", err, stderr)
	}
	if _, err := listMavenRepoArtifacts(repo); err == nil {
		t.Fatal("Go 侧推不出坐标的文件应失败")
	}
	_ = os.Remove(odd)
	link := filepath.Join(repo, "org/ex/lib/1.0/lib-1.0-tests.jar")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "符号链接") {
		t.Fatalf("符号链接应失败：%v\n%s", err, stderr)
	}
	if _, err := listMavenRepoArtifacts(repo); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("Go 侧符号链接应失败：%v", err)
	}
	_ = os.Remove(link)
	// 空锁：build 失败，prepare 只警告；锁文件格式错误两种模式都失败。
	putFile(t, lock, "# 只有注释\n", 0644)
	if stderr, err := verify("build"); err == nil || !strings.Contains(stderr, "缺少非空 Maven 锁文件") {
		t.Fatalf("空锁时 build 应失败：%v\n%s", err, stderr)
	}
	if stderr, err := verify("prepare"); err != nil || !strings.Contains(stderr, "警告") {
		t.Fatalf("空锁时 prepare 应只警告：%v\n%s", err, stderr)
	}
	for name, body := range map[string]string{
		"格式":   "org.ex:lib:jar:1.0  " + strings.Repeat("a", 64) + "\n",
		"摘要":   "org.ex:lib:jar:1.0 " + strings.Repeat("a", 63) + "\n",
		"大写":   "org.ex:lib:jar:1.0 " + strings.Repeat("A", 64) + "\n",
		"坐标":   "org.ex:lib:1.0 " + strings.Repeat("a", 64) + "\n",
		"重复坐标": "org.ex:lib:jar:1.0 " + strings.Repeat("a", 64) + "\norg.ex:lib:jar:1.0 " + strings.Repeat("b", 64) + "\n",
	} {
		putFile(t, lock, body, 0644)
		if stderr, err := verify("prepare"); err == nil {
			t.Errorf("锁文件%s错误应失败：%s", name, stderr)
		}
		if _, err := parseMavenDepsLock([]byte(body)); err == nil {
			t.Errorf("Go 侧锁文件%s错误应失败", name)
		}
	}
}

// TestPythonLockScriptParity：python-inputs.sh locks 写出的锁清单与 CLI 的 Go 实现逐字节一致。
func TestPythonLockScriptParity(t *testing.T) {
	for _, tool := range []string{"sh", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("缺少 " + tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	cmd := exec.Command("sh", filepath.Join(root, "deploy/nas/offline-build/python-inputs.sh"), "locks", out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python-inputs.sh locks 失败：%v\n%s", err, stderr.String())
	}
	want, err := os.ReadFile(filepath.Join(out, "source-locks.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ecosystemLocks(root, "python")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("python 锁清单与脚本不一致：\nCLI:\n%s\n脚本:\n%s", got, want)
	}
}

func TestVerifyMavenDistribution(t *testing.T) {
	funcs := mavenLockScript(t)
	if !strings.Contains(funcs, "verify_maven_distribution()") || !strings.Contains(funcs, "stage_verified_maven_distribution()") {
		t.Fatal("java-inputs.sh 须含 verify_maven_distribution 与 stage_verified_maven_distribution")
	}
	dir := t.TempDir()
	props := filepath.Join(dir, "maven-wrapper.properties")
	zipName := "apache-maven-3.9.16-bin.zip"
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, err := zw.Create("apache-maven-3.9.16/bin/mvn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("#!/bin/sh\necho clean-mvn\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := zbuf.Bytes()
	sum := sha256Hex(body)
	if err := os.WriteFile(props, []byte(
		"distributionUrl=https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.16/"+zipName+"\n"+
			"distributionSha256Sum="+sum+"\n",
	), 0644); err != nil {
		t.Fatal(err)
	}
	dists := filepath.Join(dir, "dists", "apache-maven-3.9.16-bin", "hash")
	if err := os.MkdirAll(filepath.Join(dists, "apache-maven-3.9.16", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// 在输入目录故意放置被篡改的预解压文件，验证 stage_verified_maven_distribution 不会使用它。
	if err := os.WriteFile(filepath.Join(dists, "apache-maven-3.9.16", "bin", "mvn"), []byte("#!/bin/sh\necho tampered\n"), 0755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dists, zipName)
	if err := os.WriteFile(zipPath, body, 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runMavenLockScript(t, funcs, "verify_maven_distribution '"+filepath.Join(dir, "dists")+"' '"+props+"'\n")
	if err != nil {
		t.Fatalf("期望通过：%v\n%s", err, stderr)
	}
	_, hasUnzip := exec.LookPath("unzip")
	_, hasJar := exec.LookPath("jar")
	if hasUnzip == nil || hasJar == nil {
		staged := filepath.Join(dir, "staged-dists")
		_, stderr, err = runMavenLockScript(t, funcs, "stage_verified_maven_distribution '"+filepath.Join(dir, "dists")+"' '"+staged+"' '"+props+"'\n")
		if err != nil {
			t.Fatalf("stage_verified_maven_distribution 应成功：%v\n%s", err, stderr)
		}
		gotMvn, err := os.ReadFile(filepath.Join(staged, "apache-maven-3.9.16-bin", "hash", "apache-maven-3.9.16", "bin", "mvn"))
		if err != nil {
			t.Fatalf("应从已复核 zip 现解开 mvn：%v", err)
		}
		if !strings.Contains(string(gotMvn), "clean-mvn") || strings.Contains(string(gotMvn), "tampered") {
			t.Fatalf("应使用 zip 现解开的干净内容，实际：%q", string(gotMvn))
		}
	}
	if err := os.WriteFile(zipPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = runMavenLockScript(t, funcs, "verify_maven_distribution '"+filepath.Join(dir, "dists")+"' '"+props+"'\n")
	if err == nil || !strings.Contains(stderr, "发行包摘要不符") {
		t.Fatalf("篡改 zip 应失败：err=%v stderr=%s", err, stderr)
	}
	_ = os.Remove(zipPath)
	_, stderr, err = runMavenLockScript(t, funcs, "verify_maven_distribution '"+filepath.Join(dir, "dists")+"' '"+props+"'\n")
	if err == nil || !strings.Contains(stderr, "应恰好有一份") {
		t.Fatalf("缺 zip 应失败：err=%v stderr=%s", err, stderr)
	}
}

func TestPrepareMirroredMavenDistributionKeepsCanonicalCache(t *testing.T) {
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("需要 JDK")
	}
	funcs := mavenLockScript(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"maven-wrapper.jar", "DistributionCachePath.java"} {
		data, err := os.ReadFile(filepath.Join(root, ".mvn", "wrapper", name))
		if err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(dir, ".mvn", "wrapper", name), string(data), 0644)
	}
	const name = "apache-maven-3.9.16-bin.zip"
	const content = "pinned Maven ZIP bytes"
	putFile(t, filepath.Join(dir, ".mvn", "wrapper", "maven-wrapper.properties"),
		"distributionUrl=https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.16/"+name+"\n"+
			"distributionSha256Sum="+sha256Hex([]byte(content))+"\n", 0644)
	putFile(t, filepath.Join(dir, ".mvn", "wrapper", "bootstrap.sh"), `#!/bin/sh
set -eu
test "$MVNW_REPOURL" = https://mirror.example/maven
mkdir -p "$MAVEN_USER_HOME/wrapper/dists/mirror-key"
printf '%s' 'pinned Maven ZIP bytes' > "$MAVEN_USER_HOME/wrapper/dists/mirror-key/`+name+`"
touch "$scratch/downloaded"
`, 0644)
	body := `
cd "$fixture"
src="$fixture"
export scratch="$fixture/work"
export MAVEN_USER_HOME="$fixture/cache"
prepare_mirrored_maven_distribution https://mirror.example/maven
test -f "$scratch/downloaded"
test "$MAVEN_DISTRIBUTION_ZIP" = "$MAVEN_USER_HOME/wrapper/dists/apache-maven-3.9.16-bin/4200fbc6/` + name + `"
test "$(cat "$MAVEN_DISTRIBUTION_ZIP")" = 'pinned Maven ZIP bytes'
rm "$scratch/downloaded"
prepare_mirrored_maven_distribution https://mirror.example/maven
test ! -e "$scratch/downloaded"
printf '%s' 'tampered' > "$MAVEN_DISTRIBUTION_ZIP"
prepare_mirrored_maven_distribution https://mirror.example/maven
`
	_, stderr, err := runMavenLockScript(t, funcs, body, "fixture="+dir)
	if err == nil || !strings.Contains(stderr, "Maven 发行包摘要不符") {
		t.Fatalf("应从镜像预填并复用已验证的 canonical 缓存，拒绝替换字节：%v\n%s", err, stderr)
	}
}

func TestPrepareContractNodeUsesLockedFileMirror(t *testing.T) {
	funcs := mavenLockScript(t)
	dir := t.TempDir()
	const version = "22.19.0"
	const name = "node-v" + version + "-linux-x64.tar.gz"
	const fixture = "locked linux Node tarball"
	putFile(t, filepath.Join(dir, "services", "contracts", "pom.xml"),
		"<nodeVersion>v"+version+"</nodeVersion>\n", 0644)
	putFile(t, filepath.Join(dir, "services", "maven-deps.sha256"),
		"com.github.eirslett:node:linux-x64:tar.gz:"+version+" "+sha256Hex([]byte(fixture))+"\n", 0644)
	putFile(t, filepath.Join(dir, "mirror", "dist", "v"+version, name), fixture, 0644)
	body := `
cd "$fixture"
repo="$fixture/repository"
export AURIGA_JAVA_FILES="file://$fixture/mirror"
prepare_contract_node
test "$(cat "$node_archive")" = 'locked linux Node tarball'
rm "$fixture/mirror/dist/v` + version + `/` + name + `"
prepare_contract_node
rm "$node_archive"
printf '%s' 'invalid' > "$fixture/mirror/` + name + `"
prepare_contract_node
`
	_, stderr, err := runMavenLockScript(t, funcs, body, "fixture="+dir)
	if err == nil || !strings.Contains(stderr, "contracts Node 归档缺失或摘要不符") {
		t.Fatalf("应从站点目录预填并复用锁定归档，拒绝被换字节的镜像：%v\n%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "repository", "com", "github", "eirslett", "node", version, "node-"+version+"-linux-x64.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("摘要失败不应写入本地 Maven 缓存：%v", err)
	}
}
