package main

// auriga deploy（设计 D10；第 2c/2d 步：输入指纹决定是否构建，装配摘要决定是否换镜像，
// 编排摘要决定完整启动还是只换容器）。
//
// 流程：统一站点锁 → 取源码快照 → 读源码配方与 services.json → 依赖输入 → 固定 digest 镜像 →
// 计算产物指纹（与当前发行一致则跳过构建并复用 artifacts/）→ 断网构建需要的 go/web/java →
// 按暂存清单装配（装配摘要分项与当前相同则跳过切换，收据写触发项）→ 打 r<序号> 标签 →
// 写不可变发行目录（含 provenance.json、fingerprints.json、artifacts/、versions.txt）→
// 首装或切换（编排未变且仅 edge/platform 时只换容器，否则完整启动）→ 收据。
//
// 受限调用（经 sudo，见 privilege.go）只接受白名单参数，且只允许部署当前运行提交的后代
// （快进）；root 人工 --allow-downgrade 可向旧提交部署，先做与 rollback 相同的数据检查。
// adopt 接管的站点（source=adopted）可继续 deploy：无可信指纹/装配摘要时一律重建并切换。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	// Operator：经 sudo 的人工 root 显式跳过白名单（privilege.go）；本身不改变部署行为。
	Operator bool
	// AllowDowngrade：root 人工向非当前提交后代的提交部署（先做数据检查）。
	AllowDowngrade bool
	// Restricted：受限调用（白名单已在解析前执行），部署只允许快进。
	Restricted bool
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
	f.BoolVar(&o.Operator, "operator", false, "")
	f.BoolVar(&o.AllowDowngrade, "allow-downgrade", false, "")
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
	rest, restricted, err := sitePrivilege("deploy", args, inv)
	if err != nil {
		return err
	}
	o, err := parseDeploy(rest)
	if err != nil {
		return err
	}
	o.Restricted = restricted
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
	run.receipt.Notes = append(run.receipt.Notes, invocationNote(inv, restricted))
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
		if src := s.source(); src != sourceLocalDeploy && src != sourceSignedPackage && src != sourceAdopted {
			return exitWith(1, fmt.Errorf("当前安装来源为 %s：deploy 只接管 deploy、签名包安装或 adopt 接管的站点", src))
		}
		if _, err := os.Lstat(data + ".upgrade.json"); err == nil {
			return exitWith(1, errors.New("存在中断的签名包升级，请先重新运行 upgrade 恢复"))
		}
		if prev = s.record(s.Current); prev == nil {
			if s.source() != sourceSignedPackage || len(s.History) != 0 {
				return exitWith(1, fmt.Errorf("state 缺少当前发行 r%d 的历史记录，拒绝继续", s.Current))
			}
			// 签名包安装、还没有历史的站点：把当前安装包登记为基线历史，切换失败时可以回到它。
			base, err := packageBaseline(ctx, &s, !o.DryRun)
			if err != nil {
				return exitWith(1, err)
			}
			prev = base
			rc.Notes = append(rc.Notes, fmt.Sprintf("签名包安装的站点第一次 deploy：当前安装包已登记为历史 r%d（%s）", base.Seq, base.Release))
		}
		if prev.Status == recordInstalling {
			return exitWith(1, fmt.Errorf("首次安装（r%d）未完成：现场已保留，请先排查 %s 与数据目录；确认放弃后移走二者再重新 deploy", prev.Seq, data+".oneclick"))
		}
		if prev.Status != recordOK && len(o.Services) > 0 {
			return exitWith(1, fmt.Errorf("上一版 r%d 状态为 %s，不能使用 --services 增量部署，请去掉 --services 完整部署", prev.Seq, prev.Status))
		}
	}
	if first && len(o.Services) > 0 {
		return exitWith(1, errors.New("首次安装必须构建全部镜像，不能用 --services"))
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
	if src.Signature != "" {
		fmt.Fprintf(w, "提交签名：%s\n", src.Signature)
	}
	if g := cfg.Source; g != nil && g.Git != nil && !g.Git.RequireSignedCommits && len(g.Git.TrustedSigners) > 0 {
		rc.Notes = append(rc.Notes, "source.git.trustedSigners 已配置但 requireSignedCommits=false：本次未校验提交签名")
	}
	var downgradeBaseline *dataVersions
	if !first {
		baseline, err := checkDeployDirection(ctx, data, &s, prev, src, o, rc, w)
		if err != nil {
			return err
		}
		downgradeBaseline = baseline
	}
	recipe, err := loadDeployRecipe(src.Dir)
	if err != nil {
		return exitWith(1, err)
	}
	services, err := loadSiteServices(src.Dir)
	if err != nil {
		return exitWith(1, err)
	}

	// 先按用户选择确定候选；指纹算完后再补共享输入变化的服务。
	manual := len(o.Services) > 0
	selected := map[string]bool{}
	if !manual {
		for _, svc := range deployServices {
			selected[svc] = true
		}
	}
	for _, svc := range o.Services {
		selected[svc] = true
	}
	var seed []string
	for _, svc := range deployServices {
		if selected[svc] {
			seed = append(seed, svc)
		}
	}

	rc.Deps.Sources = map[string]string{}
	for _, eco := range []string{"go", "web", "java", "python", "system"} {
		rc.Deps.Sources[eco] = depsSourceLabel(cfg.Deps, eco)
	}
	// deps-lock 输入集用源码锁清单的摘要（与依赖输入的锁摘要同一口径），不要求依赖输入在场。
	srcLocks, err := sourceEcosystemLocks(src.Dir)
	if err != nil {
		return exitWith(1, err)
	}
	locks := map[string]string{}
	for _, l := range srcLocks {
		locks[l.Ecosystem] = l.Digest
	}
	r := newDepsResolver(cfg)
	resolved := map[string]resolvedImage{}
	inputDir := map[string]string{}
	var inputs []resolvedInput
	var missing []string
	addInputs := func(got []resolvedInput, miss []string) {
		inputs = append(inputs, got...)
		missing = append(missing, miss...)
		rc.Deps.Inputs, rc.Deps.Missing = inputs, append([]string{}, missing...)
		for _, in := range got {
			inputDir[in.Ecosystem] = in.Dir
		}
	}
	resolveRole := func(role string) error {
		if _, ok := resolved[role]; ok {
			return nil
		}
		img, err := resolveRecipeImage(ctx, r, cfg, role, recipe.Images[role], stage)
		if err != nil {
			return err
		}
		resolved[role] = img
		rc.BuildImages = append(rc.BuildImages, img)
		return nil
	}
	// Go 指纹要在断网工具容器里跑 go list -deps（依赖只来自已校验的 Go 依赖输入），
	// 所以 Go 依赖输入与 tool-go 镜像每次都先就位；--dry-run 不跑容器，Go 产物按“将重建”计。
	var goDeps map[string][]string
	if !o.DryRun {
		if err := deployBuildEnv(ctx, o.Budget); err != nil {
			return exitWith(1, err)
		}
		got, miss, err := resolveDeployInputs(ctx, cfg, cfgPath, src.Dir, []string{"go"}, o.Accept, false, o.Budget, stage, w)
		addInputs(got, miss)
		if err != nil {
			return exitWith(1, err)
		}
		if err := resolveRole("tool-go"); err != nil {
			return exitWith(1, err)
		}
		if goDeps, err = goDepsRunner(ctx, resolved["tool-go"].ID, src.Dir, inputDir["go"], stage, o.Budget); err != nil {
			return exitWith(1, err)
		}
	}
	prevOK := prev != nil && prev.Status == recordOK
	prevFP := map[string]string{}
	if prevOK && len(prev.Fingerprints) > 0 {
		prevFP = prev.Fingerprints
	}
	fc := fingerprintContext{Source: src.Dir, Services: services, Recipe: recipe, Locks: locks, Tools: recipe.Images, GoDeps: goDeps}
	fpReport, err := computeAllFingerprints(ctx, fc)
	if err != nil {
		return exitWith(1, fmt.Errorf("计算输入指纹失败：%w", err))
	}
	rc.Fingerprints = fingerprintMap(fpReport)

	chosen := seed
	if manual {
		var autoAdded []string
		chosen, autoAdded = expandSelectedServices(services, seed, fpReport, prevFP)
		for _, a := range autoAdded {
			selected[a] = true
			fmt.Fprintf(w, "提示：服务 %s 与所选服务共享输入且指纹已变，自动补上\n", a)
			rc.Notes = append(rc.Notes, "因共享输入变化自动补上服务 "+a)
		}
	}

	builds := kindsToBuild(services, fpReport, prevFP, o.Full || first || (prev != nil && !prevOK), chosen)
	out := filepath.Join(stage, "output")
	for _, kind := range []string{"go", "web", "java"} {
		if err := mkdirAllExact(filepath.Join(out, kind), 0755); err != nil {
			return exitWith(1, err)
		}
	}
	// 先复用未变产物（复用前按回滚规则核对上一版发行目录）；任何问题都改为重建，不拿可疑产物装配。
	// 上一版不是 ok 记录时禁止复用，避免把坏产物带入新发行。
	if !o.DryRun && !o.Full && !first && prevOK {
		var reuse []string
		for _, a := range reusableArtifacts(fpReport, prevFP, chosen, services) {
			if !builds[artifactBuildKind(a)] {
				reuse = append(reuse, a)
			}
		}
		if err := reuseArtifactsFromRelease(ctx, data, prev, out, reuse); err != nil {
			fmt.Fprintf(w, "警告：%v；改为重建相关产物\n", err)
			rc.Notes = append(rc.Notes, "复用上一版产物失败，改为重建："+err.Error())
			for k := range kindsToBuild(services, fpReport, map[string]string{}, true, chosen) {
				builds[k] = true
			}
		}
	}
	_, needInputs := recipe.needs(chosen)
	var restEcos []string
	for _, k := range []string{"web", "java"} {
		if builds[k] {
			restEcos = append(restEcos, k)
		}
	}
	for _, eco := range []string{"system", "python"} {
		if needInputs[eco] {
			restEcos = append(restEcos, eco)
		}
	}
	if o.DryRun || len(restEcos) > 0 {
		need := restEcos
		if o.DryRun {
			need = append([]string{"go"}, restEcos...)
		}
		got, miss, err := resolveDeployInputs(ctx, cfg, cfgPath, src.Dir, need, o.Accept, o.DryRun, o.Budget, stage, w)
		addInputs(got, miss)
		if err != nil {
			return exitWith(1, err)
		}
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
	if o.DryRun {
		return printDeployPlan(ctx, w, cfg, r, recipe, fpReport, prevFP, &s, first, prev, chosen, builds, roles, inputs, missing, o, rc, src.Dir)
	}
	for _, role := range roles {
		if err := resolveRole(role); err != nil {
			return exitWith(1, err)
		}
	}

	buildCache := &receiptBuildCache{AssemblyLayerCache: !o.Full}
	var goBuildOpts toolBuildOpts
	if builds["go"] {
		toolRef := recipe.Images["tool-go"]
		if img, ok := resolved["tool-go"]; ok && img.Ref != "" {
			toolRef = img.Ref
		}
		if o.Full {
			fmt.Fprintln(w, "Go 构建缓存：--full，使用容器内临时 GOCACHE（不挂持久化缓存）")
			rc.Notes = append(rc.Notes, "Go 构建缓存：--full，临时 GOCACHE")
		} else {
			if _, err := ensureDepsCache(cfg.Deps.Cache, true); err != nil {
				return exitWith(1, err)
			}
			cacheDir, err := ensureGoBuildCache(cfg.Deps.Cache, digestOf(toolRef))
			if err != nil {
				return exitWith(1, fmt.Errorf("准备 Go 构建缓存失败：%w", err))
			}
			goBuildOpts.GoCacheHost = cacheDir
			buildCache.GoUsed = true
			buildCache.GoPath = cacheDir
			buildCache.GoToolImage = toolRef
			fmt.Fprintf(w, "Go 构建缓存：%s（工具镜像 %s）\n", cacheDir, toolRef)
		}
	}
	if o.Full {
		fmt.Fprintln(w, "装配层缓存：--full，带 --no-cache")
	} else {
		fmt.Fprintln(w, "装配层缓存：启用（BuildKit 确定性层缓存）")
	}
	rc.BuildCache = buildCache

	for _, kind := range []string{"go", "web", "java"} {
		dir := filepath.Join(out, kind)
		if !builds[kind] {
			if dirHasFiles(dir) {
				// 复用的产物已按上一版清单核对过；按本次实际内容重写 SHA256SUMS 供暂存核对。
				if err := rewriteArtifactSums(dir); err != nil {
					return exitWith(1, err)
				}
				rc.Artifacts = append(rc.Artifacts, receiptArtifact{Name: kind, Action: "reused", Reason: fmt.Sprintf("输入指纹与 r%d 一致，复用其 artifacts/%s", prev.Seq, kind)})
			}
			continue
		}
		// 重建该生态时清空后全量构建，避免与复用文件混杂。
		if err := os.RemoveAll(dir); err != nil {
			return exitWith(1, err)
		}
		if err := mkdirAllExact(dir, 0755); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "构建 %s 产物（断网）\n", kind)
		opts := toolBuildOpts{}
		if kind == "go" {
			opts = goBuildOpts
		}
		if err := runToolBuild(ctx, kind, resolved["tool-"+kind].ID, src.Dir, inputDir[kind], dir, o.Budget, opts); err != nil {
			return exitWith(1, err)
		}
		if err := checkArtifactSums(dir); err != nil {
			return exitWith(1, err)
		}
		reason := "输入指纹变化"
		switch {
		case first:
			reason = "首次安装"
		case o.Full:
			reason = "--full 强制重建"
		case prev != nil && !prevOK:
			reason = fmt.Sprintf("上一版 r%d 状态为 %s，强制重建", prev.Seq, prev.Status)
		}
		rc.Artifacts = append(rc.Artifacts, receiptArtifact{Name: kind, Action: "built", Reason: reason})
	}
	recorded := recordedFingerprints(fpReport, out)
	rc.Fingerprints = fingerprintMap(recorded)

	prov := &provenance{
		Version:        "local-" + src.SnapshotSHA256[:12],
		Commit:         src.Commit,
		Dirty:          src.Dirty,
		SiteChanges:    src.Dirty,
		SnapshotSHA256: src.SnapshotSHA256,
		BuiltAt:        time.Now().UTC().Format(time.RFC3339),
	}

	seq := nextSeq(s)
	images, assembly := map[string]string{}, map[string]string{}
	assemblyPartsMap := map[string]assemblyParts{}
	if prevOK {
		images = cloneImages(prev.Images)
		for k, v := range prev.Assembly {
			assembly[k] = v
		}
		for k, v := range prev.AssemblyParts {
			assemblyPartsMap[k] = v
		}
	}
	dockerfile := filepath.Join(src.Dir, filepath.FromSlash(deployDockerfile))
	for _, svc := range deployServices {
		key := imageKey(svc)
		old := ""
		if prev != nil {
			old = prev.Images[key]
		}
		if !selected[svc] {
			rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "not-selected", Reason: "--services 未选中，沿用 r" + fmt.Sprint(prev.Seq), OldID: old, NewID: old, Tag: imageTag(svc, seq), Assembly: assembly[svc]})
			continue
		}
		scratch := filepath.Join(stage, "assemble", svc)
		if err := mkdirAllExact(scratch, 0755); err != nil {
			return exitWith(1, err)
		}
		if err := stageService(ctx, svc, recipe.Services[svc], src.Dir, out, inputDir, scratch); err != nil {
			return exitWith(1, fmt.Errorf("暂存 %s 失败：%w", svc, err))
		}
		parts, err := assemblyDigest(ctx, svc, recipe, scratch)
		if err != nil {
			return exitWith(1, err)
		}
		assembly[svc] = parts.Total
		assemblyPartsMap[svc] = parts
		needBuild, reason := decideAssembly(first, o.Full, svc, prev, parts, func(id string) bool {
			_, _, ok := localImage(ctx, id)
			return ok
		})
		if !needBuild {
			rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "unchanged", Reason: reason, OldID: old, NewID: old, Tag: imageTag(svc, seq), Assembly: parts.Total})
			continue
		}
		base := resolved["base-"+svc]
		baseTag := kitImageTag("base-"+svc, base.Ref)
		if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"tag", base.ID, baseTag}}); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "装配 %s 镜像（%s）…\n", svc, reason)
		id, err := assembleImage(ctx, svc, baseTag, dockerfile, scratch, imageTag(svc, seq), o.Full)
		if err != nil {
			return exitWith(1, err)
		}
		images[key] = id
		rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "built", Reason: reason, Triggers: reason, OldID: old, NewID: id, Tag: imageTag(svc, seq), Assembly: parts.Total})
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
	if o.BackupDB && !first {
		if err := backupDatabases(s, rc, w); err != nil {
			return exitWith(1, err)
		}
	} else if o.BackupDB && first {
		rc.Notes = append(rc.Notes, "--backup-db：首次安装无运行中的库，已跳过 pg_dump")
	}
	if err := tagImages(ctx, images, func(svc string) string { return imageTag(svc, seq) }); err != nil {
		return exitWith(1, err)
	}
	floor, formatMax, err := loadCompatForManifest(src.Dir)
	if err != nil {
		return exitWith(1, fmt.Errorf("读取源码 schema 兼容声明失败：%w", err))
	}
	manifest := releaseManifest{
		Source: *src, Images: images, Assembly: assembly, AssemblyParts: assemblyPartsMap,
		Fingerprints: fingerprintMap(recorded), SchemaCompatFloor: floorPtr(floor), FormatMax: formatMax, CreatedAt: nowRFC3339(),
	}
	var orchIn *orchInputs
	if !first {
		orchIn = &orchInputs{Data: s.Data, Access: s.Access}
	}
	release, releaseSums, err := writeReleaseDir(ctx, data, seq, src.Dir, filepath.Join(out, "go"), out, manifest, prov, recorded, orchIn)
	if err != nil {
		return exitWith(1, err)
	}
	rec := deployRecord{
		Seq: seq, Status: recordInstalling, At: nowRFC3339(), Source: *src, Release: release, ReleaseSums: releaseSums,
		Images: images, Assembly: assembly, AssemblyParts: assemblyPartsMap, Fingerprints: fingerprintMap(recorded),
		SchemaCompatFloor: floor, FormatMax: formatMax,
	}
	rc.Release = &receiptRelease{Seq: seq, Dir: release, Previous: s.Current}
	fmt.Fprintf(w, "发行目录 %s\n", release)
	if first {
		return deployFirstInstall(ctx, cfg, data, rec, run)
	}
	return deploySwitch(ctx, ops, cfg, &s, prev, rec, run, downgradeBaseline)
}

// deploySwitch：按编排摘要与镜像差选择只换容器或完整启动；失败且数据未被触碰时自动回滚（退出码 2），否则退出码 3。
// downgrade 非空时是 --allow-downgrade 检查时读到的数据版本：切换前再读一次，变了就不切换（退出码 3）。
func deploySwitch(ctx context.Context, ops siteOps, cfg *siteConfig, s *state, prev *deployRecord, rec deployRecord, run *siteRun, downgrade *dataVersions) error {
	rc, w := run.receipt, run.out
	rc.Phase = "switch"
	plan, err := planSwitch(prev, &rec, s.Data, s.Access)
	if err != nil {
		return exitWith(1, fmt.Errorf("计算切换方案失败：%w", err))
	}
	if cfg != nil && cfg.Server != nil && cfg.Server.Install != nil && cfg.Server.Install.Runtime != nil {
		req := *cfg.Server.Install.Runtime
		if s.Runtime == nil {
			fmt.Fprintf(w, "为既有站点受控追加 DSH 运行时（%s）…\n", req.Image)
			if err := attachInstalledRuntime(s, rec.Release, req); err != nil {
				return exitWith(1, fmt.Errorf("追加 DSH 运行时失败：%w", err))
			}
			rc.Notes = append(rc.Notes, "已为既有站点受控追加 DSH 运行时（config/runtime-source）")
			if plan.Mode != switchModeFull {
				plan.Mode = switchModeFull
				plan.Reason += "；已追加 DSH 运行时，改为完整启动"
			}
		} else if s.Runtime.Source != req {
			return exitWith(1, errors.New("site.json 的 server.install.runtime 与已登记的站点 runtime 不同；不得通过 deploy 替换根或模板，请使用 runtime-upgrade"))
		}
	}
	rec.Orchestration = plan.Orchestration
	changed := plan.OrchestrationChanged
	rc.OrchestrationChanged = &changed
	rc.Orchestration = plan.Orchestration
	rc.SwitchMode = plan.Mode
	rc.MixedVersions = mixedVersionsNote(plan)
	rc.Notes = append(rc.Notes, "切换原因："+plan.Reason)
	if plan.MixedBroker {
		rc.Notes = append(rc.Notes, rc.MixedVersions)
	}
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		if prev.Images[imageKey(svc)] != rec.Images[imageKey(svc)] {
			rc.SwitchedServices = append(rc.SwitchedServices, svc)
		}
	}
	before := readDataVersions(*s)
	rc.Data.Before = before
	if downgrade != nil && !dataUntouched(downgrade, before) {
		return exitWith(3, errors.New("--allow-downgrade 检查之后迁移或格式版本发生了变化（或读不到），不切换，需人工处理"))
	}
	rec.Kind = recordKindDeploy
	rec.Status = recordFailed // 成功后改为 ok；中途崩溃时不会被当作可回滚目标
	s.History = append(s.History, rec)
	cur := s.record(rec.Seq)
	if p := s.record(prev.Seq); p != nil {
		prev = p // append 可能换了底层数组：之后对 prev 的修改（broker 运行镜像）要落进 state
	}
	timeout := healthTimeout(cfg)
	fmt.Fprintf(w, "切换到 r%d（%s）…\n", rec.Seq, formatSwitchPlanLine(plan))
	err = startRecord(ops, s, cur, timeout, plan)
	if err == nil {
		rc.Health = "healthy"
		if cfg.Server != nil && cfg.Server.Smoke != nil && !*cfg.Server.Smoke {
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
		if err := executePostDeployHook(ctx, cfg, "upgrade", *s, rc, w); err != nil {
			return exitWith(1, err)
		}
		fmt.Fprintf(w, "部署完成：当前 r%d\n", rec.Seq)
		return nil
	}
	rc.Phase = "rollback"
	_ = saveState(*s)
	// 切换阶段可能已被取消：收尾用独立带超时的上下文，避免 runningImages / floor 祖先检查
	// 拿到已取消的 ctx 而把本可自动回滚的情况误判成退出码 3（第 7 步遗留）。
	cleanupCtx, cleanupCancel := deployCleanupContext()
	defer cleanupCancel()
	// 先停掉新版本里会写库或改写数据目录的容器，确认停止后再读迁移与格式版本：否则读完到
	// 回滚完成之间，新容器（例如迁移较慢的 platform）仍可能提交迁移（P2）。
	const manualConvergeHint = "人工收敛指引：先运行 auriga status 查看当前状态与容器镜像，排查修复后运行 auriga deploy <commit> --full 收敛到目标发行（或按需检查发行目录 deploy-nas/scripts/start.sh / auriga rollback）"
	refuse := func(reason string) error {
		rc.RollbackReason = reason
		rc.Notes = append(rc.Notes, runningImages(cleanupCtx)...)
		rc.Notes = append(rc.Notes, manualConvergeHint)
		return exitWith(3, fmt.Errorf("切换到 r%d 失败：%v；%s，需人工处理。%s", rec.Seq, err, reason, manualConvergeHint))
	}
	stopped, stopErr := ops.stopWriters(*s)
	if stopErr != nil {
		rc.Data.After = &dataVersions{Err: "未读取：" + stopErr.Error()}
		return refuse("无法确认新版本会写数据的容器已停止（" + stopErr.Error() + "），视为数据可能已被改动，不自动回滚")
	}
	rc.Notes = append(rc.Notes, "判定是否回滚前已停止新版本会写数据的服务："+strings.Join(stopped, "、"))
	after := readDataVersions(*s)
	rc.Data.After = after
	if prev.Status != recordOK {
		return refuse(fmt.Sprintf("上一版 r%d 不是成功记录（%s），没有可自动回滚的健康基线", prev.Seq, prev.Status))
	}
	// P2：切换前后数据版本相同即可回滚；否则用新发行 floor + 上一版 FormatMax（2b）。
	var reason string
	if dataUntouched(before, after) {
		reason = fmt.Sprintf("迁移与格式版本与切换前相同（数据未被新版本触碰），自动回滚到 r%d", prev.Seq)
	} else {
		ok, why := rollbackDecision(prev, cur, newerRecords(s, prev), after, floorAncestorCheck(cleanupCtx, s.Data, prev))
		if !ok {
			return refuse(why + "；会写数据的新容器保持停止")
		}
		reason = why + fmt.Sprintf("，自动回滚到 r%d", prev.Seq)
	}
	// 与手动 rollback 一样，回到上一版前核对其发行目录（SHA256SUMS 与 state 锚定的 releaseSums）。
	if err := checkReleaseDir(s.Data, prev); err != nil {
		return refuse(fmt.Sprintf("上一版 r%d 的发行目录校验失败（%v），不自动回滚", prev.Seq, err))
	}
	rc.RollbackReason = reason
	fmt.Fprintf(w, "切换失败：%v\n%s\n", err, rc.RollbackReason)
	rbPlan, rbPlanErr := planRollbackSwitch(cur, prev, s.Data, s.Access)
	if rbPlanErr != nil {
		return refuse("计算回滚切换方案失败：" + rbPlanErr.Error())
	}
	if rbPlan.Mode != switchModeFull {
		// 上面为判定数据是否被触碰已停掉全部写数据服务（含 platform、agent、bifrost、network-broker），
		// 只换容器“其余服务保持运行”的前提不成立，只能完整启动上一版才能把它们按原样拉起。
		rbPlan.Mode = switchModeFull
		rbPlan.Reason += "；但自动回滚前已停止全部写数据服务，改为完整启动"
		rbPlan.setBroker(cur, prev)
	}
	rc.Notes = append(rc.Notes, "自动回滚路径："+formatSwitchPlanLine(rbPlan))
	if rbErr := startRecord(ops, s, prev, timeout, rbPlan); rbErr != nil {
		rc.Health += "；回滚：" + rbErr.Error()
		rc.Notes = append(rc.Notes, runningImages(cleanupCtx)...)
		rc.Notes = append(rc.Notes, manualConvergeHint)
		return exitWith(3, fmt.Errorf("切换到 r%d 失败（%v），自动回滚到 r%d 也失败，不重试，需人工处理（%s）：%w", rec.Seq, err, prev.Seq, manualConvergeHint, rbErr))
	}
	rc.RolledBack = true
	_ = tagImages(ctx, prev.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
	return exitWith(2, fmt.Errorf("切换到 r%d 失败，已自动回滚到 r%d：%w", rec.Seq, prev.Seq, err))
}

// deployCleanupTimeout 是切换失败收尾（读运行镜像、floor 祖先检查）用的独立超时。
const deployCleanupTimeout = 2 * time.Minute

// deployCleanupContext 返回与信号无关的收尾上下文（可注入以便单测）。
var deployCleanupContext = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), deployCleanupTimeout)
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
	if in.Runtime != nil {
		o.Runtime = *in.Runtime
	}
	o.UACMaterialsDir = str(in.UACMaterialsDir)
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
	var prepared *preparedInstallRuntime
	if o.Runtime.enabled() {
		prepared, err = prepareRuntimeSnapshot(rec.Release, o.Runtime)
		if err != nil {
			return exitWith(1, fmt.Errorf("首次安装 runtime 校验失败：%w", err))
		}
		defer func() { _ = os.RemoveAll(prepared.directory) }()
	}
	if err := os.Mkdir(meta, 0755); err != nil {
		return exitWith(1, err)
	}
	rec.Status, rec.Kind = recordInstalling, recordKindDeploy
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
	err = deployInstallSteps(&s, o, key != "", prepared, func(state) (map[string]string, error) { return images, nil }, healthTimeout(cfg))
	if err != nil {
		rc.Health = err.Error()
		return exitWith(1, fmt.Errorf("首次安装失败，已保留现场（%s 与数据目录），排查后处理：%w", meta, err))
	}
	cur := s.record(rec.Seq)
	cur.Status = recordOK
	if orch, err := orchestrationDigest(filepath.Join(rec.Release, "deploy-nas"), s.Data, s.Access); err == nil {
		cur.Orchestration = orch
		rc.Orchestration = orch
	} else {
		// 没有摘要的记录下次部署按编排变化完整启动，安全；只提示。
		rc.Notes = append(rc.Notes, "编排摘要计算失败（下次部署将完整启动）："+err.Error())
	}
	after := readDataVersions(s)
	rc.Data.After = after
	if after.ok() {
		cur.Migrations, cur.Formats = after.Migrations, after.Formats
	}
	changed := false
	rc.OrchestrationChanged = &changed
	rc.MixedVersions = "无"
	if err := saveState(s); err != nil {
		return exitWith(1, err)
	}
	rc.Health = "healthy"
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		rc.SwitchedServices = append(rc.SwitchedServices, svc)
	}
	_ = tagImages(ctx, cur.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
	if err := executePostDeployHook(ctx, cfg, "install", s, rc, w); err != nil {
		return exitWith(1, err)
	}
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

func printDeployPlan(ctx context.Context, w io.Writer, cfg *siteConfig, r *depsResolver, recipe *deployRecipe, fp *fingerprintReport, prevFP map[string]string, s *state, first bool, prev *deployRecord, chosen []string, builds map[string]bool, roles []string, inputs []resolvedInput, missing []string, o deployOptions, rc *siteReceipt, srcDir string) error {
	fmt.Fprintln(w, "部署计划（--dry-run：不构建、不切换）")
	if first {
		fmt.Fprintln(w, "  类型：首次安装（复用 install 步骤），构建全部镜像")
	} else {
		fmt.Fprintf(w, "  类型：再次部署，当前 r%d，新序号 r%d\n", prev.Seq, max(nextSeq(*s), prev.Seq+1))
	}
	fmt.Fprintf(w, "  目标镜像：%s\n", strings.Join(chosen, "、"))
	var buildKinds, skipKinds []string
	for _, k := range []string{"go", "web", "java"} {
		if builds[k] {
			buildKinds = append(buildKinds, k)
		} else {
			skipKinds = append(skipKinds, k)
		}
	}
	if len(buildKinds) > 0 {
		fmt.Fprintf(w, "  将构建产物生态：%s（输入指纹变化、--full 或首次安装）\n", strings.Join(buildKinds, "、"))
	}
	if len(skipKinds) > 0 && !first {
		fmt.Fprintf(w, "  跳过构建（指纹未变，复用上一版 artifacts/）：%s\n", strings.Join(skipKinds, "、"))
	}
	fmt.Fprintln(w, "  产物指纹（--dry-run 不跑工具容器展开 Go 依赖图，Go 产物一律按“将重建”列出）：")
	names := make([]string, 0, len(fp.Artifacts))
	for n := range fp.Artifacts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := fp.Artifacts[n]
		status := "将重建"
		if !o.Full && !first && prevFP[n] != "" && prevFP[n] == a.Digest {
			status = "未变"
		}
		fmt.Fprintf(w, "    %-22s %s  %s\n", n, a.Digest[:12], status)
	}
	var skipped []string
	chosenSet := map[string]bool{}
	for _, c := range chosen {
		chosenSet[c] = true
	}
	for _, svc := range deployServices {
		if !chosenSet[svc] {
			skipped = append(skipped, svc)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "  沿用当前镜像：%s（--services 未选中且无共享输入变化）\n", strings.Join(skipped, "、"))
	}
	fmt.Fprintln(w, "  装配：装配摘要（产物树、基础镜像、系统输入、Dockerfile 目标段、暂存清单）与当前相同则跳过换镜像")
	// 构建缓存计划（D14）：只打印路径与是否启用，不创建目录、不读缓存内容。
	bc := &receiptBuildCache{AssemblyLayerCache: !o.Full}
	if builds["go"] {
		toolRef := recipe.Images["tool-go"]
		if o.Full {
			fmt.Fprintln(w, "  Go 构建缓存：--full，将使用临时 GOCACHE（不挂持久化缓存）")
		} else {
			path := planGoBuildCache(cfg.Deps.Cache, digestOf(toolRef))
			if path != "" {
				bc.GoUsed = true
				bc.GoPath = path
				bc.GoToolImage = toolRef
				fmt.Fprintf(w, "  Go 构建缓存：将使用 %s（工具镜像 %s）\n", path, toolRef)
			}
		}
	} else {
		fmt.Fprintln(w, "  Go 构建缓存：本次不构建 go 产物")
	}
	if o.Full {
		fmt.Fprintln(w, "  装配层缓存：--full，将带 --no-cache")
	} else {
		fmt.Fprintln(w, "  装配层缓存：启用（BuildKit 确定性层缓存；切换仍只看装配摘要）")
	}
	rc.BuildCache = bc
	if !first && prev != nil {
		// dry-run 时尚无新发行目录：编排按源码快照的 deploy/nas + 当前发行的 deploy-nas/bin（假定
		// bootstrap 与 runtime-release 不变）+ 当前站点输入估算；选中镜像一律按将更换（真实装配后
		// 可能因摘要未变而跳过）；broker 指纹按未变假定（--dry-run 不展开 Go 依赖图，无法确认）。
		probe := *prev
		probe.Seq = prev.Seq + 1
		probe.Images = cloneImages(prev.Images)
		for _, svc := range chosen {
			probe.Images[imageKey(svc)] = "dry-run-new-" + svc
		}
		parts, err := orchestrationParts(filepath.Join(srcDir, "deploy", "nas"), filepath.Join(prev.Release, "deploy-nas", "bin"), s.Data, s.Access)
		if err == nil {
			plan := planSwitchWith(prev, &probe, parts.Total, s.Data)
			line := formatSwitchPlanLine(plan)
			if plan.Mode == switchModeContainers && plan.Edge {
				line += "；前提：auriga-network-broker 产物指纹未变，否则完整启动"
			}
			fmt.Fprintf(w, "  切换（预估）：%s\n", line)
			rc.SwitchMode = plan.Mode
			changed := plan.OrchestrationChanged
			rc.OrchestrationChanged = &changed
			rc.Orchestration = parts.Total
			rc.Notes = append(rc.Notes, "dry-run 切换预估："+line)
		} else {
			fmt.Fprintf(w, "  切换：无法预估（%v）；将按完整启动处理\n", err)
		}
	} else {
		fmt.Fprintln(w, "  切换：首次安装走完整安装步骤")
	}
	if cfg != nil && cfg.Server != nil {
		if cfg.Server.Install != nil && cfg.Server.Install.Runtime != nil {
			if first {
				fmt.Fprintf(w, "  DSH 运行时：首次安装将验签并登记 %s\n", cfg.Server.Install.Runtime.Image)
			} else if s.Runtime == nil {
				fmt.Fprintf(w, "  DSH 运行时：将为既有站点受控追加 %s（config/runtime-source）并完整启动\n", cfg.Server.Install.Runtime.Image)
			}
		}
		if first && cfg.Server.Install != nil && cfg.Server.Install.UACMaterialsDir != nil {
			fmt.Fprintf(w, "  UAC 材料：首次安装将从 %s 同步到 config/materials/uac\n", *cfg.Server.Install.UACMaterialsDir)
		}
		if cfg.Server.PostDeployHook != nil {
			fmt.Fprintf(w, "  部署后置钩子：健康检查通过后执行 %s\n", *cfg.Server.PostDeployHook)
		}
	}
	fmt.Fprintln(w, "  依赖输入：")
	for _, in := range inputs {
		dig := in.LockDigest
		if len(dig) > 12 {
			dig = dig[:12]
		}
		fmt.Fprintf(w, "    %-6s %s  %s\n", in.Ecosystem, dig, in.Location)
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
