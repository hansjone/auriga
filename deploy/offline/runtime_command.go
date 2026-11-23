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

func runtimeCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：./auriga runtime attach --bundle <目录> --root <root.pb> --image <repo@sha256:...> [--data <数据目录>]")
	}
	if args[0] != "attach" {
		return fmt.Errorf("未知 runtime 子命令 %s；可用 attach", args[0])
	}
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
