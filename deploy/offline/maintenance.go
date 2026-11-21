package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func stopped(s state) error {
	ids, err := capture("docker", "ps", "-q", "--no-trunc")
	if err != nil {
		return err
	}
	if ids == "" {
		return nil
	}
	args := append([]string{"inspect"}, strings.Fields(ids)...)
	raw, err := capture("docker", args...)
	if err != nil {
		return err
	}
	var records []struct {
		Mounts []struct {
			Source string
			RW     bool
		}
	}
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return err
	}
	for _, c := range records {
		for _, m := range c.Mounts {
			if m.RW && (m.Source == s.Data || strings.HasPrefix(m.Source, s.Data+"/")) {
				return errors.New("仍有运行容器写入数据目录，冷备份/恢复已中止")
			}
		}
	}
	return nil
}

func coldBackup(s state) (string, error) {
	volumes, err := configuredVolumes(s)
	if err != nil {
		return "", err
	}
	if err := nas(s, "stop", "--with-registered-instances"); err != nil {
		return "", err
	}
	if err := stopped(s); err != nil {
		return "", err
	}
	dir := backupDir(s)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := noSymlinks(dir); err != nil {
		return "", err
	}
	snapshot, err := os.MkdirTemp(dir, ".volume-snapshot-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(snapshot) }()
	if err := snapshotVolumes(s, volumes, snapshot); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "backup-"+time.Now().UTC().Format("20060102T150405.000000000")+".tar")
	// tar retains numeric ownership, modes and legitimate database symlinks. This
	// archive is private installation state and is never a public release input.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	err = command(nil, f, nil, "tar", "--numeric-owner", "-cpf", "-", "-C", filepath.Dir(s.Data), filepath.Base(s.Data), filepath.Base(s.Data)+".oneclick", "-C", snapshot, "volumes")
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	hash, err := digest(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".sha256", []byte(hash+"\n"), 0600); err != nil {
		return "", err
	}
	fmt.Printf("冷备份 %s\nSHA256 %s\n含私钥和数据库，请保存在受控介质。\n", path, hash)
	return path, nil
}

func backup(s state) (string, error) {
	path, err := coldBackup(s)
	restartErr := nas(s, "start", "--network-offline")
	if err != nil {
		return "", errors.Join(err, restartErr)
	}
	if restartErr != nil {
		return path, restartErr
	}
	return path, waitHealthy(s)
}

// pendingUpgrade 是升级恢复记录：冷备份、升级前 state，以及本次写入的发行目录（恢复冷备份后
// 删除，恢复后的 state 不引用它）。
type pendingUpgrade struct {
	Backup  string `json:"backup"`
	State   state  `json:"state"`
	Release string `json:"release,omitempty"`
}

func upgrade(data, archive string) error {
	return upgradeAllowed(data, archive, false)
}

// upgradeOps 是 upgrade 激活新发行用到的站点动作（测试注入）。
var upgradeOps = siteOps{nas: nas, health: waitHealthyFor}

// 签名包 upgrade（设计 D10 接入新布局）：
//   - 来源为 signed-package 或 local-deploy 的站点都可以接收签名包；adopted 属 2e。
//   - state 没有钉定任何公钥（deploy 首装未钉定）时先要求 trust add-release-key / add-site-key。
//   - 旧布局的签名包安装（没有历史）先把当前安装包登记为基线历史（kind=install）。
//   - 包内发行写到 auriga-releases/<序号>/（占用新序号，产生一条 kind=upgrade 的历史），镜像
//     打 :r<序号>，切换 current；冷备份语义不变：先冷备份，失败从冷备份恢复并删除本次发行目录。
func upgradeAllowed(data, archive string, allow bool) error {
	journal := data + ".upgrade.json"
	if _, err := os.Lstat(journal); err == nil {
		var pending pendingUpgrade
		if err := readJSON(journal, &pending); err != nil {
			return err
		}
		if pending.State.Data != data {
			return errors.New("升级恢复记录与目录不匹配")
		}
		if err := rollback(pending); err != nil {
			return err
		}
		removeFailedUpgradeRelease(pending)
		return os.Remove(journal)
	}
	s, err := readState(data)
	if err != nil {
		return err
	}
	switch s.source() {
	case sourceSignedPackage, sourceLocalDeploy:
	default:
		return fmt.Errorf("当前安装来源为 %s：签名包 upgrade 只接收签名包安装或 deploy 建立的站点（adopted 站点属统一现场部署 2e）", s.source())
	}
	if len(s.TrustedKeys) == 0 && s.PublicKey == "" {
		return errors.New("state 没有钉定任何发行/站点公钥（本机构建首装未钉定发行钥）：第一次签名包 upgrade 前请先 trust add-release-key <公钥.pem> --confirm-fingerprint <指纹>，或 trust add-site-key 登记站点构建公钥")
	}
	if err := verifySums(s.Package); err != nil {
		return err
	}
	oldUnsigned, err := verifyStatePackage(s, s.Package, "", allow)
	if err != nil {
		return err
	}
	if s.record(s.Current) == nil && (s.source() != sourceSignedPackage || len(s.History) != 0) {
		return fmt.Errorf("state 缺少当前发行 r%d 的历史记录，拒绝升级", s.Current)
	}
	stage, err := os.MkdirTemp(filepath.Dir(data), ".auriga-upgrade-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := unpack(archive, stage); err != nil {
		return err
	}
	if err := verifySums(stage); err != nil {
		return err
	}
	unsigned, err := verifyStatePackage(s, stage, archiveIndex(archive), allow)
	if err != nil {
		return err
	}
	sums, err := digest(filepath.Join(stage, "SHA256SUMS"))
	if err != nil {
		return err
	}
	images, err := loadImages(stage)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if s.record(s.Current) == nil {
		// 旧布局的签名包安装：先把当前安装包登记为基线历史，之后可以 rollback 回它。
		if _, err := packageBaseline(ctx, &s, true); err != nil {
			return err
		}
	}
	seq := nextSeq(s)
	path, err := coldBackup(s)
	if err != nil {
		return errors.Join(err, nas(s, "start", "--network-offline"))
	}
	pending := pendingUpgrade{Backup: path, State: s, Release: releaseDir(data, seq)}
	if err := jsonFile(journal, pending, 0600); err != nil {
		return errors.Join(err, nas(s, "start", "--network-offline"))
	}
	s.AllowUnsigned = s.AllowUnsigned || oldUnsigned || unsigned
	activateErr := activateUpgrade(ctx, &s, stage, images, seq, sums, archive)
	if activateErr != nil {
		fmt.Println("升级失败，恢复升级前冷备份与原发行。")
		if err := rollback(pending); err != nil {
			return errors.Join(activateErr, fmt.Errorf("自动回滚未完成，请保留升级记录与备份 %w", err))
		}
		removeFailedUpgradeRelease(pending)
		if err := os.Remove(journal); err != nil {
			return err
		}
		return fmt.Errorf("升级失败，已恢复原版 %w", activateErr)
	}
	if err := os.Remove(journal); err != nil {
		return err
	}
	if pruned, err := pruneReleases(ctx, &s, siteKeepHistory(), os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "警告：%v\n", err)
	} else if len(pruned) > 0 {
		if err := saveState(s); err != nil {
			fmt.Fprintf(os.Stderr, "警告：淘汰历史后保存 state 失败：%v\n", err)
		}
	}
	fmt.Printf("升级完成：当前 r%d（%s），数据和原密钥已保留，升级前冷备份已保留；auriga rollback 可退回保留的历史。\n", seq, s.Package)
	return nil
}

// activateUpgrade 把已核验的包移到 auriga-releases/<序号>/，记一条 upgrade 历史并切换：
// stack.env 只改镜像与 seed，render → preflight → start → 等待健康，成功后记录迁移与格式版本。
func activateUpgrade(ctx context.Context, s *state, stage string, images map[string]string, seq int, sums, archive string) error {
	root := releasesRoot(s.Data)
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	if err := depsTrustedSourceCheck("发行根目录 ", root); err != nil {
		return err
	}
	release := releaseDir(s.Data, seq)
	if _, err := os.Lstat(release); !os.IsNotExist(err) {
		return fmt.Errorf("发行目录 %s 已存在，拒绝覆盖", release)
	}
	if err := os.Rename(stage, release); err != nil {
		return err
	}
	if err := tagImages(ctx, images, func(svc string) string { return imageTag(svc, seq) }); err != nil {
		return err
	}
	rec := deployRecord{Seq: seq, Kind: recordKindUpgrade, Status: recordFailed, At: nowRFC3339(), Source: packageSource(release, archive), Release: release, ReleaseSums: sums, Images: cloneImages(images)}
	s.History = append(s.History, rec)
	// Preserve every operator setting. Only exact image references and seed state
	// change, using the same parser-safe KEY=VALUE format as the existing scripts.
	raw, err := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if err != nil {
		return err
	}
	updates := map[string]string{"AURIGA_INSTALLER_IMAGE": images["POSTGRES"], "AURIGA_PLATFORM_SEED": "false"}
	for name, id := range images {
		updates["AURIGA_IMAGE_"+name] = id
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if ok {
			if value, ok := updates[key]; ok {
				lines[i] = key + "=" + value
			}
		}
	}
	if err := writeFileAtomic(filepath.Join(s.Data, "stack.env"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return err
	}
	s.Images, s.Package, s.Current, s.Source = images, release, seq, sourceSignedPackage
	if err := saveState(*s); err != nil {
		return err
	}
	for _, step := range [][]string{{"render"}, {"preflight"}, {"start", "--network-offline"}} {
		if err := upgradeOps.nas(*s, step[0], step[1:]...); err != nil {
			return err
		}
	}
	if err := upgradeOps.health(*s, 5*time.Minute); err != nil {
		return err
	}
	cur := s.record(seq)
	cur.Status = recordOK
	if v := readDataVersions(*s); v.ok() {
		cur.Migrations, cur.Formats = v.Migrations, v.Formats
	}
	if err := saveState(*s); err != nil {
		return err
	}
	_ = tagImages(ctx, images, func(svc string) string { return "auriga/" + svc + ":nas" })
	return nil
}

// removeFailedUpgradeRelease：从冷备份恢复后删除本次升级写入的发行目录（恢复后的 state 不引用
// 它；序号不复用，nextSeq 会跳过已用过的序号）。
func removeFailedUpgradeRelease(p pendingUpgrade) {
	if p.Release == "" || filepath.Dir(p.Release) != releasesRoot(p.State.Data) {
		return
	}
	for _, r := range p.State.History {
		if r.Release == p.Release {
			return
		}
	}
	if err := os.RemoveAll(p.Release); err != nil {
		fmt.Fprintf(os.Stderr, "警告：删除失败升级的发行目录 %s 失败：%v\n", p.Release, err)
	}
}

// siteKeepHistory：有 site.json 时取 server.keepHistory，否则保留 3 份。
func siteKeepHistory() int {
	if _, err := os.Lstat(defaultSiteConfigPath()); err == nil {
		if cfg, _, err := loadSiteConfig("", siteInvocation{Root: true}); err == nil && cfg.Server != nil && cfg.Server.KeepHistory != nil {
			return *cfg.Server.KeepHistory
		}
	}
	return 3
}

func rollback(p pendingUpgrade) error {
	s := p.State
	stopErr := nas(s, "stop", "--with-registered-instances")
	if err := stopped(s); err != nil {
		return errors.Join(stopErr, err)
	}
	raw, err := os.ReadFile(p.Backup + ".sha256")
	if err != nil {
		return err
	}
	hash, err := digest(p.Backup)
	if err != nil {
		return err
	}
	if hash != strings.TrimSpace(string(raw)) {
		return errors.New("冷备份摘要不符，拒绝恢复")
	}
	plan, err := prepareRestore(p)
	if err != nil {
		return err
	}
	for _, target := range []string{s.Data, s.Data + ".oneclick"} {
		staged := filepath.Join(plan.Staging, filepath.Base(target))
		if _, err := os.Lstat(staged); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			if _, err := os.Lstat(target + plan.Suffix); !os.IsNotExist(err) {
				return errors.New("回滚隔离目标已存在，保留恢复记录并检查现场")
			}
			if err := os.Rename(target, target+plan.Suffix); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(staged, target); err != nil {
			return err
		}
	}
	if err := restoreVolumes(s, plan.Staging); err != nil {
		return err
	}
	if err := nas(s, "start", "--network-offline"); err != nil {
		return err
	}
	if err := waitHealthy(s); err != nil {
		return err
	}
	if err := os.Remove(s.Data + ".restore.json"); err != nil {
		return err
	}
	if err := os.RemoveAll(plan.Staging); err != nil {
		return err
	}
	fmt.Println("原数据库、密钥与发行已恢复。失败现场以 .failed-时间 后缀保留。")
	return nil
}

type restorePlan struct {
	Staging string `json:"staging"`
	Suffix  string `json:"suffix"`
}

func prepareRestore(p pendingUpgrade) (restorePlan, error) {
	file := p.State.Data + ".restore.json"
	var plan restorePlan
	if _, err := os.Lstat(file); err == nil {
		if err := readJSON(file, &plan); err != nil {
			return plan, err
		}
		if filepath.Dir(plan.Staging) != filepath.Dir(p.State.Data) || !strings.HasPrefix(filepath.Base(plan.Staging), ".auriga-restore-") || !strings.HasPrefix(plan.Suffix, ".failed-") || strings.ContainsAny(plan.Suffix, "/\\") {
			return plan, errors.New("恢复记录路径无效")
		}
		return plan, noSymlinks(plan.Staging)
	}
	stage, err := os.MkdirTemp(filepath.Dir(p.State.Data), ".auriga-restore-")
	if err != nil {
		return plan, err
	}
	if err := command(nil, io.Discard, nil, "tar", "--same-owner", "-xpf", p.Backup, "-C", stage); err != nil {
		return plan, err
	}
	plan = restorePlan{Staging: stage, Suffix: ".failed-" + time.Now().UTC().Format("20060102T150405.000000000")}
	return plan, jsonFile(file, plan, 0600)
}

func nasView(root string) (string, error) {
	view, err := os.MkdirTemp(filepath.Dir(root), ".auriga-verify-")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"images", "deploy-nas"} {
		if err := linkTree(filepath.Join(root, name), filepath.Join(view, name)); err != nil {
			_ = os.RemoveAll(view)
			return "", err
		}
	}
	for source, target := range map[string]string{"NAS-SHA256SUMS": "SHA256SUMS", "versions.txt": "versions.txt"} {
		if err := copyFile(filepath.Join(root, source), filepath.Join(view, target), 0644); err != nil {
			_ = os.RemoveAll(view)
			return "", err
		}
	}
	return view, nil
}
