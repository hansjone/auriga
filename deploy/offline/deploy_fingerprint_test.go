package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestLoadSiteServicesAndGoPrograms(t *testing.T) {
	root := repoRoot(t)
	s, err := loadSiteServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Images["auriga-nas-bootstrap"]; !ok || !s.Images["auriga-nas-bootstrap"].Pseudo {
		t.Fatal("伪服务 auriga-nas-bootstrap 未正确声明")
	}
	g, err := loadGoPrograms(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Programs["auriga-edge"]; !ok {
		t.Fatal("缺少 auriga-edge 程序表")
	}
}

// go-programs.json（覆盖测试用的程序表）必须与 go-inputs.sh 的 record_run 表一致：
// 指纹用的 go list -deps 跑的是后者，两者漂移会让覆盖测试按错误的目录放行文件。
func TestGoProgramsMatchGoInputsScript(t *testing.T) {
	root := repoRoot(t)
	g, err := loadGoPrograms(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy/nas/offline-build/go-inputs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^record_run (\S+) (\S+) (\S+) (.+)$`)
	got := map[string]goProgramSpec{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		ws := "infra/go.work"
		if m[2] == "off" {
			ws = "off"
		} else if m[2] != `"$work"` {
			t.Fatalf("go-inputs.sh 的 workspace 形式未知：%s", m[2])
		}
		got[m[3]] = goProgramSpec{Directory: m[1], Workspace: ws, Packages: strings.Fields(m[4])}
	}
	if len(got) != len(g.Programs) {
		t.Fatalf("程序数不一致：go-inputs.sh %d，go-programs.json %d", len(got), len(g.Programs))
	}
	for name, want := range g.Programs {
		have, ok := got[name]
		if !ok || have.Directory != want.Directory || have.Workspace != want.Workspace || strings.Join(have.Packages, " ") != strings.Join(want.Packages, " ") {
			t.Fatalf("程序 %s 不一致：go-inputs.sh %+v，go-programs.json %+v", name, have, want)
		}
	}
	s, err := loadSiteServices(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, is := range s.InputSets {
		if is.Kind == "go-program" {
			if _, ok := got[is.Program]; !ok {
				t.Fatalf("输入集 %s 引用的程序 %s 不在 go-inputs.sh 里", name, is.Program)
			}
		}
	}
}

func TestParseGoDeps(t *testing.T) {
	m, err := parseGoDeps([]byte("auriga-agent /source/infra/agent\r\nauriga-agent /usr/local/go/src/fmt\n\nbifrost /source/infra/ai-gateway/cmd/relay-backend\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m["auriga-agent"]) != 2 || len(m["bifrost"]) != 1 {
		t.Fatalf("解析结果不对：%v", m)
	}
	for _, bad := range []string{"", "auriga-agent", "auriga-agent relative/dir", "auriga-agent [] /x"} {
		if _, err := parseGoDeps([]byte(bad)); err == nil {
			t.Fatalf("无效输出 %q 应被拒绝", bad)
		}
	}
}

// runGoDeps 的容器参数：断网、只读挂源码与依赖输入、调用源码配方 go-inputs.sh deps。
func TestRunGoDepsInvocation(t *testing.T) {
	var seen []string
	stubDeployRun(t, func(r runSpec) (string, error) {
		seen = r.Args
		for i, a := range r.Args {
			if a == "--mount" && strings.HasSuffix(r.Args[i+1], ",dst=/output") {
				dir := strings.TrimSuffix(strings.TrimPrefix(r.Args[i+1], "type=bind,src="), ",dst=/output")
				return "", os.WriteFile(filepath.Join(dir, "deps.txt"), []byte("auriga-agent /source/infra/agent\n"), 0644)
			}
		}
		return "", nil
	})
	m, err := runGoDeps(context.Background(), "sha256:tool", "/src", "/inputs-go", t.TempDir(), buildBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m["auriga-agent"]) != 1 {
		t.Fatalf("结果不对：%v", m)
	}
	joined := strings.Join(seen, " ")
	for _, want := range []string{"--network none", "--pull never", "src=/src,dst=/source,readonly", "src=/inputs-go,dst=/inputs,readonly", "AURIGA_GO_PROXY_DIR=/inputs", "sha256:tool sh /source/deploy/nas/offline-build/go-inputs.sh deps /output"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker 参数缺少 %q：%s", want, joined)
		}
	}
}

// fingerprintFixture 搭一个最小源码树与精简 services，用注入的 go list 结果计算指纹。
func fingerprintFixture(t *testing.T) (string, fingerprintContext) {
	t.Helper()
	src := t.TempDir()
	putFile(t, filepath.Join(src, "deploy/site/input-sets/web-pack.list"), "web\n", 0644)
	putFile(t, filepath.Join(src, "deploy/site/input-sets/java-pack.list"), "pom.xml\n", 0644)
	for _, eco := range []string{"go", "web", "java"} {
		putFile(t, filepath.Join(src, "deploy/nas/offline-build/"+eco+"-inputs.sh"), "#!/bin/sh\n# "+eco+"\n", 0755)
	}
	putFile(t, filepath.Join(src, "web/a.txt"), "web-v1\n", 0644)
	putFile(t, filepath.Join(src, "web/node_modules/x/index.js"), "ignored\n", 0644)
	putFile(t, filepath.Join(src, "pom.xml"), "<project/>\n", 0644)
	putFile(t, filepath.Join(src, "go.mod"), "module example\n", 0644)
	putFile(t, filepath.Join(src, "deploy/nas/test/admin-bootstrap-requirements.txt"), "requests==1\n", 0644)
	putFile(t, filepath.Join(src, "pkg/agent/main.go"), "package main\n", 0644)
	putFile(t, filepath.Join(src, "pkg/bifrost/main.go"), "package main\n", 0644)
	putFile(t, filepath.Join(src, "pkg/shared/lib.go"), "package shared\n", 0644)
	putFile(t, filepath.Join(src, "pkg/shared/static/embedded.txt"), "v1\n", 0644)
	putFile(t, filepath.Join(src, "pkg/boot/main.go"), "package main\n", 0644)
	putFile(t, filepath.Join(src, "pkg/rr/main.go"), "package main\n", 0644)

	services := &siteServices{
		Schema: 1,
		Images: map[string]siteImage{
			"agent":                {Artifacts: []string{"auriga-agent"}},
			"bifrost":              {Artifacts: []string{"bifrost"}},
			"edge":                 {Artifacts: []string{"web"}},
			"platform":             {Artifacts: []string{"platform.jar"}},
			"operator":             {Artifacts: []string{"operator-runtime"}},
			"auriga-nas-bootstrap": {Pseudo: true, Artifacts: []string{"auriga-nas-bootstrap"}},
			"runtime-release":      {Pseudo: true, Artifacts: []string{"runtime-release"}},
		},
		Artifacts: map[string]siteArtifact{
			"auriga-agent":         {InputSets: []string{"go-auriga-agent", "go-mod-files", "deps-go", "tool-go"}},
			"bifrost":              {InputSets: []string{"go-bifrost", "go-mod-files", "deps-go", "tool-go"}},
			"web":                  {InputSets: []string{"web-pack", "deps-web", "tool-web"}},
			"platform.jar":         {InputSets: []string{"java-pack", "deps-java", "tool-java"}},
			"operator-runtime":     {InputSets: []string{"deps-python", "assemble-operator"}},
			"auriga-nas-bootstrap": {InputSets: []string{"go-bootstrap", "go-mod-files"}},
			"runtime-release":      {InputSets: []string{"go-runtime", "go-mod-files"}},
		},
		InputSets: map[string]siteInputSet{
			"go-auriga-agent":   {Kind: "go-program", Program: "auriga-agent"},
			"go-bifrost":        {Kind: "go-program", Program: "bifrost"},
			"go-bootstrap":      {Kind: "go-program", Program: "auriga-nas-bootstrap"},
			"go-runtime":        {Kind: "go-program", Program: "runtime-release"},
			"go-mod-files":      {Kind: "go-mod-files"},
			"web-pack":          {Kind: "pack-list", File: "deploy/site/input-sets/web-pack.list"},
			"java-pack":         {Kind: "pack-list", File: "deploy/site/input-sets/java-pack.list"},
			"deps-go":           {Kind: "deps-lock", Ecosystem: "go"},
			"deps-web":          {Kind: "deps-lock", Ecosystem: "web"},
			"deps-java":         {Kind: "deps-lock", Ecosystem: "java"},
			"deps-python":       {Kind: "deps-lock", Ecosystem: "python"},
			"tool-go":           {Kind: "tool-image", Role: "tool-go"},
			"tool-web":          {Kind: "tool-image", Role: "tool-web"},
			"tool-java":         {Kind: "tool-image", Role: "tool-java"},
			"assemble-operator": {Kind: "assemble", Service: "operator"},
		},
	}
	recipe := &deployRecipe{
		Images: map[string]string{
			"base-operator": "python@sha256:" + strings.Repeat("a", 64),
			"tool-go":       "golang@sha256:" + strings.Repeat("b", 64),
			"tool-web":      "node@sha256:" + strings.Repeat("c", 64),
			"tool-java":     "temurin@sha256:" + strings.Repeat("d", 64),
		},
		Services: map[string]recipeService{
			"operator": {Stage: []stageItem{
				{Kind: "python"},
				{Kind: "source", Sources: map[string]string{"requirements.txt": "deploy/nas/test/admin-bootstrap-requirements.txt"}},
			}},
		},
	}
	goDeps := map[string][]string{
		// 共享包 pkg/shared 只被 agent 编译；GOROOT 与模块缓存目录不进源码指纹。
		"auriga-agent":         {"/source/pkg/agent", "/source/pkg/shared", "/usr/local/go/src/fmt", "/tmp/modules/github.com/x/y@v1"},
		"bifrost":              {"/source/pkg/bifrost"},
		"auriga-nas-bootstrap": {"/source/pkg/boot"},
		"runtime-release":      {"/source/pkg/rr"},
	}
	return src, fingerprintContext{
		Source: src, Services: services, Recipe: recipe, GoDeps: goDeps,
		Locks: map[string]string{"go": "g", "web": "w", "java": "j", "python": "p", "system": "s"},
		Tools: recipe.Images,
	}
}

func fingerprintsOf(t *testing.T, fc fingerprintContext) map[string]string {
	t.Helper()
	r, err := computeAllFingerprints(context.Background(), fc)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprintMap(r)
}

// changedArtifacts 返回指纹与 base 不同的产物（排序）。
func changedArtifacts(base, cur map[string]string) []string {
	var out []string
	for name, d := range cur {
		if base[name] != d {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func TestFingerprintStableAndScoped(t *testing.T) {
	src, fc := fingerprintFixture(t)
	base := fingerprintsOf(t, fc)
	if again := fingerprintsOf(t, fc); len(changedArtifacts(base, again)) != 0 {
		t.Fatal("同样输入指纹不稳定")
	}
	expect := func(what string, want ...string) {
		t.Helper()
		cur := fingerprintsOf(t, fc)
		got := changedArtifacts(base, cur)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s：变化的产物应为 %v，实际 %v", what, want, got)
		}
		base = cur
	}
	putFile(t, filepath.Join(src, "web/a.txt"), "web-v2\n", 0644)
	expect("改 web 打包清单内文件", "web")
	putFile(t, filepath.Join(src, "web/node_modules/x/index.js"), "changed\n", 0644)
	expect("node_modules 与打包脚本一样排除")
	putFile(t, filepath.Join(src, "pkg/shared/lib.go"), "package shared\n// v2\n", 0644)
	expect("改 agent 依赖的源码树内包", "auriga-agent")
	putFile(t, filepath.Join(src, "pkg/shared/static/embedded.txt"), "v2\n", 0644)
	expect("改包目录下的嵌入文件", "auriga-agent")
	putFile(t, filepath.Join(src, "pkg/boot/main.go"), "package main\n// changed\n", 0644)
	expect("伪服务源码", "auriga-nas-bootstrap")
	putFile(t, filepath.Join(src, "go.mod"), "module example\n\ngo 1.27\n", 0644)
	expect("go.mod 影响全部 Go 产物", "auriga-agent", "bifrost", "auriga-nas-bootstrap", "runtime-release")
	putFile(t, filepath.Join(src, "deploy/nas/offline-build/go-inputs.sh"), "#!/bin/sh\n# go -ldflags\n", 0755)
	expect("Go 构建脚本", "auriga-agent", "bifrost")
	putFile(t, filepath.Join(src, "deploy/nas/offline-build/java-inputs.sh"), "#!/bin/sh\n# java v2\n", 0755)
	expect("Java 构建脚本", "platform.jar")
	fc.Locks["web"] = "w2"
	expect("Web 依赖锁摘要", "web")
	fc.Tools = map[string]string{"tool-go": "golang@sha256:" + strings.Repeat("e", 64), "tool-web": fc.Recipe.Images["tool-web"], "tool-java": fc.Recipe.Images["tool-java"]}
	expect("Go 工具镜像", "auriga-agent", "bifrost")
	putFile(t, filepath.Join(src, "deploy/nas/test/admin-bootstrap-requirements.txt"), "requests==2\n", 0644)
	expect("operator 装配源文件", "operator-runtime")
	fc.Locks["python"] = "p2"
	expect("python 依赖锁摘要", "operator-runtime")
}

func TestFingerprintGoDepsErrorsAndDryRun(t *testing.T) {
	_, fc := fingerprintFixture(t)
	delete(fc.GoDeps, "bifrost")
	if _, err := computeAllFingerprints(context.Background(), fc); err == nil || !strings.Contains(err.Error(), "bifrost") {
		t.Fatalf("go list 缺少程序时应失败：%v", err)
	}
	_, fc = fingerprintFixture(t)
	fc.GoDeps["bifrost"] = []string{"/source/../etc"}
	if _, err := computeAllFingerprints(context.Background(), fc); err == nil {
		t.Fatal("源码挂载点之外的 .. 目录应被拒绝")
	}
	_, fc = fingerprintFixture(t)
	real := fingerprintsOf(t, fc)
	fc.GoDeps = nil
	dry := fingerprintsOf(t, fc)
	for _, a := range []string{"auriga-agent", "bifrost", "auriga-nas-bootstrap", "runtime-release"} {
		if dry[a] == real[a] {
			t.Fatalf("--dry-run 未展开 Go 依赖时 %s 不能显示为未变", a)
		}
	}
	if dry["web"] != real["web"] || dry["platform.jar"] != real["platform.jar"] {
		t.Fatal("--dry-run 不应影响非 Go 产物指纹")
	}
}

func TestExpandSelectedServicesSharedInput(t *testing.T) {
	spec := &siteServices{
		Images: map[string]siteImage{
			"edge":     {Artifacts: []string{"web"}},
			"platform": {Artifacts: []string{"platform.jar"}},
			"agent":    {Artifacts: []string{"auriga-agent"}},
		},
		Artifacts: map[string]siteArtifact{
			"web":          {InputSets: []string{"shared", "web-only"}},
			"platform.jar": {InputSets: []string{"java-only"}},
			"auriga-agent": {InputSets: []string{"shared", "agent-only"}},
		},
	}
	report := &fingerprintReport{Artifacts: map[string]artifactFingerprint{
		"web":          {Digest: "web-new", InputSets: []string{"shared", "web-only"}},
		"platform.jar": {Digest: "java-same", InputSets: []string{"java-only"}},
		"auriga-agent": {Digest: "agent-new", InputSets: []string{"shared", "agent-only"}},
	}}
	prev := map[string]string{"web": "web-old", "platform.jar": "java-same", "auriga-agent": "agent-old"}
	chosen, added := expandSelectedServices(spec, []string{"edge"}, report, prev)
	if len(chosen) != 2 || len(added) != 1 || added[0] != "agent" {
		t.Fatalf("应自动补上 agent：chosen=%v added=%v", chosen, added)
	}
	for _, c := range chosen {
		if c == "platform" {
			t.Fatal("不应补上未共享输入的 platform")
		}
	}
	// 共享输入但指纹未变的服务不补。
	prev["auriga-agent"] = "agent-new"
	if _, added := expandSelectedServices(spec, []string{"edge"}, report, prev); len(added) != 0 {
		t.Fatalf("指纹未变不应补上：%v", added)
	}
}

func TestReuseArtifactsFromRelease(t *testing.T) {
	stubTrustedSource(t)
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	src, out := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(src, "deploy/nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	writeArtifactDir(t, filepath.Join(out, "go"), map[string]string{"auriga-nas-bootstrap": "boot", "runtime-release": "rr", "auriga-agent": "agent"})
	writeArtifactDir(t, filepath.Join(out, "web"), map[string]string{"admin/index.html": "<html>", "panel/index.html": "p"})
	if err := os.MkdirAll(filepath.Join(out, "java"), 0755); err != nil {
		t.Fatal(err)
	}
	report := &fingerprintReport{Artifacts: map[string]artifactFingerprint{
		"auriga-nas-bootstrap": {Digest: "b"}, "runtime-release": {Digest: "r"}, "auriga-agent": {Digest: "a"},
		"web": {Digest: "w"}, "platform.jar": {Digest: "j"}, "operator-runtime": {Digest: "o"},
	}}
	kept := recordedFingerprints(report, out)
	if _, ok := kept.Artifacts["platform.jar"]; ok {
		t.Fatal("本次没有的 platform.jar 不应记录指纹")
	}
	for _, a := range []string{"auriga-nas-bootstrap", "runtime-release", "auriga-agent", "web", "operator-runtime"} {
		if _, ok := kept.Artifacts[a]; !ok {
			t.Fatalf("应记录 %s 的指纹", a)
		}
	}
	dir, sums, err := writeReleaseDir(context.Background(), s.Data, 1, src, filepath.Join(out, "go"), out, releaseManifest{}, &provenance{Commit: "c"}, kept, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"artifacts/go/auriga-agent", "artifacts/web/admin/index.html", "provenance.json", "fingerprints.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Fatalf("发行目录缺少 %s", want)
		}
	}
	prev := s.record(1)
	prev.ReleaseSums = sums
	names := []string{"auriga-agent", "auriga-nas-bootstrap", "web"}
	reuse := func() (string, error) {
		target := t.TempDir()
		for _, k := range []string{"go", "web", "java"} {
			if err := os.MkdirAll(filepath.Join(target, k), 0755); err != nil {
				t.Fatal(err)
			}
		}
		return target, reuseArtifactsFromRelease(context.Background(), s.Data, prev, target, names)
	}
	target, err := reuse()
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(target, "go", "auriga-agent")); string(raw) != "agent" {
		t.Fatal("未复用 auriga-agent")
	}
	if raw, _ := os.ReadFile(filepath.Join(target, "web", "admin", "index.html")); string(raw) != "<html>" {
		t.Fatal("未复用 web")
	}
	if !artifactPresent(target, "web") || artifactPresent(target, "platform.jar") {
		t.Fatal("artifactPresent 判断不对")
	}
	// 上一版产物被篡改：发行目录与 state 锚定的 releaseSums 不符，拒绝复用。
	putFile(t, filepath.Join(dir, "artifacts/go/auriga-agent"), "evil", 0755)
	if _, err := reuse(); err == nil {
		t.Fatal("篡改的上一版产物不能被复用")
	}
	// state 没有记录 releaseSums 时同样拒绝。
	putFile(t, filepath.Join(dir, "artifacts/go/auriga-agent"), "agent", 0755)
	prev.ReleaseSums = ""
	if _, err := reuse(); err == nil {
		t.Fatal("state 未锚定发行目录摘要时不能复用")
	}
	// 2c 之前的发行没有 artifacts/：返回错误，由调用方改为重建。
	prev.ReleaseSums = sums
	if err := os.RemoveAll(filepath.Join(dir, "artifacts")); err != nil {
		t.Fatal(err)
	}
	if _, err := reuse(); err == nil || !strings.Contains(err.Error(), "artifacts") {
		t.Fatalf("没有 artifacts/ 时应报错：%v", err)
	}
	// 没有可复用的产物时什么都不做。
	if err := reuseArtifactsFromRelease(context.Background(), s.Data, prev, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestReusableArtifacts(t *testing.T) {
	spec := &siteServices{Images: map[string]siteImage{
		"agent":    {Artifacts: []string{"auriga-agent"}},
		"edge":     {Artifacts: []string{"web", "auriga-edge"}},
		"operator": {Artifacts: []string{"operator-runtime"}},
	}}
	report := &fingerprintReport{Artifacts: map[string]artifactFingerprint{
		"auriga-agent": {Digest: "a"}, "web": {Digest: "w2"}, "auriga-edge": {Digest: "e"},
		"operator-runtime": {Digest: "o"}, "auriga-nas-bootstrap": {Digest: "b"}, "runtime-release": {Digest: "r"},
	}}
	prev := map[string]string{"auriga-agent": "a", "web": "w1", "auriga-edge": "e", "operator-runtime": "o", "auriga-nas-bootstrap": "b"}
	got := strings.Join(reusableArtifacts(report, prev, []string{"agent", "edge", "operator"}, spec), ",")
	if got != "auriga-agent,auriga-edge,auriga-nas-bootstrap" {
		t.Fatalf("可复用产物不对：%s", got)
	}
}

func TestDockerfileTargetDigest(t *testing.T) {
	base := "ARG AGENT_BASE=a\nARG EDGE_BASE=e\n\nFROM ${AGENT_BASE} AS agent\nCOPY go/auriga-agent /x\n\nFROM ${EDGE_BASE} AS edge\nCOPY web/admin /y\n"
	agent := dockerfileTargetDigest([]byte(base), "agent")
	if dockerfileTargetDigest([]byte(strings.Replace(base, "COPY web/admin /y", "COPY web/admin /z", 1)), "agent") != agent {
		t.Fatal("改其它阶段不应影响 agent 目标段")
	}
	if dockerfileTargetDigest([]byte(strings.Replace(base, "COPY go/auriga-agent /x", "COPY go/auriga-agent /w", 1)), "agent") == agent {
		t.Fatal("改目标段应变化")
	}
	if dockerfileTargetDigest([]byte(strings.Replace(base, "ARG AGENT_BASE=a", "ARG AGENT_BASE=b", 1)), "agent") == agent {
		t.Fatal("改全局 ARG 段应变化")
	}
	// 目标名只做整词匹配。
	tricky := "FROM ${X} AS agent-build\nRUN a\nFROM ${AGENT_BASE} AS agent\nRUN b\n"
	if dockerfileTargetDigest([]byte(tricky), "agent") == dockerfileTargetDigest([]byte(strings.Replace(tricky, "RUN b", "RUN c", 1)), "agent") {
		t.Fatal("agent-build 不应被当成 agent 目标段")
	}
	// 引用其它阶段时退回整个文件。
	multi := "FROM ${B} AS builder\nRUN x\nFROM ${AGENT_BASE} AS agent\nCOPY --from=builder /a /a\n"
	if dockerfileTargetDigest([]byte(multi), "agent") == dockerfileTargetDigest([]byte(strings.Replace(multi, "RUN x", "RUN y", 1)), "agent") {
		t.Fatal("目标段引用 builder 时改 builder 也应变化")
	}
	if dockerfileTargetDigest([]byte(base), "missing") != sha256Hex([]byte(base)) {
		t.Fatal("找不到目标段时应用整个文件")
	}
}

func TestAssemblyChangeReasonTriggers(t *testing.T) {
	old := assemblyParts{Total: "t1", ArtifactsTree: "a", BaseImage: "b", SystemInputs: "s", DockerfileTarget: "d", StageManifest: "m"}
	cases := map[string]func(p *assemblyParts){
		"产物树":            func(p *assemblyParts) { p.ArtifactsTree = "a2" },
		"基础镜像":           func(p *assemblyParts) { p.BaseImage = "b2" },
		"系统输入":           func(p *assemblyParts) { p.SystemInputs = "s2" },
		"Dockerfile 目标段": func(p *assemblyParts) { p.DockerfileTarget = "d2" },
		"暂存清单":           func(p *assemblyParts) { p.StageManifest = "m2" },
	}
	for want, mutate := range cases {
		cur := old
		cur.Total = "t2"
		mutate(&cur)
		if got := assemblyChangeReason(old.Total, &old, cur); !strings.Contains(got, want) {
			t.Fatalf("触发项应含 %s：%s", want, got)
		}
	}
	// 2a 旧 state 只有总摘要：总摘要不同就是“变化”（切换），绝不判成无变化。
	if got := assemblyChangeReason("legacy-total", nil, assemblyParts{Total: "t2"}); got == "" {
		t.Fatal("与只有总摘要的旧记录比较不能判为无变化")
	}
	if got := assemblyChangeReason("", nil, assemblyParts{Total: "t2"}); !strings.Contains(got, "没有装配摘要") {
		t.Fatalf("上一版没有装配摘要：%s", got)
	}
}

func TestCoverageIgnoreAndMatch(t *testing.T) {
	if !pathMatchesIgnore("docs/dev/x.md", []string{"docs/**", "*.md"}) {
		t.Fatal("docs 应被 ignore")
	}
	if pathMatchesIgnore("infra/edge/main.go", []string{"docs/**"}) {
		t.Fatal("源码不应被 docs ignore 误伤")
	}
	// 负例：未归类文件
	covered := map[string]bool{"infra/edge/main.go": true}
	if err := checkCoverage([]string{"infra/edge/main.go", "mystery/new.go"}, covered, []string{"docs/**"}); err == nil {
		t.Fatal("未归类文件应失败")
	}
	if err := checkCoverage([]string{"infra/edge/main.go", "docs/a.md"}, covered, []string{"docs/**"}); err != nil {
		t.Fatal(err)
	}
}
