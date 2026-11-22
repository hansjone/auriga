package main

// auriga adopt（设计 D10 第 2e 步）：接管已用 deploy-nas 脚本部署的站点，建立 state 与
// 不可变发行记录，不重启容器、不改数据目录、不改 stack.env，不伪造信任。
//
// 只读性：除 auriga 自己的 <数据目录>.oneclick/（state）、<父目录>/auriga-releases/<序号>/、
// :r<序号> 镜像标签与日志收据外，不写任何文件；access 配置直接引用 stack.env 的
// AURIGA_ACCESS_FILE（不复制，否则编排摘要与现场实际使用的 access 会分叉）。
// 写入顺序：发行目录（暂存 + rename）→ 标签 → state（原子写，最后一步），任何一步失败
// 都撤掉本次已写的发行目录与 .oneclick，不留半成品 state。

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

const (
	recordKindAdopt = "adopt"
	// nasComposeProject 与 deploy/nas/scripts/lib.sh 的 AURIGA_NAS_PROJECT 固定值一致（lib.sh
	// 明确忽略 stack.env 里的 COMPOSE_PROJECT_NAME，见其 M1 注释），因此不从 stack.env 推导。
	nasComposeProject = "auriga-nas-test"
)

type adoptOptions struct {
	StackEnv  string
	DeployNas string
	DryRun    bool
	JSON      bool
	Config    string
}

func parseAdopt(args []string) (adoptOptions, error) {
	var o adoptOptions
	f := flags("adopt")
	f.StringVar(&o.StackEnv, "stack-env", "", "")
	f.StringVar(&o.DeployNas, "deploy-nas", "", "")
	f.BoolVar(&o.DryRun, "dry-run", false, "")
	f.BoolVar(&o.JSON, "json", false, "")
	f.StringVar(&o.Config, "config", "", "")
	f.Bool("operator", false, "") // 由 sitePrivilege 先行剥离；这里登记只为帮助与解析一致
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("多余 adopt 参数")
	}
	if o.StackEnv == "" {
		return o, errors.New("adopt 需要 --stack-env <路径>")
	}
	if o.DeployNas == "" {
		return o, errors.New("adopt 需要 --deploy-nas <目录>")
	}
	return o, nil
}

// adoptCommand：提权沿用 2a（D12）。sudoers 不放行 adopt，运行器在 guardSudoCommand 已被拒绝；
// 其他经 sudo 的调用默认受限，adopt 没有白名单参数故一律拒绝，管理员显式 --operator（要求
// site.json 已登记 server.runner 且调用者不是它）按人工 root 处理；root 直接运行不受限。
func adoptCommand(args []string, inv siteInvocation) error {
	rest, restricted, err := sitePrivilege("adopt", args, inv)
	if err != nil {
		return fmt.Errorf("%w（adopt 不在工作流 sudo 白名单：请以 root 直接运行，或由非运行器管理员经 sudo 加 --operator）", err)
	}
	if restricted {
		return errors.New("adopt 不接受经 sudo 的受限调用；请以 root 直接运行，或由非运行器管理员经 sudo 加 --operator")
	}
	o, err := parseAdopt(rest)
	if err != nil {
		return err
	}
	cfg, cfgPath, err := loadAdoptConfig(o.Config, inv)
	if err != nil {
		return err
	}
	stackEnv, err := absNoSymlink(o.StackEnv)
	if err != nil {
		return fmt.Errorf("--stack-env：%w", err)
	}
	deployNas, err := absNoSymlink(o.DeployNas)
	if err != nil {
		return fmt.Errorf("--deploy-nas：%w", err)
	}
	env, err := parseStackEnvFile(stackEnv)
	if err != nil {
		return err
	}
	dataRaw := strings.TrimSpace(env["AURIGA_DATA_ROOT"])
	if dataRaw == "" {
		return errors.New("stack.env 缺少 AURIGA_DATA_ROOT")
	}
	data, err := dataPath(dataRaw)
	if err != nil {
		return fmt.Errorf("stack.env 的 AURIGA_DATA_ROOT：%w", err)
	}
	if filepath.Clean(stackEnv) != filepath.Clean(filepath.Join(data, "stack.env")) {
		return fmt.Errorf("--stack-env 必须是数据目录下的 stack.env（期望 %s）", filepath.Join(data, "stack.env"))
	}
	run, err := startRun(cfg, cfgPath, "adopt", args, o.JSON)
	if err != nil {
		return err
	}
	run.receipt.Notes = append(run.receipt.Notes, invocationNote(inv, false))
	ctx, cancel := depsSignalContext()
	defer cancel()
	return run.finish(adoptLocked(ctx, data, deployNas, env, o.DryRun, run))
}

// adoptLocked：在统一 flock 站点锁内执行接管（与 install/upgrade/backup/deploy/rollback 同一把锁）。
func adoptLocked(ctx context.Context, data, deployNas string, env map[string]string, dryRun bool, run *siteRun) error {
	return withLock(data, "adopt", func() error {
		return doAdopt(ctx, data, deployNas, env, dryRun, run)
	})
}

// loadAdoptConfig：有 --config 或默认 site.json 时加载（供日志目录）；没有默认配置时用空配置
// （日志落到默认日志目录）。
func loadAdoptConfig(flagPath string, inv siteInvocation) (*siteConfig, string, error) {
	path := defaultSiteConfigPath()
	if flagPath != "" {
		return loadSiteConfig(flagPath, inv)
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return &siteConfig{}, path, nil
	}
	return loadSiteConfig("", inv)
}

func absNoSymlink(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if err := noSymlinks(abs); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("拒绝符号链接 %s", abs)
	}
	return abs, nil
}

func parseStackEnvFile(path string) (map[string]string, error) {
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("stack.env 行无效：%q", line)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("stack.env 重复键 %s", key)
		}
		out[key] = value
	}
	return out, nil
}

// adoptImageKeys：参与接管核对的镜像键（装配镜像 + postgres/nats/registry；可选 observability
// 的 LOKI/ALLOY 只在 stack.env 声明时核对运行容器，不进 state）。
func adoptImageKeys() []string {
	var keys []string
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		keys = append(keys, imageKey(svc))
	}
	sort.Strings(keys)
	return keys
}

type adoptProbe struct {
	Data         string
	InstallMode  string
	Access       string
	Images       map[string]string // 键 → 本机镜像 ID（sha256:…）
	Declared     map[string]string // 键 → stack.env 原文
	Commit       string
	CommitKnown  bool
	Formats      map[string]string // nil 表示读取失败（未知）
	Migrations   map[string]string // nil 表示读取失败（未知）
	FormatNote   string
	MigrateNote  string
	UnknownNotes []string
}

func doAdopt(ctx context.Context, data, deployNas string, env map[string]string, dryRun bool, run *siteRun) error {
	rc, w := run.receipt, run.out
	rc.Phase = "build"
	rc.DryRun = dryRun

	if _, err := readState(data); err == nil {
		return exitWith(1, errors.New("数据目录已有一键安装状态（.oneclick），拒绝 adopt；若要重新接管请先由人工确认后移走状态目录"))
	} else if _, statErr := os.Lstat(data + ".oneclick"); statErr == nil {
		return exitWith(1, fmt.Errorf("已存在 %s 但 state 不可读，拒绝 adopt：%w", data+".oneclick", err))
	}

	info, err := os.Lstat(data)
	if err != nil {
		return exitWith(1, fmt.Errorf("数据目录不存在或不可读：%w", err))
	}
	if !info.IsDir() {
		return exitWith(1, errors.New("AURIGA_DATA_ROOT 不是目录"))
	}
	if _, err := os.ReadDir(data); err != nil {
		return exitWith(1, fmt.Errorf("数据目录不可读：%w", err))
	}
	nasInfo, err := os.Lstat(deployNas)
	if err != nil {
		return exitWith(1, fmt.Errorf("--deploy-nas 必须是可读目录：%w", err))
	}
	if !nasInfo.IsDir() {
		return exitWith(1, fmt.Errorf("--deploy-nas 必须是目录：%s", deployNas))
	}
	if _, err := os.Lstat(filepath.Join(deployNas, "scripts")); err != nil {
		return exitWith(1, errors.New("--deploy-nas 缺少 scripts/，不像 deploy-nas 发行目录"))
	}

	probe, err := probeAdoptSite(ctx, data, deployNas, env)
	if err != nil {
		return err
	}
	rc.Source = &deploySource{Mode: "adopted", Commit: probe.Commit}
	rc.Notes = append(rc.Notes, probe.UnknownNotes...)
	if probe.FormatNote != "" {
		rc.Notes = append(rc.Notes, probe.FormatNote)
	}
	if probe.MigrateNote != "" {
		rc.Notes = append(rc.Notes, probe.MigrateNote)
	}
	orch, err := orchestrationDigest(deployNas, data, probe.Access)
	if err != nil {
		return exitWith(1, fmt.Errorf("计算编排摘要失败：%w", err))
	}
	rc.Orchestration = orch

	seq := nextSeq(state{Data: data})
	fmt.Fprintf(w, "接管核查通过：数据目录 %s，安装模式 %s，将占用发行序号 r%d\n", data, probe.InstallMode, seq)
	if probe.CommitKnown {
		fmt.Fprintf(w, "提交号 %s（来自与运行镜像一致的发行标记）\n", probe.Commit)
	} else {
		fmt.Fprintln(w, "提交号未知（没有与运行镜像一致的 .auriga-release 发行标记）")
	}
	fmt.Fprintln(w, "发行钥将标记为未钉定；第一次签名包 upgrade 前须先 trust add-*")

	for _, key := range adoptImageKeys() {
		id := probe.Images[key]
		if id == "" {
			continue
		}
		img := receiptImage{Service: strings.ToLower(key), Action: "adopted", Reason: "现场运行镜像与 stack.env 一致", NewID: id, OldID: id}
		if containsString(deployServices, strings.ToLower(key)) {
			img.Tag = imageTag(strings.ToLower(key), seq)
		}
		rc.Images = append(rc.Images, img)
	}
	before := &dataVersions{Migrations: probe.Migrations, Formats: probe.Formats}
	if probe.Migrations == nil || probe.Formats == nil {
		before.Err = "接管时未能读全迁移或格式版本（见 notes）"
	}
	rc.Data.Before = before
	rc.Data.After = before
	rc.Notes = append(rc.Notes,
		"发行钥未钉定（adopt 不伪造信任）：第一次签名包 upgrade 前须先 trust add-release-key 或 add-site-key",
		fmt.Sprintf("安装模式 %s；数据目录 %s；access 配置 %s（直接引用，未复制）", probe.InstallMode, data, probe.Access))
	if dryRun {
		return printAdoptPlan(w, data, deployNas, probe, seq, orch, rc)
	}

	src := deploySource{Mode: "adopted"}
	if probe.CommitKnown {
		src.Commit = probe.Commit
	}
	manifest := releaseManifest{
		Source: src, Images: cloneImages(probe.Images), SchemaCompatFloor: nil, FormatMax: nil, CreatedAt: nowRFC3339(),
	}
	release, releaseSums, relOrch, err := writeAdoptReleaseDir(ctx, data, seq, deployNas, data, probe.Access, manifest)
	if err != nil {
		return exitWith(1, err)
	}
	if relOrch != orch {
		_ = os.RemoveAll(release)
		return exitWith(1, fmt.Errorf("复制后的编排摘要 %s 与现场 %s 不一致（deploy-nas 在接管过程中被改动？），未建立 state", relOrch, orch))
	}
	changed := false
	rc.OrchestrationChanged = &changed
	if err := tagImages(ctx, probe.Images, func(svc string) string { return imageTag(svc, seq) }); err != nil {
		_ = os.RemoveAll(release)
		return exitWith(1, fmt.Errorf("打 :r%d 镜像标签失败：%w", seq, err))
	}

	// 迁移/格式任一未知时不记迁移基线（nil）：rollbackDecision 只在目标记录有迁移基线时才认定
	// “数据未触碰”，未知不能当作“空”。
	recMig, recFmt := probe.Migrations, probe.Formats
	if recMig == nil || recFmt == nil {
		recMig = nil
	}
	rec := deployRecord{
		Seq: seq, Kind: recordKindAdopt, Status: recordOK, At: nowRFC3339(), Source: src,
		Release: release, ReleaseSums: releaseSums, Images: cloneImages(probe.Images),
		Orchestration: orch, Migrations: recMig, Formats: recFmt,
	}
	s := state{
		Data: data, Package: release, Access: probe.Access, Images: cloneImages(probe.Images),
		Source: sourceAdopted, TrustUnpinned: true, Current: seq, History: []deployRecord{rec},
	}
	meta := data + ".oneclick"
	if err := os.Mkdir(meta, 0755); err != nil {
		_ = os.RemoveAll(release)
		return exitWith(1, err)
	}
	if err := saveState(s); err != nil {
		_ = os.RemoveAll(meta)
		_ = os.RemoveAll(release)
		return exitWith(1, err)
	}
	rc.Release = &receiptRelease{Seq: seq, Dir: release}
	rc.Phase = "switch"
	rc.SwitchMode = "adopt（不重启容器）"
	rc.Health = "未探测（adopt 不重启、不改运行中服务）"
	rc.MixedVersions = "无"
	fmt.Fprintf(w, "已建立 state：来源 adopted，当前 r%d，发行目录 %s\n", seq, release)
	fmt.Fprintf(w, "编排摘要 %s\n", orch)
	fmt.Fprintln(w, "未钉定发行钥：之后第一次签名包 upgrade 前请先 trust add-release-key 或 add-site-key")
	fmt.Fprintln(w, "下一步：auriga deploy <提交号>（第一次 deploy 一律完整启动）")
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func printAdoptPlan(w io.Writer, data, deployNas string, probe *adoptProbe, seq int, orch string, rc *siteReceipt) error {
	fmt.Fprintln(w, "接管计划（--dry-run：不写 state、发行目录或镜像标签）")
	fmt.Fprintf(w, "  数据目录：%s\n", data)
	fmt.Fprintf(w, "  安装模式：%s\n", probe.InstallMode)
	fmt.Fprintf(w, "  access：%s（state 直接引用）\n", probe.Access)
	fmt.Fprintf(w, "  deploy-nas：%s → %s\n", deployNas, filepath.Join(releaseDir(data, seq), "deploy-nas"))
	fmt.Fprintf(w, "  序号：r%d（接续已有 auriga-releases 的下一个；空则从 1）\n", seq)
	fmt.Fprintf(w, "  编排摘要：%s\n", orch)
	fmt.Fprintln(w, "  state.source=adopted，trustUnpinned=true，历史一条 kind=adopt（无 schemaCompatFloor/formatMax）")
	if probe.CommitKnown {
		fmt.Fprintf(w, "  提交号：%s\n", probe.Commit)
	} else {
		fmt.Fprintln(w, "  提交号：未知")
	}
	fmt.Fprintf(w, "  镜像（装配镜像将打 :r%d 标签）：\n", seq)
	for _, key := range adoptImageKeys() {
		if id := probe.Images[key]; id != "" {
			fmt.Fprintf(w, "    %s  %s  （stack.env %s）\n", key, id, probe.Declared[key])
		}
	}
	for _, n := range probe.UnknownNotes {
		fmt.Fprintf(w, "  %s\n", n)
	}
	fmt.Fprintln(w, "  不重启容器、不改 stack.env、不改数据目录")
	rc.SwitchMode = "adopt dry-run"
	rc.Notes = append(rc.Notes, "dry-run：未写入 state、发行目录或镜像标签")
	return nil
}

// adoptReadMigrations 可注入（单测避免真连 postgres）。
var adoptReadMigrations = readAdoptMigrations

func probeAdoptSite(ctx context.Context, data, deployNas string, env map[string]string) (*adoptProbe, error) {
	p := &adoptProbe{
		Data: data, Images: map[string]string{}, Declared: map[string]string{},
		InstallMode: strings.TrimSpace(env["AURIGA_INSTALL_MODE"]),
	}
	if p.InstallMode == "" {
		p.InstallMode = "host"
	}
	if p.InstallMode != "host" && p.InstallMode != "container" {
		return nil, exitWith(1, fmt.Errorf("stack.env 的 AURIGA_INSTALL_MODE=%s 无效（只能是 host 或 container）", p.InstallMode))
	}
	// lib.sh load_stack_env 要求 AURIGA_ACCESS_FILE 为绝对路径；没有它现场根本起不来。
	access := strings.TrimSpace(env["AURIGA_ACCESS_FILE"])
	if access == "" || !filepath.IsAbs(access) {
		return nil, exitWith(1, errors.New("stack.env 缺少绝对路径的 AURIGA_ACCESS_FILE（deploy-nas 脚本要求），不像可运行的现场"))
	}
	abs, err := absNoSymlink(access)
	if err != nil {
		return nil, exitWith(1, fmt.Errorf("AURIGA_ACCESS_FILE：%w", err))
	}
	if info, err := os.Stat(abs); err != nil || !info.Mode().IsRegular() {
		return nil, exitWith(1, fmt.Errorf("AURIGA_ACCESS_FILE %s 不是普通文件", abs))
	}
	p.Access = abs

	for _, key := range adoptImageKeys() {
		raw := strings.TrimSpace(env["AURIGA_IMAGE_"+key])
		if raw == "" {
			return nil, exitWith(1, fmt.Errorf("stack.env 缺少 AURIGA_IMAGE_%s", key))
		}
		p.Declared[key] = raw
		id, _, ok := localImage(ctx, raw)
		if !ok {
			return nil, exitWith(1, fmt.Errorf("stack.env 声明的 AURIGA_IMAGE_%s=%s 在本机 Docker 中不存在", key, raw))
		}
		p.Images[key] = id
	}
	// 之后的 deploy 会把 AURIGA_INSTALLER_IMAGE 写成 postgres 镜像（updateStackEnv），现场若不同
	// 则 deploy 会悄悄改变语义，交人工确认。
	installer := ""
	if inst := strings.TrimSpace(env["AURIGA_INSTALLER_IMAGE"]); inst != "" {
		id, _, ok := localImage(ctx, inst)
		if !ok {
			return nil, exitWith(1, fmt.Errorf("stack.env 的 AURIGA_INSTALLER_IMAGE=%s 在本机不存在", inst))
		}
		if id != p.Images["POSTGRES"] {
			return nil, exitWith(3, fmt.Errorf("AURIGA_INSTALLER_IMAGE（%s）与 AURIGA_IMAGE_POSTGRES（%s）不一致：之后的 deploy 会把它改成 postgres 镜像，交人工确认", id, p.Images["POSTGRES"]))
		}
		installer = id
	}
	optional := map[string]string{}
	for _, key := range []string{"LOKI", "ALLOY"} {
		if raw := strings.TrimSpace(env["AURIGA_IMAGE_"+key]); raw != "" {
			id, _, ok := localImage(ctx, raw)
			if !ok {
				return nil, exitWith(1, fmt.Errorf("stack.env 声明的 AURIGA_IMAGE_%s=%s 在本机 Docker 中不存在", key, raw))
			}
			optional[key] = id
		}
	}

	diffs, notes, err := verifyRunningImages(ctx, p.Images, optional, installer)
	if err != nil {
		return nil, exitWith(1, err)
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		return nil, exitWith(3, fmt.Errorf("运行中容器镜像与 stack.env 不一致，交人工（adopt 未写入任何内容）：\n  - %s", strings.Join(diffs, "\n  - ")))
	}
	p.UnknownNotes = append(p.UnknownNotes, notes...)

	formats, ferr := readFormats(data)
	if ferr != nil {
		p.FormatNote = "format-versions.json 读取失败（如实记录，不伪造）：" + ferr.Error()
		p.UnknownNotes = append(p.UnknownNotes, "未知项：数据目录格式版本")
	} else {
		p.Formats = formats
		if len(formats) == 0 {
			p.FormatNote = "数据目录没有 format-versions.json（或为空），按空表记录"
		}
	}
	mig, merr := adoptReadMigrations(data, deployNas)
	if merr != nil {
		p.MigrateNote = "迁移版本读取失败（如实记录，不伪造）：" + merr.Error()
		p.UnknownNotes = append(p.UnknownNotes, "未知项：数据库迁移版本（之后回滚到本记录无法按“数据未触碰”放行）")
	} else {
		p.Migrations = mig
		if len(mig) == 0 {
			p.MigrateNote = "库里没有迁移表，按空表记录"
		}
	}

	commit, known, note := adoptCommit(deployNas, p.Images)
	p.Commit, p.CommitKnown = commit, known
	if !known {
		p.Commit = "未知"
		p.UnknownNotes = append(p.UnknownNotes, "未知项：源码提交号"+note)
	}
	return p, nil
}

// verifyRunningImages：按镜像→服务表（主 compose + collab overlay）核对固定 compose 项目里
// 每个运行中容器的镜像 ID 与 stack.env 声明的是否一致。同一服务有多个容器时逐个核对。
// operator 不是 compose 服务，只要求本机有该镜像（在调用前已核对）。
// 返回差异（退出码 3）与写进收据的说明（不在表里的服务等）。
func verifyRunningImages(ctx context.Context, want, optional map[string]string, installer string) ([]string, []string, error) {
	out, err := deployCapture(ctx, "docker", "ps", "--no-trunc", "--filter", "label=com.docker.compose.project="+nasComposeProject, "--format", "{{.ID}} {{.Label \"com.docker.compose.service\"}}")
	if err != nil {
		return nil, nil, fmt.Errorf("列出运行中容器失败：%w", err)
	}
	running := map[string][]string{} // service → container IDs
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, svc, ok := strings.Cut(line, " ")
		svc = strings.TrimSpace(svc)
		if !ok || svc == "" {
			continue
		}
		running[svc] = append(running[svc], id)
	}
	// 服务 → (stack.env 变量名, 期望镜像 ID)。
	type expect struct{ env, id string }
	expected := map[string]expect{}
	for key, svcs := range imageComposeServices {
		id := want[key]
		if id == "" {
			id = optional[key]
		}
		for _, svc := range svcs {
			expected[svc] = expect{"AURIGA_IMAGE_" + key, id}
		}
	}
	for svc, env := range collabOverlayImageServices {
		var id string
		switch env {
		case "AURIGA_IMAGE_PLATFORM":
			id = want["PLATFORM"]
		case "AURIGA_IMAGE_POSTGRES":
			id = want["POSTGRES"]
		case "AURIGA_INSTALLER_IMAGE":
			// collab-compose.yml：${AURIGA_INSTALLER_IMAGE:-${AURIGA_IMAGE_PLATFORM}}
			id = installer
			if id == "" {
				id, env = want["PLATFORM"], "AURIGA_IMAGE_PLATFORM（AURIGA_INSTALLER_IMAGE 未设置时的回退）"
			}
		}
		expected[svc] = expect{env, id}
	}

	var diffs, notes []string
	services := make([]string, 0, len(running))
	for svc := range running {
		services = append(services, svc)
	}
	sort.Strings(services)
	for _, svc := range services {
		e, ok := expected[svc]
		if !ok {
			notes = append(notes, fmt.Sprintf("未知项：项目 %s 里有不在镜像→服务表中的运行服务 %s（未核对、adopt 不触碰）", nasComposeProject, svc))
			continue
		}
		if e.id == "" {
			notes = append(notes, fmt.Sprintf("未知项：服务 %s 在运行，但 stack.env 未声明 %s（使用 compose 默认镜像，未核对）", svc, e.env))
			continue
		}
		for _, cid := range running[svc] {
			got, err := deployCapture(ctx, "docker", "inspect", "--format", "{{.Image}}", cid)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect 容器 %s（%s）失败：%w", svc, cid, err)
			}
			if got = strings.TrimSpace(got); got != e.id {
				diffs = append(diffs, fmt.Sprintf("服务 %s（容器 %s）运行镜像 %s，stack.env %s 为 %s", svc, shortID(cid), got, e.env, e.id))
			}
		}
	}
	// 主 compose 里无 profile 的常驻服务必须在跑：否则现场不是 deploy-nas 正常运行状态。
	for _, svc := range adoptRequiredServices {
		if len(running[svc]) == 0 {
			diffs = append(diffs, fmt.Sprintf("必需服务 %s 未在项目 %s 中运行", svc, nasComposeProject))
		}
	}
	return diffs, notes, nil
}

// adoptRequiredServices：docker-compose.yml 里没有 profile、启动后常驻的服务（一次性的
// panel-prepare/audit-db-prepare 与 collab-network profile 的 network/network-broker 不在内）。
var adoptRequiredServices = []string{"postgres", "nats", "registry", "platform", "edge", "agent", "bifrost", "ai-relay", "egress", "panel-renew"}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func readAdoptMigrations(data, deployNas string) (map[string]string, error) {
	// compose 走 <Package>/deploy-nas/scripts/oneclick-compose.sh（exec psql 只读查询）。
	if filepath.Base(deployNas) != "deploy-nas" {
		return nil, errors.New("deploy-nas 目录名不是 deploy-nas，无法用现有脚本读迁移")
	}
	s := state{Data: data, Package: filepath.Dir(deployNas)}
	return readMigrations(s)
}

// adoptCommit：从 load-images.sh 写的发行标记（deploy-nas/.auriga-release，或默认位置即
// bundle 根的 .auriga-release）取提交号；只有标记里五个装配镜像 ID 与核实过的运行镜像全部
// 一致时才采信——deploy.mjs 之类的流程会只换 stack.env 镜像而保留旧 bundle，此时标记的提交
// 并不是运行镜像的来源，记成它会让之后的快进与 floor 判定用错提交。provenance.json 没有镜像
// 身份，无法对应运行镜像，不采信。
func adoptCommit(deployNas string, images map[string]string) (string, bool, string) {
	type markerImage struct {
		ID string `json:"id"`
	}
	var marker struct {
		Commit string                 `json:"commit"`
		Images map[string]markerImage `json:"images"`
	}
	note := "（没有 .auriga-release 发行标记）"
	for _, path := range []string{filepath.Join(deployNas, ".auriga-release"), filepath.Join(filepath.Dir(deployNas), ".auriga-release")} {
		marker.Commit, marker.Images = "", nil
		if err := readJSON(path, &marker); err != nil {
			continue
		}
		if !commitPattern.MatchString(marker.Commit) {
			note = fmt.Sprintf("（%s 没有 40 位提交号）", path)
			continue
		}
		match := true
		for _, svc := range deployServices {
			if marker.Images[svc].ID == "" || marker.Images[svc].ID != images[imageKey(svc)] {
				match = false
				break
			}
		}
		if !match {
			note = fmt.Sprintf("（%s 声称提交 %s，但其镜像 ID 与运行镜像不一致，不采信）", path, marker.Commit)
			continue
		}
		return marker.Commit, true, ""
	}
	return "", false, note
}

// writeAdoptReleaseDir 把现场 deploy-nas 复制进不可变发行目录，计算编排摘要，写标记与清单。
func writeAdoptReleaseDir(ctx context.Context, data string, seq int, deployNas, siteData, access string, m releaseManifest) (string, string, string, error) {
	root := releasesRoot(data)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", "", "", err
	}
	if err := depsTrustedSourceCheck("发行根目录 ", root); err != nil {
		return "", "", "", err
	}
	final := releaseDir(data, seq)
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		return "", "", "", fmt.Errorf("发行目录 %s 已存在，拒绝覆盖", final)
	}
	tmp, err := os.MkdirTemp(root, ".staging-"+fmt.Sprint(seq)+"-")
	if err != nil {
		return "", "", "", err
	}
	fail := func(err error) (string, string, string, error) {
		_ = os.RemoveAll(tmp)
		return "", "", "", err
	}
	if err := os.Chmod(tmp, 0755); err != nil {
		return fail(err)
	}
	if err := copyTreeContext(ctx, deployNas, filepath.Join(tmp, "deploy-nas")); err != nil {
		return fail(err)
	}
	for _, name := range []string{"auriga-nas-bootstrap", "runtime-release"} {
		src := filepath.Join(tmp, "deploy-nas", "bin", name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(tmp, "bin", name)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fail(err)
		}
		if err := copyFileContext(ctx, src, dst, 0755); err != nil {
			return fail(err)
		}
	}
	orch, err := orchestrationDigest(filepath.Join(tmp, "deploy-nas"), siteData, access)
	if err != nil {
		return fail(fmt.Errorf("计算编排摘要失败：%w", err))
	}
	m.Schema, m.Seq, m.Orchestration = 1, seq, orch
	if err := jsonFile(filepath.Join(tmp, "release.json"), m, 0644); err != nil {
		return fail(err)
	}
	if err := writeReleaseMarkers(tmp, m); err != nil {
		return fail(err)
	}
	if err := writeSumsContext(ctx, tmp); err != nil {
		return fail(err)
	}
	sums, err := digest(filepath.Join(tmp, "SHA256SUMS"))
	if err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fail(err)
	}
	return final, sums, orch, nil
}
