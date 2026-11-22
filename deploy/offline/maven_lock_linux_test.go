//go:build linux

package main

import (
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
