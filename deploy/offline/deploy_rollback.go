package main

// 回滚（设计 D8、D2，遗留 P2）。
//
// 迁移版本：主 postgres 实例里各库各 schema 的 flyway_schema_history 与
// auriga_common_schema_history（collab-db、audit 在同一实例里，各库逐个读取），记录每张表的
// 行数、最大 installed_rank 与全部行的摘要。数据目录格式版本：<数据目录>/format-versions.json。
//
// 自动回滚只在新版本未触碰数据时执行（切换前后迁移与格式版本都读得到且完全相同），作为
// 长期规则（P2）；否则退出码 3 交人工。手动 rollback：当前数据与目标历史运行时记录的
// 迁移/格式版本相同即可执行；否则需要目标声明 schemaCompatFloor 与格式上限并通过检查——
// floor 生成与祖先判定属 2b，本步对没有声明的目标拒绝，对有声明的目标也只做格式上限检查
// 后仍交人工（floor 的提交祖先判定未实现）。
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
	"strconv"
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

// rollbackDecision 判断能否回到 target：now 是当前数据版本。
func rollbackDecision(target *deployRecord, now *dataVersions) (bool, string) {
	if !now.ok() {
		return false, "读不到当前迁移或格式版本，无法确认数据状态：" + now.Err
	}
	if target.Migrations != nil && sameMap(target.Migrations, now.Migrations) && sameMap(target.Formats, now.Formats) {
		return true, fmt.Sprintf("当前迁移与格式版本与 r%d 运行时记录相同，数据未被之后的版本改动", target.Seq)
	}
	if target.SchemaCompatFloor == "" || target.FormatMax == nil {
		return false, fmt.Sprintf("数据已在 r%d 之后被改动，且 r%d 没有声明 schemaCompatFloor 与格式上限，拒绝自动回滚，需人工处理", target.Seq, target.Seq)
	}
	for k, v := range now.Formats {
		max, declared := target.FormatMax[k]
		if !declared {
			return false, fmt.Sprintf("数据目录格式 %s 不在 r%d 的格式上限声明里", k, target.Seq)
		}
		a, errA := strconv.Atoi(v)
		b, errB := strconv.Atoi(max)
		if errA != nil || errB != nil || a > b {
			return false, fmt.Sprintf("数据目录格式 %s=%s 高于 r%d 能读的上限 %s", k, v, target.Seq, max)
		}
	}
	return false, "schemaCompatFloor 的提交祖先判定属统一现场部署 2b，本版本不自动执行跨越迁移的回滚，需人工处理"
}

// siteOps 是切换与回滚用到的站点动作（测试注入）。
type siteOps struct {
	nas    func(s state, op string, args ...string) error
	health func(s state, timeout time.Duration) error
	smoke  func(s state) (string, error)
	// stopWriters 停掉会写库或改写数据目录的容器并确认已停止，返回停掉的服务。
	stopWriters func(s state) ([]string, error)
}

var defaultSiteOps = siteOps{nas: nas, health: waitHealthyFor, smoke: runDeploySmoke, stopWriters: stopDataWriters}

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
// （不用会默认报 PARTIAL 的 smoke.sh；冒烟脚本由后续步骤提供）。
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

// startRecord 把站点切到某条历史：写 stack.env 镜像、原子保存 state（current 指针），
// 再 render → preflight → start → 等待健康。
func startRecord(ops siteOps, s *state, r *deployRecord, timeout time.Duration) error {
	if err := updateStackEnv(s.Data, r.Images); err != nil {
		return err
	}
	s.Current, s.Package, s.Images = r.Seq, r.Release, cloneImages(r.Images)
	s.Source = sourceForRecord(r)
	if err := saveState(*s); err != nil {
		return err
	}
	for _, step := range [][]string{{"render"}, {"preflight"}, {"start", "--network-offline"}} {
		if err := ops.nas(*s, step[0], step[1:]...); err != nil {
			return fmt.Errorf("%s 失败：%w", step[0], err)
		}
	}
	if err := ops.health(*s, timeout); err != nil {
		return fmt.Errorf("健康检查失败：%w", err)
	}
	return nil
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
	err = withLock(data, "rollback", func() error {
		return doRollback(context.Background(), defaultSiteOps, cfg, data, *to, run)
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
	ok, reason := rollbackDecision(target, now)
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
	fmt.Fprintf(run.out, "回滚到 r%d（%s）…\n", target.Seq, target.Release)
	if err := startRecord(ops, &s, target, healthTimeout(cfg)); err != nil {
		rc.Health = err.Error()
		rc.RolledBack = false
		return exitWith(3, fmt.Errorf("回滚到 r%d 失败，不重试，需人工处理：%w", target.Seq, err))
	}
	rc.Health = "healthy"
	rc.RolledBack = true
	_ = tagImages(ctx, target.Images, func(svc string) string { return "auriga/" + svc + ":nas" })
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
		raw, err := json.MarshalIndent(struct {
			Source  string         `json:"source"`
			Current int            `json:"current"`
			History []deployRecord `json:"history"`
		}{s.source(), s.Current, s.History}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	printHistory(w, s)
	return nil
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
		if len(commit) > 12 {
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
		fmt.Fprintf(w, "当前发行 r%d（%s，源码 %s %s，快照 %s）\n", r.Seq, r.Status, r.Source.Mode, r.Source.Commit, r.Source.SnapshotSHA256)
	}
}
