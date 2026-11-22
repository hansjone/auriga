package main

// deploy 的方向检查（设计 D12 防降级）与签名包站点的基线历史。
//
//   - 受限调用只允许部署当前运行提交的后代（快进，含同一提交重新部署）：在 root 自己 fetch 的
//     暂存裸仓库里 merge-base --is-ancestor <当前提交> <目标提交>。当前发行没有可比较的提交
//     （目录模式含未提交改动、旧签名包没有提交号）或当前提交不在暂存仓库里时，受限调用拒绝。
//     首装（没有 state）不检查。
//   - root 人工部署（不经 sudo，或经 sudo 加 --operator）：目标不是当前提交的后代时必须
//     --allow-downgrade，并先做与 rollback 相同的数据检查——2b 之前的口径是“数据未被比目标
//     更新的版本触碰”：历史里存在一条成功记录，其提交是目标提交本身或祖先，且当前迁移与格式
//     版本与该记录运行时相同；否则退出码 3。当前发行没有可比较的提交时如实记入收据后放行。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// sitePrivilege 在解析参数前判定 deploy、rollback 是否受限，受限时按白名单检查参数；返回去掉
// --operator 后的参数。
func sitePrivilege(cmd string, args []string, inv siteInvocation) ([]string, bool, error) {
	rest, operator, err := splitOperator(args)
	if err != nil {
		return nil, false, err
	}
	if !inv.Sudo {
		return rest, false, nil
	}
	cfg, err := loadSudoSiteConfig(inv)
	if err != nil {
		return nil, true, fmt.Errorf("经 sudo 调用时读取 %s 失败：%w", defaultSiteConfigPath(), err)
	}
	restricted, err := resolvePrivilege(inv, cfg, operator)
	if err != nil {
		return nil, true, err
	}
	if restricted {
		if err := restrictedArgs(cmd, rest); err != nil {
			return nil, true, err
		}
	}
	return rest, restricted, nil
}

// invocationNote 写进收据的调用方式。
func invocationNote(inv siteInvocation, restricted bool) string {
	switch {
	case !inv.Sudo:
		return "调用方式：root 直接运行"
	case restricted:
		return "调用方式：经 sudo 的受限调用（参数白名单、只允许快进），调用者 uid " + inv.SudoUID
	}
	return "调用方式：经 sudo 的人工 root（--operator），调用者 uid " + inv.SudoUID
}

// restrictedStatus：经 sudo 调用 status 时，只要 site.json 存在（或调用者是运行器）就不接受
// 参数，数据目录取 site.json 的 server.data；handled=false 表示不受限，按原有 status 处理。
// 原因：status 会以 root 执行 state 里 package 指向的 status.sh，而 readState 不校验属主；
// 若允许 --data，漏登记 server.runner 的站点上运行器可伪造 state 让 root 执行自己的脚本。
// 没有 site.json 的旧签名包站点经 sudo 照旧（sudo ./auriga status --data …）。
func restrictedStatus(args []string, inv siteInvocation) (bool, error) {
	cfg, err := loadSudoSiteConfig(inv)
	if err != nil {
		return true, err
	}
	if cfg == nil && !runnerOnly(inv, cfg) {
		return false, nil
	}
	if err := restrictedArgs("status", args); err != nil {
		return true, err
	}
	if cfg == nil {
		return true, errors.New("受限调用的 status 需要 site.json 的 server.data")
	}
	data, err := siteDataPath(cfg)
	if err != nil {
		return true, err
	}
	s, err := readState(data)
	if err != nil {
		return true, err
	}
	printDeploySummary(os.Stdout, s)
	return true, nas(s, "status")
}

func sourceRepo(data string) string {
	return filepath.Join(filepath.Dir(data), "auriga-source.git")
}

// gitIsAncestor：a 是否为 b 的祖先（或就是 b）。a 不在暂存仓库里时返回错误。
func gitIsAncestor(ctx context.Context, repo, a, b string) (bool, error) {
	gitDir := "--git-dir=" + repo
	if _, err := deployProbe(ctx, "git", gitDir, "cat-file", "-e", a+"^{commit}"); err != nil {
		return false, fmt.Errorf("提交 %s 不在源码暂存仓库 %s 里", a, repo)
	}
	_, err := deployProbe(ctx, "git", gitDir, "merge-base", "--is-ancestor", a, b)
	return err == nil, nil
}

// comparableCommit：记录里可用于祖先判定的提交（40 位、没有未提交改动）。
func comparableCommit(r *deployRecord) string {
	if r == nil || r.Source.Dirty || !commitPattern.MatchString(r.Source.Commit) {
		return ""
	}
	return r.Source.Commit
}

// checkDeployDirection 执行快进限制与 --allow-downgrade 检查；返回降级检查时读到的数据版本
// （deploySwitch 切换前复核），不是降级时返回 nil。
func checkDeployDirection(ctx context.Context, data string, s *state, prev *deployRecord, src *deploySource, o deployOptions, rc *siteReceipt, w io.Writer) (*dataVersions, error) {
	if o.AllowDowngrade && o.Restricted {
		return nil, exitWith(1, errors.New("受限调用不接受 --allow-downgrade"))
	}
	base := comparableCommit(prev)
	note := func(msg string) {
		rc.Notes = append(rc.Notes, msg)
		fmt.Fprintln(w, msg)
	}
	if src.Mode != "git" {
		if o.Restricted {
			return nil, exitWith(1, errors.New("受限调用只能按提交号从配置的 git 远端部署"))
		}
		note("源码目录模式：不做快进判定（root 人工部署）")
		return nil, nil
	}
	if base == "" {
		if o.Restricted {
			return nil, exitWith(1, fmt.Errorf("当前发行 r%d 没有可比较的提交（来源 %s，提交 %q，未提交改动 %t），受限 deploy 无法确认是快进；请由 root 人工部署一次", prev.Seq, prev.Source.Mode, prev.Source.Commit, prev.Source.Dirty))
		}
		note(fmt.Sprintf("当前发行 r%d 没有可比较的提交，未做快进判定（root 人工部署）", prev.Seq))
		return nil, nil
	}
	repo := sourceRepo(data)
	ff, err := gitIsAncestor(ctx, repo, base, src.Commit)
	if err != nil {
		if o.Restricted {
			return nil, exitWith(1, fmt.Errorf("%v：受限 deploy 无法确认目标是当前运行提交的后代，拒绝；请由 root 人工部署", err))
		}
		note(fmt.Sprintf("%v，未做快进判定（root 人工部署）", err))
		return nil, nil
	}
	if ff {
		if o.AllowDowngrade {
			note("目标是当前运行提交的后代（快进），--allow-downgrade 不起作用")
		}
		return nil, nil
	}
	if o.Restricted {
		return nil, exitWith(1, fmt.Errorf("受限 deploy 只允许当前运行提交 %s 的后代（快进），%s 不是；回到旧提交请用 rollback，或由 root 人工 deploy --allow-downgrade", base, src.Commit))
	}
	if !o.AllowDowngrade {
		return nil, exitWith(1, fmt.Errorf("目标提交 %s 不是当前运行提交 %s 的后代：向旧提交部署须加 --allow-downgrade（会先做与 rollback 相同的数据检查）", src.Commit, base))
	}
	now, reason, err := downgradeCheck(ctx, repo, s, src.Commit)
	rc.Data.Before = now
	rc.RollbackReason = reason
	if err != nil {
		return nil, err
	}
	note("--allow-downgrade：" + reason)
	return now, nil
}

// downgradeCheck：2b 前的口径（D8 2b 前保护），见文件头。
func downgradeCheck(ctx context.Context, repo string, s *state, target string) (*dataVersions, string, error) {
	now := readDataVersions(*s)
	if !now.ok() {
		reason := "读不到当前迁移或格式版本，无法确认数据状态：" + now.Err
		return now, reason, exitWith(3, errors.New("--allow-downgrade 拒绝："+reason+"，需人工处理"))
	}
	var candidates []*deployRecord
	for i := range s.History {
		r := &s.History[i]
		if r.Status == recordOK && comparableCommit(r) != "" {
			candidates = append(candidates, r)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Seq > candidates[j].Seq })
	for _, r := range candidates {
		older := r.Source.Commit == target
		if !older {
			var err error
			if older, err = gitIsAncestor(ctx, repo, r.Source.Commit, target); err != nil {
				continue
			}
		}
		if !older {
			continue
		}
		if ok, why := rollbackDecision(r, now); ok {
			return now, fmt.Sprintf("r%d 的提交 %s 不晚于目标提交；%s", r.Seq, r.Source.Commit, why), nil
		}
	}
	reason := "历史里没有不晚于目标提交、且运行时迁移与格式版本与当前相同的成功记录：数据可能已被更新的版本改动（跨越迁移的降级属 2b 的 floor 检查），拒绝"
	return now, reason, exitWith(3, errors.New("--allow-downgrade "+reason+"，需人工处理"))
}

// packageBaseline 把签名包安装的当前发行登记为一条历史（kind=install），作为第一次 deploy 或
// upgrade 的基线：先按 state 钉定的公钥核验安装包（沿用已记录的 --allow-unsigned 例外），记录
// 其 SHA256SUMS 摘要、镜像与当前迁移/格式版本。persist=false（--dry-run）只在内存里生成。
func packageBaseline(ctx context.Context, s *state, persist bool) (*deployRecord, error) {
	if filepath.Clean(s.Package) != legacyReleaseDir(s.Data) {
		return nil, fmt.Errorf("签名包安装目录 %s 不是预期的 %s，拒绝登记为历史", s.Package, legacyReleaseDir(s.Data))
	}
	if err := verifySums(s.Package); err != nil {
		return nil, fmt.Errorf("当前安装包完整性校验失败，拒绝登记为历史：%w", err)
	}
	if _, err := verifyStatePackage(*s, s.Package, "", s.AllowUnsigned); err != nil {
		return nil, fmt.Errorf("当前安装包签名校验失败，拒绝登记为历史：%w", err)
	}
	sums, err := digest(filepath.Join(s.Package, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	seq := nextSeq(*s)
	rec := deployRecord{Seq: seq, Kind: recordKindInstall, Status: recordOK, At: nowRFC3339(), Source: packageSource(s.Package, ""), Release: s.Package, ReleaseSums: sums, Images: cloneImages(s.Images)}
	if v := readDataVersions(*s); v.ok() {
		rec.Migrations, rec.Formats = v.Migrations, v.Formats
	}
	if !persist {
		return &rec, nil
	}
	if err := tagImages(ctx, rec.Images, func(svc string) string { return imageTag(svc, seq) }); err != nil {
		return nil, err
	}
	s.History = append(s.History, rec)
	s.Current = seq
	if err := saveState(*s); err != nil {
		return nil, err
	}
	return s.record(seq), nil
}

// packageSource：签名包发行的来源记录（提交号等取自包内 offline-release.json 的 source）。
func packageSource(root, archive string) deploySource {
	src := deploySource{Mode: "package"}
	if archive != "" {
		src.URL = filepath.Base(archive)
	}
	var r release
	if err := readJSON(filepath.Join(root, "offline-release.json"), &r); err == nil {
		if src.URL == "" {
			src.URL = r.Version
		}
		if r.Source != nil {
			src.Commit, src.SnapshotSHA256, src.Dirty = r.Source.Commit, r.Source.SnapshotSHA256, r.Source.Dirty
			if !commitPattern.MatchString(src.Commit) {
				src.Commit = ""
			}
		}
	}
	return src
}
