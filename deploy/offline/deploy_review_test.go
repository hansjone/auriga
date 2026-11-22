package main

// 审查返工（M1–M3 与低等问题）的负例单测；M1 的 Dockerfile 用例在 deploy_test.go。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 要求签名提交时，源码目录模式（source.dir 与 --source）直接拒绝，且不调用 git。
func TestDeployDirModeRefusedWhenSigningRequired(t *testing.T) {
	stubTrustedSource(t)
	calls := stubDeployRun(t, func(runSpec) (string, error) { return "", nil })
	dir := t.TempDir()
	g := &siteGit{URL: "https://git.example.com/a.git", AllowedRefs: []string{"refs/heads/main"}, RequireSignedCommits: true, TrustedSigners: []string{"/etc/auriga/signers.pub"}}
	cfg := &siteConfig{Source: &siteSource{Git: g, Dir: &dir}}
	if _, err := resolveDeploySource(context.Background(), cfg, t.TempDir()+"/data", "", "", t.TempDir()); err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("source.dir 模式应拒绝：%v", err)
	}
	if _, err := resolveDeploySource(context.Background(), cfg, t.TempDir()+"/data", t.TempDir(), "", t.TempDir()); err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("--source 模式应拒绝：%v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("拒绝时不应调用 git：%v", *calls)
	}
}

// M2：提交必须在某个 allowedRefs 上；不在时拒绝，且不按提交号另行 fetch。
func TestGitCommitMustBeOnAllowedRefs(t *testing.T) {
	stubTrustedSource(t)
	commit := strings.Repeat("c", 40)
	run := func(t *testing.T, catFile error, onRefs map[string]bool) (*deploySource, []runSpec, error) {
		calls := stubDeployRun(t, func(r runSpec) (string, error) {
			if len(r.Args) > 1 {
				switch r.Args[1] {
				case "cat-file":
					return "", catFile
				case "merge-base":
					if onRefs[r.Args[len(r.Args)-1]] {
						return "", nil
					}
					return "", errors.New("not ancestor")
				case "archive":
					return "", writeEmptyTar(r.Args[len(r.Args)-2])
				}
			}
			return "", nil
		})
		cfg := &siteConfig{Source: &siteSource{Git: &siteGit{URL: "https://git.example.com/a.git", AllowedRefs: []string{"refs/heads/main", "refs/heads/release"}}}}
		src, err := resolveDeploySource(context.Background(), cfg, t.TempDir()+"/auriga-data", "", commit, t.TempDir())
		return src, *calls, err
	}
	src, calls, err := run(t, nil, map[string]bool{"refs/auriga/heads/release": true})
	if err != nil || src.Ref != "refs/heads/release" {
		t.Fatalf("在第二个 allowedRefs 上的提交应接受并记录引用：%+v %v", src, err)
	}
	for _, c := range calls {
		if c.Name != "git" {
			continue
		}
		if c.DropEnvPrefix != "GIT_" || !hasAll(c.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0") {
			t.Fatalf("git 调用未隔离配置与环境：%+v", c)
		}
	}
	if _, calls, err = run(t, nil, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "不在 allowedRefs") {
		t.Fatalf("不在任何 allowedRefs 上的提交应拒绝：%v", err)
	}
	if _, calls, err = run(t, errors.New("missing"), nil); err == nil || !strings.Contains(err.Error(), "不在 allowedRefs") {
		t.Fatalf("fetch 后不存在的提交应拒绝：%v", err)
	}
	for _, c := range calls {
		if strings.Contains(strings.Join(c.Args, " "), "fetch") && strings.Contains(strings.Join(c.Args, " "), commit) {
			t.Fatalf("不应按提交号另行 fetch：%v", c.Args)
		}
	}
}

func writeEmptyTar(path string) error {
	var b bytes.Buffer
	b.Write(make([]byte, 1024))
	return writeFileAtomic(path, b.Bytes(), 0600)
}

func hasAll(list []string, want ...string) bool {
	set := map[string]bool{}
	for _, v := range list {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// L5：继承的 GIT_* 变量被去掉，HOME 等由 gitEnv 覆盖。
func TestRunEnvDropsInheritedGitVariables(t *testing.T) {
	base := []string{"PATH=/usr/bin", "GIT_DIR=/evil", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fsmonitor", "HOME=/root"}
	env := runEnv(base, withGitEnv(runSpec{Name: "git"}))
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"GIT_DIR=", "GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_0="} {
		if strings.Contains(joined, bad) {
			t.Fatalf("继承的 %s 未去掉：%v", bad, env)
		}
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || env[len(env)-1] != "XDG_CONFIG_HOME=/nonexistent/auriga-deploy-git" || !hasAll(env, "HOME=/nonexistent/auriga-deploy-git") {
		t.Fatalf("环境不对：%v", env)
	}
	if docker := runEnv(base, withGitEnv(runSpec{Name: "docker"})); len(docker) != len(base) {
		t.Fatalf("非 git 调用不应改环境：%v", docker)
	}
}

// L2：--source 也必须 root 所有、组和其他用户不可写。
func TestFlagSourceMustBeTrusted(t *testing.T) {
	old := depsTrustedSourceCheck
	depsTrustedSourceCheck = func(what, path string) error { return errors.New(what + path + " 不是 root 所有") }
	t.Cleanup(func() { depsTrustedSourceCheck = old })
	oldSnap := snapshotDirSource
	snapshotDirSource = func(context.Context, string, string) (*provenance, error) {
		t.Fatal("属主检查失败时不应复制源码")
		return nil, nil
	}
	t.Cleanup(func() { snapshotDirSource = oldSnap })
	_, err := resolveDeploySource(context.Background(), &siteConfig{}, t.TempDir()+"/data", t.TempDir(), "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--source") {
		t.Fatalf("不受信的 --source 应拒绝：%v", err)
	}
}

// M3：切换失败后先停掉会写数据的新容器，再读迁移与格式版本，再决定回滚。
func TestDeploySwitchStopsWritersBeforeReadingVersions(t *testing.T) {
	for _, smokeFails := range []bool{false, true} {
		s, prev, rec, cfg := switchFixture(t)
		var events []string
		v := &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}
		old := readDataVersions
		readDataVersions = func(state) *dataVersions { events = append(events, "read"); return v }
		t.Cleanup(func() { readDataVersions = old })
		site := &fakeSite{events: &events, healthy: func(n int) error {
			if n == 1 && !smokeFails {
				return errors.New("platform 迁移超时")
			}
			return nil
		}}
		ops := site.ops()
		if smokeFails {
			first := true
			ops.smoke = func(state) (string, error) {
				if first {
					first = false
					return "失败", errors.New("冒烟失败")
				}
				return "通过", nil
			}
		}
		run, _ := testRun()
		err := deploySwitch(context.Background(), ops, cfg, &s, prev, rec, run, nil)
		if exitCodeOf(err) != 2 {
			t.Fatalf("smokeFails=%t：应自动回滚：%v", smokeFails, err)
		}
		if strings.Join(events, ",") != "read,stop,read" {
			t.Fatalf("smokeFails=%t：切换后读取版本前必须先停掉写数据的容器：%v", smokeFails, events)
		}
		if !strings.Contains(strings.Join(run.receipt.Notes, "\n"), "platform、collab") {
			t.Fatalf("收据应记录停掉的服务：%v", run.receipt.Notes)
		}
	}
}

// M3：停不下来（或确认不了）按已触碰处理，不读版本、不回滚，退出码 3。
func TestDeploySwitchStopFailureExit3(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	var events []string
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	old := readDataVersions
	readDataVersions = func(state) *dataVersions { events = append(events, "read"); return v }
	t.Cleanup(func() { readDataVersions = old })
	site := &fakeSite{events: &events, stopErr: errors.New("服务 platform 停止后仍在运行"), healthy: func(int) error { return errors.New("down") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || run.receipt.RolledBack || len(site.starts) != 1 {
		t.Fatalf("停不下来应退出码 3 且不回滚：%v starts=%v", err, site.starts)
	}
	if strings.Join(events, ",") != "read,stop" || !strings.Contains(run.receipt.RollbackReason, "无法确认") || run.receipt.Data.After.ok() {
		t.Fatalf("停止失败后不应再读版本：%v %q", events, run.receipt.RollbackReason)
	}
}

// L4：上一版不是成功记录时不自动回滚。
func TestDeploySwitchNoAutoRollbackToFailedRecord(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	prev.Status = recordFailed
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return errors.New("down") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || len(site.starts) != 1 || !strings.Contains(run.receipt.RollbackReason, "不是成功记录") {
		t.Fatalf("上一版失败时应交人工：%v %v %q", err, site.starts, run.receipt.RollbackReason)
	}
}

// M3：默认实现按镜像→服务表停服务，只停存在的写数据服务，停后复核。
func TestStopDataWritersUsesServiceTable(t *testing.T) {
	var stopArgs []string
	remaining := ""
	stopFail := false
	old := writerCompose
	writerCompose = func(_ state, out *bytes.Buffer, args ...string) error {
		switch {
		case args[0] == "ps" && args[1] == "-a":
			out.WriteString("postgres\nplatform\ncollab\nedge\nnats\nregistry\n")
		case args[0] == "stop":
			stopArgs = args
			if stopFail {
				return errors.New("stop failed")
			}
		case args[0] == "ps":
			if !strings.Contains(strings.Join(args, " "), "--status running --status restarting") {
				return fmt.Errorf("复核应同时看 running 与 restarting：%v", args)
			}
			out.WriteString(remaining)
		}
		return nil
	}
	t.Cleanup(func() { writerCompose = old })
	got, err := stopDataWriters(state{})
	if err != nil || strings.Join(got, ",") != "edge,platform,collab" {
		t.Fatalf("应停 edge、platform、collab：%v %v", got, err)
	}
	if strings.Contains(strings.Join(stopArgs, " "), "postgres") || strings.Contains(strings.Join(stopArgs, " "), "nats") {
		t.Fatalf("不应停 postgres/nats：%v", stopArgs)
	}
	remaining = "postgres\nplatform\n"
	if _, err := stopDataWriters(state{}); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("停止后仍在运行应报错：%v", err)
	}
	remaining, stopFail = "", true
	if _, err := stopDataWriters(state{}); err == nil {
		t.Fatal("stop 失败应报错")
	}
}

// L3：保留份数只算成功的发行，连续失败不会把更早的成功发行挤掉。
func TestPruneKeepsSuccessfulReleases(t *testing.T) {
	stubTrustedSource(t)
	var recs []deployRecord
	for i, st := range []string{recordOK, recordOK, recordFailed, recordFailed, recordFailed, recordOK} {
		recs = append(recs, deployRecord{Seq: i + 1, Status: st, Images: imagesFor(fmt.Sprint(i + 1))})
	}
	s := deployTestSite(t, recs...)
	stubDeployRun(t, func(r runSpec) (string, error) { return "", nil })
	var log bytes.Buffer
	removed, err := pruneReleases(context.Background(), &s, 2, &log)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(removed) != "[5 4 3 1]" || s.record(2) == nil || s.record(6) == nil {
		t.Fatalf("应保留成功的 r6、r2，淘汰失败记录与 r1：%v %+v", removed, s.History)
	}
}

// L6：JSON 形式的口令也要脱敏（含转义引号）。
func TestRedactJSONSecrets(t *testing.T) {
	in := `{"password": "a\"bc-secret", "api_key":"zzz123", "user":"alice", "AURIGA_TOKEN" : "tok-999"}`
	out := redactText(in)
	for _, secret := range []string{`a\"bc-secret`, "bc-secret", "zzz123", "tok-999"} {
		if strings.Contains(out, secret) {
			t.Fatalf("JSON 口令未脱敏 %s：%s", secret, out)
		}
	}
	if !strings.Contains(out, `"user":"alice"`) {
		t.Fatalf("非口令字段不应被遮：%s", out)
	}
}
