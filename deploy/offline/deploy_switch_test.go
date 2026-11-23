package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func composePaths(names ...string) []string {
	var out []string
	for _, n := range names {
		out = append(out, filepath.Join("..", "nas", n))
	}
	return out
}

// 读仓库真实 compose 文件双向核对代码表（新增、改名、换插值都会失败）。
func TestComposeImageEnvCrossCheck(t *testing.T) {
	var noCollab []string
	for _, n := range composeCrossCheckFiles {
		if n != "collab-compose.yml" {
			noCollab = append(noCollab, n)
		}
	}
	got, err := parseComposeImageEnvs(composePaths(noCollab...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := crossCheckComposeImages(got, expectedComposeImageEnv(false)); err != nil {
		t.Fatal(err)
	}
	gotCollab, err := parseComposeImageEnvs(composePaths(composeCrossCheckFiles...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := crossCheckComposeImages(gotCollab, expectedComposeImageEnv(true)); err != nil {
		t.Fatal(err)
	}
	if gotCollab["collab-renew"] != "AURIGA_INSTALLER_IMAGE" || gotCollab["collab"] != "AURIGA_IMAGE_PLATFORM" {
		t.Fatalf("collab overlay 插值不对：%v", gotCollab)
	}
	if _, ok := gotCollab["operator"]; ok {
		t.Fatal("operator 不是 compose 服务，不应出现")
	}
	// 代码表本身：edge 七个服务、只换容器集合是它的子集且不含 panel-prepare/network-broker。
	edge := map[string]bool{}
	for _, svc := range imageComposeServices["EDGE"] {
		edge[svc] = true
	}
	for _, svc := range edgeContainerRecreate {
		if !edge[svc] || svc == "panel-prepare" || svc == "network-broker" {
			t.Fatalf("只换容器集合不对：%v", edgeContainerRecreate)
		}
	}
}

// 负例：在真实 compose 上新增服务、改名服务、改插值，交叉核对必须失败。
func TestComposeImageEnvCrossCheckNegative(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "nas", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	cases := map[string]string{
		"新增服务": strings.Replace(src, "\nservices:\n", "\nservices:\n  edge-sidecar:\n    image: ${AURIGA_IMAGE_EDGE}\n", 1),
		"改名服务": strings.Replace(src, "\n  egress:\n", "\n  egress-v2:\n", 1),
		"改插值":  strings.Replace(src, "  ai-relay:\n    image: ${AURIGA_IMAGE_EDGE}", "  ai-relay:\n    image: ${AURIGA_IMAGE_PLATFORM}", 1),
	}
	for name, body := range cases {
		if body == src {
			t.Fatalf("%s：构造失败（compose 结构变了？）", name)
		}
		path := filepath.Join(t.TempDir(), "docker-compose.yml")
		putFile(t, path, body, 0644)
		got, err := parseComposeImageEnvs(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := crossCheckComposeImages(got, expectedComposeImageEnv(false)); err == nil {
			t.Fatalf("%s：交叉核对应失败", name)
		}
	}
}

func TestOrchestrationDigestInputs(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	nas := filepath.Join(dir, "deploy-nas")
	putFile(t, filepath.Join(nas, "scripts/start.sh"), "start-v1\n", 0755)
	putFile(t, filepath.Join(nas, "README.md"), "doc\n", 0644)        // 排除
	putFile(t, filepath.Join(nas, "test/fixture.sh"), "test\n", 0755) // 排除
	putFile(t, filepath.Join(nas, "bin/auriga-nas-bootstrap"), "boot-1\n", 0755)
	putFile(t, filepath.Join(data, "stack.env"), "FOO=1\nAURIGA_IMAGE_EDGE=e1\nBAR=2\nAURIGA_INSTALLER_IMAGE=i1\n", 0600)
	access := filepath.Join(dir, "access.json")
	putFile(t, access, `{"schemaVersion":1}`, 0600)
	sum := func() string {
		t.Helper()
		v, err := orchestrationDigest(nas, data, access)
		if err != nil || v == "" {
			t.Fatalf("%q %v", v, err)
		}
		return v
	}
	a := sum()
	// 改镜像变量、调换顺序、CRLF、注释与空行都不改变编排摘要
	putFile(t, filepath.Join(data, "stack.env"), "# c\r\nBAR=2\r\n\r\nAURIGA_IMAGE_EDGE=e2\r\nAURIGA_INSTALLER_IMAGE=i2\r\nFOO=1\r\n", 0600)
	if b := sum(); b != a {
		t.Fatal("去掉 IMAGE 并规范化后应不变")
	}
	// 文档、测试、发行标记不改变编排摘要
	putFile(t, filepath.Join(nas, "README.md"), "doc2\n", 0644)
	putFile(t, filepath.Join(nas, "test/fixture.sh"), "test2\n", 0755)
	putFile(t, filepath.Join(nas, ".auriga-release"), `{"seq":9}`, 0644)
	if b := sum(); b != a {
		t.Fatal("*.md、test/、.auriga-release 应排除")
	}
	steps := []struct {
		name string
		do   func()
	}{
		{"非镜像 stack.env", func() { putFile(t, filepath.Join(data, "stack.env"), "FOO=9\nBAR=2\n", 0600) }},
		{"deploy-nas 脚本", func() { putFile(t, filepath.Join(nas, "scripts/start.sh"), "start-v2\n", 0755) }},
		{"deploy-nas/bin 程序", func() { putFile(t, filepath.Join(nas, "bin/auriga-nas-bootstrap"), "boot-2\n", 0755) }},
		{"access", func() { putFile(t, access, `{"schemaVersion":2}`, 0600) }},
		{"collab-source 回执", func() {
			putFile(t, filepath.Join(data, "config/collab-source/activation.receipt"), "receipt-1\n", 0600)
		}},
	}
	prev := a
	for _, st := range steps {
		st.do()
		if v := sum(); v == prev {
			t.Fatalf("%s 变化应改变编排摘要", st.name)
		} else {
			prev = v
		}
	}
}

// 发行目录：编排摘要写进 release.json，且与 planSwitch 现算一致；两次发行只差镜像与序号时摘要相同。
func TestReleaseDirOrchestrationStableAcrossReleases(t *testing.T) {
	stubTrustedSource(t)
	s := deployTestSite(t)
	src := t.TempDir()
	putFile(t, filepath.Join(src, "deploy/nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	goOut := t.TempDir()
	writeArtifactDir(t, goOut, map[string]string{"auriga-nas-bootstrap": "boot", "runtime-release": "rr"})
	in := &orchInputs{Data: s.Data, Access: s.Access}
	var sums []string
	for seq, tag := range map[int]string{1: "1", 2: "2"} {
		m := releaseManifest{Images: imagesFor(tag), Source: deploySource{Commit: strings.Repeat(tag, 40)}}
		dir, _, err := writeReleaseDir(context.Background(), s.Data, seq, src, goOut, "", m, nil, nil, in)
		if err != nil {
			t.Fatal(err)
		}
		var got releaseManifest
		if err := readJSON(filepath.Join(dir, "release.json"), &got); err != nil || got.Orchestration == "" {
			t.Fatalf("release.json 缺编排摘要：%+v %v", got, err)
		}
		now, err := orchestrationDigest(filepath.Join(dir, "deploy-nas"), s.Data, s.Access)
		if err != nil || now != got.Orchestration {
			t.Fatalf("发行目录现算摘要与 release.json 不一致：%s vs %s %v", now, got.Orchestration, err)
		}
		sums = append(sums, got.Orchestration)
	}
	if sums[0] != sums[1] {
		t.Fatal("只差镜像与序号的两次发行编排摘要应相同（发行标记不能计入）")
	}
}

// containerRecords：prev（r1，已有编排摘要）→ r2 只改 edge，broker 指纹相同，编排相同。
func containerRecords(t *testing.T, s state, prev *deployRecord, keys ...string) deployRecord {
	t.Helper()
	imgs := cloneImages(prev.Images)
	for _, k := range keys {
		imgs[k] = "sha256:" + strings.ToLower(k) + "-r" + strings.Repeat("9", 3)
	}
	if prev.Fingerprints == nil {
		prev.Fingerprints = map[string]string{brokerArtifact: "broker-same"}
	}
	rec := deployRecord{Seq: prev.Seq + 1, Status: recordInstalling, Images: imgs, Release: releaseDir(s.Data, prev.Seq+1), Fingerprints: cloneImages(prev.Fingerprints)}
	putFile(t, filepath.Join(rec.Release, "deploy-nas/scripts/start.sh"), "#!/bin/sh\n", 0755)
	return rec
}

func TestPlanSwitchEdgeContainersMixedBroker(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	rec := containerRecords(t, s, prev, "EDGE")
	plan, err := planSwitch(prev, &rec, s.Data, s.Access)
	if err != nil || plan.Mode != switchModeContainers || !plan.Edge || plan.OrchestrationChanged {
		t.Fatalf("应只换容器：%+v %v", plan, err)
	}
	if strings.Join(plan.Recreate, ",") != "edge,ai-relay,egress,panel-renew,network" {
		t.Fatalf("edge 重建集合不对：%v", plan.Recreate)
	}
	if !plan.MixedBroker || plan.BrokerSeq != 1 || plan.BrokerImage != prev.Images["EDGE"] {
		t.Fatalf("应标记混合版本：%+v", plan)
	}
	if note := mixedVersionsNote(plan); note != "混合版本：network-broker 仍为 r1" {
		t.Fatalf("混合版本文案不对：%s", note)
	}
}

// 混合版本跨多次部署：broker 一直不重建时收据始终写最初的序号；完整启动后清除。
func TestPlanSwitchMixedBrokerSeqAcrossDeploys(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	r2 := containerRecords(t, s, prev, "EDGE")
	p2, _ := planSwitch(prev, &r2, s.Data, s.Access)
	r2.Status, r2.Orchestration, r2.BrokerSeq, r2.BrokerImage = recordOK, p2.Orchestration, p2.BrokerSeq, p2.BrokerImage
	// r3：只改 platform——broker 仍是 r1 的镜像，仍是混合版本
	r3 := containerRecords(t, s, &r2, "PLATFORM")
	p3, _ := planSwitch(&r2, &r3, s.Data, s.Access)
	if p3.Mode != switchModeContainers || mixedVersionsNote(p3) != "混合版本：network-broker 仍为 r1" {
		t.Fatalf("r3 应仍报 r1：%+v", p3)
	}
	r3.Status, r3.Orchestration, r3.BrokerSeq, r3.BrokerImage = recordOK, p3.Orchestration, p3.BrokerSeq, p3.BrokerImage
	// r4：再改 edge，broker 指纹仍相同——仍是 r1，而不是上一条 r3
	r4 := containerRecords(t, s, &r3, "EDGE")
	p4, _ := planSwitch(&r3, &r4, s.Data, s.Access)
	if mixedVersionsNote(p4) != "混合版本：network-broker 仍为 r1" {
		t.Fatalf("r4 应仍报 r1：%+v", p4)
	}
	r4.Status, r4.Orchestration, r4.BrokerSeq, r4.BrokerImage = recordOK, p4.Orchestration, p4.BrokerSeq, p4.BrokerImage
	// r5：agent 变 → 完整启动，broker 跟上
	r5 := containerRecords(t, s, &r4, "AGENT")
	p5, _ := planSwitch(&r4, &r5, s.Data, s.Access)
	if p5.Mode != switchModeFull || p5.MixedBroker || mixedVersionsNote(p5) != "无" || p5.BrokerSeq != 5 {
		t.Fatalf("完整启动后不应再混合：%+v", p5)
	}
}

// P5：network 新镜像 + broker 旧镜像只在 broker 产物指纹相同时允许；指纹变化（或缺失）一律完整启动。
func TestPlanSwitchBrokerArtifactForcesFull(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	for name, fps := range map[string][2]string{"指纹变化": {"broker-old", "broker-new"}, "上一版缺指纹": {"", "broker-new"}, "本版缺指纹": {"broker-old", ""}} {
		prev.Fingerprints = map[string]string{}
		if fps[0] != "" {
			prev.Fingerprints[brokerArtifact] = fps[0]
		}
		rec := containerRecords(t, s, prev, "EDGE")
		rec.Fingerprints = map[string]string{}
		if fps[1] != "" {
			rec.Fingerprints[brokerArtifact] = fps[1]
		}
		plan, err := planSwitch(prev, &rec, s.Data, s.Access)
		if err != nil || plan.Mode != switchModeFull || !strings.Contains(plan.Reason, "broker") || plan.MixedBroker {
			t.Fatalf("%s：broker 产物变应完整启动：%+v %v", name, plan, err)
		}
	}
	// 只改 platform 时 broker 指纹无关
	prev.Fingerprints = map[string]string{brokerArtifact: "a"}
	rec := containerRecords(t, s, prev, "PLATFORM")
	rec.Fingerprints = map[string]string{brokerArtifact: "b"}
	if plan, _ := planSwitch(prev, &rec, s.Data, s.Access); plan.Mode != switchModeContainers {
		t.Fatalf("只改 platform 不看 broker：%+v", plan)
	}
}

func TestPlanSwitchModes(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	cases := []struct {
		keys []string
		mode string
	}{
		{[]string{"PLATFORM"}, switchModeContainers},
		{[]string{"EDGE", "PLATFORM", "OPERATOR"}, switchModeContainers},
		{[]string{"OPERATOR"}, switchModeOperator},
		{[]string{"AGENT"}, switchModeFull},
		{[]string{"BIFROST"}, switchModeFull},
		{[]string{"EDGE", "BIFROST"}, switchModeFull},
		{[]string{"POSTGRES"}, switchModeFull},
		{nil, switchModePointer},
	}
	for _, c := range cases {
		rec := containerRecords(t, s, prev, c.keys...)
		plan, err := planSwitch(prev, &rec, s.Data, s.Access)
		if err != nil || plan.Mode != c.mode {
			t.Fatalf("%v：应 %s，得 %+v %v", c.keys, c.mode, plan, err)
		}
	}
	// 编排变 → 完整启动
	rec := containerRecords(t, s, prev, "PLATFORM")
	putFile(t, filepath.Join(rec.Release, "deploy-nas/scripts/start.sh"), "changed\n", 0755)
	if plan, _ := planSwitch(prev, &rec, s.Data, s.Access); plan.Mode != switchModeFull || !plan.OrchestrationChanged {
		t.Fatalf("编排变应完整启动：%+v", plan)
	}
	// 站点非镜像 stack.env 变 → 完整启动
	rec = containerRecords(t, s, prev, "PLATFORM")
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	putFile(t, filepath.Join(s.Data, "stack.env"), string(env)+"AURIGA_NEW_OPTION=1\n", 0600)
	if plan, _ := planSwitch(prev, &rec, s.Data, s.Access); plan.Mode != switchModeFull {
		t.Fatalf("stack.env 非镜像变化应完整启动：%+v", plan)
	}
	putFile(t, filepath.Join(s.Data, "stack.env"), string(env), 0600)
	// 上一版没有编排摘要（2d 之前的历史）→ 不现算，按编排变化完整启动
	legacy := *prev
	legacy.Orchestration = ""
	rec = containerRecords(t, s, &legacy, "EDGE")
	if plan, _ := planSwitch(&legacy, &rec, s.Data, s.Access); plan.Mode != switchModeFull || !plan.OrchestrationChanged {
		t.Fatalf("旧记录无编排摘要应完整启动：%+v", plan)
	}
}

type recordingSite struct {
	nasOps    []string
	compose   []string
	collab    int
	collabErr error
	healthy   func(call int) error
	calls     int
	smoke     string
	smokeErr  error
}

func (f *recordingSite) ops() siteOps {
	return siteOps{
		nas: func(s state, op string, args ...string) error {
			f.nasOps = append(f.nasOps, strings.Join(append([]string{op}, args...), " "))
			return nil
		},
		health: func(state, time.Duration) error {
			f.calls++
			if f.healthy != nil {
				return f.healthy(f.calls)
			}
			return nil
		},
		smoke: func(state) (string, error) {
			if f.smoke == "" {
				f.smoke = "通过"
			}
			return f.smoke, f.smokeErr
		},
		stopWriters: func(state) ([]string, error) { return []string{"edge"}, nil },
		compose: func(s state, out *bytes.Buffer, args ...string) error {
			f.compose = append(f.compose, strings.Join(args, " "))
			return nil
		},
		collabNetwork: func(state) error {
			f.collab++
			return f.collabErr
		},
	}
}

func TestDeploySwitchEdgeContainerSequence(t *testing.T) {
	s, prev, _, cfg := switchFixture(t)
	rec := containerRecords(t, s, prev, "EDGE")
	site := &recordingSite{}
	v := &dataVersions{Migrations: map[string]string{"a": "1"}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"up -d --no-deps --no-build --pull never --force-recreate --wait --wait-timeout 600 edge ai-relay egress panel-renew network",
		"restart --no-deps agent",
	}
	if strings.Join(site.compose, "|") != strings.Join(want, "|") {
		t.Fatalf("compose 序列不对：\n%v", strings.Join(site.compose, "\n"))
	}
	if len(site.nasOps) != 0 || site.collab != 0 {
		t.Fatalf("只换容器不应 render/preflight/start，也无 collab：%v %d", site.nasOps, site.collab)
	}
	if run.receipt.SwitchMode != switchModeContainers || run.receipt.OrchestrationChanged == nil || *run.receipt.OrchestrationChanged || run.receipt.Orchestration == "" {
		t.Fatalf("收据不对：%+v", run.receipt)
	}
	if run.receipt.MixedVersions != "混合版本：network-broker 仍为 r1" {
		t.Fatalf("混合版本：%s", run.receipt.MixedVersions)
	}
	got, _ := readState(s.Data)
	r2 := got.record(2)
	if got.Current != 2 || r2.Status != recordOK || r2.Orchestration == "" || r2.BrokerSeq != 1 || r2.BrokerImage != prev.Images["EDGE"] {
		t.Fatalf("state 不对：%+v", r2)
	}
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if !strings.Contains(string(env), "AURIGA_IMAGE_EDGE="+rec.Images["EDGE"]) {
		t.Fatalf("stack.env 未写新 edge 镜像：%s", env)
	}
}

// platform + collab-source：不直接 up collab，走 start-collab-network.sh（离线维护模式，失败不影响核心）；
// edge 同时变化时 network 交给消息网络脚本，不在 compose 重建集合里。
func TestDeploySwitchPlatformWithCollab(t *testing.T) {
	s, prev, _, cfg := switchFixture(t)
	putFile(t, filepath.Join(s.Data, "config/collab-source/activation.receipt"), "ok\n", 0600)
	orch, _ := orchestrationDigest(filepath.Join(prev.Release, "deploy-nas"), s.Data, s.Access)
	prev.Orchestration = orch
	rec := containerRecords(t, s, prev, "PLATFORM", "EDGE")
	site := &recordingSite{collabErr: errors.New("exit status 1")}
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil); err != nil {
		t.Fatalf("消息网络脚本失败不应让核心切换失败：%v", err)
	}
	want := []string{
		"up -d --no-deps --no-build --pull never --force-recreate --wait --wait-timeout 600 platform",
		"up -d --no-deps --no-build --pull never --force-recreate --wait --wait-timeout 600 edge ai-relay egress panel-renew",
		"restart --no-deps agent",
	}
	if strings.Join(site.compose, "|") != strings.Join(want, "|") {
		t.Fatalf("compose 序列不对：\n%v", strings.Join(site.compose, "\n"))
	}
	if site.collab != 1 {
		t.Fatalf("应运行一次 start-collab-network：%d", site.collab)
	}
	for _, c := range site.compose {
		if strings.Contains(c, "collab") || strings.Contains(c, "network-broker") {
			t.Fatalf("不应直接碰 collab/broker：%s", c)
		}
	}
}

func TestDeploySwitchPlatformWithoutCollab(t *testing.T) {
	s, prev, _, cfg := switchFixture(t)
	rec := containerRecords(t, s, prev, "PLATFORM")
	site := &recordingSite{}
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil); err != nil {
		t.Fatal(err)
	}
	if site.collab != 0 || len(site.compose) != 2 || !strings.HasSuffix(site.compose[0], " platform") || run.receipt.MixedVersions != "无" {
		t.Fatalf("无 collab 时只重建 platform 并重启 agent：%v %d %s", site.compose, site.collab, run.receipt.MixedVersions)
	}
}

func TestDeploySwitchOperatorAndSmokeOffAndWaitHealthyTimeout(t *testing.T) {
	s, prev, _, cfg := switchFixture(t)
	rec := containerRecords(t, s, prev, "OPERATOR")
	off := false
	cfg.Server.Smoke = &off
	site := &recordingSite{}
	v := &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	if err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(site.nasOps, ","); got != "preflight" {
		t.Fatalf("operator 只应 preflight：%v", site.nasOps)
	}
	if len(site.compose) != 0 || site.calls != 0 {
		t.Fatalf("operator 不应 compose 或等待健康：%v %d", site.compose, site.calls)
	}
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if !strings.Contains(string(env), "AURIGA_IMAGE_OPERATOR="+rec.Images["OPERATOR"]) {
		t.Fatalf("stack.env 未写新 operator 镜像：%s", env)
	}
	if run.receipt.Smoke != "已关闭（server.smoke=false）" {
		t.Fatalf("冒烟关闭：%q", run.receipt.Smoke)
	}
	// waitHealthy 超时参数：默认 10 分钟，可配置；传给 compose --wait-timeout
	if d := healthTimeout(&siteConfig{}); d != 10*time.Minute {
		t.Fatalf("默认超时：%v", d)
	}
	to := "90s"
	cfg.Server.HealthTimeout = &to
	if d := healthTimeout(cfg); d != 90*time.Second {
		t.Fatalf("healthTimeout=%v", d)
	}
	var seen time.Duration
	site2 := &recordingSite{}
	ops := site2.ops()
	ops.health = func(_ state, d time.Duration) error { seen = d; return nil }
	plan := switchPlan{Mode: switchModeContainers, Recreate: []string{"platform"}}
	cur := s.record(s.Current)
	if err := startRecord(ops, &s, cur, healthTimeout(cfg), plan); err != nil {
		t.Fatal(err)
	}
	if seen != 90*time.Second || !strings.Contains(site2.compose[0], "--wait-timeout 90 ") {
		t.Fatalf("超时未传到健康等待与 compose：%v %v", seen, site2.compose)
	}
}

// 前向完整启动失败（含 agent 变化）→ 自动回滚完整启动。
func TestDeploySwitchRollbackFull(t *testing.T) {
	s, prev, rec, cfg := switchFixture(t)
	site := &recordingSite{healthy: func(n int) error {
		if n == 1 {
			return errors.New("unhealthy")
		}
		return nil
	}}
	v := &dataVersions{Migrations: map[string]string{"x": "1"}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 2 || !run.receipt.RolledBack {
		t.Fatalf("应自动回滚：%v", err)
	}
	if strings.Join(site.nasOps, ",") != "render,preflight,start --network-offline,render,preflight,start --network-offline" {
		t.Fatalf("前向与回滚都应完整启动：%v", site.nasOps)
	}
}

// 前向只换容器失败 → 自动回滚。回滚前已停掉全部写数据服务（2b P2），只换容器的前提不成立，
// 必须完整启动上一版；stack.env 写回上一版镜像。
func TestDeploySwitchContainersFailRollsBackFull(t *testing.T) {
	s, prev, _, cfg := switchFixture(t)
	rec := containerRecords(t, s, prev, "EDGE")
	site := &recordingSite{healthy: func(n int) error {
		if n == 1 {
			return errors.New("edge unhealthy")
		}
		return nil
	}}
	v := &dataVersions{Migrations: map[string]string{"x": "1"}, Formats: map[string]string{}}
	stubDataVersions(t, v, v)
	run, _ := testRun()
	err := deploySwitch(context.Background(), site.ops(), cfg, &s, prev, rec, run, nil)
	if exitCodeOf(err) != 2 || !run.receipt.RolledBack {
		t.Fatalf("应自动回滚：%v", err)
	}
	if strings.Join(site.nasOps, ",") != "render,preflight,start --network-offline" || len(site.compose) != 2 {
		t.Fatalf("前向只换容器、回滚完整启动：%v %v", site.compose, site.nasOps)
	}
	got, _ := readState(s.Data)
	if got.Current != 1 || got.record(2).Status != recordFailed || got.record(1).BrokerSeq != 1 {
		t.Fatalf("回滚后 state 不对：%+v", got)
	}
	env, _ := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if !strings.Contains(string(env), "AURIGA_IMAGE_EDGE="+prev.Images["EDGE"]) {
		t.Fatalf("stack.env 未写回上一版：%s", env)
	}
}

// 手动 rollback：编排未变只换容器（写回目标镜像），编排变了或当前记录无摘要则完整启动。
func TestPlanRollbackSwitchBranches(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	cur := containerRecords(t, s, prev, "EDGE")
	p0, _ := planSwitch(prev, &cur, s.Data, s.Access)
	cur.Status, cur.Orchestration, cur.BrokerSeq, cur.BrokerImage = recordOK, p0.Orchestration, p0.BrokerSeq, p0.BrokerImage

	p, err := planRollbackSwitch(&cur, prev, s.Data, s.Access)
	if err != nil || p.Mode != switchModeContainers || !p.Edge || !strings.HasPrefix(p.Reason, "回滚：") {
		t.Fatalf("编排未变回滚应只换容器：%+v %v", p, err)
	}
	if p.MixedBroker || p.BrokerSeq != 1 {
		t.Fatalf("broker 一直是 r1，回到 r1 后不再混合：%+v", p)
	}

	// 目标发行的 deploy-nas 与当前不同 → 完整启动
	putFile(t, filepath.Join(prev.Release, "deploy-nas/scripts/extra.sh"), "x\n", 0755)
	if p2, _ := planRollbackSwitch(&cur, prev, s.Data, s.Access); p2.Mode != switchModeFull || !p2.OrchestrationChanged {
		t.Fatalf("编排变回滚应完整启动：%+v", p2)
	}
	_ = os.Remove(filepath.Join(prev.Release, "deploy-nas/scripts/extra.sh"))
	// 当前记录无编排摘要 → 完整启动
	cur.Orchestration = ""
	if p3, _ := planRollbackSwitch(&cur, prev, s.Data, s.Access); p3.Mode != switchModeFull {
		t.Fatalf("当前无摘要应完整启动：%+v", p3)
	}
}

func TestWriteReleaseMarkersForStatus(t *testing.T) {
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, "deploy-nas/scripts/x.sh"), "x\n", 0755)
	m := releaseManifest{
		Seq: 3, Source: deploySource{Commit: strings.Repeat("a", 40)},
		Images: map[string]string{"EDGE": "sha256:e", "PLATFORM": "sha256:p"}, CreatedAt: "2026-01-01T00:00:00Z",
	}
	if err := writeReleaseMarkers(dir, m); err != nil {
		t.Fatal(err)
	}
	var marker struct {
		Commit string                       `json:"commit"`
		Images map[string]map[string]string `json:"images"`
	}
	if err := readJSONLoose(filepath.Join(dir, "deploy-nas", ".auriga-release"), &marker); err != nil {
		t.Fatal(err)
	}
	// status.sh 读 m["commit"] 与 m["images"][k]["id"]
	if marker.Commit != m.Source.Commit || marker.Images["edge"]["id"] != "sha256:e" || marker.Images["platform"]["id"] != "sha256:p" {
		t.Fatalf("标记格式应兼容 status.sh：%+v", marker)
	}
	ver, _ := os.ReadFile(filepath.Join(dir, "versions.txt"))
	if string(ver) != "commit="+m.Source.Commit+"\nseq=3\ncreated=2026-01-01T00:00:00Z\n" {
		t.Fatalf("versions.txt：%s", ver)
	}
}

func TestRunDeploySmokeMissingScript(t *testing.T) {
	s := state{Package: t.TempDir()}
	msg, err := runDeploySmoke(s)
	if err != nil || !strings.Contains(msg, "跳过") {
		t.Fatalf("%q %v", msg, err)
	}
}

func TestPlanSwitchForcesFullWhenPrevNotOK(t *testing.T) {
	s, prev, _, _ := switchFixture(t)
	prev.Status = recordFailed
	// 即使镜像与编排完全相同（正常会走 switchModePointer），或仅改 EDGE（正常会走 switchModeContainers），
	// 上一版不是 ok 时一律必须走 switchModeFull。
	for _, keys := range [][]string{nil, {"EDGE"}, {"PLATFORM"}, {"OPERATOR"}} {
		rec := containerRecords(t, s, prev, keys...)
		plan, err := planSwitch(prev, &rec, s.Data, s.Access)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Mode != switchModeFull || plan.MixedBroker {
			t.Fatalf("keys=%v prev=failed 应强制完整启动：%+v", keys, plan)
		}
		if !strings.Contains(plan.Reason, "failed") || !strings.Contains(plan.Reason, "无法作为已运行基线") {
			t.Fatalf("keys=%v 原因未说明失败基线：%q", keys, plan.Reason)
		}
	}
}
