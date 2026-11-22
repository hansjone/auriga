package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunToolBuildMountsGoCacheAndEnv(t *testing.T) {
	calls := stubDeployRun(t, func(r runSpec) (string, error) { return "", nil })
	cacheHost := filepath.Join(t.TempDir(), "gocache")
	if err := os.Mkdir(cacheHost, 0700); err != nil {
		t.Fatal(err)
	}
	tool := "sha256:" + strings.Repeat("f", 64)
	if err := runToolBuild(context.Background(), "go", tool, "/src", "/inputs", "/out", buildBudget{}, toolBuildOpts{GoCacheHost: cacheHost}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("调用次数 %d", len(*calls))
	}
	args := strings.Join((*calls)[0].Args, " ")
	for _, want := range []string{
		"--network none",
		"src=/src,dst=/source,readonly",
		"src=/inputs,dst=/inputs,readonly",
		"src=" + cacheHost + ",dst=/gocache",
		"AURIGA_GO_BUILD_CACHE=/gocache",
		"AURIGA_GO_PROXY_DIR=/inputs",
		"go-inputs.sh build /output",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("缺少 %q\n%s", want, args)
		}
	}
	*calls = nil
	if err := runToolBuild(context.Background(), "go", tool, "/src", "/inputs", "/out", buildBudget{}, toolBuildOpts{}); err != nil {
		t.Fatal(err)
	}
	args = strings.Join((*calls)[0].Args, " ")
	if strings.Contains(args, "/gocache") || strings.Contains(args, "AURIGA_GO_BUILD_CACHE") {
		t.Fatalf("--full/无缓存时不应挂 GOCACHE：%s", args)
	}
	*calls = nil
	if err := runToolBuild(context.Background(), "web", tool, "/src", "/inputs", "/out", buildBudget{}, toolBuildOpts{GoCacheHost: cacheHost}); err != nil {
		t.Fatal(err)
	}
	args = strings.Join((*calls)[0].Args, " ")
	if strings.Contains(args, "AURIGA_GO_BUILD_CACHE") {
		t.Fatal("web 构建不应挂 Go 缓存")
	}
}

func TestAssembleImageNoCacheFlag(t *testing.T) {
	calls := stubDeployRun(t, func(r runSpec) (string, error) {
		if len(r.Args) > 0 && r.Args[0] == "image" {
			return "sha256:" + strings.Repeat("1", 64), nil
		}
		return "", nil
	})
	id, err := assembleImage(context.Background(), "edge", "base:tag", "/df", "/scratch", "auriga/edge:r1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !imageIDPattern.MatchString(id) {
		t.Fatalf("镜像 ID：%s", id)
	}
	var buildArgs []string
	for _, c := range *calls {
		if len(c.Args) > 0 && c.Args[0] == "build" {
			buildArgs = c.Args
		}
	}
	joined := strings.Join(buildArgs, " ")
	if strings.Contains(joined, "--no-cache") {
		t.Fatalf("非 --full 不应带 --no-cache：%s", joined)
	}
	for _, want := range []string{"--network=none", "--pull=false", "--provenance=false", "--sbom=false", "--target", "edge"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 %q：%s", want, joined)
		}
	}
	*calls = nil
	if _, err := assembleImage(context.Background(), "edge", "base:tag", "/df", "/scratch", "auriga/edge:r2", true); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if len(c.Args) > 0 && c.Args[0] == "build" {
			if !containsString(c.Args, "--no-cache") {
				t.Fatalf("--full 应带 --no-cache：%v", c.Args)
			}
		}
	}
}

func TestAssemblySwitchIgnoresImageID(t *testing.T) {
	// 切换只看装配摘要：摘要相同且旧镜像在本机时 decideAssembly 直接跳过装配，
	// 沿用旧镜像 ID，planSwitchWith 判定为仅切指针，与层缓存无关。
	oldID := "sha256:" + strings.Repeat("a", 64)
	prev := &deployRecord{
		Seq:           1,
		Kind:          recordKindDeploy,
		Orchestration: "orch-1",
		Assembly:      map[string]string{"edge": "same-digest"},
		Images:        map[string]string{imageKey("edge"): oldID},
		Fingerprints:  map[string]string{brokerArtifact: "fp-broker-1"},
	}
	same := assemblyParts{Total: "same-digest"}
	needBuild, reason := decideAssembly(false, false, "edge", prev, same, func(id string) bool { return id == oldID })
	if needBuild || !strings.Contains(reason, "跳过切换") {
		t.Fatalf("装配摘要相同时应跳过装配：needBuild=%v reason=%q", needBuild, reason)
	}
	// --full 即使装配摘要相同也必须重建。
	needBuild, reason = decideAssembly(false, true, "edge", prev, same, func(string) bool { return true })
	if !needBuild || !strings.Contains(reason, "--full") {
		t.Fatalf("--full 应强制重建：needBuild=%v reason=%q", needBuild, reason)
	}
	// 装配摘要变化时触发装配；若层缓存产出新镜像 ID 则进入切换，若所有镜像未变则仅切指针。
	changed := assemblyParts{Total: "other-digest"}
	needBuild, reason = decideAssembly(false, false, "edge", prev, changed, func(string) bool { return true })
	if !needBuild {
		t.Fatalf("装配摘要变化应触发装配：reason=%q", reason)
	}
	sameImgRec := &deployRecord{
		Seq:          2,
		Kind:         recordKindDeploy,
		Images:       map[string]string{imageKey("edge"): oldID},
		Fingerprints: map[string]string{brokerArtifact: "fp-broker-1"},
	}
	if plan := planSwitchWith(prev, sameImgRec, "orch-1", t.TempDir()); plan.Mode != switchModePointer {
		t.Fatalf("镜像 ID 与编排均未变应仅切指针：got %s (%s)", plan.Mode, plan.Reason)
	}
	newImgRec := &deployRecord{
		Seq:          2,
		Kind:         recordKindDeploy,
		Images:       map[string]string{imageKey("edge"): "sha256:" + strings.Repeat("b", 64)},
		Fingerprints: map[string]string{brokerArtifact: "fp-broker-1"},
	}
	if plan := planSwitchWith(prev, newImgRec, "orch-1", t.TempDir()); plan.Mode != switchModeContainers {
		t.Fatalf("edge 新镜像且 broker 指纹不变应只换容器：got %s (%s)", plan.Mode, plan.Reason)
	}
}

func TestPlanGoBuildCacheAndReceiptShape(t *testing.T) {
	hex := strings.Repeat("9", 64)
	cache := `/srv/auriga/cache`
	got := planGoBuildCache(cache, hex)
	if !strings.Contains(got, filepath.Join("build", "go", hex)) && !strings.Contains(got, "build/go/"+hex) && !strings.Contains(got, `build\go\`+hex) {
		t.Fatalf("计划路径异常：%s", got)
	}
	if planGoBuildCache(cache, "bad") != "" {
		t.Fatal("非法 digest 应返回空")
	}
	rc := &siteReceipt{BuildCache: &receiptBuildCache{
		GoUsed: true, GoPath: got, GoToolImage: "golang@sha256:" + hex, AssemblyLayerCache: true,
	}}
	raw, err := json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"buildCache":{`, `"goUsed":true`, `"goPath":`, `"goToolImage":"golang@sha256:` + hex + `"`, `"assemblyLayerCache":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("收据 JSON 缺少 %s：%s", want, text)
		}
	}
	full := &siteReceipt{BuildCache: &receiptBuildCache{GoUsed: false, AssemblyLayerCache: false}}
	fullRaw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	fullText := string(fullRaw)
	if !strings.Contains(fullText, `"goUsed":false`) || !strings.Contains(fullText, `"assemblyLayerCache":false`) || strings.Contains(fullText, `"goPath"`) {
		t.Fatalf("--full 收据 JSON 异常：%s", fullText)
	}
}

func TestGoInputsBuildCacheEnvDefaultScratch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy/nas/offline-build/go-inputs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "\r") {
		t.Fatal("go-inputs.sh 必须是 LF")
	}
	if !strings.Contains(text, "AURIGA_GO_BUILD_CACHE") {
		t.Fatal("缺少 AURIGA_GO_BUILD_CACHE 支持")
	}
	// 未设置环境变量时 GOCACHE 必须落到 scratch；有变量且 mode=build 才用持久化路径。
	if !strings.Contains(text, `export GOCACHE="$scratch/build"`) {
		t.Fatal("默认仍须使用 scratch/build 作 GOCACHE")
	}
	if !strings.Contains(text, `export GOCACHE="$AURIGA_GO_BUILD_CACHE"`) {
		t.Fatal("设置 AURIGA_GO_BUILD_CACHE 时应导出为 GOCACHE")
	}
	// 确认仅 build 模式读取该变量（prepare/deps 不受影响）。
	idx := strings.Index(text, `if [ "$mode" = build ] && [ -n "${AURIGA_GO_BUILD_CACHE:-}" ]`)
	if idx < 0 {
		t.Fatal("AURIGA_GO_BUILD_CACHE 必须限定在 build 模式")
	}
	// GOMODCACHE 始终 scratch，不持久化。
	if !strings.Contains(text, `export GOMODCACHE="$scratch/modules"`) {
		t.Fatal("GOMODCACHE 必须仍是 scratch/modules")
	}
	if strings.Contains(text, "AURIGA_GO_MOD_CACHE") || strings.Contains(text, "GOMODCACHE=\"$AURIGA") {
		t.Fatal("不应持久化 GOMODCACHE")
	}
}

func TestAssembleImageShKeepsNoCache(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy/nas/offline-build/assemble-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--no-cache") {
		t.Fatal("bundle 路径 assemble-image.sh 须保持 --no-cache")
	}
}
