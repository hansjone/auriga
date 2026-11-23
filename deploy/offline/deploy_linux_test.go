//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSiteLockHelper 不是测试：子进程模式下拿锁并一直持有，供“进程被杀后释放”使用。
func TestSiteLockHelper(t *testing.T) {
	path := os.Getenv("AURIGA_SITE_LOCK_HELPER")
	if path == "" {
		t.Skip("仅作子进程辅助")
	}
	if _, err := acquireSiteLock(path, "deploy"); err != nil {
		fmt.Println("ERR", err)
		os.Exit(2)
	}
	fmt.Println("LOCKED")
	time.Sleep(time.Minute)
}

func TestSiteLockMutualExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auriga-data.oneclick-lock")
	release, err := acquireSiteLock(path, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSiteLock(path, "rollback"); err == nil || !strings.Contains(err.Error(), "deploy") || !strings.Contains(err.Error(), "rollback 未执行") {
		t.Fatalf("第二个任务应直接失败并说明正在进行的操作：%v", err)
	}
	release()
	again, err := acquireSiteLock(path, "backup")
	if err != nil {
		t.Fatalf("释放后应能再次拿锁：%v", err)
	}
	again()
	dir := filepath.Join(t.TempDir(), "old.oneclick-lock")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSiteLock(dir, "deploy"); err == nil || !strings.Contains(err.Error(), "旧版本") {
		t.Fatalf("旧版目录锁应给出说明：%v", err)
	}
	link := filepath.Join(t.TempDir(), "link.oneclick-lock")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSiteLock(link, "deploy"); err == nil {
		t.Fatal("锁文件是符号链接时应拒绝")
	}
}

func TestSiteLockReleasedWhenHolderKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auriga-data.oneclick-lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSiteLockHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "AURIGA_SITE_LOCK_HELPER="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.Contains(line, "LOCKED") {
		t.Fatalf("子进程未拿到锁：%q %v", line, err)
	}
	if _, err := acquireSiteLock(path, "deploy"); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("PID %d", cmd.Process.Pid)) {
		t.Fatalf("持锁期间应失败并给出持锁进程：%v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := acquireSiteLock(path, "deploy")
	if err != nil {
		t.Fatalf("持锁进程被杀后内核应释放锁：%v", err)
	}
	release()
}

func TestWithLockUsesFlock(t *testing.T) {
	stubSiteOwner(t, nil)
	if os.Geteuid() != 0 {
		t.Skip("trustedParent 需要 root 所有的祖先目录")
	}
	parent, err := os.MkdirTemp("/root", "sd2a-lock-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(parent)
	data := filepath.Join(parent, "x", "auriga-data")
	err = withLock(data, "deploy", func() error {
		if err := withLock(data, "upgrade", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "deploy") {
			t.Fatalf("嵌套拿锁应失败：%v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(data + ".oneclick-lock"); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("锁应是普通文件：%v", err)
	}
}

func TestLogDirAndReceiptFilesProtected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := ensureLogDir(dir); err == nil {
		t.Fatal("组或其他用户可写的日志目录应被拒绝")
	}
	if err := os.Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	old := siteLogDirCheck
	siteLogDirCheck = func(string) error { return nil }
	t.Cleanup(func() { siteLogDirCheck = old })
	link := filepath.Join(t.TempDir(), "logs")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := startRun(&siteConfig{Logs: &siteLogs{Dir: link}}, "/nonexistent/site.json", "deploy", nil, false); err == nil {
		t.Fatal("日志目录是符号链接时应拒绝")
	}
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "x.log")); err != nil {
		t.Fatal(err)
	}
	if f, err := openNoFollow(filepath.Join(dir, "x.log"), os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		_ = f.Close()
		t.Fatal("O_NOFOLLOW 应拒绝写入符号链接")
	}
	run, err := startRun(&siteConfig{Logs: &siteLogs{Dir: dir}}, "/nonexistent/site.json", "rollback", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = run.finish(nil)
	for _, p := range []string{run.logPath, run.receiptPath} {
		info, err := os.Lstat(p)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s 权限应为 0600：%v", p, err)
		}
	}
	raw, _ := os.ReadFile(run.receiptPath)
	var rc siteReceipt
	if err := json.Unmarshal(raw, &rc); err != nil || rc.ExitCode != 0 || rc.Result != "success" || rc.Command != "rollback" {
		t.Fatalf("收据不对：%v %s", err, raw)
	}
}

func TestImportPartsRealOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	stubSiteOwner(t, nil)
	for i, mode := range []os.FileMode{0644, 0666} {
		name := filepath.Join(dir, fmt.Sprintf("x.tar.part%03d", i+1))
		if err := os.WriteFile(name, []byte("p"), mode); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(name, mode)
	}
	manifest := `{"schemaVersion":1,"original":{"name":"x.tar","size":2,"sha256":"` + strings.Repeat("0", 64) + `"},"parts":[{"name":"x.tar.part001","size":1,"sha256":"` + strings.Repeat("0", 64) + `"},{"name":"x.tar.part002","size":1,"sha256":"` + strings.Repeat("0", 64) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, "x.tar.parts.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	err := checkImportParts(filepath.Join(dir, "x.tar.part001"))
	if err == nil || !strings.Contains(err.Error(), "part002") {
		t.Fatalf("0666 的第二个分卷应被拒绝：%v", err)
	}
}

func TestCopyAndUnpackPreserveExplicitModesUnderRestrictiveUmask(t *testing.T) {
	oldMask := syscall.Umask(0077)
	defer syscall.Umask(oldMask)

	base := t.TempDir()
	srcBin := filepath.Join(base, "src-bin")
	if err := os.WriteFile(srcBin, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srcBin, 0755); err != nil {
		t.Fatal(err)
	}

	dstBin := filepath.Join(base, "copied", "auriga-nas-bootstrap")
	if err := copyFile(srcBin, dstBin, 0755); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dstBin); err != nil || st.Mode().Perm() != 0755 {
		t.Fatalf("copyFile under umask 0077 got mode %o, want 0755 (err=%v)", st.Mode().Perm(), err)
	}

	srcScript := filepath.Join(base, "installer.sh")
	if err := os.WriteFile(srcScript, []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dstScript := filepath.Join(base, "copied", "installer.sh")
	if err := copySourceFile(srcScript, dstScript, "deploy/nas/scripts/installer.sh", 0755); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dstScript); err != nil || st.Mode().Perm() != 0755 {
		t.Fatalf("copySourceFile under umask 0077 got mode %o, want 0755 (err=%v)", st.Mode().Perm(), err)
	}

	archiveRoot := filepath.Join(base, "archive-src")
	if err := os.MkdirAll(archiveRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(srcBin, filepath.Join(archiveRoot, "bin/boot"), 0755); err != nil {
		t.Fatal(err)
	}
	archiveFile := filepath.Join(base, "pkg.tar")
	if err := pack(archiveRoot, archiveFile); err != nil {
		t.Fatal(err)
	}
	unpackDir := filepath.Join(base, "unpacked")
	if err := unpack(archiveFile, unpackDir); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(unpackDir, "bin/boot")); err != nil || st.Mode().Perm() != 0755 {
		t.Fatalf("unpack under umask 0077 got mode %o, want 0755 (err=%v)", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Join(unpackDir, "bin")); err != nil || st.Mode().Perm() != 0755 {
		t.Fatalf("unpack dir under umask 0077 got mode %o, want 0755 (err=%v)", st.Mode().Perm(), err)
	}
}

func TestReleaseStageAndCopyTreeNormalizePermissionsUnderUmask0077(t *testing.T) {
	stubTrustedSource(t)
	oldMask := syscall.Umask(0077)
	defer syscall.Umask(oldMask)

	ctx := context.Background()
	base := t.TempDir()

	// 故意构造 0700 目录与 0600/0700 文件作为输入，模拟受 umask 0077 污染的源产物。
	mk := func(path string, content string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}

	// 1. copyTreeContext：源目录 0700、静态文件 0600、可执行文件 0700 -> 目标全 0755/0644/0755。
	webSrc := filepath.Join(base, "artifacts", "web")
	mk(filepath.Join(webSrc, "panel/index.html"), "<html>ok</html>\n", 0600)
	mk(filepath.Join(webSrc, "panel/assets/app.js"), "console.log(1)\n", 0600)
	mk(filepath.Join(webSrc, "portal/index.html"), "<html>portal</html>\n", 0600)
	mk(filepath.Join(webSrc, "admin/index.html"), "<html>admin</html>\n", 0600)
	mk(filepath.Join(webSrc, "panel/run.sh"), "#!/bin/sh\n", 0700)
	if err := writeSumsContext(ctx, webSrc); err != nil {
		t.Fatal(err)
	}
	copiedTree := filepath.Join(base, "copied-web")
	if err := copyTreeContext(ctx, webSrc, copiedTree); err != nil {
		t.Fatal(err)
	}
	assertTreePerms(t, copiedTree, map[string]fs.FileMode{
		"panel/run.sh": 0755,
	})

	// 2. stageService（edge）：包含 go 可执行文件（源 0700）、web 静态目录（源 0600/0700）、dirs 空目录；
	// 顶层 stage 保持 0700 不被放宽，scratch 内所有目录 0755、普通文件 0644、可执行文件 0755。
	r, err := loadDeployRecipe(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	goSrc := filepath.Join(base, "artifacts", "go")
	for _, bin := range []string{"auriga-nas-bootstrap", "runtime-release", "auriga-edge", "auriga-ai-gateway", "auriga-egress", "auriga-network-broker", "auriga-network-probe", "auriga-panel"} {
		mk(filepath.Join(goSrc, bin), "bin-"+bin, 0700)
	}
	if err := writeSumsContext(ctx, goSrc); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(base, ".auriga-deploy-stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(stage, "assemble", "edge")
	if err := mkdirAllExact(scratch, 0755); err != nil {
		t.Fatal(err)
	}
	if err := stageService(ctx, "edge", r.Services["edge"], repoRoot(t), filepath.Join(base, "artifacts"), map[string]string{}, scratch); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(stage); err != nil || st.Mode().Perm() != 0700 {
		t.Fatalf("顶层敏感 stage 目录权限应保持 0700，实际 %o (err=%v)", st.Mode().Perm(), err)
	}
	assertTreePerms(t, scratch, map[string]fs.FileMode{
		"go/auriga-edge":           0755,
		"go/auriga-ai-gateway":     0755,
		"go/auriga-egress":         0755,
		"go/auriga-network-broker": 0755,
		"go/auriga-network-probe":  0755,
		"go/auriga-panel":          0755,
		"web/panel/run.sh":         0755,
	})
	for _, emptyDir := range []string{"directories/policies", "directories/reports"} {
		if st, err := os.Stat(filepath.Join(scratch, filepath.FromSlash(emptyDir))); err != nil || !st.IsDir() || st.Mode().Perm() != 0755 {
			t.Fatalf("stageService dirs %s 权限应为 0755，实际 %o (err=%v)", emptyDir, st.Mode().Perm(), err)
		}
	}

	// 3. writeReleaseDir：在 umask 0077 下生成完整发行目录，目录全 0755、普通文件全 0644、可执行文件全 0755。
	srcTree := filepath.Join(base, "src")
	mk(filepath.Join(srcTree, "deploy/nas/scripts/start.sh"), "#!/bin/sh\necho ok\n", 0700)
	mk(filepath.Join(srcTree, "deploy/nas/docker-compose.yml"), "services: {}\n", 0600)
	s := deployTestSite(t)
	m := releaseManifest{
		Images:    imagesFor("1"),
		Source:    deploySource{Commit: strings.Repeat("a", 40)},
		CreatedAt: "2026-11-23T00:00:00Z",
	}
	prov := &provenance{Version: "local-test", Commit: strings.Repeat("a", 40), BuiltAt: "2026-11-23T00:00:00Z"}
	fpRep := &fingerprintReport{Artifacts: map[string]artifactFingerprint{"web-panel": {Digest: "abc"}}}
	final, _, err := writeReleaseDir(ctx, s.Data, 1, srcTree, goSrc, filepath.Join(base, "artifacts"), m, prov, fpRep, &orchInputs{Data: s.Data, Access: s.Access})
	if err != nil {
		t.Fatal(err)
	}
	execFiles := map[string]fs.FileMode{
		"bin/auriga-nas-bootstrap":            0755,
		"bin/runtime-release":                 0755,
		"deploy-nas/bin/auriga-nas-bootstrap": 0755,
		"deploy-nas/bin/runtime-release":      0755,
		"deploy-nas/scripts/start.sh":         0755,
		"artifacts/go/auriga-nas-bootstrap":   0755,
		"artifacts/go/runtime-release":        0755,
		"artifacts/go/auriga-edge":            0755,
		"artifacts/go/auriga-ai-gateway":      0755,
		"artifacts/go/auriga-egress":          0755,
		"artifacts/go/auriga-network-broker":  0755,
		"artifacts/go/auriga-network-probe":   0755,
		"artifacts/go/auriga-panel":           0755,
		"artifacts/web/panel/run.sh":          0755,
	}
	assertTreePerms(t, final, execFiles)
}

func assertTreePerms(t *testing.T, root string, execFiles map[string]fs.FileMode) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		got := info.Mode().Perm()
		if d.IsDir() {
			if got != 0755 {
				t.Errorf("目录 %s 权限为 %04o，期望 0755", rel, got)
			}
			return nil
		}
		want := fs.FileMode(0644)
		if w, ok := execFiles[rel]; ok {
			want = w
		}
		if got != want {
			t.Errorf("文件 %s 权限为 %04o，期望 %04o", rel, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
