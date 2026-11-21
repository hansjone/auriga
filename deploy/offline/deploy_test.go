package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubDeployRun 用假执行器替换 docker/git 调用；handler 返回的字符串写进 Stdout。
func stubDeployRun(t *testing.T, handler func(r runSpec) (string, error)) *[]runSpec {
	t.Helper()
	var calls []runSpec
	old := deployRun
	deployRun = func(_ context.Context, r runSpec) error {
		calls = append(calls, r)
		out, err := handler(r)
		if r.Stdout != nil && out != "" {
			_, _ = io.WriteString(r.Stdout, out)
		}
		return err
	}
	t.Cleanup(func() { deployRun = old })
	return &calls
}

func putFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func testRun() (*siteRun, *bytes.Buffer) {
	var b bytes.Buffer
	return &siteRun{receipt: &siteReceipt{Notes: []string{}}, out: &b, stop: func() {}}, &b
}

func TestDirectorySnapshotMatchesGitSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	repo := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "core.autocrlf=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitRun("init", "-q")
	putFile(t, filepath.Join(repo, "a.txt"), "alpha\n", 0644)
	putFile(t, filepath.Join(repo, "dir/b.go"), "package b\n", 0644)
	putFile(t, filepath.Join(repo, "dir/z/c.json"), "{}\n", 0644)
	if runtime.GOOS != "windows" {
		putFile(t, filepath.Join(repo, "run.sh"), "#!/bin/sh\n", 0755)
	}
	gitRun("add", ".")
	gitRun("commit", "-q", "-m", "init")
	putFile(t, filepath.Join(repo, "untracked.md"), "new\n", 0644)
	target := filepath.Join(t.TempDir(), "snap")
	info, err := snapshotGitSourceContext(context.Background(), repo, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := directorySnapshotSHA256(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got != info.SnapshotSHA256 || !info.Dirty {
		t.Fatalf("目录摘要 %s 与原函数 %s 不一致（dirty=%t）", got, info.SnapshotSHA256, info.Dirty)
	}
	putFile(t, filepath.Join(target, "a.txt"), "changed\n", 0644)
	if again, _ := directorySnapshotSHA256(context.Background(), target); again == got {
		t.Fatal("改动后摘要不变")
	}
}

// validDockerfile 是能通过检查的最小 Dockerfile.assembly（与仓库里的写法同构）。
const validDockerfile = "ARG AGENT_BASE=debian:bookworm-slim\nFROM ${AGENT_BASE} AS agent\nRUN --mount=type=bind,source=system,target=/inputs,readonly \\\n    echo ok\nCOPY go/x /x\n"

func TestDockerfileSyntaxDirectiveRejected(t *testing.T) {
	for _, bad := range []string{
		"# syntax=docker/dockerfile:1\n" + validDockerfile,
		"#SYNTAX = example.com/front\n" + validDockerfile,
		"ARG A=1\n   #  syntax=x\n" + validDockerfile,
		"\xEF\xBB\xBF# syntax=example.com/front\n" + validDockerfile, // BOM
		"#\fsyntax=example.com/front\n" + validDockerfile,            // \f
		"#\vSyNtAx\v=\vexample.com/front\n" + validDockerfile,        // \v、大小写混合
		" # syntax=x\n" + validDockerfile,                            // 不换行空格
		"// syntax=example.com/front\n" + validDockerfile,            // // 前缀
		"//syntax = x\n" + validDockerfile,
		"# check=error=true\n# syntax=x\n" + validDockerfile, // 解析器指令区第二行
		"# syntax=x\r" + validDockerfile,                     // 只用 \r 换行
	} {
		if err := checkDockerfile([]byte(bad)); err == nil || !strings.Contains(err.Error(), "syntax") {
			t.Fatalf("未拒绝 %q：%v", bad, err)
		}
	}
	for _, bad := range []string{
		`{"syntax": "example.com/front"}`,
		"\xEF\xBB\xBF \n{\"syntax\":\"x\"}",
		"#!/bin/sh\n" + validDockerfile,
		"# escape=`\n" + validDockerfile,
		"FROM x\x00\n",
		"FROM \xff\n",
	} {
		if err := checkDockerfile([]byte(bad)); err == nil {
			t.Fatalf("未拒绝 %q", bad)
		}
	}
	for _, ok := range []string{
		"# comment about syntax\n" + validDockerfile,
		validDockerfile + "RUN echo '# syntax=x'\n",
		"# check=error=true\n" + validDockerfile,
		"\xEF\xBB\xBF" + validDockerfile,
	} {
		if err := checkDockerfile([]byte(ok)); err != nil {
			t.Fatalf("误拒 %q：%v", ok, err)
		}
	}
}

func TestDockerfileInstructionRules(t *testing.T) {
	pinned := "debian@sha256:" + strings.Repeat("a", 64)
	for _, ok := range []string{
		validDockerfile,
		"FROM " + pinned + " AS tools\nFROM tools AS agent\n",
		"FROM --platform=linux/amd64 " + pinned + " AS tools\nFROM ${EDGE_BASE} AS edge\nCOPY --from=tools /x /x\n",
		"FROM " + pinned + " AS tools\nFROM $AGENT_BASE AS agent\nRUN --mount=type=bind,from=tools,source=/a,target=/b true\n",
		"FROM ${AGENT_BASE} AS agent\nADD local.tar /x\nRUN --network=none true\n",
		"FROM ${AGENT_BASE} AS agent\nRUN echo a \\\n# 续行中的注释\n  && echo b\n",
	} {
		if err := checkDockerfile([]byte(ok)); err != nil {
			t.Fatalf("误拒 %q：%v", ok, err)
		}
	}
	for _, bad := range []string{
		"FROM debian:bookworm-slim AS agent\n",              // 未固定 digest
		"FROM ${EDGE_BASE} AS agent\n",                      // 构建参数与阶段名不符（默认值未固定）
		"FROM ${AGENT_BASE}\n",                              // 构建参数用在无名阶段
		"FROM ${FOO_BASE} AS foo\n",                         // 未知服务
		"FROM ${AGENT_BASE} AS agent\nFROM agent AS edge\n", // 引用构建参数阶段
		"FROM ${AGENT_BASE} AS agent\nFROM ${EDGE_BASE} AS edge\nCOPY --from=agent /x /x\n",
		"FROM ${AGENT_BASE} AS agent\nCOPY --from=alpine:3.22 /x /x\n", // 外部镜像
		"FROM ${AGENT_BASE} AS agent\nCOPY --FROM=" + pinned + " /x /x\n",
		"FROM ${AGENT_BASE} AS agent\nADD https://example.com/x.tar /x\n",
		"FROM ${AGENT_BASE} AS agent\nADD git@example.com:a/b.git /x\n",
		"FROM ${AGENT_BASE} AS agent\nRUN --mount=type=bind,from=alpine,target=/a true\n",
		"FROM ${AGENT_BASE} AS agent\nRUN --network=host true\n",
		"FROM ${AGENT_BASE} AS agent\nRUN --security=insecure true\n",
		"FROM ${AGENT_BASE} AS agent\nONBUILD RUN true\n",
		"FROM ${AGENT_BASE} AS agent\nRUN <<EOF\necho\nEOF\n",
		"FROM --platform=$BUILDPLATFORM ${AGENT_BASE} AS agent\n",
		"FROM \\\n  debian:12 AS agent\n", // 续行
		"from debian:12 as agent\n",       // 关键字小写
	} {
		if err := checkDockerfile([]byte(bad)); err == nil {
			t.Fatalf("未拒绝 %q", bad)
		}
	}
}

// repoRoot 是工作区根目录（deploy/offline 的上两级）。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDeployRecipeInSourceIsValid(t *testing.T) {
	r, err := loadDeployRecipe(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	builds, system := r.needs([]string{"operator"})
	if !builds["go"] || builds["web"] || builds["java"] || !system {
		t.Fatalf("operator 的构建需求不对 %v %v", builds, system)
	}
	builds, _ = r.needs(deployServices)
	if !builds["web"] || !builds["java"] {
		t.Fatalf("全部服务应需要 web 与 java：%v", builds)
	}
}

// fakeRecipeSource 写一个最小源码树：配方、Dockerfile、上游摘要。
func fakeRecipeSource(t *testing.T, mutate func(m map[string]any), dockerfile string) string {
	t.Helper()
	src := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(deployRecipeRel)))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(m)
	}
	out, _ := json.Marshal(m)
	putFile(t, filepath.Join(src, filepath.FromSlash(deployRecipeRel)), string(out), 0644)
	putFile(t, filepath.Join(src, filepath.FromSlash(deployDockerfile)), dockerfile, 0644)
	putFile(t, filepath.Join(src, "deploy/nas/docker/Dockerfile.agent"), "FROM a\n", 0644)
	sum, _ := digest(filepath.Join(src, "deploy/nas/docker/Dockerfile.agent"))
	putFile(t, filepath.Join(src, filepath.FromSlash(deployUpstreamRel)), sum+"  deploy/nas/docker/Dockerfile.agent\n", 0644)
	return src
}

func TestDeployRecipeRejections(t *testing.T) {
	if _, err := loadDeployRecipe(fakeRecipeSource(t, nil, validDockerfile)); err != nil {
		t.Fatalf("合法配方被拒：%v", err)
	}
	_, err := loadDeployRecipe(fakeRecipeSource(t, func(m map[string]any) { m["cliCapability"] = 2; m["futureField"] = true }, validDockerfile))
	if err == nil || !strings.Contains(err.Error(), "先升级") {
		t.Fatalf("能力版本不匹配应提示先升级 CLI：%v", err)
	}
	if _, err := loadDeployRecipe(fakeRecipeSource(t, nil, "# syntax=docker/dockerfile:1.7\n"+validDockerfile)); err == nil || !strings.Contains(err.Error(), "syntax") {
		t.Fatalf("# syntax= 未被拒绝：%v", err)
	}
	_, err = loadDeployRecipe(fakeRecipeSource(t, func(m map[string]any) { m["images"].(map[string]any)["tool-go"] = "golang:1.27.3" }, validDockerfile))
	if err == nil || !strings.Contains(err.Error(), "固定 digest") {
		t.Fatalf("未固定 digest 的镜像未被拒绝：%v", err)
	}
	_, err = loadDeployRecipe(fakeRecipeSource(t, func(m map[string]any) {
		m["services"].(map[string]any)["agent"] = map[string]any{"stage": []any{map[string]any{"kind": "script", "files": []string{"x"}}}}
	}, validDockerfile))
	if err == nil || !strings.Contains(err.Error(), "未知暂存种类") {
		t.Fatalf("未知暂存种类未被拒绝：%v", err)
	}
	src := fakeRecipeSource(t, nil, validDockerfile)
	putFile(t, filepath.Join(src, "deploy/nas/docker/Dockerfile.agent"), "FROM changed\n", 0644)
	if _, err := loadDeployRecipe(src); err == nil || !strings.Contains(err.Error(), "upstream-recipes") {
		t.Fatalf("上游配方摘要不符未被拒绝：%v", err)
	}
	if _, err := loadDeployRecipe(t.TempDir()); err == nil || !strings.Contains(err.Error(), "不支持 deploy") {
		t.Fatalf("缺少配方应说明不支持：%v", err)
	}
}

func TestCompareSourceLocksMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	lock := ecosystemLock{Ecosystem: "go", Raw: []byte("aaa  go.sum\n")}
	putFile(t, filepath.Join(dir, "source-locks.sha256"), "aaa  go.sum\n", 0644)
	if err := compareSourceLocks(dir, lock); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(dir, "source-locks.sha256"), "bbb  go.sum\n", 0644)
	if err := compareSourceLocks(dir, lock); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("锁清单不一致未被拒绝：%v", err)
	}
	if err := compareSourceLocks(t.TempDir(), lock); err == nil {
		t.Fatal("缺少锁清单未被拒绝")
	}
}

func writeArtifactDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	var sums strings.Builder
	for name, body := range files {
		putFile(t, filepath.Join(dir, filepath.FromSlash(name)), body, 0755)
		sum, _ := digest(filepath.Join(dir, filepath.FromSlash(name)))
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
	}
	putFile(t, filepath.Join(dir, "SHA256SUMS"), sums.String(), 0644)
}

func TestStageServiceAndAssemblyDigest(t *testing.T) {
	r, err := loadDeployRecipe(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	putFile(t, filepath.Join(src, "deploy/nas/docker/agent-entrypoint.sh"), "#!/bin/sh\n", 0755)
	artifacts, system := t.TempDir(), t.TempDir()
	writeArtifactDir(t, filepath.Join(artifacts, "go"), map[string]string{"auriga-agent": "agent-v1"})
	writeArtifactDir(t, filepath.Join(system, "agent"), map[string]string{"packages.txt": "ca-certificates\n"})
	stage := func() string {
		scratch := t.TempDir()
		if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, system, scratch); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"go/auriga-agent", "system/packages.txt", "system/SHA256SUMS", "agent-entrypoint.sh"} {
			if _, err := os.Stat(filepath.Join(scratch, filepath.FromSlash(want))); err != nil {
				t.Fatalf("暂存缺少 %s", want)
			}
		}
		d, err := assemblyDigest(context.Background(), "agent", r, scratch)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := stage()
	if stage() != first {
		t.Fatal("同样输入的装配摘要不稳定")
	}
	writeArtifactDir(t, filepath.Join(artifacts, "go"), map[string]string{"auriga-agent": "agent-v2"})
	if stage() == first {
		t.Fatal("产物变化后装配摘要不变")
	}
	r2 := *r
	r2.Images = cloneImages(r.Images)
	r2.Images["base-agent"] = "debian@sha256:" + strings.Repeat("1", 64)
	scratch := t.TempDir()
	if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, system, scratch); err != nil {
		t.Fatal(err)
	}
	a, _ := assemblyDigest(context.Background(), "agent", r, scratch)
	b, _ := assemblyDigest(context.Background(), "agent", &r2, scratch)
	if a == b {
		t.Fatal("基础镜像变化后装配摘要不变")
	}
	putFile(t, filepath.Join(artifacts, "go", "auriga-agent"), "tampered", 0755)
	if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, system, t.TempDir()); err == nil {
		t.Fatal("产物与 SHA256SUMS 不符未被拒绝")
	}
}

func TestGitRemoteSnapshotUsesBareRepoAndArchive(t *testing.T) {
	stubTrustedSource(t)
	parent := t.TempDir()
	data := filepath.Join(parent, "auriga-data")
	stage := t.TempDir()
	commit := strings.Repeat("a", 40)
	calls := stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name != "git" {
			return "", fmt.Errorf("意外命令 %s", r.Name)
		}
		for _, a := range r.Args {
			if a == "-C" {
				return "", errors.New("不应在任何工作目录里执行 git -C")
			}
		}
		switch {
		case r.Args[0] == "init":
			return "", nil
		case len(r.Args) > 1 && r.Args[1] == "archive":
			out := r.Args[len(r.Args)-2]
			if r.Args[len(r.Args)-1] != commit+"^{tree}" {
				return "", fmt.Errorf("应导出提交的树：%v", r.Args)
			}
			f, err := os.Create(out)
			if err != nil {
				return "", err
			}
			tw := tar.NewWriter(f)
			for name, body := range map[string]string{"README.md": "hi\n", "deploy/x.sh": "echo\n"} {
				_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg})
				_, _ = tw.Write([]byte(body))
			}
			_ = tw.Close()
			return "", f.Close()
		}
		return "", nil
	})
	cfg := &siteConfig{Source: &siteSource{Git: &siteGit{URL: "https://git.example.com/a.git", AllowedRefs: []string{"refs/heads/main"}}}}
	src, err := resolveDeploySource(context.Background(), cfg, data, "", commit, stage)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := directorySnapshotSHA256(context.Background(), src.Dir)
	if src.Mode != "git" || src.Commit != commit || src.Dirty || src.SnapshotSHA256 != want {
		t.Fatalf("来源记录不对 %+v", src)
	}
	var fetch []string
	for _, c := range *calls {
		if len(c.Args) > 1 && c.Args[1] == "fetch" {
			fetch = c.Args
		}
		if !strings.HasPrefix(c.Args[0], "--git-dir="+filepath.Join(parent, "auriga-source.git")) && c.Args[0] != "init" {
			t.Fatalf("git 必须只操作数据目录同级的暂存裸仓库：%v", c.Args)
		}
	}
	if !strings.Contains(strings.Join(fetch, " "), "+refs/heads/main:refs/auriga/heads/main") {
		t.Fatalf("应按 allowedRefs fetch：%v", fetch)
	}
	if _, err := resolveDeploySource(context.Background(), cfg, data, "", "notacommit", t.TempDir()); err == nil {
		t.Fatal("非法提交号未被拒绝")
	}
	if _, err := resolveDeploySource(context.Background(), &siteConfig{}, data, "", commit, t.TempDir()); err == nil {
		t.Fatal("没有 source.git 时按提交号部署应失败")
	}
}

func TestParseDeployArguments(t *testing.T) {
	c := strings.Repeat("b", 40)
	for _, args := range [][]string{{c, "--full"}, {"--full", c}} {
		o, err := parseDeploy(args)
		if err != nil || o.Commit != c || !o.Full {
			t.Fatalf("%v: %+v %v", args, o, err)
		}
	}
	if o, err := parseDeploy([]string{"--services", "edge,platform,edge"}); err != nil || len(o.Services) != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	for _, bad := range [][]string{{"--services", "edge,nope"}, {"--full", "--services", "edge"}, {"abc"}, {c, c}} {
		if _, err := parseDeploy(bad); err == nil {
			t.Fatalf("未拒绝 %v", bad)
		}
	}
}

func TestRegistryPullRef(t *testing.T) {
	d := strings.Repeat("c", 64)
	for in, want := range map[string]string{
		"postgres:17@sha256:" + d:                               "reg.example:5000/postgres@sha256:" + d,
		"gcr.io/distroless/static-debian12:nonroot@sha256:" + d: "reg.example:5000/distroless/static-debian12@sha256:" + d,
		"library/registry@sha256:" + d:                          "reg.example:5000/library/registry@sha256:" + d,
		"localhost:5000/team/img:tag@sha256:" + d:               "reg.example:5000/team/img@sha256:" + d,
	} {
		if got := registryPullRef("reg.example:5000", in); got != want {
			t.Fatalf("%s → %s，期望 %s", in, got, want)
		}
	}
}

// deployTestSite 建一个已有 local-deploy state 的站点目录。
func deployTestSite(t *testing.T, records ...deployRecord) state {
	t.Helper()
	parent := t.TempDir()
	data := filepath.Join(parent, "auriga-data")
	if err := os.MkdirAll(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(data, "stack.env"), "AURIGA_DATA_ROOT="+data+"\nAURIGA_IMAGE_EDGE=old\nAURIGA_INSTALLER_IMAGE=old\n", 0600)
	s := state{Data: data, Source: sourceLocalDeploy}
	for i := range records {
		records[i].Release = releaseDir(data, records[i].Seq)
		s.History = append(s.History, records[i])
	}
	if len(records) > 0 {
		last := records[len(records)-1]
		s.Current, s.Package, s.Images = last.Seq, last.Release, last.Images
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func imagesFor(tag string) map[string]string {
	m := map[string]string{}
	for i, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		m[imageKey(svc)] = fmt.Sprintf("sha256:%s%02d%s", tag, i, strings.Repeat("0", 62-len(tag)))
	}
	return m
}

func TestStateSourceAndAtomicCurrentSwitch(t *testing.T) {
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	path := filepath.Join(s.Data+".oneclick", "state.json")
	putFile(t, path+".new", "leftover", 0600)
	s.Current = 7
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path + ".new"); !os.IsNotExist(err) {
		t.Fatal("原子写后不应留下 .new")
	}
	got, err := readState(s.Data)
	if err != nil || got.Current != 7 || got.source() != sourceLocalDeploy {
		t.Fatalf("%+v %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"source": "local-deploy"`) || !strings.Contains(string(raw), `"current": 7`) {
		t.Fatalf("state 缺少 source/current 字段：%s", raw)
	}
	var old state
	if err := json.Unmarshal([]byte(`{"data":"/x","package":"/p"}`), &old); err != nil || old.source() != sourceSignedPackage {
		t.Fatal("旧 state 缺省来源应为 signed-package")
	}
	if err := upgradeAllowed(s.Data, filepath.Join(t.TempDir(), "x.tar"), false); err == nil || !strings.Contains(err.Error(), "2a（下）") {
		t.Fatalf("local-deploy 站点的签名包 upgrade 应明确拒绝：%v", err)
	}
}

func TestReleaseDirAndRetention(t *testing.T) {
	stubTrustedSource(t)
	var recs []deployRecord
	for i := 1; i <= 5; i++ {
		recs = append(recs, deployRecord{Seq: i, Status: recordOK, Images: imagesFor(fmt.Sprint(i))})
	}
	s := deployTestSite(t, recs...)
	src, goOut := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(src, "deploy/nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	writeArtifactDir(t, goOut, map[string]string{"auriga-nas-bootstrap": "boot", "runtime-release": "rr"})
	for _, r := range recs {
		dir, sums, err := writeReleaseDir(context.Background(), s.Data, r.Seq, src, goOut, releaseManifest{Images: r.Images})
		if err != nil {
			t.Fatal(err)
		}
		if err := checkReleaseDir(s.Data, s.record(r.Seq)); err == nil {
			t.Fatal("state 没有记录 SHA256SUMS 摘要时应拒绝")
		}
		s.record(r.Seq).ReleaseSums = sums
		if err := checkReleaseDir(s.Data, s.record(r.Seq)); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"bin/auriga-nas-bootstrap", "bin/runtime-release", "deploy-nas/bin/auriga-nas-bootstrap", "deploy-nas/scripts/start.sh", "deploy-nas/.auriga-release", "release.json", "SHA256SUMS"} {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
				t.Fatalf("发行目录缺少 %s", want)
			}
		}
	}
	if _, _, err := writeReleaseDir(context.Background(), s.Data, 3, src, goOut, releaseManifest{}); err == nil {
		t.Fatal("已存在的发行目录被覆盖")
	}
	putFile(t, filepath.Join(releaseDir(s.Data, 3), "deploy-nas/scripts/start.sh"), "tampered", 0755)
	if err := checkReleaseDir(s.Data, s.record(3)); err == nil {
		t.Fatal("篡改的发行目录未被发现")
	}
	// 文件与本地清单一并改写：清单自洽，但与 state 锚定的摘要不符。
	putFile(t, filepath.Join(releaseDir(s.Data, 4), "deploy-nas/scripts/start.sh"), "tampered", 0755)
	if err := os.Remove(filepath.Join(releaseDir(s.Data, 4), "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	if err := writeSumsContext(context.Background(), releaseDir(s.Data, 4)); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(releaseDir(s.Data, 4)); err != nil {
		t.Fatalf("重写后的清单应自洽：%v", err)
	}
	if err := checkReleaseDir(s.Data, s.record(4)); err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("清单与文件一并改写未被发现：%v", err)
	}
	// go 产物与其 SHA256SUMS 不符时不写发行目录。
	putFile(t, filepath.Join(goOut, "runtime-release"), "changed", 0755)
	if _, _, err := writeReleaseDir(context.Background(), s.Data, 9, src, goOut, releaseManifest{}); err == nil {
		t.Fatal("go 产物被篡改时应拒绝写发行目录")
	}
	if nextSeq(s) != 6 {
		t.Fatalf("下一个序号应为 6，得到 %d", nextSeq(s))
	}
	busy := recs[1].Images["EDGE"] // r2 独有的 edge 镜像仍有容器在用
	var removedTags []string
	stubDeployRun(t, func(r runSpec) (string, error) {
		switch {
		case r.Args[0] == "ps":
			return "c1 c2\n", nil
		case r.Args[0] == "inspect":
			return busy + "\n" + recs[4].Images["EDGE"] + "\n", nil
		case r.Args[0] == "image" && r.Args[1] == "rm":
			removedTags = append(removedTags, r.Args[2])
			return "", nil
		}
		return "", fmt.Errorf("意外命令 %v", r.Args)
	})
	var log bytes.Buffer
	removed, err := pruneReleases(context.Background(), &s, 3, &log)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != 1 {
		t.Fatalf("应只淘汰 r1（r2 有容器在用），得到 %v；%s", removed, log.String())
	}
	if _, err := os.Stat(releaseDir(s.Data, 1)); !os.IsNotExist(err) {
		t.Fatal("r1 发行目录未删除")
	}
	if _, err := os.Stat(releaseDir(s.Data, 2)); err != nil {
		t.Fatal("有容器在用的 r2 被删除")
	}
	if len(removedTags) != len(deployServices) || !strings.Contains(strings.Join(removedTags, " "), "auriga/edge:r1") {
		t.Fatalf("删除的标签不对 %v", removedTags)
	}
	if s.record(1) != nil || s.record(2) == nil || !strings.Contains(log.String(), "r2") {
		t.Fatalf("历史更新不对：%+v", s.History)
	}
}

func TestRollbackDecisionRules(t *testing.T) {
	mig := map[string]string{"auriga/public.flyway_schema_history": "10:10:abc"}
	target := &deployRecord{Seq: 2, Migrations: mig, Formats: map[string]string{}}
	now := &dataVersions{Migrations: cloneImages(mig), Formats: map[string]string{}}
	if ok, _ := rollbackDecision(target, now); !ok {
		t.Fatal("数据未变时应允许回滚")
	}
	changed := &dataVersions{Migrations: map[string]string{"auriga/public.flyway_schema_history": "11:11:def"}, Formats: map[string]string{}}
	if ok, reason := rollbackDecision(target, changed); ok || !strings.Contains(reason, "没有声明 schemaCompatFloor") {
		t.Fatalf("无 floor 目标应拒绝：%v %s", ok, reason)
	}
	declared := &deployRecord{Seq: 2, SchemaCompatFloor: strings.Repeat("d", 40), FormatMax: map[string]string{"materials": "2"}}
	if ok, reason := rollbackDecision(declared, &dataVersions{Migrations: mig, Formats: map[string]string{"materials": "3"}}); ok || !strings.Contains(reason, "高于") {
		t.Fatalf("格式超上限应拒绝：%v %s", ok, reason)
	}
	if ok, reason := rollbackDecision(declared, &dataVersions{Migrations: mig, Formats: map[string]string{"materials": "2"}}); ok || !strings.Contains(reason, "2b") {
		t.Fatalf("floor 判定未实现时仍应交人工：%v %s", ok, reason)
	}
	if ok, _ := rollbackDecision(target, &dataVersions{Err: "psql 不可用"}); ok {
		t.Fatal("读不到版本时不能回滚")
	}
	if !dataUntouched(now, &dataVersions{Migrations: cloneImages(mig), Formats: map[string]string{}}) || dataUntouched(now, changed) || dataUntouched(&dataVersions{Err: "x"}, now) {
		t.Fatal("dataUntouched 判定不对")
	}
}

func stubDataVersions(t *testing.T, seq ...*dataVersions) {
	t.Helper()
	old := readDataVersions
	i := 0
	readDataVersions = func(state) *dataVersions {
		v := seq[min(i, len(seq)-1)]
		i++
		return v
	}
	t.Cleanup(func() { readDataVersions = old })
}

type fakeSite struct {
	starts  []int
	healthy func(call int) error
	calls   int
	stopErr error
	events  *[]string // 可选：记录 stop 的先后（与 readDataVersions 的记录对照）
}

func (f *fakeSite) ops() siteOps {
	return siteOps{
		nas: func(s state, op string, _ ...string) error {
			if op == "start" {
				f.starts = append(f.starts, s.Current)
			}
			return nil
		},
		health: func(state, time.Duration) error {
			f.calls++
			return f.healthy(f.calls)
		},
		smoke: func(state) (string, error) { return "通过", nil },
		stopWriters: func(state) ([]string, error) {
			if f.events != nil {
				*f.events = append(*f.events, "stop")
			}
			if f.stopErr != nil {
				return nil, f.stopErr
			}
			return []string{"platform", "collab"}, nil
		},
	}
}

func switchFixture(t *testing.T) (state, *deployRecord, deployRecord, *siteConfig) {
	t.Helper()
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	prev := s.record(1)
	rec := deployRecord{Seq: 2, Status: recordInstalling, Images: imagesFor("2"), Release: releaseDir(s.Data, 2)}
	keep := 3
	cfg := &siteConfig{Server: &siteServer{Data: s.Data, KeepHistory: &keep}}
	stubDeployRun(t, func(runSpec) (string, error) { return "", nil })
	return s, prev, rec, cfg
}

func TestDeploySwitchAutoRollbackWhenDataUntouched(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	v := &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(n int) error {
		if n == 1 {
			return errors.New("edge unhealthy")
		}
		return nil
	}}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run)
	if exitCodeOf(err) != 2 || !run.receipt.RolledBack {
		t.Fatalf("应自动回滚并以退出码 2 结束：%v", err)
	}
	if fmt.Sprint(site.starts) != "[2 1]" {
		t.Fatalf("应先启动 r2 再回到 r1：%v", site.starts)
	}
	got, _ := readState(s.Data)
	if got.Current != 1 || got.record(2).Status != recordFailed || got.Package != prev.Release {
		t.Fatalf("回滚后 state 不对：%+v", got)
	}
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if !strings.Contains(string(env), "AURIGA_IMAGE_EDGE="+prev.Images["EDGE"]) || !strings.Contains(string(env), "AURIGA_INSTALLER_IMAGE="+prev.Images["POSTGRES"]) {
		t.Fatalf("stack.env 未写回上一版镜像：%s", env)
	}
}

func TestDeploySwitchDataTouchedExit3(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}, &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{}})
	site := &fakeSite{healthy: func(int) error { return errors.New("platform unhealthy") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run)
	if exitCodeOf(err) != 3 || run.receipt.RolledBack || len(site.starts) != 1 {
		t.Fatalf("数据被触碰时应不回滚并退出码 3：%v starts=%v", err, site.starts)
	}
	if got, _ := readState(s.Data); got.Current != 2 {
		t.Fatalf("拒绝回滚时 state 应如实指向正在运行的 r2：%d", got.Current)
	}
}

func TestDeploySwitchRollbackFailureExit3(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return errors.New("down") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run)
	if exitCodeOf(err) != 3 || !strings.Contains(err.Error(), "不重试") || len(site.starts) != 2 {
		t.Fatalf("回滚失败应退出码 3 且不重试：%v starts=%v", err, site.starts)
	}
}

func TestDeploySwitchSuccessRecordsVersions(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	v := &dataVersions{Migrations: map[string]string{"auriga/x": "3:3:c"}, Formats: map[string]string{"materials": "1"}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return nil }}
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(s.Data)
	r := got.record(2)
	if got.Current != 2 || r.Status != recordOK || r.Migrations["auriga/x"] != "3:3:c" || r.Formats["materials"] != "1" {
		t.Fatalf("成功后 state 不对：%+v", got)
	}
	if len(run.receipt.SwitchedServices) != len(deployServices)+len(deployExtraServices) || run.receipt.Phase != "switch" {
		t.Fatalf("收据切换信息不对：%+v", run.receipt)
	}
}

func TestManualRollbackRefusesUndeclaredTarget(t *testing.T) {
	s := deployTestSite(t,
		deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1"), Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}},
		deployRecord{Seq: 2, Status: recordOK, Images: imagesFor("2")})
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{}})
	run, _ := testRun()
	site := &fakeSite{healthy: func(int) error { return nil }}
	err := doRollback(context.Background(), site.ops(), &siteConfig{Server: &siteServer{Data: s.Data}}, s.Data, 0, run)
	if exitCodeOf(err) != 3 || len(site.starts) != 0 || !strings.Contains(run.receipt.RollbackReason, "schemaCompatFloor") {
		t.Fatalf("无 floor 目标应拒绝且不启动：%v %v", err, site.starts)
	}
	if err := doRollback(context.Background(), site.ops(), nil, s.Data, 9, run); exitCodeOf(err) != 1 {
		t.Fatalf("不存在的序号应退出码 1：%v", err)
	}
}

func TestReceiptFieldsAndRedaction(t *testing.T) {
	dir := t.TempDir()
	old := siteLogDirCheck
	siteLogDirCheck = func(string) error { return nil }
	t.Cleanup(func() { siteLogDirCheck = old })
	cfg := &siteConfig{Logs: &siteLogs{Dir: dir}}
	run, err := startRun(cfg, filepath.Join(dir, "site.json"), "deploy", []string{"--json"}, true)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("AURIGA_PG_PASSWORD=supersecret1")
	fmt.Println("-----BEGIN PRIVATE KEY-----\nMIIBVgIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----")
	fmt.Println("Authorization: Bearer abcdefghijklmnop")
	fmt.Println("clone https://user:hunter22@git.example.com/x.git")
	run.receipt.Phase = "rollback"
	run.receipt.Source = &deploySource{Mode: "git", Commit: strings.Repeat("e", 40), SnapshotSHA256: strings.Repeat("f", 64)}
	ferr := run.finish(exitWith(2, errors.New("切换失败 token=abc123456")))
	if exitCodeOf(ferr) != 2 {
		t.Fatalf("退出码应保留为 2：%v", ferr)
	}
	var reported *reportedError
	if !errors.As(ferr, &reported) {
		t.Fatal("finish 应返回已报告错误")
	}
	logRaw, _ := os.ReadFile(run.logPath)
	rcRaw, err := os.ReadFile(run.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"supersecret1", "MIIBVgIBADAN", "abcdefghijklmnop", "hunter22", "abc123456"} {
		if strings.Contains(string(logRaw), secret) || strings.Contains(string(rcRaw), secret) {
			t.Fatalf("日志或收据泄露 %s：\n%s\n%s", secret, logRaw, rcRaw)
		}
	}
	if !strings.Contains(string(logRaw), "[已脱敏：私钥材料]") || !strings.Contains(string(logRaw), "AURIGA_PG_PASSWORD=***") {
		t.Fatalf("日志脱敏标记缺失：%s", logRaw)
	}
	var rc map[string]any
	if err := json.Unmarshal(rcRaw, &rc); err != nil {
		t.Fatalf("收据不是合法 JSON：%v\n%s", err, rcRaw)
	}
	for _, key := range []string{"command", "configSha256", "source", "deps", "buildImages", "images", "switchedServices", "mixedVersions", "orchestrationChanged", "data", "phase", "rolledBack", "durationSeconds", "exitCode", "log"} {
		if _, ok := rc[key]; !ok {
			t.Fatalf("收据缺少字段 %s", key)
		}
	}
	if rc["exitCode"].(float64) != 2 || rc["result"] != "rolled-back" || rc["phase"] != "rollback" || !strings.HasSuffix(run.logPath, "-deploy.log") {
		t.Fatalf("收据字段不对：%v", rc)
	}
}

func TestCheckImportPartsOwnership(t *testing.T) {
	dir := t.TempDir()
	var parts []assetInfo
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("x.tar.part%03d", i)
		putFile(t, filepath.Join(dir, name), "part", 0644)
		parts = append(parts, assetInfo{Name: name, Size: 4, SHA256: strings.Repeat("0", 64)})
	}
	raw, _ := json.Marshal(partsManifest{Schema: 1, Original: assetInfo{Name: "x.tar", Size: 8, SHA256: strings.Repeat("0", 64)}, Parts: parts})
	putFile(t, filepath.Join(dir, "x.tar.parts.json"), string(raw), 0644)
	var checked []string
	old := depsTrustedSourceCheck
	depsTrustedSourceCheck = func(what, path string) error {
		checked = append(checked, filepath.Base(path))
		if strings.HasSuffix(path, "part002") {
			return errors.New(what + path + " 不是 root 所有")
		}
		return nil
	}
	t.Cleanup(func() { depsTrustedSourceCheck = old })
	err := checkImportParts(filepath.Join(dir, "x.tar.part001"))
	if err == nil || !strings.Contains(err.Error(), "part002") {
		t.Fatalf("第二个分卷属主不对未被拒绝：%v", err)
	}
	if strings.Join(checked, ",") != "x.tar.parts.json,x.tar.part001,x.tar.part002" {
		t.Fatalf("应逐个检查清单与每个分卷：%v", checked)
	}
	cfg := &siteConfig{Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache")}}
	if err := depsImport(context.Background(), cfg, filepath.Join(dir, "x.tar.part001"), depsImportOptions{Out: io.Discard}); err == nil || !strings.Contains(err.Error(), "part002") {
		t.Fatalf("deps import 分卷属主负例未被拒绝：%v", err)
	}
	if err := checkImportParts(filepath.Join(dir, "plain.tar")); err != nil {
		t.Fatalf("非分卷归档不应检查分卷：%v", err)
	}
}
