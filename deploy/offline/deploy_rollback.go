package main

// 回滚（设计 D8、D2，遗留 P2）。
//
// 迁移版本：主 postgres 实例里各库各 schema 的 flyway_schema_history 与
// auriga_common_schema_history（collab-db、audit 在同一实例里，各库逐个读取），记录每张表的
// 行数、最大 installed_rank 与全部行的摘要。数据目录格式版本：<数据目录>/format-versions.json。
//
// 自动回滚、手动 rollback、deploy --allow-downgrade 用同一判定（rollbackDecision）：数据与目标
// 运行时记录相同（未触碰）即允许，作为长期规则（P2）；否则检查（1）当前运行版本以及目标之后
// 运行过的每个更新发行的 schemaCompatFloor——目标提交须不早于它们；（2）目标的 formatMax——
// 当前数据目录格式不得超过。floor 在「较新、留下数据形态」的发行上；formatMax 在回滚目标
// （旧代码能读的上限）上。缺声明、读不到或祖先判定失败一律退出码 3 交人工。
//
// 历史里的 deploy、upgrade、install（签名包基线）记录都可以作为 rollback 目标，检查相同；切到
// 某条记录后 state.source 随之变为该记录的来源（deploy → local-deploy，其余 → signed-package）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// dataVersions 是一次读取的迁移版本与格式版本；读不到时 Err 非空。
type dataVersions struct {
	Migrations map[string]string `json:"migrations,omitempty"`
	Formats    map[string]string `json:"formats,omitempty"`
	Err        string            `json:"error,omitempty"`
}

func (v *dataVersions) ok() bool { return v != nil && v.Err == "" }

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// dataUntouched：切换前后都读到了版本且完全相同。
func dataUntouched(before, after *dataVersions) bool {
	return before.ok() && after.ok() && sameMap(before.Migrations, after.Migrations) && sameMap(before.Formats, after.Formats)
}

const migrationTablesSQL = `SELECT format('%I.%I', n.nspname, c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','p') AND c.relname IN ('flyway_schema_history','auriga_common_schema_history') ORDER BY 1`

func migrationRowsSQL(table string) string {
	return "SELECT count(*) || ':' || coalesce(max(installed_rank), 0) || ':' || coalesce(md5(string_agg(installed_rank || '|' || coalesce(version, '') || '|' || coalesce(checksum::text, '') || '|' || success::text, ',' ORDER BY installed_rank)), '') FROM " + table
}

// psqlQuery 在 postgres 容器里以 auriga_bootstrap 执行只读查询（可注入）。
var psqlQuery = func(s state, db, sql string) (string, error) {
	var out bytes.Buffer
	script := `PGPASSWORD="$(cat /run/secrets/pg_bootstrap_password 2>/dev/null)" exec psql -X -At -v ON_ERROR_STOP=1 -U auriga_bootstrap -d "$1" -c "$2"`
	err := compose(s, &out, "exec", "-T", "postgres", "sh", "-c", script, "psql", db, sql)
	return strings.TrimSpace(out.String()), err
}

func readMigrations(s state) (map[string]string, error) {
	dbs, err := psqlQuery(s, "auriga", "SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY 1")
	if err != nil {
		return nil, fmt.Errorf("读取数据库列表失败：%w", err)
	}
	out := map[string]string{}
	for _, db := range strings.Fields(dbs) {
		tables, err := psqlQuery(s, db, migrationTablesSQL)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 的迁移表失败：%w", db, err)
		}
		for _, table := range strings.Fields(tables) {
			v, err := psqlQuery(s, db, migrationRowsSQL(table))
			if err != nil {
				return nil, fmt.Errorf("读取 %s %s 失败：%w", db, table, err)
			}
			out[db+"/"+table] = v
		}
	}
	return out, nil
}

// readFormats 读取数据目录的 format-versions.json（没有该文件视为空表）。
func readFormats(data string) (map[string]string, error) {
	path := filepath.Join(data, "format-versions.json")
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("format-versions.json 无效：%w", err)
	}
	out := map[string]string{}
	for k, v := range m {
		var b bytes.Buffer
		if err := json.Compact(&b, v); err != nil {
			return nil, err
		}
		out[k] = b.String()
	}
	return out, nil
}

// readDataVersions 是可注入点（测试替换）。
var readDataVersions = func(s state) *dataVersions {
	v := &dataVersions{}
	m, err := readMigrations(s)
	if err != nil {
		v.Err = err.Error()
		return v
	}
	f, err := readFormats(s.Data)
	if err != nil {
		v.Err = err.Error()
		return v
	}
	v.Migrations, v.Formats = m, f
	return v
}

// rollbackDecision 判断能否回到 target。
//
//   - now：当前数据版本
//   - current：当前运行发行；newer：current 以外、在 target 之后运行过（可能改过数据）的历史
//   - atOrBeforeTarget(c)：提交 c 是否为目标提交本身或其祖先（git 祖先判定由调用方注入，
//     本函数保持纯逻辑）；为 nil 时一律按“无法判定”处理。
//
// 数据与目标运行时记录相同（未触碰）直接允许（P2）。否则目标必须声明 formatMax 且当前格式不超过；
// current 与 newer 里每一条比目标新的记录（提交不是目标本身或祖先）都必须声明兼容元数据
// （FormatMax 非空；schemaCompatFloor 空串表示设计合入起点），且目标不早于它的 floor。只看
// current 不够：降级到 floor 较旧的提交后，之前破坏性迁移留下的库结构仍在。
func rollbackDecision(target, current *deployRecord, newer []*deployRecord, now *dataVersions, atOrBeforeTarget func(commit string) (bool, error)) (bool, string) {
	if !now.ok() {
		return false, "读不到当前迁移或格式版本，无法确认数据状态：" + now.Err
	}
	if target == nil {
		return false, "没有回滚目标，需人工处理"
	}
	if target.Migrations != nil && sameMap(target.Migrations, now.Migrations) && sameMap(target.Formats, now.Formats) {
		return true, fmt.Sprintf("当前迁移与格式版本与 r%d 运行时记录相同，数据未被之后的版本改动", target.Seq)
	}
	if current == nil {
		return false, fmt.Sprintf("数据已在 r%d 之后被改动，且找不到当前运行版本的记录（没有 schemaCompatFloor 可对照），拒绝回滚，需人工处理", target.Seq)
	}
	if target.FormatMax == nil {
		return false, fmt.Sprintf("数据已被改动，且目标 r%d 没有声明兼容元数据（schemaCompatFloor 与格式上限 formatMax），拒绝回滚，需人工处理", target.Seq)
	}
	for k, v := range now.Formats {
		max, declared := target.FormatMax[k]
		if !declared {
			return false, fmt.Sprintf("数据目录格式 %s 不在 r%d 的格式上限声明里", k, target.Seq)
		}
		if !formatVersionAtMost(v, max) {
			return false, fmt.Sprintf("数据目录格式 %s=%s 高于 r%d 能读的上限 %s", k, v, target.Seq, max)
		}
	}
	checked := map[int]bool{target.Seq: true}
	var floors []string
	for _, r := range append([]*deployRecord{current}, newer...) {
		if r == nil || checked[r.Seq] {
			continue
		}
		checked[r.Seq] = true
		if c := comparableCommit(r); c != "" && atOrBeforeTarget != nil {
			if ok, err := atOrBeforeTarget(c); err == nil && ok {
				continue // 不比目标新：它的迁移目标代码都认识
			}
		}
		who := fmt.Sprintf("r%d", r.Seq)
		if r == current {
			who = "当前运行版本 " + who
		}
		if r.FormatMax == nil {
			return false, fmt.Sprintf("数据已在目标发行之后被改动，且%s 没有声明 schemaCompatFloor（兼容元数据），拒绝回滚到 r%d，需人工处理", who, target.Seq)
		}
		floor := r.SchemaCompatFloor
		if floor == "" {
			continue
		}
		if atOrBeforeTarget == nil {
			return false, "缺少 schemaCompatFloor 祖先判定，拒绝回滚，需人工处理"
		}
		ok, err := atOrBeforeTarget(floor)
		if err != nil {
			return false, fmt.Sprintf("判定目标是否不早于 %s 的 schemaCompatFloor %s 失败：%v；拒绝回滚，需人工处理", who, floor, err)
		}
		if !ok {
			return false, fmt.Sprintf("目标 r%d 早于%s 的 schemaCompatFloor %s，拒绝回滚，需人工处理", target.Seq, who, floor)
		}
		floors = append(floors, who+"="+floor)
	}
	if len(floors) == 0 {
		return true, fmt.Sprintf("格式上限通过；比目标新的发行 schemaCompatFloor 均为空（设计合入起点）或不比目标新，允许回滚到 r%d", target.Seq)
	}
	return true, fmt.Sprintf("格式上限通过，且目标不早于 schemaCompatFloor（%s），允许回滚到 r%d", strings.Join(floors, "、"), target.Seq)
}

// newerRecords：历史里序号大于 target、且不是 current 的记录（不论成败——失败的部署也可能已迁移）。
func newerRecords(s *state, target *deployRecord) []*deployRecord {
	var out []*deployRecord
	for i := range s.History {
		r := &s.History[i]
		if target != nil && r.Seq > target.Seq && r.Seq != s.Current {
			out = append(out, r)
		}
	}
	return out
}

// targetAncestorCheck 返回「提交 c 是否为目标提交本身或其祖先」的回调。
func targetAncestorCheck(ctx context.Context, repo, targetCommit string, seq int) func(c string) (bool, error) {
	return func(c string) (bool, error) {
		if !commitPattern.MatchString(targetCommit) {
			return false, fmt.Errorf("目标 r%d 没有可比较的提交号，无法对照 schemaCompatFloor", seq)
		}
		if c == targetCommit {
			return true, nil
		}
		return gitIsAncestor(ctx, repo, c, targetCommit)
	}
}

// floorAncestorCheck：回到历史记录 target 时用的祖先判定。
func floorAncestorCheck(ctx context.Context, data string, target *deployRecord) func(c string) (bool, error) {
	return targetAncestorCheck(ctx, sourceRepo(data), comparableCommit(target), target.Seq)
}

// siteOps 是切换与回滚用到的站点动作（测试注入）。
type siteOps struct {
	nas    func(s state, op string, args ...string) error
	health func(s state, timeout time.Duration) error
	smoke  func(s state) (string, error)
	// stopWriters 停掉会写库或改写数据目录的容器并确认已停止，返回停掉的服务。
	stopWriters func(s state) ([]string, error)
	// compose 只换容器路径用；nil 时用包级 compose。
	compose func(s state, out *bytes.Buffer, args ...string) error
	// collabNetwork 只换容器且有 collab-source 时运行 start-collab-network.sh（离线维护模式）；nil 时用默认。
	collabNetwork func(s state) error
}

var defaultSiteOps = siteOps{nas: nas, health: waitHealthyFor, smoke: runDeploySmoke, stopWriters: stopDataWriters, compose: compose, collabNetwork: startCollabNetworkOffline}

// dataWriterServices：按镜像列出会写库或改写数据目录的 compose 服务（docker-compose.yml、
// collab-compose.yml）。postgres、collab-db、nats、registry 不在内：读迁移版本要用 postgres，
// 它们的镜像由配方固定 digest，不随源码变化。
var dataWriterServices = map[string][]string{
	"PLATFORM": {"platform", "collab", "collab-renew"},
	"EDGE":     {"edge", "panel-prepare", "panel-renew", "ai-relay", "network-broker", "network", "egress"},
	"AGENT":    {"agent"},
	"BIFROST":  {"bifrost"},
}

func dataWriterList() []string {
	keys := make([]string, 0, len(dataWriterServices))
	for k := range dataWriterServices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, dataWriterServices[k]...)
	}
	return out
}

// stopDataWriters：切换失败后、读取迁移与格式版本前，停掉新版本里会写数据的服务（含正在
// 反复重启的），再确认它们没有处于 running/restarting；任何一步失败都返回错误（调用方按
// “数据可能已被触碰”处理）。
// writerCompose 是 stopDataWriters 调用 compose 的可注入点。
var writerCompose = compose

func stopDataWriters(s state) ([]string, error) {
	compose := writerCompose
	var out bytes.Buffer
	if err := compose(s, &out, "ps", "-a", "--services"); err != nil {
		return nil, fmt.Errorf("列出服务失败：%w", err)
	}
	present := map[string]bool{}
	for _, svc := range strings.Fields(out.String()) {
		present[svc] = true
	}
	var targets []string
	for _, svc := range dataWriterList() {
		if present[svc] {
			targets = append(targets, svc)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	var discard bytes.Buffer
	if err := compose(s, &discard, append([]string{"stop", "--timeout", "60"}, targets...)...); err != nil {
		return nil, fmt.Errorf("停止 %s 失败：%w", strings.Join(targets, "、"), err)
	}
	out.Reset()
	if err := compose(s, &out, "ps", "--services", "--status", "running", "--status", "restarting"); err != nil {
		return nil, fmt.Errorf("确认停止状态失败：%w", err)
	}
	for _, svc := range strings.Fields(out.String()) {
		for _, t := range targets {
			if svc == t {
				return nil, fmt.Errorf("服务 %s 停止后仍在运行", svc)
			}
		}
	}
	return targets, nil
}

// runDeploySmoke：发行目录里有只读部署冒烟脚本 scripts/deploy-smoke.sh 时运行；没有就跳过
// （不用会默认报 PARTIAL 的 smoke.sh）。
func runDeploySmoke(s state) (string, error) {
	script := filepath.Join(s.Package, "deploy-nas", "scripts", "deploy-smoke.sh")
	if _, err := os.Lstat(script); os.IsNotExist(err) {
		return "跳过：源码没有只读部署冒烟脚本 scripts/deploy-smoke.sh", nil
	}
	if err := nas(s, "deploy-smoke"); err != nil {
		return "失败", err
	}
	return "通过", nil
}

// previousRecord 返回当前序号之前最近一条成功的历史。
func previousRecord(s *state) *deployRecord {
	var best *deployRecord
	for i := range s.History {
		r := &s.History[i]
		if r.Seq < s.Current && r.Status == recordOK && (best == nil || r.Seq > best.Seq) {
			best = r
		}
	}
	return best
}

func healthTimeout(cfg *siteConfig) time.Duration {
	if cfg != nil && cfg.Server != nil && cfg.Server.HealthTimeout != nil {
		if d, err := time.ParseDuration(*cfg.Server.HealthTimeout); err == nil {
			return d
		}
	}
	return 10 * time.Minute
}

// siteDataPath 从站点配置取数据目录（server.data，规则同 --data）。
func siteDataPath(cfg *siteConfig) (string, error) {
	if cfg.Server == nil {
		return "", errors.New("站点配置缺少 server 段（server.data）")
	}
	return dataPath(cfg.Server.Data)
}

func rollbackCommand(args []string, inv siteInvocation) error {
	rest, restricted, err := sitePrivilege("rollback", args, inv)
	if err != nil {
		return err
	}
	f := flags("rollback")
	config := f.String("config", "", "")
	to := f.Int("to", 0, "")
	asJSON := f.Bool("json", false, "")
	f.Bool("operator", false, "")
	if err := f.Parse(rest); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 rollback 参数")
	}
	cfg, cfgPath, err := loadSiteConfig(*config, inv)
	if err != nil {
		return err
	}
	data, err := siteDataPath(cfg)
	if err != nil {
		return err
	}
	run, err := startRun(cfg, cfgPath, "rollback", args, *asJSON)
	if err != nil {
		return err
	}
	run.receipt.Notes = append(run.receipt.Notes, invocationNote(inv, restricted))
	// 与 deploy 一样捕获 SIGINT/SIGTERM：取消时子进程失败，仍经 finish 写收据（第 7 步遗留）。
	ctx, cancel := depsSignalContext()
	defer cancel()
	err = withLock(data, "rollback", func() error {
		return doRollback(ctx, defaultSiteOps, cfg, data, *to, run)
	})
	return run.finish(err)
}

func doRollback(ctx context.Context, ops siteOps, cfg *siteConfig, data string, to int, run *siteRun) error {
	rc := run.receipt
	rc.Phase = "rollback"
	s, err := readState(data)
	if err != nil {
		return exitWith(1, err)
	}
	if len(s.History) == 0 {
		return exitWith(1, fmt.Errorf("当前安装来源为 %s，没有部署历史可回滚（旧布局的签名包安装在第一次 deploy 或 upgrade 后才有历史）", s.source()))
	}
	if _, err := os.Lstat(data + ".upgrade.json"); err == nil {
		return exitWith(1, errors.New("存在中断的签名包升级，请先重新运行 upgrade 恢复"))
	}
	current := s.record(s.Current)
	var target *deployRecord
	if to != 0 {
		target = s.record(to)
		if target == nil {
			return exitWith(1, fmt.Errorf("历史里没有序号 %d；auriga history 查看保留的历史", to))
		}
		if target.Status != recordOK {
			return exitWith(1, fmt.Errorf("r%d 不是成功的部署（%s），不能作为回滚目标", to, target.Status))
		}
	} else if target = previousRecord(&s); target == nil {
		return exitWith(1, errors.New("没有比当前更早的成功部署可回滚"))
	}
	if target.Seq == s.Current {
		return exitWith(1, fmt.Errorf("r%d 已是当前版本", target.Seq))
	}
	rc.Release = &receiptRelease{Seq: target.Seq, Dir: target.Release, Previous: s.Current}
	now := readDataVersions(s)
	rc.Data.Before = now
	ok, reason := rollbackDecision(target, current, newerRecords(&s, target), now, floorAncestorCheck(ctx, data, target))
	rc.RollbackReason = reason
	fmt.Fprintln(run.out, reason)
	if !ok {
		return exitWith(3, errors.New(reason))
	}
	if err := checkReleaseDir(data, target); err != nil {
		return exitWith(1, err)
	}
	if err := checkTaggedImages(ctx, target); err != nil {
		return exitWith(1, err)
	}
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		key := imageKey(svc)
		old := ""
		if current != nil {
			old = current.Images[key]
		}
		if old != target.Images[key] {
			rc.Images = append(rc.Images, receiptImage{Service: svc, Action: "rollback", OldID: old, NewID: target.Images[key], Tag: imageTag(svc, target.Seq)})
		}
	}
	plan, planErr := planRollbackSwitch(current, target, data, s.Access)
	if planErr != nil {
		return exitWith(1, fmt.Errorf("计算回滚切换方案失败：%w", planErr))
	}
	rc.SwitchMode = plan.Mode
	orchChanged := plan.OrchestrationChanged
	rc.OrchestrationChanged = &orchChanged
	rc.Orchestration = plan.Orchestration
	rc.MixedVersions = mixedVersionsNote(plan)
	rc.Notes = append(rc.Notes, "回滚路径："+formatSwitchPlanLine(plan))
	fmt.Fprintf(run.out, "回滚到 r%d（%s）…\n", target.Seq, formatSwitchPlanLine(plan))
	if err := startRecord(ops, &s, target, healthTimeout(cfg), plan); err != nil {
		rc.Health = err.Error()
		rc.RolledBack = false
		return exitWith(3, fmt.Errorf("回滚到 r%d 失败，不重试，需人工处理：%w", target.Seq, err))
	}
	rc.Health = "healthy"
	rc.RolledBack = true
	cleanupCtx, cleanupCancel := deployCleanupContext()
	defer cleanupCancel()
	_ = tagImages(cleanupCtx, target.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
	fmt.Fprintf(run.out, "已回滚到 r%d\n", target.Seq)
	return nil
}

func historyCommand(args []string, inv siteInvocation, w io.Writer) error {
	if inv.Sudo {
		cfg, err := loadSudoSiteConfig(inv)
		if err != nil {
			return err
		}
		if runnerOnly(inv, cfg) {
			if err := restrictedArgs("history", args); err != nil {
				return err
			}
		}
	}
	f := flags("history")
	config := f.String("config", "", "")
	asJSON := f.Bool("json", false, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 history 参数")
	}
	cfg, _, err := loadSiteConfig(*config, inv)
	if err != nil {
		return err
	}
	data, err := siteDataPath(cfg)
	if err != nil {
		return err
	}
	s, err := readState(data)
	if err != nil {
		return err
	}
	if *asJSON {
		raw, err := historyJSON(s)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	printHistory(w, s)
	return nil
}

// historyJSON 是 history --json 的输出：只含来源、当前序号与发行元数据，并按收据同样的规则脱敏
// （工作流会上传或打印它）。
func historyJSON(s state) ([]byte, error) {
	raw, err := json.MarshalIndent(struct {
		Source  string         `json:"source"`
		Current int            `json:"current"`
		History []deployRecord `json:"history"`
	}{s.source(), s.Current, s.History}, "", "  ")
	if err != nil {
		return nil, err
	}
	return redactJSON(raw)
}

func printHistory(w io.Writer, s state) {
	fmt.Fprintf(w, "安装来源 %s；当前发行 r%d\n", s.source(), s.Current)
	if len(s.History) == 0 {
		fmt.Fprintln(w, "没有部署历史")
		return
	}
	sorted := append([]deployRecord(nil), s.History...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq > sorted[j].Seq })
	for _, r := range sorted {
		mark := " "
		if r.Seq == s.Current {
			mark = "*"
		}
		commit := r.Source.Commit
		if commit == "" {
			commit = "未知"
		} else if len(commit) > 12 {
			commit = commit[:12]
		}
		floor := "未声明 floor"
		if r.SchemaCompatFloor != "" {
			floor = "floor " + r.SchemaCompatFloor
		}
		dirty := ""
		if r.Source.Dirty {
			dirty = " 含未提交改动"
		}
		fmt.Fprintf(w, "%s r%-4d %-8s %-10s %s 源码 %s %s 快照 %s%s，%s\n", mark, r.Seq, r.kind(), r.Status, r.At, r.Source.Mode, commit, r.Source.SnapshotSHA256[:min(12, len(r.Source.SnapshotSHA256))], dirty, floor)
	}
}

// printDeploySummary 供 auriga status 在服务状态前显示部署来源与当前发行。
func printDeploySummary(w io.Writer, s state) {
	fmt.Fprintf(w, "安装来源 %s\n", s.source())
	if s.TrustUnpinned {
		fmt.Fprintln(w, "发行钥未钉定：第一次签名包 upgrade 前须先 trust add-*")
	}
	if r := s.record(s.Current); r != nil {
		commit := r.Source.Commit
		if commit == "" {
			commit = "提交未知"
		}
		fmt.Fprintf(w, "当前发行 r%d（%s，%s，源码 %s %s，快照 %s）\n", r.Seq, r.kind(), r.Status, r.Source.Mode, commit, r.Source.SnapshotSHA256)
	}
	printStatusRuntimeSummary(w, s)
	fmt.Fprintln(w, accessCapabilities(s).StatusLine())
}
