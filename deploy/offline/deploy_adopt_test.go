package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adoptImages() map[string]string {
	return imagesFor("a")
}

func stubAdoptMigrations(t *testing.T, mig map[string]string, err error) {
	t.Helper()
	old := adoptReadMigrations
	adoptReadMigrations = func(string, string) (map[string]string, error) {
		if err != nil {
			return nil, err
		}
		out := map[string]string{}
		for k, v := range mig {
			out[k] = v
		}
		return out, nil
	}
	t.Cleanup(func() { adoptReadMigrations = old })
}

// stubAdoptDocker 模拟本机镜像与固定项目下运行中的容器。running：服务 → 容器 ID（多个用逗号）；
// mismatch：容器 ID → 实际镜像。
func stubAdoptDocker(t *testing.T, images map[string]string, running map[string]string, mismatch map[string]string) *[]runSpec {
	t.Helper()
	return stubDeployRun(t, func(r runSpec) (string, error) {
		if r.Name != "docker" {
			return "", fmt.Errorf("unexpected %s", r.Name)
		}
		switch {
		case len(r.Args) >= 3 && r.Args[0] == "image" && r.Args[1] == "inspect":
			ref := r.Args[len(r.Args)-1]
			for _, id := range images {
				if ref == id {
					return id + " linux/amd64", nil
				}
			}
			return "", errors.New("not found")
		case len(r.Args) >= 1 && r.Args[0] == "ps":
			if !strings.Contains(strings.Join(r.Args, " "), "label=com.docker.compose.project="+nasComposeProject) {
				return "", fmt.Errorf("ps 应按固定项目过滤：%v", r.Args)
			}
			var b strings.Builder
			for svc, cids := range running {
				for _, cid := range strings.Split(cids, ",") {
					fmt.Fprintf(&b, "%s %s\n", cid, svc)
				}
			}
			return b.String(), nil
		case len(r.Args) >= 4 && r.Args[0] == "inspect":
			cid := r.Args[len(r.Args)-1]
			if alt, ok := mismatch[cid]; ok {
				return alt + "\n", nil
			}
			for svc, cids := range running {
				for _, id := range strings.Split(cids, ",") {
					if id != cid {
						continue
					}
					if env, ok := collabOverlayImageServices[svc]; ok {
						if env == "AURIGA_IMAGE_POSTGRES" || env == "AURIGA_INSTALLER_IMAGE" {
							return images["POSTGRES"] + "\n", nil
						}
						return images["PLATFORM"] + "\n", nil
					}
					return images[imageKeyForComposeService(svc)] + "\n", nil
				}
			}
			return "", errors.New("container missing")
		case len(r.Args) >= 1 && r.Args[0] == "tag":
			return "", nil
		}
		return "", fmt.Errorf("unhandled docker %v", r.Args)
	})
}

func imageKeyForComposeService(svc string) string {
	for key, svcs := range imageComposeServices {
		for _, s := range svcs {
			if s == svc {
				return key
			}
		}
	}
	return ""
}

func writeAdoptMarker(t *testing.T, path, commit string, images map[string]string) {
	t.Helper()
	m := map[string]map[string]string{}
	for _, svc := range deployServices {
		m[svc] = map[string]string{"id": images[imageKey(svc)]}
	}
	if err := jsonFile(path, map[string]any{"commit": commit, "images": m}, 0644); err != nil {
		t.Fatal(err)
	}
}

func writeAdoptFixture(t *testing.T, hostMode bool) (data, stackEnv, deployNas, access string, images map[string]string) {
	t.Helper()
	stubTrustedSource(t)
	parent := t.TempDir()
	data = filepath.Join(parent, "auriga-data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	access = filepath.Join(data, "access.json")
	putFile(t, access, `{"adminOrigin":{"scheme":"https","host":"example.com","port":443}}`+"\n", 0644)
	images = adoptImages()
	var env strings.Builder
	fmt.Fprintf(&env, "AURIGA_DATA_ROOT=%s\n", data)
	mode := "host"
	if !hostMode {
		mode = "container"
	}
	fmt.Fprintf(&env, "AURIGA_INSTALL_MODE=%s\n", mode)
	fmt.Fprintf(&env, "AURIGA_ACCESS_FILE=%s\n", access)
	for _, key := range adoptImageKeys() {
		fmt.Fprintf(&env, "AURIGA_IMAGE_%s=%s\n", key, images[key])
	}
	fmt.Fprintf(&env, "AURIGA_INSTALLER_IMAGE=%s\n", images["POSTGRES"])
	fmt.Fprintf(&env, "AURIGA_SOME_OPTION=1\n")
	stackEnv = filepath.Join(data, "stack.env")
	putFile(t, stackEnv, env.String(), 0600)
	putFile(t, filepath.Join(data, "config/keep.txt"), "site data", 0600)

	bundle := filepath.Join(parent, "bundle")
	deployNas = filepath.Join(bundle, "deploy-nas")
	putFile(t, filepath.Join(deployNas, "scripts/start.sh"), "#!/bin/sh\n", 0755)
	putFile(t, filepath.Join(deployNas, "docker-compose.yml"), "services:\n  edge:\n    image: ${AURIGA_IMAGE_EDGE}\n", 0644)
	putFile(t, filepath.Join(deployNas, "bin/auriga-nas-bootstrap"), "boot", 0755)
	putFile(t, filepath.Join(deployNas, "bin/runtime-release"), "rr", 0755)
	// load-images.sh 默认把标记写在 bundle 根（AURIGA_RELEASE_DIR 缺省为 bundle）。
	writeAdoptMarker(t, filepath.Join(bundle, ".auriga-release"), strings.Repeat("c", 40), images)
	return data, stackEnv, deployNas, access, images
}

func adoptRunningContainers() map[string]string {
	return map[string]string{
		"edge":           "cid-edge",
		"platform":       "cid-platform",
		"agent":          "cid-agent",
		"bifrost":        "cid-bifrost",
		"postgres":       "cid-postgres",
		"nats":           "cid-nats",
		"registry":       "cid-registry",
		"ai-relay":       "cid-relay",
		"network":        "cid-network",
		"egress":         "cid-egress",
		"panel-renew":    "cid-panel",
		"network-broker": "cid-broker",
	}
}

func mustParseEnv(t *testing.T, data string) map[string]string {
	t.Helper()
	env, err := parseStackEnvFile(filepath.Join(data, "stack.env"))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, _ := os.ReadFile(path)
		out[path] = string(raw)
		return nil
	})
	return out
}

func TestAdoptHappyPathHostNoOneclick(t *testing.T) {
	data, _, deployNas, access, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, map[string]string{"auriga/flyway": "1:1:a"}, nil)
	calls := stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	dataBefore := snapshotTree(t, data)
	nasBefore := snapshotTree(t, deployNas)
	run, buf := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err, buf.String())
	}
	got, err := readState(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.source() != sourceAdopted || !got.TrustUnpinned || got.Current != 1 || got.PublicKey != "" || len(got.TrustedKeys) != 0 {
		t.Fatalf("%+v", got)
	}
	rec := got.record(1)
	if rec == nil || rec.Kind != recordKindAdopt || rec.Status != recordOK || rec.Orchestration == "" || rec.ReleaseSums == "" {
		t.Fatalf("%+v", rec)
	}
	if rec.Source.Commit != strings.Repeat("c", 40) || rec.Source.Mode != "adopted" {
		t.Fatalf("提交号 %+v", rec.Source)
	}
	if rec.Migrations["auriga/flyway"] != "1:1:a" {
		t.Fatalf("迁移 %v 格式 %v", rec.Migrations, rec.Formats)
	}
	if rec.SchemaCompatFloor != "" || rec.FormatMax != nil || len(rec.Fingerprints) != 0 || len(rec.Assembly) != 0 {
		t.Fatalf("adopt 记录不应有 floor/formatMax/指纹/装配摘要：%+v", rec)
	}
	if err := checkReleaseDir(data, rec); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"deploy-nas/scripts/start.sh", "bin/auriga-nas-bootstrap", "bin/runtime-release", "release.json", "versions.txt", "deploy-nas/.auriga-release", "SHA256SUMS"} {
		if _, err := os.Lstat(filepath.Join(rec.Release, rel)); err != nil {
			t.Fatalf("发行目录缺 %s：%v", rel, err)
		}
	}
	if orch, _ := orchestrationDigest(filepath.Join(rec.Release, "deploy-nas"), data, got.Access); orch != rec.Orchestration {
		t.Fatalf("发行目录编排摘要 %s 与记录 %s 不一致", orch, rec.Orchestration)
	}
	// access 直接引用现场文件，不复制进 .oneclick（否则编排摘要与现场实际使用的 access 分叉）。
	if got.Access != access {
		t.Fatalf("state.access 应引用 stack.env 的 AURIGA_ACCESS_FILE：%s", got.Access)
	}
	if _, err := os.Lstat(filepath.Join(data+".oneclick", "access.json")); !os.IsNotExist(err) {
		t.Fatal("adopt 不应复制 access 进 .oneclick")
	}
	// 数据目录（含 stack.env）与原 deploy-nas 一字节都不变。
	if after := snapshotTree(t, data); fmt.Sprint(after) != fmt.Sprint(dataBefore) {
		t.Fatal("adopt 改动了数据目录")
	}
	if after := snapshotTree(t, deployNas); fmt.Sprint(after) != fmt.Sprint(nasBefore) {
		t.Fatal("adopt 改动了原 deploy-nas")
	}
	var tagged []string
	for _, c := range *calls {
		if c.Name != "docker" || len(c.Args) == 0 {
			continue
		}
		switch c.Args[0] {
		case "tag":
			tagged = append(tagged, c.Args[2])
		case "image", "ps", "inspect":
		default:
			t.Fatalf("adopt 不应执行 docker %v", c.Args)
		}
	}
	if len(tagged) != len(deployServices) || !strings.HasSuffix(tagged[0], ":r1") {
		t.Fatalf("应给五个装配镜像打 :r1：%v", tagged)
	}
	rc := run.receipt
	if rc.Release == nil || rc.Release.Seq != 1 || rc.Orchestration != rec.Orchestration || len(rc.Images) != len(adoptImageKeys()) {
		t.Fatalf("收据 %#v", rc)
	}
	notes := strings.Join(rc.Notes, "\n")
	for _, want := range []string{"未钉定", data, access} {
		if !strings.Contains(notes, want) {
			t.Fatalf("收据缺 %q：%v", want, rc.Notes)
		}
	}
}

func TestAdoptRejectsExistingState(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	s := state{Data: data, Package: releaseDir(data, 1), Source: sourceLocalDeploy, Current: 1}
	if err := os.MkdirAll(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run)
	if exitCodeOf(err) != 1 || err == nil || !strings.Contains(err.Error(), "已有一键安装状态") {
		t.Fatalf("%v", err)
	}
	// 只有空的 .oneclick（state 不可读）同样拒绝。
	if err := os.RemoveAll(data + ".oneclick"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); exitCodeOf(err) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestAdoptImageMismatchExit3(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	running := adoptRunningContainers()
	running["edge"] = "cid-edge,cid-edge2" // 同一服务多个容器逐个核对
	bad := map[string]string{"cid-edge2": "sha256:" + strings.Repeat("f", 64)}
	calls := stubAdoptDocker(t, images, running, bad)
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run)
	if exitCodeOf(err) != 3 || err == nil || !strings.Contains(err.Error(), "不一致") || !strings.Contains(err.Error(), "cid-edge2") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(data + ".oneclick"); !os.IsNotExist(err) {
		t.Fatal("失败不应留下 .oneclick")
	}
	if entries, _ := os.ReadDir(releasesRoot(data)); len(entries) != 0 {
		t.Fatalf("失败不应留下发行目录：%v", entries)
	}
	for _, c := range *calls {
		if len(c.Args) > 0 && c.Args[0] == "tag" {
			t.Fatal("核查失败不应打标签")
		}
	}
}

func TestAdoptCollabOverlayAndRequiredServices(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	// collab 跑着旧 platform 镜像：必须算差异。
	running := adoptRunningContainers()
	running["collab"] = "cid-collab"
	stubAdoptDocker(t, images, running, map[string]string{"cid-collab": "sha256:" + strings.Repeat("9", 64)})
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), true, run)
	if exitCodeOf(err) != 3 || !strings.Contains(err.Error(), "collab") || !strings.Contains(err.Error(), "AURIGA_IMAGE_PLATFORM") {
		t.Fatalf("collab overlay 镜像不一致应退出码 3：%v", err)
	}
	// 常驻服务没在跑：不是 deploy-nas 正常运行状态。
	running = adoptRunningContainers()
	delete(running, "bifrost")
	stubAdoptDocker(t, images, running, nil)
	err = doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), true, run)
	if exitCodeOf(err) != 3 || !strings.Contains(err.Error(), "bifrost") {
		t.Fatalf("必需服务未运行应退出码 3：%v", err)
	}
	// collab-network profile 的 network/network-broker 没跑不算差异；表外服务记未知项。
	running = adoptRunningContainers()
	delete(running, "network")
	delete(running, "network-broker")
	running["collab-db"] = "cid-cdb"
	running["mystery"] = "cid-m"
	stubAdoptDocker(t, images, running, map[string]string{"cid-m": "sha256:" + strings.Repeat("1", 64)})
	run, buf := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), true, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.receipt.Notes, "\n"), "mystery") || !strings.Contains(buf.String(), "mystery") {
		t.Fatalf("表外服务应记为未知项：%v", run.receipt.Notes)
	}
}

func TestAdoptMissingDataDir(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "auriga-data")
	deployNas := filepath.Join(parent, "bundle", "deploy-nas")
	putFile(t, filepath.Join(deployNas, "scripts/start.sh"), "#!/bin/sh\n", 0755)
	env := map[string]string{"AURIGA_DATA_ROOT": data}
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, env, false, run)
	if exitCodeOf(err) != 1 || err == nil || !strings.Contains(err.Error(), "数据目录") {
		t.Fatalf("%v", err)
	}
}

func TestAdoptRequiresAccessFile(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	env := mustParseEnv(t, data)
	delete(env, "AURIGA_ACCESS_FILE")
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, env, false, run); exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "AURIGA_ACCESS_FILE") {
		t.Fatalf("%v", err)
	}
	env = mustParseEnv(t, data)
	env["AURIGA_INSTALL_MODE"] = "weird"
	if err := doAdopt(context.Background(), data, deployNas, env, false, run); exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "AURIGA_INSTALL_MODE") {
		t.Fatalf("%v", err)
	}
}

func TestAdoptDryRunNoPersist(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, map[string]string{"x": "1"}, nil)
	calls := stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, buf := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), true, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "--dry-run") || !strings.Contains(buf.String(), "编排摘要："+run.receipt.Orchestration) || run.receipt.Orchestration == "" {
		t.Fatalf("dry-run 应打印将写入的编排摘要：%s", buf.String())
	}
	if _, err := os.Lstat(data + ".oneclick"); !os.IsNotExist(err) {
		t.Fatal("dry-run 不应写 .oneclick")
	}
	if _, err := os.Lstat(releasesRoot(data)); !os.IsNotExist(err) {
		t.Fatal("dry-run 不应创建发行根目录")
	}
	for _, c := range *calls {
		if c.Name == "docker" && len(c.Args) > 0 && c.Args[0] == "tag" {
			t.Fatal("dry-run 不应打标签")
		}
	}
}

func TestAdoptCommitOnlyFromMatchingMarker(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	// deploy.mjs 式现场：bundle 标记是旧镜像，stack.env 已换成新镜像——提交号不能采信。
	writeAdoptMarker(t, filepath.Join(filepath.Dir(deployNas), ".auriga-release"), strings.Repeat("c", 40), imagesFor("z"))
	putFile(t, filepath.Join(deployNas, "provenance.json"), `{"commit":"`+strings.Repeat("d", 40)+`"}`, 0644)
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	if c := got.record(1).Source.Commit; c != "" {
		t.Fatalf("标记镜像与运行镜像不一致时提交号应为未知，得到 %q", c)
	}
	if !strings.Contains(strings.Join(run.receipt.Notes, "\n"), "不采信") {
		t.Fatalf("收据应说明提交号为何未知：%v", run.receipt.Notes)
	}
}

func TestAdoptUnknownMigrationsNoUntouchedBaseline(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, errors.New("postgres 不可达"))
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	rec := got.record(1)
	if rec.Migrations != nil {
		t.Fatalf("迁移未知不应记成空表：%v", rec.Migrations)
	}
	// 当前读到空迁移、空格式也不能按“数据未触碰”放行。
	ok, reason := rollbackDecision(rec, &deployRecord{Seq: 2, Status: recordOK}, nil, &dataVersions{Migrations: map[string]string{}, Formats: map[string]string{}}, nil)
	if ok {
		t.Fatalf("迁移基线未知时不应放行：%s", reason)
	}
	if run.receipt.Data.Before == nil || run.receipt.Data.Before.Err == "" {
		t.Fatalf("收据应标出数据版本未读全：%+v", run.receipt.Data.Before)
	}
}

func TestAdoptUpgradeRequiresTrust(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	err := upgradeAllowed(data, filepath.Join(t.TempDir(), "x.tar"), false)
	if err == nil || !strings.Contains(err.Error(), "trust add-release-key") {
		t.Fatalf("未钉定应要求 trust add-*：%v", err)
	}
	// 当前 adopted 发行按本地清单核对；被篡改即失败（不因 adopted 放宽而绕过）。
	got, _ := readState(data)
	if unsigned, err := verifyStatePackage(got, got.Package, "", false); err != nil || unsigned {
		t.Fatalf("%v unsigned=%v", err, unsigned)
	}
	putFile(t, filepath.Join(got.Package, "deploy-nas/scripts/start.sh"), "#!/bin/sh\nevil\n", 0755)
	if _, err := verifyStatePackage(got, got.Package, "", true); err == nil {
		t.Fatal("篡改后的 adopted 发行不应通过本地清单核对（allow-unsigned 也不能借用）")
	}
	// 待升级的包（不是当前发行）不走本地清单：未签名且未钉定公钥一律拒绝。
	pkg := t.TempDir()
	putFile(t, filepath.Join(pkg, "SHA256SUMS"), "", 0644)
	if _, err := verifyStatePackage(got, pkg, "", false); err == nil {
		t.Fatal("adopted 站点的待升级包不应绕过签名")
	}
}

func TestAdoptThenDeployFullSwitch(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, map[string]string{"auriga/x": "1:1:a"}, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	prev := got.record(got.Current)
	// 编排未变、只 platform 变：普通记录会只换容器；以 adopt 记录为上一版必须完整启动。
	next := deployRecord{Seq: 2, Kind: recordKindDeploy, Status: recordOK, Images: cloneImages(prev.Images), Fingerprints: map[string]string{brokerArtifact: "x"}}
	next.Images["PLATFORM"] = "sha256:" + strings.Repeat("b", 64)
	plan := planSwitchWith(prev, &next, prev.Orchestration, data)
	if plan.Mode != switchModeFull || !strings.Contains(plan.Reason, "adopt") {
		t.Fatalf("adopt 后第一次切换应完整启动：%+v", plan)
	}
	// 镜像与编排都没变也不能只切指针。
	same := deployRecord{Seq: 2, Kind: recordKindDeploy, Status: recordOK, Images: cloneImages(prev.Images)}
	if plan := planSwitchWith(prev, &same, prev.Orchestration, data); plan.Mode != switchModeFull {
		t.Fatalf("adopt 后镜像未变也应完整启动：%+v", plan)
	}
	// 对照：同样的变化在普通 deploy 记录之间只换容器（说明上面的完整启动来自 adopt 规则）。
	plain := *prev
	plain.Kind = recordKindDeploy
	if plan := planSwitchWith(&plain, &next, prev.Orchestration, data); plan.Mode != switchModeContainers {
		t.Fatalf("对照组应只换容器：%+v", plan)
	}
	// 回滚到 adopt 版同样完整启动。
	if plan := planSwitchWith(&next, prev, prev.Orchestration, data); plan.Mode != switchModeFull {
		t.Fatalf("回滚到 adopt 版应完整启动：%+v", plan)
	}
}

func TestAdoptRollbackFloorBranch(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, map[string]string{"auriga/x": "1:1:a"}, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	adoptRec := got.record(1)
	newer := &deployRecord{Seq: 2, Status: recordOK, Source: deploySource{Mode: "git", Commit: strings.Repeat("d", 40)},
		Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{"materials": "1"},
		SchemaCompatFloor: strings.Repeat("d", 40), FormatMax: map[string]string{"materials": "1"}}
	changed := &dataVersions{Migrations: map[string]string{"auriga/x": "2:2:b"}, Formats: map[string]string{"materials": "1"}}
	ok, reason := rollbackDecision(adoptRec, newer, nil, changed, nil)
	if ok || !strings.Contains(reason, "没有声明兼容元数据") {
		t.Fatalf("adopt 无 floor 且数据已变应拒绝：ok=%v reason=%s", ok, reason)
	}
	untouched := &dataVersions{Migrations: map[string]string{"auriga/x": "1:1:a"}, Formats: map[string]string{}}
	if ok, reason = rollbackDecision(adoptRec, newer, nil, untouched, nil); !ok {
		t.Fatalf("数据未触碰应允许回滚到 adopt 版：%s", reason)
	}
}

func TestAdoptSeqContinuesExistingReleases(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	if err := os.MkdirAll(releaseDir(data, 3), 0755); err != nil {
		t.Fatal(err)
	}
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images, adoptRunningContainers(), nil)
	run, _ := testRun()
	if err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run); err != nil {
		t.Fatal(err)
	}
	got, _ := readState(data)
	if got.Current != 4 || got.record(4) == nil || got.Package != releaseDir(data, 4) {
		t.Fatalf("应接续已有发行目录占 r4，得到 %+v", got)
	}
}

func TestAdoptTagFailureLeavesNothing(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	stubAdoptMigrations(t, nil, nil)
	stubDeployRun(t, func(r runSpec) (string, error) {
		if len(r.Args) > 0 && r.Args[0] == "tag" {
			return "", errors.New("tag boom")
		}
		switch {
		case r.Args[0] == "image":
			return r.Args[len(r.Args)-1] + " linux/amd64", nil
		case r.Args[0] == "ps":
			var b strings.Builder
			for svc, cid := range adoptRunningContainers() {
				fmt.Fprintf(&b, "%s %s\n", cid, svc)
			}
			return b.String(), nil
		case r.Args[0] == "inspect":
			for svc, cid := range adoptRunningContainers() {
				if cid == r.Args[len(r.Args)-1] {
					return images[imageKeyForComposeService(svc)], nil
				}
			}
		}
		return "", fmt.Errorf("unhandled %v", r.Args)
	})
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, mustParseEnv(t, data), false, run)
	if exitCodeOf(err) != 1 || !strings.Contains(err.Error(), "标签") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(data + ".oneclick"); !os.IsNotExist(err) {
		t.Fatal("打标签失败不应留下 .oneclick")
	}
	if _, err := os.Lstat(releaseDir(data, 1)); !os.IsNotExist(err) {
		t.Fatal("打标签失败应撤掉发行目录")
	}
}

func TestAdoptSudoPrivilege(t *testing.T) {
	if _, err := parseAdopt(nil); err == nil {
		t.Fatal("缺参数应失败")
	}
	o, err := parseAdopt([]string{"--stack-env", "/a/b/c/stack.env", "--deploy-nas", "/x/deploy-nas", "--dry-run"})
	if err != nil || !o.DryRun || o.StackEnv == "" {
		t.Fatal(err, o)
	}
	stubRunnerUID(t, map[string]string{"runner": "1001"})
	args := []string{"--stack-env", "x", "--deploy-nas", "y"}
	// 没登记运行器：经 sudo 默认受限，adopt 无白名单 → 拒绝；--operator 也无法证明不是运行器。
	stubSudoConfig(t, nil, nil)
	if err := adoptCommand(args, siteInvocation{Sudo: true, Root: true, SudoUID: "1000"}); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("%v", err)
	}
	if err := adoptCommand(append([]string{"--operator"}, args...), siteInvocation{Sudo: true, Root: true, SudoUID: "1000"}); err == nil || !strings.Contains(err.Error(), "server.runner") {
		t.Fatalf("%v", err)
	}
	stubSudoConfig(t, runnerConfig("runner"), nil)
	// 运行器加 --operator 也拒绝。
	if err := adoptCommand(append([]string{"--operator"}, args...), siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}); err == nil || !strings.Contains(err.Error(), "运行器") {
		t.Fatalf("%v", err)
	}
	// 非运行器管理员 --operator：通过提权判定，后续因路径不存在失败（不再是 sudo 拒绝）。
	err = adoptCommand(append([]string{"--operator"}, args...), siteInvocation{Sudo: true, Root: true, SudoUID: "1000"})
	if err == nil || strings.Contains(err.Error(), "sudo") || strings.Contains(err.Error(), "受限") {
		t.Fatalf("非运行器 --operator 应通过提权判定：%v", err)
	}
	// 运行器在分派前就被 guardSudoCommand 拦下。
	if err := guardSudoCommand([]string{"adopt"}, siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}); err == nil {
		t.Fatal("运行器经 sudo 不能运行 adopt")
	}
}

func TestAdoptSourceForRecordAndHistory(t *testing.T) {
	if sourceForRecord(&deployRecord{Kind: recordKindAdopt}) != sourceAdopted {
		t.Fatal()
	}
	s := state{Source: sourceAdopted, TrustUnpinned: true, Current: 1, History: []deployRecord{{
		Seq: 1, Kind: recordKindAdopt, Status: recordOK, At: "t", Source: deploySource{Mode: "adopted"},
	}}}
	var buf bytes.Buffer
	printHistory(&buf, s)
	if !strings.Contains(buf.String(), "adopted") || !strings.Contains(buf.String(), "adopt") || !strings.Contains(buf.String(), "未知") {
		t.Fatalf("%s", buf.String())
	}
	buf.Reset()
	printDeploySummary(&buf, s)
	if !strings.Contains(buf.String(), "安装来源 adopted") || !strings.Contains(buf.String(), "未钉定") || !strings.Contains(buf.String(), "提交未知") {
		t.Fatalf("%s", buf.String())
	}
}

func TestAdoptInstallerImageMismatch(t *testing.T) {
	data, _, deployNas, _, images := writeAdoptFixture(t, true)
	env := mustParseEnv(t, data)
	env["AURIGA_INSTALLER_IMAGE"] = "sha256:" + strings.Repeat("e", 64)
	images2 := cloneImages(images)
	images2["INSTALLER"] = env["AURIGA_INSTALLER_IMAGE"]
	stubAdoptMigrations(t, nil, nil)
	stubAdoptDocker(t, images2, adoptRunningContainers(), nil)
	run, _ := testRun()
	err := doAdopt(context.Background(), data, deployNas, env, false, run)
	if exitCodeOf(err) != 3 || err == nil || !strings.Contains(err.Error(), "INSTALLER_IMAGE") {
		t.Fatalf("%v", err)
	}
}
