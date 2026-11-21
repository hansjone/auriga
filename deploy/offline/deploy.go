package main

// auriga deploy（设计 D10，统一现场部署第 2a 步上半：root 直接运行的全量部署）。
//
// 流程：统一站点锁 → 取源码快照 → 读源码配方（能力版本、# syntax=、上游摘要）→ 依赖输入
// （缓存 → 套件 → 依赖源，verifyInput，锁清单比较）→ 固定 digest 的工具/基础/服务镜像 →
// 断网构建 go/web/java → 按暂存清单装配镜像（装配摘要与当前相同的跳过）→ 打 r<序号> 标签 →
// 写不可变发行目录 → 首装复用 install 步骤，或完整启动切换（失败且数据未被触碰时自动回滚）→
// 淘汰旧历史 → 收据。
//
// 不在本步：sudo 入口与参数白名单、快进限制、提交签名校验（配置了就拒绝）、只换容器、按指纹
// 跳过构建、--backup-db、floor 生成。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type deployOptions struct {
	Commit   string
	Full     bool
	Services []string
	DryRun   bool
	JSON     bool
	Config   string
	Source   string
	Accept   bool
	BackupDB bool
	Budget   buildBudget
}

func parseDeploy(args []string) (deployOptions, error) {
	var o deployOptions
	f := flags("deploy")
	f.BoolVar(&o.Full, "full", false, "")
	services := f.String("services", "", "")
	f.BoolVar(&o.DryRun, "dry-run", false, "")
	f.BoolVar(&o.JSON, "json", false, "")
	f.BoolVar(&o.BackupDB, "backup-db", false, "")
	f.StringVar(&o.Config, "config", "", "")
	f.StringVar(&o.Source, "source", "", "")
	f.BoolVar(&o.Accept, "accept-new-deps", false, "")
	f.IntVar(&o.Budget.MemoryMiB, "memory-mib", 4096, "")
	f.IntVar(&o.Budget.CPUs, "cpus", 2, "")
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		o.Commit, rest = args[0], args[1:]
	}
	if err := f.Parse(rest); err != nil {
		return o, err
	}
	switch {
	case f.NArg() == 1 && o.Commit == "":
		o.Commit = f.Arg(0)
	case f.NArg() != 0:
		return o, errors.New("deploy 只接受一个提交号")
	}
	if o.Commit != "" && !commitPattern.MatchString(o.Commit) {
		return o, fmt.Errorf("提交号必须是 40 位小写十六进制（当前 %q）", o.Commit)
	}
	if *services != "" {
		known := map[string]bool{}
		for _, s := range deployServices {
			known[s] = true
		}
		seen := map[string]bool{}
		for _, s := range strings.Split(*services, ",") {
			if !known[s] {
				return o, fmt.Errorf("--services 含未知镜像 %q（可选 %s）", s, strings.Join(deployServices, ","))
			}
			if !seen[s] {
				seen[s] = true
				o.Services = append(o.Services, s)
			}
		}
		if o.Full {
			return o, errors.New("--full 与 --services 不能同用")
		}
	}
	return o, nil
}

func deployCommand(args []string, inv siteInvocation) error {
	o, err := parseDeploy(args)
	if err != nil {
		return err
	}
	if inv.Sudo {
		return errors.New("经 sudo 调用的 deploy 与参数白名单在统一现场部署 2a（下）开放；请以 root 直接运行")
	}
	if o.BackupDB {
		return errors.New("--backup-db 属统一现场部署 2b，本版本尚未实现")
	}
	cfg, cfgPath, err := loadSiteConfig(o.Config, inv)
	if err != nil {
		return err
	}
	data, err := siteDataPath(cfg)
	if err != nil {
		return err
	}
	run, err := startRun(cfg, cfgPath, "deploy", args, o.JSON)
	if err != nil {
		return err
	}
	ctx, cancel := depsSignalContext()
	defer cancel()
	err = withLock(data, "deploy", func() error {
		return doDeploy(ctx, defaultSiteOps, cfg, cfgPath, data, o, run)
	})
	return run.finish(err)
}

// deployBuildEnv 是可注入点：构建前检查 Docker 与内存预算。
var deployBuildEnv = func(ctx context.Context, b buildBudget) error { return depsDockerReady(ctx, b) }

func doDeploy(ctx context.Context, ops siteOps, cfg *siteConfig, cfgPath, data string, o deployOptions, run *siteRun) error {
	rc, w := run.receipt, run.out
	rc.DryRun = o.DryRun
	var s state
	first := false
	if st, err := readState(data); err == nil {
		s = st
	} else if _, statErr := os.Lstat(data + ".oneclick"); os.IsNotExist(statErr) {
		first = true
	} else {
		return exitWith(1, err)
	}
	rc.FirstInstall = first
	var prev *deployRecord
	if !first {
		if s.source() != sourceLocalDeploy {
			return exitWith(1, fmt.Errorf("当前安装来源为 %s：deploy 只接管自己建立的站点（签名包安装接入新布局属 2a（下），已有 deploy-nas 站点用 adopt，属 2e）", s.source()))
		}
		if prev = s.record(s.Current); prev == nil {
			return exitWith(1, fmt.Errorf("state 缺少当前发行 r%d 的历史记录，拒绝继续", s.Current))
		}
		if prev.Status == recordInstalling {
			return exitWith(1, fmt.Errorf("首次安装（r%d）未完成：现场已保留，请先排查 %s 与数据目录；确认放弃后移走二者再重新 deploy", prev.Seq, data+".oneclick"))
		}
	}
	selected := map[string]bool{}
	if first && len(o.Services) > 0 {
		return exitWith(1, errors.New("首次安装必须构建全部镜像，不能用 --services"))
	}
	if len(o.Services) == 0 {
		for _, svc := range deployServices {
			selected[svc] = true
		}
	}
	for _, svc := range o.Services {
		selected[svc] = true
	}
	var chosen []string
	for _, svc := range deployServices {
		if selected[svc] {
			chosen = append(chosen, svc)
		}
	}

	stage, err := os.MkdirTemp(filepath.Dir(data), ".auriga-deploy-")
	if err != nil {
		return exitWith(1, err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0700); err != nil {
		return exitWith(1, err)
	}
	src, err := resolveDeploySource(ctx, cfg, data, o.Source, o.Commit, stage)
	if err != nil {
		return exitWith(1, err)
	}
	rc.Source = src
	fmt.Fprintf(w, "源码：%s %s 提交 %s，快照 %s，未提交改动 %t\n", src.Mode, src.URL, src.Commit, src.SnapshotSHA256, src.Dirty)
	recipe, err := loadDeployRecipe(src.Dir)
	if err != nil {
		return exitWith(1, err)
	}
	builds, needSystem := recipe.needs(chosen)
	ecos := []string{"go"}
	for _, k := range []string{"web", "java"} {
		if builds[k] {
			ecos = append(ecos, k)
		}
	}
	if needSystem {
		ecos = append(ecos, "system")
	}
	rc.Deps.Sources = map[string]string{}
	for _, eco := range ecos {
		rc.Deps.Sources[eco] = depsSourceLabel(cfg.Deps, eco)
	}
	var roles []string
	for _, k := range []string{"go", "web", "java"} {
		if builds[k] {
			roles = append(roles, "tool-"+k)
		}
	}
	for _, svc := range chosen {
		roles = append(roles, "base-"+svc)
	}
	for _, svc := range deployExtraServices {
		roles = append(roles, "service-"+svc)
	}
	if !o.DryRun {
		if err := deployBuildEnv(ctx, o.Budget); err != nil {
			return exitWith(1, err)
		}
	}
	inputs, missing, err := resolveDeployInputs(ctx, cfg, cfgPath, src.Dir, ecos, o.Accept, o.DryRun, o.Budget, stage, w)
	rc.Deps.Inputs, rc.Deps.Missing = inputs, append([]string{}, missing...)
	if err != nil {
		return exitWith(1, err)
	}
	r := newDepsResolver(cfg)
	if o.DryRun {
		return printDeployPlan(ctx, w, cfg, r, recipe, &s, first, prev, chosen, roles, inputs, missing, rc)
	}
	resolved := map[string]resolvedImage{}
	for _, role := range roles {
		img, err := resolveRecipeImage(ctx, r, cfg, role, recipe.Images[role], stage)
		if err != nil {
			return exitWith(1, err)
		}
		resolved[role] = img
		rc.BuildImages = append(rc.BuildImages, img)
	}
	inputDir := map[string]string{}
	for _, in := range inputs {
		inputDir[in.Ecosystem] = in.Dir
	}

	out := filepath.Join(stage, "output")
	for _, kind := range []string{"go", "web", "java"} {
		if !builds[kind] {
			continue
		}
		if err := os.MkdirAll(filepath.Join(out, kind), 0755); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "构建 %s（工具镜像 %s，断网）…\n", kind, recipe.Images["tool-"+kind])
		if err := runToolBuild(ctx, kind, resolved["tool-"+kind].ID, src.Dir, inputDir[kind], filepath.Join(out, kind), o.Budget); err != nil {
			return exitWith(1, err)
		}
	}
	if builds["web"] {
		// 2c 前 provenance 仍打进 edge 镜像；不写构建时间，产物不变时装配摘要也不变。
		info := provenance{Version: "local-" + src.SnapshotSHA256[:12], Commit: src.Commit, Dirty: src.Dirty, SiteChanges: src.Dirty, SnapshotSHA256: src.SnapshotSHA256}
		if err := jsonFile(filepath.Join(out, "web/admin/provenance.json"), info, 0644); err != nil {
			return exitWith(1, err)
		}
	}

	seq := nextSeq(s)
	images, assembly := map[string]string{}, map[string]string{}
	if prev != nil {
		images = cloneImages(prev.Images)
	}
	dockerfile := filepath.Join(src.Dir, filepath.FromSlash(deployDockerfile))
	for _, svc := range deployServices {
		key := imageKey(svc)
		old := ""
		if prev != nil {
			old = prev.Images[key]
		}
		if !selected[svc] {
			if prev != nil && prev.Assembly[svc] != "" {
				assembly[svc] = prev.Assembly[svc]
			}
			rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "not-selected", Reason: "--services 未选中，沿用 r" + fmt.Sprint(prev.Seq), OldID: old, NewID: old, Tag: imageTag(svc, seq)})
			continue
		}
		scratch := filepath.Join(stage, "assemble", svc)
		if err := os.MkdirAll(scratch, 0755); err != nil {
			return exitWith(1, err)
		}
		if err := stageService(ctx, svc, recipe.Services[svc], src.Dir, out, inputDir["system"], scratch); err != nil {
			return exitWith(1, fmt.Errorf("暂存 %s 失败：%w", svc, err))
		}
		sum, err := assemblyDigest(ctx, svc, recipe, scratch)
		if err != nil {
			return exitWith(1, err)
		}
		assembly[svc] = sum
		reason := "首次安装"
		switch {
		case first:
		case o.Full:
			reason = "--full 强制重建"
		case prev.Assembly[svc] == "":
			reason = "上一版没有装配摘要"
		case prev.Assembly[svc] == sum:
			if _, _, ok := localImage(ctx, old); ok {
				rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "unchanged", Reason: fmt.Sprintf("装配摘要与 r%d 相同，跳过构建与切换", prev.Seq), OldID: old, NewID: old, Tag: imageTag(svc, seq), Assembly: sum})
				continue
			}
			reason = "装配摘要相同但旧镜像已不在本机"
		default:
			reason = "装配摘要变化（产物、系统输入、基础镜像、Dockerfile 或暂存清单）"
		}
		base := resolved["base-"+svc]
		baseTag := kitImageTag("base-"+svc, base.Ref)
		if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"tag", base.ID, baseTag}}); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "装配 %s 镜像（%s）…\n", svc, reason)
		id, err := assembleImage(ctx, svc, baseTag, dockerfile, scratch, imageTag(svc, seq))
		if err != nil {
			return exitWith(1, err)
		}
		images[key] = id
		rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "built", Reason: reason, OldID: old, NewID: id, Tag: imageTag(svc, seq), Assembly: sum})
	}
	for _, svc := range deployExtraServices {
		key := imageKey(svc)
		id := resolved["service-"+svc].ID
		old := ""
		if prev != nil {
			old = prev.Images[key]
		}
		images[key] = id
		action := "service"
		if old == id {
			action = "unchanged"
		}
		rc.Images = append(rc.Images, receiptImage{Service: svc, Action: action, Reason: "配方固定 digest " + recipe.Images["service-"+svc], OldID: old, NewID: id})
	}
	if err := tagImages(ctx, images, func(svc string) string { return imageTag(svc, seq) }); err != nil {
		return exitWith(1, err)
	}
	manifest := releaseManifest{Source: *src, Images: images, Assembly: assembly, CreatedAt: nowRFC3339()}
	release, releaseSums, err := writeReleaseDir(ctx, data, seq, src.Dir, filepath.Join(out, "go"), manifest)
	if err != nil {
		return exitWith(1, err)
	}
	rec := deployRecord{Seq: seq, Status: recordInstalling, At: nowRFC3339(), Source: *src, Release: release, ReleaseSums: releaseSums, Images: images, Assembly: assembly}
	rc.Release = &receiptRelease{Seq: seq, Dir: release, Previous: s.Current}
	fmt.Fprintf(w, "发行目录 %s\n", release)
	if first {
		return deployFirstInstall(ctx, cfg, data, rec, run)
	}
	return deploySwitch(ctx, ops, cfg, &s, prev, rec, run)
}

// deploySwitch：完整启动切到新发行；失败且数据未被触碰时自动回滚（退出码 2），否则退出码 3。
func deploySwitch(ctx context.Context, ops siteOps, cfg *siteConfig, s *state, prev *deployRecord, rec deployRecord, run *siteRun) error {
	rc, w := run.receipt, run.out
	rc.Phase = "switch"
	rc.SwitchMode = "完整启动（render → preflight → start）"
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		if prev.Images[imageKey(svc)] != rec.Images[imageKey(svc)] {
			rc.SwitchedServices = append(rc.SwitchedServices, svc)
		}
	}
	rc.Notes = append(rc.Notes, "编排摘要与只换容器属 2d：本版本每次都完整启动，orchestrationChanged 未计算")
	before := readDataVersions(*s)
	rc.Data.Before = before
	rec.Status = recordFailed // 成功后改为 ok；中途崩溃时不会被当作可回滚目标
	s.History = append(s.History, rec)
	cur := s.record(rec.Seq)
	timeout := healthTimeout(cfg)
	fmt.Fprintf(w, "切换到 r%d（%s）…\n", rec.Seq, rc.SwitchMode)
	err := startRecord(ops, s, cur, timeout)
	if err == nil {
		rc.Health = "healthy"
		if cfg.Server.Smoke != nil && !*cfg.Server.Smoke {
			rc.Smoke = "已关闭（server.smoke=false）"
		} else {
			var smokeErr error
			rc.Smoke, smokeErr = ops.smoke(*s)
			if smokeErr != nil {
				err = fmt.Errorf("部署冒烟失败：%w", smokeErr)
			}
		}
	} else {
		rc.Health = err.Error()
	}
	if err == nil {
		after := readDataVersions(*s)
		rc.Data.After = after
		cur.Status = recordOK
		if after.ok() {
			cur.Migrations, cur.Formats = after.Migrations, after.Formats
		}
		if err := saveState(*s); err != nil {
			return exitWith(3, fmt.Errorf("新版本已健康，但 state 保存失败，需人工核对：%w", err))
		}
		_ = tagImages(ctx, cur.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
		keep := 3
		if cfg.Server.KeepHistory != nil {
			keep = *cfg.Server.KeepHistory
		}
		pruned, perr := pruneReleases(ctx, s, keep, w)
		if perr != nil {
			fmt.Fprintf(w, "警告：%v\n", perr)
		}
		if len(pruned) > 0 {
			rc.Pruned = pruned
			if err := saveState(*s); err != nil {
				fmt.Fprintf(w, "警告：淘汰历史后保存 state 失败：%v\n", err)
			}
		}
		fmt.Fprintf(w, "部署完成：当前 r%d\n", rec.Seq)
		return nil
	}
	rc.Phase = "rollback"
	_ = saveState(*s)
	// 先停掉新版本里会写库或改写数据目录的容器，确认停止后再读迁移与格式版本：否则读完到
	// 回滚完成之间，新容器（例如迁移较慢的 platform）仍可能提交迁移（P2）。
	refuse := func(reason string) error {
		rc.RollbackReason = reason
		rc.Notes = append(rc.Notes, runningImages(ctx)...)
		return exitWith(3, fmt.Errorf("切换到 r%d 失败：%v；%s，需人工处理", rec.Seq, err, reason))
	}
	stopped, stopErr := ops.stopWriters(*s)
	if stopErr != nil {
		rc.Data.After = &dataVersions{Err: "未读取：" + stopErr.Error()}
		return refuse("无法确认新版本会写数据的容器已停止（" + stopErr.Error() + "），视为数据可能已被改动，不自动回滚")
	}
	rc.Notes = append(rc.Notes, "判定是否回滚前已停止新版本会写数据的服务："+strings.Join(stopped, "、"))
	after := readDataVersions(*s)
	rc.Data.After = after
	if !dataUntouched(before, after) {
		return refuse("迁移或数据目录格式版本与切换前不同（或读不到），新版本可能已改动数据，不自动回滚（会写数据的新容器保持停止）")
	}
	if prev.Status != recordOK {
		return refuse(fmt.Sprintf("上一版 r%d 不是成功记录（%s），没有可自动回滚的健康基线", prev.Seq, prev.Status))
	}
	rc.RollbackReason = fmt.Sprintf("迁移与格式版本与切换前相同（数据未被新版本触碰），自动回滚到 r%d", prev.Seq)
	fmt.Fprintf(w, "切换失败：%v\n%s\n", err, rc.RollbackReason)
	if rbErr := startRecord(ops, s, prev, timeout); rbErr != nil {
		rc.Health += "；回滚：" + rbErr.Error()
		rc.Notes = append(rc.Notes, runningImages(ctx)...)
		return exitWith(3, fmt.Errorf("切换到 r%d 失败（%v），自动回滚到 r%d 也失败，不重试，需人工处理：%w", rec.Seq, err, prev.Seq, rbErr))
	}
	rc.RolledBack = true
	_ = tagImages(ctx, prev.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
	return exitWith(2, fmt.Errorf("切换到 r%d 失败，已自动回滚到 r%d：%w", rec.Seq, prev.Seq, err))
}

// runningImages 记录各容器实际运行的镜像（回滚失败或拒绝回滚时写进收据）。
func runningImages(ctx context.Context) []string {
	out, err := deployCapture(ctx, "docker", "ps", "-a", "--format", "{{.Names}} {{.Image}} {{.Status}}")
	if err != nil {
		return []string{"实际运行镜像读取失败：" + err.Error()}
	}
	lines := []string{"各容器实际运行的镜像："}
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

// siteInstallOptions 由 server.install 生成首装参数（访问方式三选一）。
func siteInstallOptions(cfg *siteConfig, data string) (installOptions, error) {
	o := installOptions{Data: data}
	in := cfg.Server.Install
	if in == nil {
		return o, errors.New("首次安装需要 server.install（address、domain、proxy 三选一）")
	}
	count := 0
	for _, v := range []*string{in.Address, in.Domain, in.Proxy} {
		if v != nil {
			count++
		}
	}
	if count != 1 {
		return o, errors.New("server.install 的 address、domain、proxy 必须恰好填一个")
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	o.Address, o.Domain, o.Proxy = str(in.Address), str(in.Domain), str(in.Proxy)
	o.DesktopPublicKey, o.DesktopPublisherTenant, o.DesktopConfirmFingerprint = str(in.DesktopPublicKey), str(in.DesktopPublisherTenant), str(in.DesktopConfirmFingerprint)
	return o, nil
}

// initialTrustKey：配置了带 trustFingerprint 的套件时，用其发行公钥钉定（D10）；返回公钥
// 文件路径，没有则返回空（state 标记未钉定）。
func initialTrustKey(cfg *siteConfig) string {
	for _, kit := range cfg.Deps.Kits {
		if kit.TrustFingerprint == "" {
			continue
		}
		fp, err := normalizeFingerprint("trustFingerprint", kit.TrustFingerprint)
		if err != nil {
			continue
		}
		pem := filepath.Join(kit.Path, "release-public.pem")
		if noSymlinks(pem) != nil {
			continue
		}
		if got, err := publicFingerprint(pem); err == nil && got == fp {
			return pem
		}
	}
	return ""
}

// 首装用到的可注入点。
var (
	deployInstallSteps = installSteps
	deployPinTrust     = pinInitialTrust
)

func deployFirstInstall(ctx context.Context, cfg *siteConfig, data string, rec deployRecord, run *siteRun) error {
	rc, w := run.receipt, run.out
	rc.Phase = "switch"
	rc.SwitchMode = "首次安装（复用 install 步骤）"
	meta := data + ".oneclick"
	if entries, err := os.ReadDir(data); err == nil && len(entries) > 0 {
		return exitWith(1, errors.New("数据目录非空，拒绝接管已有安装（已用 deploy-nas 脚本部署的站点请用 adopt，属 2e）"))
	}
	o, err := siteInstallOptions(cfg, data)
	if err != nil {
		return exitWith(1, err)
	}
	if err := os.Mkdir(meta, 0755); err != nil {
		return exitWith(1, err)
	}
	rec.Status = recordInstalling
	s := state{Data: data, Package: rec.Release, Access: filepath.Join(meta, "access.json"), Source: sourceLocalDeploy, Current: rec.Seq, History: []deployRecord{rec}}
	key := initialTrustKey(cfg)
	if key != "" {
		if err := deployPinTrust(&s, key); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "已用套件的 trustFingerprint 钉定发行钥 %s\n", key)
	} else {
		s.TrustUnpinned = true
		rc.Notes = append(rc.Notes, "没有带 trustFingerprint 的套件，发行钥未钉定；第一次签名包 upgrade 前须先 trust add-*")
		fmt.Fprintln(w, "没有带 trustFingerprint 的套件：发行钥未钉定（state 标记 trustUnpinned）；第一次签名包 upgrade 前须先 trust add-*")
	}
	images := cloneImages(rec.Images)
	err = deployInstallSteps(&s, o, key != "", nil, func(state) (map[string]string, error) { return images, nil }, healthTimeout(cfg))
	if err != nil {
		rc.Health = err.Error()
		return exitWith(1, fmt.Errorf("首次安装失败，已保留现场（%s 与数据目录），排查后处理：%w", meta, err))
	}
	cur := s.record(rec.Seq)
	cur.Status = recordOK
	after := readDataVersions(s)
	rc.Data.After = after
	if after.ok() {
		cur.Migrations, cur.Formats = after.Migrations, after.Formats
	}
	if err := saveState(s); err != nil {
		return exitWith(1, err)
	}
	rc.Health = "healthy"
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		rc.SwitchedServices = append(rc.SwitchedServices, svc)
	}
	_ = tagImages(ctx, cur.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
	fmt.Fprintf(w, "首次安装完成：当前 r%d\n", rec.Seq)
	return nil
}

// probeRecipeImage（--dry-run）：只报告镜像来源，不载入、不拉取。
func probeRecipeImage(ctx context.Context, r *depsResolver, cfg *siteConfig, role, ref string) string {
	if hits, err := r.findImages(role); err == nil {
		for _, h := range hits {
			if h.Image.digestHex() == digestOf(ref) {
				return h.Location
			}
		}
	}
	if _, _, ok := localImage(ctx, ref); ok {
		return "本机 Docker"
	}
	if cfg.Deps.Registry != nil {
		return "将从 registry " + *cfg.Deps.Registry + " 按 digest 拉取"
	}
	return "缺失（deps import 或配置 deps.registry）"
}

func printDeployPlan(ctx context.Context, w io.Writer, cfg *siteConfig, r *depsResolver, recipe *deployRecipe, s *state, first bool, prev *deployRecord, chosen, roles []string, inputs []resolvedInput, missing []string, rc *siteReceipt) error {
	fmt.Fprintln(w, "部署计划（--dry-run：不构建、不切换）")
	if first {
		fmt.Fprintln(w, "  类型：首次安装（复用 install 步骤），构建全部镜像")
	} else {
		fmt.Fprintf(w, "  类型：再次部署，当前 r%d，新序号 r%d\n", prev.Seq, nextSeq(*s))
	}
	fmt.Fprintf(w, "  构建镜像：%s（原因：全量构建；按输入指纹跳过构建属 2c，构建后装配摘要与当前相同的镜像跳过切换）\n", strings.Join(chosen, "、"))
	var skipped []string
	for _, svc := range deployServices {
		found := false
		for _, c := range chosen {
			found = found || c == svc
		}
		if !found {
			skipped = append(skipped, svc)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "  沿用当前镜像：%s（--services 未选中）\n", strings.Join(skipped, "、"))
	}
	fmt.Fprintln(w, "  切换方式：完整启动（render → preflight → start）；只换容器属 2d")
	fmt.Fprintln(w, "  依赖输入：")
	for _, in := range inputs {
		fmt.Fprintf(w, "    %-6s %s  %s\n", in.Ecosystem, in.LockDigest[:12], in.Location)
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "  需要补的依赖：%s（acceptNewDeps=%t）\n", strings.Join(missing, "、"), cfg.Deps.AcceptNewDeps)
	}
	fmt.Fprintln(w, "  镜像（配方固定 digest）：")
	sort.Strings(roles)
	for _, role := range roles {
		loc := probeRecipeImage(ctx, r, cfg, role, recipe.Images[role])
		fmt.Fprintf(w, "    %-16s %s  %s\n", role, recipe.Images[role], loc)
		rc.BuildImages = append(rc.BuildImages, resolvedImage{Role: role, Ref: recipe.Images[role], Location: loc})
	}
	if first {
		fmt.Fprintln(w, "  迁移与格式版本：首次安装，无")
		return nil
	}
	now := readDataVersions(*s)
	rc.Data.Before = now
	if !now.ok() {
		fmt.Fprintf(w, "  迁移与格式版本：读取失败（%s）；切换失败时将不自动回滚\n", now.Err)
		return nil
	}
	fmt.Fprintf(w, "  迁移版本：%d 张迁移表；格式版本：%d 项\n", len(now.Migrations), len(now.Formats))
	keys := make([]string, 0, len(now.Migrations))
	for k := range now.Migrations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "    %s  %s\n", k, now.Migrations[k])
	}
	fmt.Fprintln(w, "  切换失败时：迁移与格式版本未变则自动回滚（退出码 2），否则交人工（退出码 3）")
	return nil
}
