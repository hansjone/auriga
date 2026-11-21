package main

// 统一现场部署 2a（下）：受限调用判定、参数白名单、快进与 --allow-downgrade、提交签名、
// local-deploy 的 verifyStatePackage 与 doctor、签名包 upgrade 接入新布局、自动回滚前核对
// 上一版发行目录。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func runnerConfig(name string) *siteConfig {
	return &siteConfig{Server: &siteServer{Data: "/srv/auriga/auriga-data", Runner: strPtr(name)}}
}

func stubRunnerUID(t *testing.T, users map[string]string) {
	t.Helper()
	old := lookupRunnerUID
	lookupRunnerUID = func(name string) (string, error) {
		if uid, ok := users[name]; ok {
			return uid, nil
		}
		return "", fmt.Errorf("unknown user %s", name)
	}
	t.Cleanup(func() { lookupRunnerUID = old })
}

func stubSudoConfig(t *testing.T, cfg *siteConfig, err error) {
	t.Helper()
	old := loadSudoSiteConfig
	loadSudoSiteConfig = func(siteInvocation) (*siteConfig, error) { return cfg, err }
	t.Cleanup(func() { loadSudoSiteConfig = old })
}

func TestSiteConfigRunnerField(t *testing.T) {
	base := `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache"},"server":{"data":"/srv/auriga","runner":%s}}`
	if cfg, err := parseSiteConfig([]byte(fmt.Sprintf(base, `"forgejo-runner"`))); err != nil || *cfg.Server.Runner != "forgejo-runner" {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{`"root"`, `"Runner"`, `"a b"`, `""`, `"-x"`, `1`, `"` + strings.Repeat("a", 33) + `"`} {
		if _, err := parseSiteConfig([]byte(fmt.Sprintf(base, bad))); err == nil {
			t.Fatalf("server.runner=%s 未被拒绝", bad)
		}
	}
}

func TestResolvePrivilegeMatrix(t *testing.T) {
	stubRunnerUID(t, map[string]string{"runner": "1001"})
	cfg := runnerConfig("runner")
	runner := siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}
	admin := siteInvocation{Sudo: true, Root: true, SudoUID: "1000"} // sudo -i 的管理员同样带 SUDO_UID
	root := siteInvocation{Root: true}
	cases := []struct {
		name       string
		inv        siteInvocation
		cfg        *siteConfig
		operator   bool
		restricted bool
		errLike    string
	}{
		{"运行器", runner, cfg, false, true, ""},
		{"运行器用 --operator", runner, cfg, true, true, "运行器"},
		{"sudo -i 管理员未加 --operator", admin, cfg, false, true, ""},
		{"sudo -i 管理员加 --operator", admin, cfg, true, false, ""},
		{"无 SUDO_UID 的 root", root, cfg, false, false, ""},
		{"无 SUDO_UID 的 root 加 --operator", root, nil, true, false, ""},
		{"未登记运行器时 --operator", admin, &siteConfig{Server: &siteServer{}}, true, true, "server.runner"},
		{"未登记运行器、无 --operator", admin, nil, false, true, ""},
		{"运行器账户解析失败时 --operator", admin, runnerConfig("ghost"), true, true, "无法解析"},
		{"运行器账户解析失败", runner, runnerConfig("ghost"), false, true, ""},
	}
	for _, c := range cases {
		restricted, err := resolvePrivilege(c.inv, c.cfg, c.operator)
		if c.errLike != "" {
			if err == nil || !strings.Contains(err.Error(), c.errLike) {
				t.Fatalf("%s：应报错含 %q：%v", c.name, c.errLike, err)
			}
			continue
		}
		if err != nil || restricted != c.restricted {
			t.Fatalf("%s：restricted=%t err=%v，期望 %t", c.name, restricted, err, c.restricted)
		}
	}
	if !runnerOnly(runner, cfg) || runnerOnly(admin, cfg) || runnerOnly(root, cfg) || !runnerOnly(runner, runnerConfig("ghost")) {
		t.Fatal("status/history 只对运行器受限（解析失败时失败关闭）")
	}
}

func TestGuardSudoCommand(t *testing.T) {
	stubRunnerUID(t, map[string]string{"runner": "1001"})
	runner := siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}
	admin := siteInvocation{Sudo: true, Root: true, SudoUID: "1000"}
	stubSudoConfig(t, runnerConfig("runner"), nil)
	for _, args := range [][]string{{"install", "--address", "192.0.2.10"}, {"upgrade", "/x.tar"}, {"trust", "add-site-key", "x"}, {"deps", "fetch"}, {"bundle", "--build"}, {"doctor"}, {"deploy", "--help"}, {"rollback", "-h"}} {
		if err := guardSudoCommand(args, runner); err == nil {
			t.Fatalf("运行器经 sudo 运行 %v 应被拒绝", args)
		}
	}
	for _, args := range [][]string{{"deploy", strings.Repeat("a", 40)}, {"rollback"}, {"status"}, {"history", "--json"}} {
		if err := guardSudoCommand(args, runner); err != nil {
			t.Fatalf("%v 应放行到命令自己的白名单：%v", args, err)
		}
	}
	if err := guardSudoCommand([]string{"install", "--address", "192.0.2.10"}, admin); err != nil {
		t.Fatalf("管理员经 sudo 的旧命令应照旧：%v", err)
	}
	if err := guardSudoCommand([]string{"trust"}, siteInvocation{Root: true}); err != nil {
		t.Fatal(err)
	}
	stubSudoConfig(t, nil, nil) // 没有 site.json：旧签名包站点经 sudo 运维照旧
	if err := guardSudoCommand([]string{"upgrade", "/x.tar"}, runner); err != nil {
		t.Fatal(err)
	}
	stubSudoConfig(t, nil, errors.New("权限不对"))
	if err := guardSudoCommand([]string{"upgrade", "/x.tar"}, admin); err == nil {
		t.Fatal("site.json 读不出来时应失败关闭")
	}
}

func TestRestrictedArgsWhitelist(t *testing.T) {
	c := strings.Repeat("a", 40)
	good := map[string][][]string{
		"deploy":   {{c}, {c, "--full"}, {"--services", "edge,platform", c}, {c, "--services=edge"}, {c, "--dry-run", "--json"}, {c, "--backup-db"}},
		"rollback": {{}, {"--to", "3"}, {"--to=12", "--json"}},
		"history":  {{}, {"--json"}},
		"status":   {{}},
	}
	bad := map[string][][]string{
		"deploy": {
			{}, {c, c}, {strings.ToUpper(c)}, {c[:39]}, {c + "0"}, {"HEAD"},
			{c, "--config", "/tmp/site.json"}, {c, "--config=/tmp/x"}, {c, "--source", "/home/x"}, {c, "--allow-downgrade"},
			{c, "--operator"}, {c, "--accept-new-deps"}, {c, "--memory-mib", "1"}, {c, "-full"}, {c, "--full=true"},
			{c, "--full", "--full"}, {c, "--services"}, {c, "--services="}, {c, "--services", "--json"}, {c, "--"},
			{"--full", c, "--json"},
		},
		"rollback": {{"--to"}, {"--to", "0"}, {"--to", "x"}, {"--to", "01"}, {"--to", "-1"}, {"--config", "/x"}, {"--json", "--json"}, {"3"}, {"--operator"}},
		"history":  {{"--config", "/x"}, {"--data", "/x"}},
		"status":   {{"--data", "/home/runner/fake"}, {"--json"}},
		"install":  {{}},
	}
	for cmd, list := range good {
		for _, args := range list {
			if err := restrictedArgs(cmd, args); err != nil {
				t.Fatalf("%s %v 应接受：%v", cmd, args, err)
			}
		}
	}
	for cmd, list := range bad {
		for _, args := range list {
			if err := restrictedArgs(cmd, args); err == nil {
				t.Fatalf("%s %v 应拒绝", cmd, args)
			}
		}
	}
}

func TestSitePrivilegeAppliesWhitelistAndStripsOperator(t *testing.T) {
	stubRunnerUID(t, map[string]string{"runner": "1001"})
	stubSudoConfig(t, runnerConfig("runner"), nil)
	c := strings.Repeat("b", 40)
	runner := siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}
	admin := siteInvocation{Sudo: true, Root: true, SudoUID: "1000"}
	if _, _, err := sitePrivilege("deploy", []string{c, "--source", "/home/runner/src"}, runner); err == nil {
		t.Fatal("运行器的 --source 应被白名单拒绝")
	}
	if _, _, err := sitePrivilege("deploy", []string{c, "--operator"}, runner); err == nil || !strings.Contains(err.Error(), "运行器") {
		t.Fatalf("运行器不能用 --operator：%v", err)
	}
	if _, _, err := sitePrivilege("deploy", []string{c, "--allow-downgrade"}, admin); err == nil {
		t.Fatal("未加 --operator 的 sudo 调用应受限")
	}
	rest, restricted, err := sitePrivilege("deploy", []string{c, "--allow-downgrade", "--operator"}, admin)
	if err != nil || restricted || strings.Join(rest, " ") != c+" --allow-downgrade" {
		t.Fatalf("管理员加 --operator 应不受限并去掉 --operator：%v %t %v", rest, restricted, err)
	}
	if _, _, err := sitePrivilege("deploy", []string{"--operator", "--operator"}, admin); err == nil {
		t.Fatal("重复 --operator 应拒绝")
	}
	if _, _, err := sitePrivilege("deploy", []string{"--operator=true"}, admin); err == nil {
		t.Fatal("--operator=值 应拒绝")
	}
	rest, restricted, err = sitePrivilege("rollback", []string{"--to", "2", "--json"}, runner)
	if err != nil || !restricted || len(rest) != 3 {
		t.Fatalf("运行器 rollback 白名单内参数应通过：%v %t %v", rest, restricted, err)
	}
	if err := deployCommand([]string{c, "--config", "/tmp/x"}, runner); err == nil || !strings.Contains(err.Error(), "受限") {
		t.Fatalf("deployCommand 应在读配置前按白名单拒绝：%v", err)
	}
	if err := rollbackCommand([]string{"--to", "2", "--config", "/tmp/x"}, runner); err == nil || !strings.Contains(err.Error(), "受限") {
		t.Fatalf("rollbackCommand 应按白名单拒绝：%v", err)
	}
	if err := historyCommand([]string{"--config", "/tmp/x"}, runner, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "受限") {
		t.Fatalf("运行器的 history 只接受 --json：%v", err)
	}
}

// fakeGit 模拟暂存仓库里的祖先关系：ancestors[a] 列出 a 是其祖先的提交（含自身）。
func fakeGit(t *testing.T, present map[string]bool, ancestors map[string][]string) {
	t.Helper()
	stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name != "git" || len(r.Args) < 2 {
			return "", fmt.Errorf("意外命令 %v", r.Args)
		}
		switch r.Args[1] {
		case "cat-file":
			if present[strings.TrimSuffix(r.Args[3], "^{commit}")] {
				return "", nil
			}
			return "", errors.New("missing")
		case "merge-base":
			a, b := r.Args[3], r.Args[4]
			if a == b {
				return "", nil
			}
			for _, d := range ancestors[a] {
				if d == b {
					return "", nil
				}
			}
			return "", errors.New("exit status 1")
		}
		return "", fmt.Errorf("意外 git %v", r.Args)
	})
}

func TestFastForwardAndDowngrade(t *testing.T) {
	old, mid, cur, next, side := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40), strings.Repeat("4", 40), strings.Repeat("5", 40)
	present := map[string]bool{old: true, mid: true, cur: true, next: true, side: true}
	fakeGit(t, present, map[string][]string{old: {mid, cur, next, side}, mid: {cur, next}, cur: {next}})
	v1 := &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}
	v3 := &dataVersions{Migrations: map[string]string{"auriga/x": "3:3:c"}, Formats: map[string]string{}}
	s := deployTestSite(t,
		deployRecord{Seq: 1, Status: recordOK, Source: deploySource{Mode: "git", Commit: old}, Images: imagesFor("1"), Migrations: v1.Migrations, Formats: v1.Formats},
		deployRecord{Seq: 2, Status: recordOK, Source: deploySource{Mode: "git", Commit: cur}, Images: imagesFor("2"), Migrations: v3.Migrations, Formats: v3.Formats})
	prev := s.record(2)
	check := func(target string, o deployOptions) (*dataVersions, error) {
		return checkDeployDirection(context.Background(), s.Data, &s, prev, &deploySource{Mode: "git", Commit: target}, o, &siteReceipt{}, &bytes.Buffer{})
	}
	restricted := deployOptions{Restricted: true}
	if _, err := check(next, restricted); err != nil {
		t.Fatalf("后代应允许：%v", err)
	}
	if _, err := check(cur, restricted); err != nil {
		t.Fatalf("同一提交重新部署应允许：%v", err)
	}
	if _, err := check(mid, restricted); exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "快进") {
		t.Fatalf("受限调用部署祖先应拒绝（退出码 1）：%v", err)
	}
	if _, err := check(side, restricted); exitCodeOf(err) != 1 {
		t.Fatalf("受限调用部署分叉提交应拒绝：%v", err)
	}
	if _, err := check(next, deployOptions{Restricted: true, AllowDowngrade: true}); exitCodeOf(err) != 1 {
		t.Fatalf("受限调用不接受 --allow-downgrade：%v", err)
	}
	if _, err := check(mid, deployOptions{}); exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Fatalf("root 部署旧提交应要求 --allow-downgrade：%v", err)
	}
	// --allow-downgrade：数据与 r1（提交 old，是 mid 的祖先）运行时相同 → 允许。
	stubDataVersions(t, v1)
	if base, err := check(mid, deployOptions{AllowDowngrade: true}); err != nil || base == nil {
		t.Fatalf("数据未被更新版本触碰时应允许降级：%v", err)
	}
	// 数据是 r2 的（比目标新）→ 退出码 3。
	stubDataVersions(t, v3)
	if _, err := check(mid, deployOptions{AllowDowngrade: true}); exitCodeOf(err) != 3 {
		t.Fatalf("数据被更新版本触碰时降级应退出码 3：%v", err)
	}
	stubDataVersions(t, &dataVersions{Err: "psql 不可用"})
	if _, err := check(mid, deployOptions{AllowDowngrade: true}); exitCodeOf(err) != 3 {
		t.Fatalf("读不到版本时降级应退出码 3：%v", err)
	}
	// 当前提交不可比较（目录模式含改动）：受限拒绝，root 放行并记录。
	prev.Source = deploySource{Mode: "dir", Commit: cur, Dirty: true}
	if _, err := check(next, restricted); exitCodeOf(err) != 1 {
		t.Fatalf("当前提交不可比较时受限调用应拒绝：%v", err)
	}
	if _, err := check(next, deployOptions{}); err != nil {
		t.Fatalf("root 人工部署应放行：%v", err)
	}
	// 当前提交不在暂存仓库里：受限拒绝。
	prev.Source = deploySource{Mode: "git", Commit: strings.Repeat("9", 40)}
	if _, err := check(next, restricted); exitCodeOf(err) != 1 {
		t.Fatalf("当前提交不在暂存仓库时受限调用应拒绝：%v", err)
	}
	// 降级检查之后数据变了：deploySwitch 切换前复核，不切换（退出码 3）。
	s2, p2, rec, cfg := switchFixture(t)
	stubDataVersions(t, v3)
	site := &fakeSite{healthy: func(int) error { return nil }}
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s2, p2, rec, run, v1); exitCodeOf(err) != 3 || len(site.starts) != 0 {
		t.Fatalf("降级检查后数据变化应不切换：%v %v", err, site.starts)
	}
}

func TestDeployFirstInstallSkipsDirectionCheck(t *testing.T) {
	// 首装没有 state：doDeploy 只在有 state 时调用 checkDeployDirection；这里确认没有上一版时
	// comparableCommit 为空、不会被误当作可比较提交。
	if comparableCommit(nil) != "" || comparableCommit(&deployRecord{Source: deploySource{Commit: "abc"}}) != "" {
		t.Fatal("comparableCommit 判定不对")
	}
}

func TestAutoRollbackChecksPreviousReleaseDir(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	putFile(t, filepath.Join(prev.Release, "deploy-nas/scripts/start.sh"), "tampered", 0755)
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return errors.New("down") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || len(site.starts) != 1 || !strings.Contains(run.receipt.RollbackReason, "发行目录校验失败") {
		t.Fatalf("上一版发行目录被篡改时不应自动回滚：%v starts=%v %q", err, site.starts, run.receipt.RollbackReason)
	}
}

func TestSplitCommitSignature(t *testing.T) {
	raw := "tree t\nparent p\nauthor a\ncommitter c\ngpgsig -----BEGIN SSH SIGNATURE-----\n abc\n -----END SSH SIGNATURE-----\ngpgsig-sha256 x\n y\n\nmsg\n gpgsig not header\n"
	payload, sig, ok := splitCommitSignature([]byte(raw))
	if !ok || string(sig) != "-----BEGIN SSH SIGNATURE-----\nabc\n-----END SSH SIGNATURE-----\n" {
		t.Fatalf("签名拆分不对：%q", sig)
	}
	if string(payload) != "tree t\nparent p\nauthor a\ncommitter c\n\nmsg\n gpgsig not header\n" {
		t.Fatalf("被签名内容不对：%q", payload)
	}
	if _, _, ok := splitCommitSignature([]byte("tree t\n\nmsg\n")); ok {
		t.Fatal("未签名提交不应拆出签名")
	}
}

func TestLoadTrustedSigners(t *testing.T) {
	stubTrustedSource(t)
	dir := t.TempDir()
	if _, err := loadTrustedSigners(nil); err == nil {
		t.Fatal("签名者列表为空应拒绝")
	}
	putFile(t, filepath.Join(dir, "a.pub"), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEJ5c2tlbW9uIHRlc3Qga2V5IGJ5dGVzIDEyMzQ1Ng== ci@example\n# 注释\n", 0644)
	putFile(t, filepath.Join(dir, "b.asc"), "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nxx\n-----END PGP PUBLIC KEY BLOCK-----\n", 0644)
	set, err := loadTrustedSigners([]string{filepath.Join(dir, "a.pub"), filepath.Join(dir, "b.asc")})
	if err != nil || len(set.SSH) != 1 || len(set.PGP) != 1 || !strings.HasPrefix(set.SSH[0], sshPrincipal+` namespaces="git" ssh-ed25519 `) {
		t.Fatalf("%+v %v", set, err)
	}
	for name, body := range map[string]string{"opt": `cert-authority ssh-ed25519 AAAA`, "junk": "hello world\n", "empty": "\n# x\n", "b64": "ssh-ed25519 !!!\n"} {
		putFile(t, filepath.Join(dir, name), body, 0644)
		if _, err := loadTrustedSigners([]string{filepath.Join(dir, name)}); err == nil {
			t.Fatalf("%s 应拒绝", name)
		}
	}
}

// gitSign 在临时仓库里造提交：signArgs 为空时不签名。返回仓库 .git 目录与提交号。
func gitRun(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), append([]string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + dir, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}, env...)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Skipf("本机 git 签名环境不可用（%v）：%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func newSigRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	repo := t.TempDir()
	gitRun(t, repo, nil, "init", "-q")
	return repo
}

func commitIn(t *testing.T, repo string, env []string, sign ...string) string {
	t.Helper()
	args := append(append([]string{}, sign...), "commit", "-q", "--allow-empty", "-m", "x")
	if len(sign) > 0 {
		args = append(args, "-S")
	}
	gitRun(t, repo, env, args...)
	return gitRun(t, repo, nil, "rev-parse", "HEAD")
}

func TestCommitSignatureSSH(t *testing.T) {
	stubTrustedSource(t)
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("没有 ssh-keygen")
	}
	repo := newSigRepo(t)
	keys := t.TempDir()
	for _, name := range []string{"trusted", "other"} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", filepath.Join(keys, name)).CombinedOutput(); err != nil {
			t.Skipf("ssh-keygen 生成密钥失败：%v %s", err, out)
		}
	}
	signWith := func(key string) []string {
		return []string{"-c", "gpg.format=ssh", "-c", "user.signingkey=" + filepath.Join(keys, key)}
	}
	good := commitIn(t, repo, nil, signWith("trusted")...)
	other := commitIn(t, repo, nil, signWith("other")...)
	unsigned := commitIn(t, repo, nil)
	gitDir := filepath.Join(repo, ".git")
	signers := []string{filepath.Join(keys, "trusted.pub")}
	work := t.TempDir()
	if desc, err := verifyCommitSignature(context.Background(), gitDir, good, signers, work); err != nil || !strings.Contains(desc, "ssh") {
		t.Fatalf("受信签名应通过：%q %v", desc, err)
	}
	if _, err := verifyCommitSignature(context.Background(), gitDir, other, signers, work); err == nil {
		t.Fatal("非受信签名者应拒绝")
	}
	if _, err := verifyCommitSignature(context.Background(), gitDir, unsigned, signers, work); err == nil || !strings.Contains(err.Error(), "没有签名") {
		t.Fatalf("未签名提交应拒绝：%v", err)
	}
	if _, err := verifyCommitSignature(context.Background(), gitDir, good, nil, work); err == nil {
		t.Fatal("签名者列表为空应拒绝")
	}
	// 只配了 PGP 公钥时，ssh 签名的提交按非受信拒绝。
	putFile(t, filepath.Join(keys, "pgp.asc"), "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nxx\n-----END PGP PUBLIC KEY BLOCK-----\n", 0644)
	if _, err := verifyCommitSignature(context.Background(), gitDir, good, []string{filepath.Join(keys, "pgp.asc")}, work); err == nil || !strings.Contains(err.Error(), "不受信") {
		t.Fatalf("ssh 签名但没有 ssh 受信公钥应拒绝：%v", err)
	}
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Fatalf("签名校验的临时文件应清理：%v", entries)
	}
}

func TestCommitSignatureGPG(t *testing.T) {
	stubTrustedSource(t)
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("没有 gpg")
	}
	repo := newSigRepo(t)
	gen := func(name string) (home, fpr string) {
		home = filepath.Join(t.TempDir(), "g")
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = exec.Command("gpgconf", "--homedir", home, "--kill", "all").Run() })
		gpg := func(args ...string) string {
			c := exec.Command("gpg", append([]string{"--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase", ""}, args...)...)
			out, err := c.CombinedOutput()
			if err != nil {
				t.Skipf("gpg 不可用（%v）：%s", err, out)
			}
			return string(out)
		}
		gpg("--quick-gen-key", name+" <"+name+"@example.com>", "ed25519", "sign", "never")
		for _, line := range strings.Split(gpg("--with-colons", "--list-secret-keys"), "\n") {
			if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" {
				fpr = f[9]
				break
			}
		}
		pub := gpg("--armor", "--export", fpr)
		putFile(t, filepath.Join(filepath.Dir(home), name+".asc"), pub, 0644)
		return home, fpr
	}
	trustedHome, trustedFpr := gen("trusted")
	otherHome, otherFpr := gen("other")
	sign := func(home, fpr string) string {
		return commitIn(t, repo, []string{"GNUPGHOME=" + home}, "-c", "gpg.program=gpg", "-c", "user.signingkey="+fpr)
	}
	good := sign(trustedHome, trustedFpr)
	other := sign(otherHome, otherFpr)
	unsigned := commitIn(t, repo, nil)
	gitDir := filepath.Join(repo, ".git")
	signers := []string{filepath.Join(filepath.Dir(trustedHome), "trusted.asc")}
	work := t.TempDir()
	desc, err := verifyCommitSignature(context.Background(), gitDir, good, signers, work)
	if err != nil || !strings.Contains(desc, trustedFpr) {
		t.Fatalf("受信 gpg 签名应通过：%q %v", desc, err)
	}
	if _, err := verifyCommitSignature(context.Background(), gitDir, other, signers, work); err == nil {
		t.Fatal("非受信 gpg 签名者应拒绝")
	}
	if _, err := verifyCommitSignature(context.Background(), gitDir, unsigned, signers, work); err == nil {
		t.Fatal("未签名提交应拒绝")
	}
}

func TestLocalDeployVerifyStatePackageAndDoctor(t *testing.T) {
	stubTrustedSource(t)
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	s.TrustUnpinned = true
	writeTestRelease(t, &s, 1)
	if unsigned, err := verifyStatePackage(s, s.Package, "", false); err != nil || unsigned {
		t.Fatalf("local-deploy 当前发行应按本地清单通过（不借用 AllowUnsigned）：%v %v", unsigned, err)
	}
	if err := trustHealth(s); err != nil {
		t.Fatalf("未钉定发行钥的 local-deploy 站点发行信任应通过：%v", err)
	}
	moved := false
	stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name == "docker" && r.Args[0] == "image" && r.Args[1] == "inspect" {
			ref := r.Args[len(r.Args)-1]
			if i := strings.Index(ref, ":r"); strings.HasPrefix(ref, "auriga/") && i > 0 {
				seq, _ := strconv.Atoi(ref[i+2:])
				svc := strings.TrimPrefix(ref[:i], "auriga/")
				if moved && svc == "edge" {
					return imagesFor("9")["EDGE"] + " linux/amd64", nil
				}
				return s.record(seq).Images[imageKey(svc)] + " linux/amd64", nil
			}
			return ref + " linux/amd64", nil
		}
		return "", fmt.Errorf("意外命令 %v", r.Args)
	})
	for _, c := range releaseChecks(s) {
		if err := c.test(); err != nil {
			t.Fatalf("doctor %s 应通过：%v", c.name, err)
		}
	}
	// 标签被挪到别的镜像：镜像检查失败。
	moved = true
	if err := releaseChecks(s)[2].test(); err == nil {
		t.Fatal("标签与历史记录不符时镜像检查应失败")
	}
	moved = false
	// 发行目录被篡改：完整性两项都失败。
	putFile(t, filepath.Join(s.Package, "deploy-nas/scripts/start.sh"), "tampered", 0755)
	if _, err := verifyStatePackage(s, s.Package, "", true); err == nil {
		t.Fatal("篡改的发行目录不能借 --allow-unsigned 通过")
	}
	if err := releaseChecks(s)[1].test(); err == nil {
		t.Fatal("篡改后离线包完整性应失败")
	}
}

func TestPinReleaseKeyAndSiteKeyOnUnpinnedState(t *testing.T) {
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	s.TrustUnpinned = true
	_, sitePub := testSigningKeys(t)
	siteFp, _ := publicFingerprint(sitePub)
	if err := registerSiteKey(&s, sitePub, siteFp); err != nil || len(s.TrustedKeys) != 1 {
		t.Fatalf("未钉定站点应能登记站点钥：%v %v", err, s.TrustedKeys)
	}
	_, relPub := testSigningKeys(t)
	relFp, _ := publicFingerprint(relPub)
	if err := pinReleaseKey(&s, relPub, siteFp); err == nil {
		t.Fatal("指纹不符应拒绝")
	}
	if err := pinReleaseKey(&s, relPub, relFp); err != nil {
		t.Fatal(err)
	}
	if s.TrustUnpinned || s.PublicKey == "" || len(s.TrustedKeys) != 2 || s.TrustedKeys[0] != s.PublicKey {
		t.Fatalf("钉定后 state 不对：%+v", s)
	}
	_, another := testSigningKeys(t)
	anotherFp, _ := publicFingerprint(another)
	if err := pinReleaseKey(&s, another, anotherFp); err == nil {
		t.Fatal("已钉定的发行钥不能替换")
	}
}

// imageInspectByTag 让 localImage 按 auriga/<svc>:r<序号> 返回 state 历史里的镜像。
func imageInspectByTag(t *testing.T, data string) {
	t.Helper()
	stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name == "docker" && len(r.Args) > 1 && r.Args[0] == "image" && r.Args[1] == "inspect" {
			ref := r.Args[len(r.Args)-1]
			if i := strings.Index(ref, ":r"); strings.HasPrefix(ref, "auriga/") && i > 0 {
				s, err := readState(data)
				if err != nil {
					return "", err
				}
				seq, _ := strconv.Atoi(ref[i+2:])
				rec := s.record(seq)
				if rec == nil {
					return "", errors.New("no such image")
				}
				return rec.Images[imageKey(strings.TrimPrefix(ref[:i], "auriga/"))] + " linux/amd64", nil
			}
			return ref + " linux/amd64", nil
		}
		return "", nil
	})
}

func TestUpgradeWritesSeqReleaseAndRollbackRoundTrip(t *testing.T) {
	stubTrustedSource(t)
	v := &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1"), Source: deploySource{Mode: "git", Commit: strings.Repeat("1", 40)}, Migrations: v.Migrations, Formats: v.Formats})
	writeTestRelease(t, &s, 1)
	imageInspectByTag(t, s.Data)
	stubDataVersions(t, v)
	var steps []string
	oldOps := upgradeOps
	upgradeOps = siteOps{
		nas: func(st state, op string, _ ...string) error {
			steps = append(steps, op+":"+filepath.Base(st.Package))
			return nil
		},
		health: func(state, time.Duration) error { return nil },
	}
	t.Cleanup(func() { upgradeOps = oldOps })
	stage, err := os.MkdirTemp(filepath.Dir(s.Data), ".auriga-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("7", 40)
	if err := jsonFile(filepath.Join(stage, "offline-release.json"), release{Schema: 1, Version: "1.1", Source: &provenance{Commit: commit}}, 0644); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(stage, "deploy-nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	if err := writeSumsContext(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	sums, _ := digest(filepath.Join(stage, "SHA256SUMS"))
	seq := nextSeq(s)
	if err := activateUpgrade(context.Background(), &s, stage, imagesFor("2"), seq, sums, "/media/auriga-1.1.tar"); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(s.Data)
	r2 := got.record(2)
	if seq != 2 || got.Current != 2 || got.Package != releaseDir(s.Data, 2) || got.source() != sourceSignedPackage || r2 == nil || r2.Kind != recordKindUpgrade || r2.Status != recordOK || r2.Source.Commit != commit || r2.Migrations == nil {
		t.Fatalf("upgrade 后 state 不对：%+v", got)
	}
	if strings.Join(steps, ",") != "render:2,preflight:2,start:2" {
		t.Fatalf("应在序号目录里 render/preflight/start：%v", steps)
	}
	if err := checkReleaseDir(s.Data, r2); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if !strings.Contains(string(env), "AURIGA_IMAGE_EDGE="+imagesFor("2")["EDGE"]) {
		t.Fatalf("stack.env 未切到新镜像：%s", env)
	}
	if unsigned, err := verifyStatePackage(got, got.Package, "", true); err != nil || !unsigned {
		t.Fatalf("签名包来源的当前发行仍按签名核验（缺签名需显式例外）：%v %v", unsigned, err)
	}
	// rollback 回到 deploy 产生的 r1，再回到 upgrade 产生的 r2。
	site := &fakeSite{healthy: func(int) error { return nil }}
	run, _ := testRun()
	if err := doRollback(context.Background(), site.ops(), &siteConfig{Server: &siteServer{Data: s.Data}}, s.Data, 1, run); err != nil {
		t.Fatal(err)
	}
	got, _ = readState(s.Data)
	if got.Current != 1 || got.source() != sourceLocalDeploy || got.Package != releaseDir(s.Data, 1) {
		t.Fatalf("回到 deploy 历史后 state 不对：%+v", got)
	}
	run, _ = testRun()
	if err := doRollback(context.Background(), site.ops(), &siteConfig{Server: &siteServer{Data: s.Data}}, s.Data, 2, run); err != nil {
		t.Fatal(err)
	}
	got, _ = readState(s.Data)
	if got.Current != 2 || got.source() != sourceSignedPackage {
		t.Fatalf("回到 upgrade 历史后 state 不对：%+v", got)
	}
	// 数据被改动后，rollback 到 upgrade 历史同样受检查约束。
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "9:9:z"}, Formats: map[string]string{}})
	run, _ = testRun()
	if err := doRollback(context.Background(), site.ops(), &siteConfig{Server: &siteServer{Data: s.Data}}, s.Data, 1, run); exitCodeOf(err) != 3 {
		t.Fatalf("数据被改动后应拒绝回滚：%v", err)
	}
}

func TestUpgradeFailureRemovesSeqRelease(t *testing.T) {
	stubTrustedSource(t)
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	writeTestRelease(t, &s, 1)
	before := s
	before.History = append([]deployRecord(nil), s.History...)
	stubDeployRun(t, func(runSpec) (string, error) { return "", nil })
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}})
	oldOps := upgradeOps
	upgradeOps = siteOps{nas: func(state, string, ...string) error { return nil }, health: func(state, time.Duration) error { return errors.New("platform unhealthy") }}
	t.Cleanup(func() { upgradeOps = oldOps })
	stage, _ := os.MkdirTemp(filepath.Dir(s.Data), ".auriga-upgrade-")
	putFile(t, filepath.Join(stage, "SHA256SUMS"), "", 0644)
	if err := activateUpgrade(context.Background(), &s, stage, imagesFor("2"), 2, "x", ""); err == nil {
		t.Fatal("健康失败应返回错误（由调用方从冷备份恢复）")
	}
	if _, err := os.Stat(releaseDir(s.Data, 2)); err != nil {
		t.Fatal("失败时序号目录应已写入，待恢复后删除")
	}
	removeFailedUpgradeRelease(pendingUpgrade{State: before, Release: releaseDir(s.Data, 2)})
	if _, err := os.Stat(releaseDir(s.Data, 2)); !os.IsNotExist(err) {
		t.Fatal("恢复冷备份后应删除失败升级的发行目录")
	}
	if nextSeq(before) != 2 {
		t.Fatalf("失败的序号目录删除后 nextSeq 应为 2：%d", nextSeq(before))
	}
	// 被恢复后的 state 引用的目录不能删。
	removeFailedUpgradeRelease(pendingUpgrade{State: before, Release: releaseDir(s.Data, 1)})
	if _, err := os.Stat(releaseDir(s.Data, 1)); err != nil {
		t.Fatal("恢复后的 state 引用的发行目录被误删")
	}
}

func TestSignedPackageBaselineForDeploy(t *testing.T) {
	stubTrustedSource(t)
	parent := t.TempDir()
	data := filepath.Join(parent, "auriga-data")
	s := state{Data: data, Package: legacyReleaseDir(data), Images: imagesFor("0")}
	putFile(t, filepath.Join(s.Package, "deploy-nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	commit := strings.Repeat("6", 40)
	if err := jsonFile(filepath.Join(s.Package, "offline-release.json"), release{Schema: 1, Version: "1.0", Source: &provenance{Commit: commit}}, 0644); err != nil {
		t.Fatal(err)
	}
	issuer, public := testSigningKeys(t)
	signTestRoot(t, s.Package, issuer)
	if err := pinInitialTrust(&s, public); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	var tags []string
	stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name == "docker" && r.Args[0] == "tag" {
			tags = append(tags, r.Args[2])
		}
		return "", nil
	})
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}})
	dry, err := packageBaseline(context.Background(), &s, false)
	if err != nil || len(tags) != 0 || len(s.History) != 0 {
		t.Fatalf("--dry-run 不应写 state 或打标签：%v %v", err, tags)
	}
	_ = dry
	rec, err := packageBaseline(context.Background(), &s, true)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 1 || rec.Kind != recordKindInstall || rec.Release != legacyReleaseDir(data) || rec.Source.Commit != commit || rec.Migrations == nil || len(tags) != len(deployServices) {
		t.Fatalf("基线记录不对：%+v %v", rec, tags)
	}
	if err := checkReleaseDir(data, rec); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	if got.Current != 1 || got.source() != sourceSignedPackage {
		t.Fatalf("%+v", got)
	}
	if _, err := verifyStatePackage(got, got.Package, "", false); err != nil {
		t.Fatalf("登记基线后当前安装包仍应通过签名与 releaseSums 核对：%v", err)
	}
	// 未签名、签名者不受信的安装包不登记。
	other, _ := testSigningKeys(t)
	signTestRoot(t, s.Package, other)
	s2 := s
	s2.History, s2.Current = nil, 0
	if _, err := packageBaseline(context.Background(), &s2, false); err == nil {
		t.Fatal("签名不属于 state 固定公钥的安装包不应登记为基线")
	}
}
