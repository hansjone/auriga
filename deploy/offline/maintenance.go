package main

import (
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

type pendingUpgrade struct {
	Backup string `json:"backup"`
	State  state  `json:"state"`
}

func upgrade(data, archive string) error {
	return upgradeAllowed(data, archive, false)
}

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
		return os.Remove(journal)
	}
	s, err := readState(data)
	if err != nil {
		return err
	}
	if s.source() != sourceSignedPackage {
		return fmt.Errorf("当前安装来源为 %s：签名包 upgrade 接入 deploy 的发行目录布局属统一现场部署 2a（下），暂不支持；本机更新请用 auriga deploy", s.source())
	}
	if err := verifySums(s.Package); err != nil {
		return err
	}
	oldUnsigned, err := verifyStatePackage(s, s.Package, "", allow)
	if err != nil {
		return err
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
	images, err := loadImages(stage)
	if err != nil {
		return err
	}
	path, err := coldBackup(s)
	if err != nil {
		return errors.Join(err, nas(s, "start", "--network-offline"))
	}
	pending := pendingUpgrade{Backup: path, State: s}
	if err := jsonFile(journal, pending, 0600); err != nil {
		return errors.Join(err, nas(s, "start", "--network-offline"))
	}
	s.AllowUnsigned = s.AllowUnsigned || oldUnsigned || unsigned
	activateErr := activateUpgrade(s, stage, images)
	if activateErr != nil {
		fmt.Println("升级失败，恢复升级前冷备份与原发行。")
		if err := rollback(pending); err != nil {
			return errors.Join(activateErr, fmt.Errorf("自动回滚未完成，请保留升级记录与备份 %w", err))
		}
		if err := os.Remove(journal); err != nil {
			return err
		}
		return fmt.Errorf("升级失败，已恢复原版 %w", activateErr)
	}
	if err := os.Remove(journal); err != nil {
		return err
	}
	fmt.Println("升级完成，数据和原密钥已保留，升级前冷备份已保留。")
	return nil
}

func activateUpgrade(s state, stage string, images map[string]string) error {
	previous := s.Package + ".previous"
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		return errors.New("上次发行归档目录已存在，请保留并移到受控备份位置后重试")
	}
	if err := os.Rename(s.Package, previous); err != nil {
		return err
	}
	if err := os.Rename(stage, s.Package); err != nil {
		return err
	}
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
	if err := os.WriteFile(filepath.Join(s.Data, "stack.env"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return err
	}
	s.Images = images
	if err := saveState(s); err != nil {
		return err
	}
	if err := nas(s, "render"); err != nil {
		return err
	}
	if err := nas(s, "preflight"); err != nil {
		return err
	}
	if err := nas(s, "start", "--network-offline"); err != nil {
		return err
	}
	if err := waitHealthy(s); err != nil {
		return err
	}
	// Keep previous code with the cold backup, outside the next backup's tree.
	archivePreviousRelease(previous, filepath.Join(filepath.Dir(s.Data), "auriga-backups", "release-"+time.Now().UTC().Format("20060102T150405.000000000")))
	return nil
}

func archivePreviousRelease(previous, target string) {
	if err := os.Rename(previous, target); err != nil {
		fmt.Fprintf(os.Stderr, "警告：新发行已健康，旧发行归档移动失败；保留 %s，请人工归档后再升级：%v\n", previous, err)
	}
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
