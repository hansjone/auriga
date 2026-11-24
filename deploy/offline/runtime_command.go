package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type runtimeAttachOptions struct {
	Data      string
	NoRestart bool
	Runtime   installRuntimeOptions
}

type runtimeSyncPinOptions struct {
	Data    string
	Runtime installRuntimeOptions
}

func parseRuntimeAttach(args []string) (runtimeAttachOptions, error) {
	var o runtimeAttachOptions
	f := flags("runtime attach")
	f.StringVar(&o.Data, "data", "/srv/auriga", "")
	f.StringVar(&o.Runtime.Bundle, "bundle", "", "")
	f.StringVar(&o.Runtime.Root, "root", "", "")
	f.StringVar(&o.Runtime.Image, "image", "", "")
	f.BoolVar(&o.NoRestart, "no-restart", false, "")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("多余参数")
	}
	if !o.Runtime.enabled() {
		return o, errors.New("runtime attach 必须同时提供 --bundle、--root 和 --image")
	}
	if err := o.Runtime.validate(); err != nil {
		return o, err
	}
	data, err := dataPath(o.Data)
	o.Data = data
	return o, err
}

func parseRuntimeSyncPin(args []string) (runtimeSyncPinOptions, error) {
	var o runtimeSyncPinOptions
	f := flags("runtime sync-pin")
	f.StringVar(&o.Data, "data", "/srv/auriga", "")
	f.StringVar(&o.Runtime.Bundle, "bundle", "", "")
	f.StringVar(&o.Runtime.Root, "root", "", "")
	f.StringVar(&o.Runtime.Image, "image", "", "")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("多余参数")
	}
	if !o.Runtime.enabled() {
		return o, errors.New("runtime sync-pin 必须同时提供 --bundle、--root 和 --image")
	}
	if err := o.Runtime.validate(); err != nil {
		return o, err
	}
	data, err := dataPath(o.Data)
	o.Data = data
	return o, err
}

func runtimeCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：./auriga runtime attach|sync-pin --bundle <目录> --root <root.pb> --image <repo@sha256:...> [--data <数据目录>]")
	}
	switch args[0] {
	case "attach":
		o, err := parseRuntimeAttach(args[1:])
		if err != nil {
			return err
		}
		return withLock(o.Data, "runtime-attach", func() error {
			if _, err := os.Lstat(o.Data + ".upgrade.json"); err == nil {
				return errors.New("存在中断的升级，请先重新运行 upgrade 恢复")
			}
			s, err := readState(o.Data)
			if err != nil {
				return err
			}
			if err := attachInstalledRuntime(&s, s.Package, o.Runtime); err != nil {
				return err
			}
			fmt.Printf("已为站点 %s 追加并登记 DSH 运行时（%s）。\n", s.Data, o.Runtime.Image)
			if o.NoRestart {
				fmt.Println("已指定 --no-restart；请随后执行 render/start 或 auriga deploy 使运行时生效。")
				return nil
			}
			if err := nas(s, "render"); err != nil {
				return err
			}
			if err := nas(s, "start", "--network-offline"); err != nil {
				return err
			}
			return waitHealthy(s)
		})
	case "sync-pin":
		o, err := parseRuntimeSyncPin(args[1:])
		if err != nil {
			return err
		}
		return withLock(o.Data, "runtime-sync-pin", func() error {
			if _, err := os.Lstat(o.Data + ".upgrade.json"); err == nil {
				return errors.New("存在中断的升级，请先重新运行 upgrade 恢复")
			}
			s, err := readState(o.Data)
			if err != nil {
				return err
			}
			oldImage, slot, err := syncPinnedInstalledRuntime(&s, s.Package, o.Runtime)
			if err != nil {
				return err
			}
			fmt.Printf("已同步站点 %s 的安装钉定运行时（槽位 %s）：\n", s.Data, slot)
			fmt.Printf("  原钉定镜像：%s\n", oldImage)
			fmt.Printf("  新钉定镜像：%s\n", o.Runtime.Image)
			fmt.Printf("  已原子更新：%s、%s 与 %s\n",
				runtimeSnapshot(s.Data),
				filepath.Join(s.Data+".oneclick", "state.json"),
				filepath.Join(s.Data, "stack.env"),
			)
			fmt.Println("请同步更新 site.json 中 server.install.runtime 的 bundle/root/image 为相同参数，以便后续 auriga deploy 与干净重装保持一致：")
			fmt.Printf("  bundle: %s\n  root:   %s\n  image:  %s\n", o.Runtime.Bundle, o.Runtime.Root, o.Runtime.Image)
			return nil
		})
	default:
		return fmt.Errorf("未知 runtime 子命令 %s；可用 attach、sync-pin", args[0])
	}
}

// attachInstalledRuntime 对已安装且尚未登记 DSH 运行时的站点执行受控追加：
//  1. 先用 runtime-release verify 严格验签原始输入与副本快照；
//  2. 将公开快照固定到 <data>.oneclick/runtime-inputs；
//  3. 调用 auriga-nas-bootstrap runtime-upgrade -action attach 写入独立的
//     <data>/config/runtime-source（不修改已封存的 config/materials 及 bootstrap.receipt）；
//  4. 原子持久化 state.json。
func attachInstalledRuntime(s *state, pkg string, req installRuntimeOptions) error {
	if !req.enabled() {
		return errors.New("缺少 runtime 输入（bundle、root、image）")
	}
	if err := req.validate(); err != nil {
		return err
	}
	if s.AllowUnsigned {
		return errors.New("未签名安装不能追加受信任的 DSH 运行时")
	}
	if s.Runtime != nil {
		return errors.New("已有安装已登记 DSH 运行时；不得通过 attach 替换根或模板，请使用 runtime-upgrade")
	}
	prepared, err := prepareRuntimeSnapshot(pkg, req)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(prepared.directory) }()
	snapDir := runtimeSnapshot(s.Data)
	if _, err := os.Lstat(snapDir); err == nil {
		return errors.New("已存在未清理的 runtime-inputs 快照目录，拒绝覆盖现场")
	}
	if err := copyTree(prepared.directory, snapDir); err != nil {
		return err
	}
	cleanupSnap := true
	defer func() {
		if cleanupSnap {
			_ = os.RemoveAll(snapDir)
		}
	}()
	s.Runtime = &prepared.identity
	if err := checkPinnedInstallRuntime(pkg, *s); err != nil {
		s.Runtime = nil
		return err
	}
	snapOpts := runtimeSnapshotOptions(snapDir, req.Image)
	if err := attachBootstrapRuntimeFunc(*s, pkg, snapOpts); err != nil {
		s.Runtime = nil
		return err
	}
	if err := saveState(*s); err != nil {
		s.Runtime = nil
		return err
	}
	cleanupSnap = false
	return nil
}

func readStackEnvValue(data, key string) string {
	raw, err := smallFile(filepath.Join(data, "stack.env"), 1<<20)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func attachBootstrapRuntime(s state, pkg string, snap installRuntimeOptions) error {
	mode := readStackEnvValue(s.Data, "AURIGA_INSTALL_MODE")
	if mode == "container" {
		env := []string{
			"AURIGA_DATA_ROOT=" + s.Data,
			"NAS_ROOT=" + filepath.Join(pkg, "deploy-nas"),
			"STACK_ENV=" + filepath.Join(s.Data, "stack.env"),
		}
		return command(nil, os.Stdout, env, "sh",
			filepath.Join(pkg, "deploy-nas/scripts/installer.sh"),
			"runtime-attach", snap.Bundle, snap.Root, snap.Image)
	}
	args := []string{
		"runtime-upgrade",
		"-action", "attach",
		"-base", filepath.Join(s.Data, "config/materials"),
		"-out", filepath.Join(s.Data, "config/runtime-source"),
		"-bundle", snap.Bundle,
		"-root", snap.Root,
		"-image", snap.Image,
	}
	if mem := readStackEnvValue(s.Data, "AURIGA_INSTANCE_MAX_MEMORY_MB"); mem != "" {
		args = append(args, "-instance-max-memory-mb", mem)
	}
	return command(nil, os.Stdout, nil, filepath.Join(pkg, "deploy-nas/bin/auriga-nas-bootstrap"), args...)
}

// syncPinnedInstalledRuntime 将已通过 runtime-upgrade 正式激活的新运行时同步回安装钉定快照、
// state.json 与 stack.env（解决 Forgejo #25 安装钉定与生效运行时两处真相问题）：
//  1. 拒绝未签名安装与尚未登记 runtime 的站点（未登记请先用 runtime attach）；
//  2. 严格核对 <data>/config/runtime-source/active.json 为已提交（非 Pending）状态且生效镜像与 req.Image 一致；
//  3. 经由 prepareRuntimeSnapshot 走完整 runtime-release verify 验签并计算快照摘要；
//  4. 原子替换 <data>.oneclick/runtime-inputs、更新 stack.env 的 AURIGA_RUNTIME_* 与 state.json，
//     任一步失败自动回滚快照与 stack.env。
func syncPinnedInstalledRuntime(s *state, pkg string, req installRuntimeOptions) (oldImage string, activeSlot string, err error) {
	if !req.enabled() {
		return "", "", errors.New("缺少 runtime 输入（bundle、root、image）")
	}
	if err := req.validate(); err != nil {
		return "", "", err
	}
	if s.AllowUnsigned {
		return "", "", errors.New("未签名安装不能同步受信任的 DSH 运行时钉定")
	}
	if s.Runtime == nil {
		return "", "", errors.New("当前站点尚未登记安装钉定运行时，请先使用 `./auriga runtime attach` 登记")
	}
	oldImage = s.Runtime.Source.Image

	active := readActiveRuntimeInfo(s.Data)
	if !active.Configured || active.SourceKind != "runtime-source" {
		return "", "", errors.New("未找到已激活的 config/runtime-source/active.json；sync-pin 只能同步已经由 runtime-upgrade 正式激活的运行时")
	}
	if active.ParseError != nil {
		return "", "", fmt.Errorf("config/runtime-source/active.json 状态异常：%w", active.ParseError)
	}
	if active.Pending {
		return "", "", fmt.Errorf("config/runtime-source/active.json 处于未提交状态（pending，槽位 %s），请先完成 runtime-upgrade commit", active.Slot)
	}
	if active.Slot == "" || active.Image == "" {
		return "", "", errors.New("config/runtime-source/active.json 缺少有效的生效槽位或镜像声明")
	}
	if active.Image != req.Image {
		return "", "", fmt.Errorf("请求同步的镜像（%s）与当前生效运行时（%s，槽位 %s）不一致；sync-pin 只能同步已经由 runtime-upgrade 正式激活的运行时", req.Image, active.Image, active.Slot)
	}

	prepared, err := prepareRuntimeSnapshot(pkg, req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(prepared.directory) }()

	snapDir := runtimeSnapshot(s.Data)
	if err := noSymlinks(snapDir); err != nil {
		return "", "", err
	}
	stageDir := snapDir + ".sync-pin.new"
	backupDir := snapDir + ".sync-pin.bak"
	_ = os.RemoveAll(stageDir)
	_ = os.RemoveAll(backupDir)
	if err := copyTree(prepared.directory, stageDir); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", "", err
	}
	if hash, err := runtimeSnapshotDigest(stageDir); err != nil || hash != prepared.identity.SHA256 {
		_ = os.RemoveAll(stageDir)
		return "", "", errors.New("暂存 runtime 快照摘要核验失败")
	}
	if err := verifyInstallRuntimeFunc(pkg, runtimeSnapshotOptions(stageDir, req.Image)); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", "", err
	}

	hadBackup := false
	if _, statErr := os.Lstat(snapDir); statErr == nil {
		if err := os.Rename(snapDir, backupDir); err != nil {
			_ = os.RemoveAll(stageDir)
			return "", "", fmt.Errorf("备份原 runtime-inputs 失败：%w", err)
		}
		hadBackup = true
	} else if !os.IsNotExist(statErr) {
		_ = os.RemoveAll(stageDir)
		return "", "", statErr
	}

	rollbackSnap := func() {
		_ = os.RemoveAll(snapDir)
		if hadBackup {
			_ = os.Rename(backupDir, snapDir)
		}
	}

	if err := os.Rename(stageDir, snapDir); err != nil {
		_ = os.RemoveAll(stageDir)
		rollbackSnap()
		return "", "", fmt.Errorf("替换 runtime-inputs 失败：%w", err)
	}

	candidate := *s
	idCopy := prepared.identity
	candidate.Runtime = &idCopy
	if err := checkPinnedInstallRuntime(pkg, candidate); err != nil {
		rollbackSnap()
		return "", "", err
	}

	origEnv, origPerm, err := updateStackEnvRuntime(s.Data, installRuntimeEnv(candidate))
	if err != nil {
		rollbackSnap()
		return "", "", err
	}
	if err := saveState(candidate); err != nil {
		_ = writeFileAtomic(filepath.Join(s.Data, "stack.env"), origEnv, origPerm)
		rollbackSnap()
		return "", "", err
	}

	*s = candidate
	if hadBackup {
		_ = os.RemoveAll(backupDir)
	}
	return oldImage, active.Slot, nil
}

// updateStackEnvRuntime 原子更新 <data>/stack.env 中的 AURIGA_RUNTIME_BUNDLE、
// AURIGA_RUNTIME_ROOT、AURIGA_RUNTIME_IMAGE，保持文件权限与其它行原样不变。
func updateStackEnvRuntime(data string, values map[string]string) (origRaw []byte, origPerm os.FileMode, err error) {
	envPath := filepath.Join(data, "stack.env")
	if err := noSymlinks(envPath); err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(envPath)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("stack.env 不是普通文件")
	}
	origPerm = info.Mode().Perm()
	if origPerm == 0 {
		origPerm = 0600
	}
	origRaw, err = smallFile(envPath, 1<<20)
	if err != nil {
		return nil, 0, err
	}
	runtimeKeys := []string{"AURIGA_RUNTIME_BUNDLE", "AURIGA_RUNTIME_ROOT", "AURIGA_RUNTIME_IMAGE"}
	isRuntimeKey := map[string]bool{
		"AURIGA_RUNTIME_BUNDLE": true,
		"AURIGA_RUNTIME_ROOT":   true,
		"AURIGA_RUNTIME_IMAGE":  true,
	}
	written := map[string]bool{}
	normalized := strings.ReplaceAll(string(origRaw), "\r\n", "\n")
	rawLines := strings.Split(normalized, "\n")
	// 去掉末尾切分产生的空尾元素，最后统一补换行。
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	outLines := make([]string, 0, len(rawLines)+len(runtimeKeys))
	for _, line := range rawLines {
		key, _, ok := strings.Cut(line, "=")
		trimmedKey := strings.TrimSpace(key)
		if ok && isRuntimeKey[trimmedKey] {
			if written[trimmedKey] {
				continue
			}
			outLines = append(outLines, trimmedKey+"="+values[trimmedKey])
			written[trimmedKey] = true
			continue
		}
		outLines = append(outLines, line)
	}
	for _, key := range runtimeKeys {
		if !written[key] {
			outLines = append(outLines, key+"="+values[key])
		}
	}
	updated := []byte(strings.Join(outLines, "\n") + "\n")
	if err := writeFileAtomic(envPath, updated, origPerm); err != nil {
		return nil, 0, err
	}
	return origRaw, origPerm, nil
}
