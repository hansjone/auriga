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
	if deployCLICapability != 2 {
		t.Fatalf("deployCLICapability 应为 2，当前 %d", deployCLICapability)
	}
	r, err := loadDeployRecipe(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.CLICapability != 2 {
		t.Fatalf("源码配方 cliCapability 应为 2，当前 %d", r.CLICapability)
	}
	builds, inputs := r.needs([]string{"operator"})
	if !builds["go"] || builds["web"] || builds["java"] || !inputs["python"] || inputs["system"] {
		t.Fatalf("operator 的构建需求不对 builds=%v inputs=%v", builds, inputs)
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
	_, err := loadDeployRecipe(fakeRecipeSource(t, func(m map[string]any) { m["cliCapability"] = deployCLICapability + 1; m["futureField"] = true }, validDockerfile))
	if err == nil || !strings.Contains(err.Error(), "先升级") {
		t.Fatalf("能力版本不匹配应提示先升级 CLI：%v", err)
	}
	_, err = loadDeployRecipe(fakeRecipeSource(t, func(m map[string]any) { m["cliCapability"] = 1 }, validDockerfile))
	if err == nil || !strings.Contains(err.Error(), "先升级") {
		t.Fatalf("旧版 cliCapability=1 应被拒绝：%v", err)
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
	inputs := map[string]string{"system": system}
	writeArtifactDir(t, filepath.Join(artifacts, "go"), map[string]string{"auriga-agent": "agent-v1"})
	writeArtifactDir(t, filepath.Join(system, "agent"), map[string]string{"packages.txt": "ca-certificates\n"})
	stage := func() string {
		scratch := t.TempDir()
		if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, inputs, scratch); err != nil {
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
		return d.Total
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
	if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, inputs, scratch); err != nil {
		t.Fatal(err)
	}
	a, _ := assemblyDigest(context.Background(), "agent", r, scratch)
	b, _ := assemblyDigest(context.Background(), "agent", &r2, scratch)
	if a.Total == b.Total {
		t.Fatal("基础镜像变化后装配摘要不变")
	}
	if reason := assemblyChangeReason(a.Total, &a, b); !strings.Contains(reason, "基础镜像") {
		t.Fatalf("触发项应含基础镜像：%s", reason)
	}
	putFile(t, filepath.Join(artifacts, "go", "auriga-agent"), "tampered", 0755)
	if err := stageService(context.Background(), "agent", r.Services["agent"], src, artifacts, inputs, t.TempDir()); err == nil {
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
	if err := upgradeAllowed(s.Data, filepath.Join(t.TempDir(), "x.tar"), false); err == nil || !strings.Contains(err.Error(), "trust add-release-key") {
		t.Fatalf("发行钥未钉定的 local-deploy 站点第一次签名包 upgrade 前应要求 trust add-*：%v", err)
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
		dir, sums, err := writeReleaseDir(context.Background(), s.Data, r.Seq, src, goOut, "", releaseManifest{Images: r.Images}, nil, nil, nil)
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
	if _, _, err := writeReleaseDir(context.Background(), s.Data, 3, src, goOut, "", releaseManifest{}, nil, nil, nil); err == nil {
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
	if _, _, err := writeReleaseDir(context.Background(), s.Data, 9, src, goOut, "", releaseManifest{}, nil, nil, nil); err == nil {
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
	floor := strings.Repeat("a", 40)
	targetCommit := strings.Repeat("b", 40)
	target := &deployRecord{Seq: 2, Migrations: mig, Formats: map[string]string{}, Source: deploySource{Mode: "git", Commit: targetCommit}, FormatMax: map[string]string{"materials": "2"}}
	current := &deployRecord{Seq: 3, SchemaCompatFloor: floor, FormatMax: map[string]string{"materials": "3"}}
	now := &dataVersions{Migrations: cloneImages(mig), Formats: map[string]string{}}
	allowFloor := func(string) (bool, error) { return true, nil }
	denyFloor := func(string) (bool, error) { return false, nil }
	if ok, _ := rollbackDecision(target, current, nil, now, allowFloor); !ok {
		t.Fatal("数据未变时应允许回滚")
	}
	changed := &dataVersions{Migrations: map[string]string{"auriga/public.flyway_schema_history": "11:11:def"}, Formats: map[string]string{"materials": "2"}}
	if ok, reason := rollbackDecision(target, &deployRecord{Seq: 3}, nil, changed, allowFloor); ok || !strings.Contains(reason, "没有声明 schemaCompatFloor") {
		t.Fatalf("当前无兼容元数据应拒绝：%v %s", ok, reason)
	}
	noMax := &deployRecord{Seq: 2, Migrations: map[string]string{"x": "1"}, Formats: map[string]string{}}
	if ok, reason := rollbackDecision(noMax, current, nil, changed, allowFloor); ok || !strings.Contains(reason, "格式上限") {
		t.Fatalf("目标无 FormatMax 应拒绝：%v %s", ok, reason)
	}
	if ok, reason := rollbackDecision(target, current, nil, &dataVersions{Migrations: mig, Formats: map[string]string{"materials": "3"}}, allowFloor); ok || !strings.Contains(reason, "高于") {
		t.Fatalf("格式超上限应拒绝：%v %s", ok, reason)
	}
	if ok, reason := rollbackDecision(target, current, nil, changed, denyFloor); ok || !strings.Contains(reason, "早于") {
		t.Fatalf("目标早于 floor 应拒绝：%v %s", ok, reason)
	}
	if ok, reason := rollbackDecision(target, current, nil, changed, allowFloor); !ok {
		t.Fatalf("floor 与格式通过应允许：%v %s", ok, reason)
	}
	baseline := &deployRecord{Seq: 3, SchemaCompatFloor: "", FormatMax: map[string]string{"materials": "2"}}
	if ok, _ := rollbackDecision(target, baseline, nil, changed, nil); !ok {
		t.Fatal("空 floor（设计起点）且格式通过时应允许")
	}
	// 降级到 floor 较旧的提交后再回滚：之前更新发行（r4）的 floor 仍要满足。
	r4 := &deployRecord{Seq: 4, Source: deploySource{Mode: "git", Commit: strings.Repeat("e", 40)}, SchemaCompatFloor: strings.Repeat("f", 40), FormatMax: map[string]string{"materials": "2"}}
	lenient := &deployRecord{Seq: 5, Source: deploySource{Mode: "git", Commit: strings.Repeat("c", 40)}, SchemaCompatFloor: "", FormatMax: map[string]string{"materials": "2"}}
	onlyFloorF := func(c string) (bool, error) {
		return c != strings.Repeat("f", 40) && c != strings.Repeat("e", 40) && c != strings.Repeat("c", 40), nil
	}
	if ok, reason := rollbackDecision(target, lenient, []*deployRecord{r4}, changed, onlyFloorF); ok || !strings.Contains(reason, "r4") {
		t.Fatalf("目标早于更新发行 r4 的 floor 应拒绝：%v %s", ok, reason)
	}
	undeclared := &deployRecord{Seq: 4, Source: deploySource{Mode: "git", Commit: strings.Repeat("e", 40)}}
	if ok, reason := rollbackDecision(target, lenient, []*deployRecord{undeclared}, changed, onlyFloorF); ok || !strings.Contains(reason, "没有声明 schemaCompatFloor") {
		t.Fatalf("目标之后运行过、未声明 floor 的发行应拒绝：%v %s", ok, reason)
	}
	// 不比目标新的记录（提交是目标的祖先）不需要声明。
	ancestorOld := func(c string) (bool, error) { return c == strings.Repeat("e", 40), nil }
	if ok, reason := rollbackDecision(target, lenient, []*deployRecord{undeclared}, changed, ancestorOld); !ok {
		t.Fatalf("祖先提交的记录不应拦截：%s", reason)
	}
	failing := func(string) (bool, error) { return false, errors.New("git 不可用") }
	if ok, _ := rollbackDecision(target, current, nil, changed, failing); ok {
		t.Fatal("祖先判定失败必须保守拒绝")
	}
	if ok, _ := rollbackDecision(target, nil, nil, changed, allowFloor); ok {
		t.Fatal("找不到当前记录必须拒绝")
	}
	if ok, _ := rollbackDecision(target, current, nil, &dataVersions{Err: "psql 不可用"}, allowFloor); ok {
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

// writeTestRelease 为历史记录写一个最小的发行目录与 SHA256SUMS，并记下 releaseSums。
func writeTestRelease(t *testing.T, s *state, seq int) {
	t.Helper()
	r := s.record(seq)
	putFile(t, filepath.Join(r.Release, "deploy-nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	if err := writeSumsContext(context.Background(), r.Release); err != nil {
		t.Fatal(err)
	}
	sums, err := digest(filepath.Join(r.Release, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	r.ReleaseSums = sums
	if err := saveState(*s); err != nil {
		t.Fatal(err)
	}
}

func switchFixture(t *testing.T) (state, *deployRecord, deployRecord, *siteConfig) {
	t.Helper()
	stubTrustedSource(t)
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	writeTestRelease(t, &s, 1)
	prev := s.record(1)
	if orch, err := orchestrationDigest(filepath.Join(prev.Release, "deploy-nas"), s.Data, s.Access); err == nil {
		prev.Orchestration = orch
		_ = saveState(s)
	}
	rec := deployRecord{Seq: 2, Status: recordInstalling, Images: imagesFor("2"), Release: releaseDir(s.Data, 2), Fingerprints: cloneImages(prev.Fingerprints)}
	putFile(t, filepath.Join(rec.Release, "deploy-nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
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
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
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
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || run.receipt.RolledBack || len(site.starts) != 1 {
		t.Fatalf("数据被触碰时应不回滚并退出码 3：%v starts=%v", err, site.starts)
	}
	if !strings.Contains(err.Error(), "auriga status") || !strings.Contains(err.Error(), "auriga deploy <commit> --full") {
		t.Fatalf("拒绝回滚错误应含人工收敛指引：%v", err)
	}
	notes := strings.Join(run.receipt.Notes, "\n")
	if !strings.Contains(notes, "auriga status") || !strings.Contains(notes, "auriga deploy <commit> --full") {
		t.Fatalf("收据 notes 应含人工收敛指引：%s", notes)
	}
	if got, _ := readState(s.Data); got.Current != 2 {
		t.Fatalf("拒绝回滚时 state 应如实指向正在运行的 r2：%d", got.Current)
	}
}

// 切换阶段父 ctx 已取消时，失败收尾仍用独立 cleanup 上下文读运行镜像，避免误记 context canceled。
func TestDeploySwitchRefuseUsesCleanupContext(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}, &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{}})
	var psCalled, psCtxAlive bool
	oldRun := deployRun
	deployRun = func(ctx context.Context, r runSpec) error {
		if len(r.Args) > 0 && r.Args[0] == "ps" {
			psCalled = true
			psCtxAlive = ctx.Err() == nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Stdout != nil {
			_, _ = io.WriteString(r.Stdout, "c1 auriga/edge:r2 Up")
		}
		return nil
	}
	t.Cleanup(func() { deployRun = oldRun })
	old := deployCleanupContext
	var cleanupUsed bool
	deployCleanupContext = func() (context.Context, context.CancelFunc) {
		cleanupUsed = true
		return context.WithTimeout(context.Background(), time.Minute)
	}
	t.Cleanup(func() { deployCleanupContext = old })
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	site := &fakeSite{healthy: func(int) error { return errors.New("platform unhealthy") }}
	run, _ := testRun()
	err := deploySwitch(cancelled, site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || !cleanupUsed || !psCalled || !psCtxAlive {
		t.Fatalf("应走拒绝回滚且用未取消的 cleanup 上下文调 docker ps：err=%v cleanup=%v ps=%v alive=%v", err, cleanupUsed, psCalled, psCtxAlive)
	}
	notes := strings.Join(run.receipt.Notes, "\n")
	if strings.Contains(notes, "context canceled") || strings.Contains(notes, "读取失败") {
		t.Fatalf("收尾不应因已取消的父 ctx 失败：%s", notes)
	}
	if !strings.Contains(notes, "各容器实际运行的镜像") {
		t.Fatalf("收据应含运行镜像摘录：%s", notes)
	}
}

func TestDeploySwitchRollbackFailureExit3(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return errors.New("down") }}
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 3 || !strings.Contains(err.Error(), "不重试") || len(site.starts) != 2 {
		t.Fatalf("回滚失败应退出码 3 且不重试：%v starts=%v", err, site.starts)
	}
	if !strings.Contains(err.Error(), "auriga status") || !strings.Contains(err.Error(), "auriga deploy <commit> --full") {
		t.Fatalf("回滚失败错误应含人工收敛指引：%v", err)
	}
}

func TestDoDeployRejectsServicesAndRebuildsWhenPrevFailed(t *testing.T) {
	stubTrustedSource(t)
	stubSiteOwner(t, nil)
	s := deployTestSite(t,
		deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")},
		deployRecord{
			Seq:           2,
			Status:        recordFailed,
			Images:        imagesFor("2"),
			Fingerprints:  map[string]string{"web-panel": "same", "platform-jar": "same", brokerArtifact: "same"},
			Assembly:      map[string]string{"edge": "same", "platform": "same"},
			Orchestration: "same-orch",
		},
	)
	writeTestRelease(t, &s, 2)
	cfg := &siteConfig{
		Server: &siteServer{Data: s.Data},
		Deps:   &siteDeps{Cache: filepath.Join(t.TempDir(), "cache")},
	}
	run, _ := testRun()
	err := doDeploy(context.Background(), defaultSiteOps, cfg, "", s.Data, deployOptions{Services: []string{"edge"}}, run)
	if exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "状态为 failed") || !strings.Contains(err.Error(), "--services") {
		t.Fatalf("上一版 failed 时使用 --services 应拒绝：%v", err)
	}

	// 不带 --services 的 dry-run：不得复用上一版失败记录的指纹/产物，且切换预估必须走 switchModeFull。
	src := fakeRecipeSource(t, nil, validDockerfile)
	putFile(t, filepath.Join(src, "deploy/nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	putFile(t, filepath.Join(src, "deploy/site/services.json"), `{
  "schemaVersion": 1,
  "images": {
    "platform": {"artifacts": ["platform.jar"]},
    "edge": {"artifacts": ["auriga-edge", "web"]},
    "agent": {"artifacts": ["auriga-agent"]},
    "bifrost": {"artifacts": ["bifrost"]},
    "operator": {"artifacts": ["operator-runtime"]},
    "auriga-nas-bootstrap": {"pseudo": true, "artifacts": ["auriga-nas-bootstrap"]},
    "runtime-release": {"pseudo": true, "artifacts": ["runtime-release"]}
  },
  "artifacts": {
    "platform.jar": {"inputSets": ["java-lock"]},
    "auriga-edge": {"inputSets": ["go-lock"]},
    "web": {"inputSets": ["web-lock"]},
    "auriga-agent": {"inputSets": ["go-lock"]},
    "bifrost": {"inputSets": ["go-lock"]},
    "operator-runtime": {"inputSets": ["py-lock"]},
    "auriga-nas-bootstrap": {"inputSets": ["go-lock"]},
    "runtime-release": {"inputSets": ["go-lock"]}
  },
  "inputSets": {
    "go-lock": {"kind": "deps-lock", "ecosystem": "go"},
    "web-lock": {"kind": "deps-lock", "ecosystem": "web"},
    "java-lock": {"kind": "deps-lock", "ecosystem": "java"},
    "py-lock": {"kind": "deps-lock", "ecosystem": "python"}
  },
  "ignore": []
}`, 0644)
	for _, rel := range []string{
		"web/package.json", "clients/package.json", "package.json", "pnpm-workspace.yaml", ".npmrc", "web/pnpm-lock.yaml",
		"services/pom.xml", "pom.xml", "mvnw", ".mvn/wrapper/maven-wrapper.properties", ".mvn/wrapper/maven-wrapper.jar",
		"services/contracts/package.json", "services/contracts/package-lock.json", mavenDepsLockRel,
		pythonRequirementsRel, "deploy/nas/offline-build/system-inputs.sh",
	} {
		putFile(t, filepath.Join(src, filepath.FromSlash(rel)), "x\n", 0644)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "init"},
	} {
		if err := deployRun(context.Background(), runSpec{Name: "git", Args: append([]string{"-C", src}, args...)}); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// 先算一次该极简源码树的真实指纹，故意塞进失败的 prev.Fingerprints 里：
	// 如果 doDeploy 错误复用了失败记录的指纹，就会跳过 web/java 构建！
	srcLocks, err := sourceEcosystemLocks(src)
	if err != nil {
		t.Fatal(err)
	}
	lockMap := map[string]string{}
	for _, l := range srcLocks {
		lockMap[l.Ecosystem] = l.Digest
	}
	recRecipe, err := loadDeployRecipe(src)
	if err != nil {
		t.Fatal(err)
	}
	recSvcs, err := loadSiteServices(src)
	if err != nil {
		t.Fatal(err)
	}
	fpRep, err := computeAllFingerprints(context.Background(), fingerprintContext{Source: src, Services: recSvcs, Recipe: recRecipe, Locks: lockMap, Tools: recRecipe.Images})
	if err != nil {
		t.Fatal(err)
	}
	s.record(2).Fingerprints = fingerprintMap(fpRep)
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}

	stubDataVersions(t, &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}})
	stubDeployRun(t, func(runSpec) (string, error) { return "", errors.New("no docker") })
	run2, out2 := testRun()
	err = doDeploy(context.Background(), defaultSiteOps, cfg, "", s.Data, deployOptions{
		DryRun: true,
		Source: src,
	}, run2)
	if err != nil {
		t.Fatalf("dry-run 不应失败：%v\n%s", err, out2.String())
	}
	text := out2.String()
	if strings.Contains(text, "跳过构建") {
		t.Fatalf("上一版 failed 时不得跳过构建复用产物：\n%s", text)
	}
	if !strings.Contains(text, "将构建产物生态：go、web、java") {
		t.Fatalf("上一版 failed 时必须重建 go、web、java：\n%s", text)
	}
	if run2.receipt.SwitchMode != switchModeFull {
		t.Fatalf("上一版 failed 时切换预估必须为完整启动，得 %q：\n%s", run2.receipt.SwitchMode, text)
	}
}

func TestDeploySwitchSuccessRecordsVersions(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	v := &dataVersions{Migrations: map[string]string{"auriga/x": "3:3:c"}, Formats: map[string]string{"materials": "1"}}
	stubDataVersions(t, v, v)
	site := &fakeSite{healthy: func(int) error { return nil }}
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil); err != nil {
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

// 2b：数据已被新版本改动时，自动回滚按新发行的 floor 与上一版的 formatMax 判定。
func TestDeploySwitchAutoRollbackByFloor(t *testing.T) {
	maxAll := map[string]string{"materials": "1", "collab-keys": "1", "materials-upgrades": "1", "relay": "1"}
	for _, tc := range []struct {
		name     string
		prevMax  map[string]string
		recMax   map[string]string
		wantCode int
	}{
		{"双方已声明且 floor 为设计起点", maxAll, maxAll, 2},
		{"上一版未声明 formatMax", nil, maxAll, 3},
		{"新发行未声明 floor", maxAll, nil, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, prev, rec, cfg := switchFixture(t)
			prev.FormatMax = tc.prevMax
			if err := saveState(s); err != nil {
				t.Fatal(err)
			}
			rec.FormatMax = tc.recMax
			stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}, &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{"materials": "1"}})
			site := &fakeSite{healthy: func(n int) error {
				if n == 1 {
					return errors.New("platform unhealthy")
				}
				return nil
			}}
			run, _ := testRun()
			err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
			if exitCodeOf(err) != tc.wantCode || run.receipt.RolledBack != (tc.wantCode == 2) {
				t.Fatalf("期望退出码 %d：%v（%s）", tc.wantCode, err, run.receipt.RollbackReason)
			}
		})
	}
}

// 2b：手动 rollback 的 floor 路径（真实祖先判定由 fakeGit 模拟）。
func TestManualRollbackByFloor(t *testing.T) {
	old, mid, cur := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	fakeGit(t, map[string]bool{old: true, mid: true, cur: true}, map[string][]string{old: {mid, cur}, mid: {cur}})
	maxAll := map[string]string{"materials": "1", "collab-keys": "1", "materials-upgrades": "1", "relay": "1"}
	for _, tc := range []struct {
		name     string
		floor    string
		wantCode int
	}{
		{"目标等于 floor", old, 0},
		{"目标早于 floor", mid, 3},
		{"floor 不在暂存仓库", strings.Repeat("9", 40), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubTrustedSource(t)
			s := deployTestSite(t,
				deployRecord{Seq: 1, Status: recordOK, Source: deploySource{Mode: "git", Commit: old}, Images: imagesFor("1"), Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}, FormatMax: maxAll},
				deployRecord{Seq: 2, Status: recordOK, Source: deploySource{Mode: "git", Commit: cur}, Images: imagesFor("2"), SchemaCompatFloor: tc.floor, FormatMax: maxAll})
			writeTestRelease(t, &s, 1)
			stubDataVersions(t, &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{}})
			run, _ := testRun()
			site := &fakeSite{healthy: func(int) error { return nil }}
			err := doRollback(context.Background(), site.ops(), &siteConfig{Server: &siteServer{Data: s.Data}}, s.Data, 1, run)
			if tc.wantCode == 0 {
				// 判定通过即可；之后的镜像核对由 fakeGit 桩拦下，不在本测试范围。
				if exitCodeOf(err) == 3 || !strings.Contains(run.receipt.RollbackReason, "允许回滚到 r1") {
					t.Fatalf("floor 判定应通过：%v（%s）", err, run.receipt.RollbackReason)
				}
				return
			}
			if exitCodeOf(err) != tc.wantCode {
				t.Fatalf("期望退出码 %d：%v（%s）", tc.wantCode, err, run.receipt.RollbackReason)
			}
			if len(site.starts) != 0 {
				t.Fatalf("拒绝时不应启动：%v", site.starts)
			}
		})
	}
}
