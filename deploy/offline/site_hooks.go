package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

const (
	postDeployHookTimeout = 10 * time.Minute
	uacMaterialFileLimit  = 1 << 20
)

// allowedUACMaterialFiles 与 deploy/nas/bootstrap/material_metadata_linux.go 中的
// nasMaterialGuard 白名单保持严格一致，确保复制后的文件不会触发 unknown NAS source leaf。
var allowedUACMaterialFiles = map[string]bool{
	"uac.properties": true,
	"adapter.json":   true,
	"members.json":   true,
}

// 测试可注入点。
var (
	postDeployHookCheck  = checkPostDeployHook
	postDeployHookRunner = runPostDeployHook
	uacMaterialsDirCheck = func(dir string) error {
		return checkRootOwnedDir("UAC 材料目录 server.install.uacMaterialsDir ", dir)
	}
	uacChownFunc = chownUACPath
)

func chownUACPath(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chown(path, 10001, 10001)
}

// checkPostDeployHook 校验部署后置钩子脚本：必须是规范绝对路径、无符号链接、普通文件、
// 属主受信（Linux root / Windows Administrators|SYSTEM）、组和其他用户不可写、祖先目录受信；
// Linux 上额外要求属主可执行位。
func checkPostDeployHook(path string) error {
	if err := checkConfigPath("server.postDeployHook", path); err != nil {
		return err
	}
	if err := noSymlinks(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("部署后置钩子不可读取 %s：%w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("部署后置钩子必须是普通文件 %s", path)
	}
	if err := checkRootOwnedPath("部署后置钩子 server.postDeployHook ", path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0100 == 0 {
		return fmt.Errorf("部署后置钩子缺少可执行权限 %s（当前 %04o，建议 chmod 0700）", path, info.Mode().Perm())
	}
	return nil
}

// runPostDeployHook 执行部署后置钩子，仅传入只读上下文环境变量（不传任何口令、私钥或令牌）。
func runPostDeployHook(ctx context.Context, path, phase string, s state, w io.Writer) error {
	if err := postDeployHookCheck(path); err != nil {
		return err
	}
	env := []string{
		"AURIGA_DATA=" + s.Data,
		"AURIGA_DEPLOY_PHASE=" + phase,
		"AURIGA_PACKAGE=" + s.Package,
		"AURIGA_ACCESS_FILE=" + s.Access,
	}
	if w == nil {
		w = os.Stdout
	}
	return commandContextLimit(ctx, postDeployHookTimeout, nil, w, env, path)
}

func executePostDeployHook(ctx context.Context, cfg *siteConfig, phase string, s state, rc *siteReceipt, w io.Writer) error {
	if cfg == nil || cfg.Server == nil || cfg.Server.PostDeployHook == nil {
		return nil
	}
	hook := *cfg.Server.PostDeployHook
	fmt.Fprintf(w, "执行部署后置钩子 %s（阶段 %s）…\n", hook, phase)
	if err := postDeployHookRunner(ctx, hook, phase, s, w); err != nil {
		return fmt.Errorf("部署后置钩子执行失败（%s，阶段 %s）：%w", hook, phase, err)
	}
	if rc != nil {
		rc.Notes = append(rc.Notes, fmt.Sprintf("部署后置钩子已执行：%s（阶段 %s）", hook, phase))
	}
	return nil
}

// syncUACMaterials 将受信任源目录中的 UAC 配置文件（uac.properties / adapter.json / members.json）
// 按 nasMaterialGuard 要求的权限（目录 10001:10001 0750，文件 10001:10001 0640）同步到
// <data>/config/materials/uac/。已存在的目标文件不会被覆盖。
func syncUACMaterials(data, srcDir string) error {
	if srcDir == "" {
		return nil
	}
	if err := checkConfigPath("server.install.uacMaterialsDir", srcDir); err != nil {
		return err
	}
	if err := uacMaterialsDirCheck(srcDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("读取 UAC 材料目录失败 %s：%w", srcDir, err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !allowedUACMaterialFiles[name] {
			return fmt.Errorf("UAC 材料目录含不允许的文件或目录 %s（仅允许 uac.properties、adapter.json、members.json）", filepath.Join(srcDir, name))
		}
		srcPath := filepath.Join(srcDir, name)
		if err := noSymlinks(srcPath); err != nil {
			return err
		}
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("UAC 材料必须是普通文件 %s", srcPath)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("UAC 材料不能被组或其他用户写 %s（当前 %04o）", srcPath, info.Mode().Perm())
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	dstDir := filepath.Join(data, "config", "materials", "uac")
	if err := noSymlinks(dstDir); err != nil {
		return err
	}
	if _, err := os.Lstat(dstDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dstDir, 0750); err != nil {
			return err
		}
		if err := os.Chmod(dstDir, 0750); err != nil {
			return err
		}
		if err := uacChownFunc(dstDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for _, name := range names {
		dstPath := filepath.Join(dstDir, name)
		if _, err := os.Lstat(dstPath); err == nil {
			continue // 不覆盖已存在的 UAC 材料
		} else if !os.IsNotExist(err) {
			return err
		}
		raw, err := smallFile(filepath.Join(srcDir, name), uacMaterialFileLimit)
		if err != nil {
			return fmt.Errorf("读取 UAC 材料失败 %s：%w", name, err)
		}
		f, err := openNoFollow(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(raw)
		if writeErr == nil {
			writeErr = f.Chmod(0640)
		}
		closeErr := f.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(dstPath)
			return err
		}
		if err := uacChownFunc(dstPath); err != nil {
			_ = os.Remove(dstPath)
			return err
		}
	}
	return nil
}
