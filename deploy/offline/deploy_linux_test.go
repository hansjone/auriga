//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
