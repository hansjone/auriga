package main

// 构建缓存加速（设计 D14 / 第 10 步）。
//
// 在 deps.cache 下持久化 Go 的 GOCACHE：build/go/<tool-go 镜像 digest 完整 hex>/。
// 按工具镜像分目录，换 Go 版本不复用。只缓存编译结果，不持久化 GOMODCACHE。
// 目录由 CLI 创建：root 所有、0700，父链按 deps.cache 规则校验，拒绝符号链接。
// --full 不使用该缓存（构建容器内用 scratch 临时 GOCACHE），装配带回 --no-cache。

import (
	"fmt"
	"os"
	"path/filepath"
)

const goBuildCacheMount = "/gocache"

// goBuildCacheRel 返回相对 deps.cache 的 Go 构建缓存路径。
func goBuildCacheRel(toolDigestHex string) string {
	return filepath.Join("build", "go", toolDigestHex)
}

// ensureGoBuildCache 在 deps.cache 下创建或核对 Go 构建缓存目录。
// toolDigestHex 必须是 64 位小写十六进制（tool-go 镜像 digest）。
// 返回宿主绝对路径；调用方须已确保 deps.cache 本身通过 ensureDepsCache。
func ensureGoBuildCache(cache, toolDigestHex string) (string, error) {
	if !hex64.MatchString(toolDigestHex) {
		return "", fmt.Errorf("Go 构建缓存目录名不是工具镜像 digest：%s", toolDigestHex)
	}
	if err := noSymlinks(cache); err != nil {
		return "", err
	}
	buildRoot := filepath.Join(cache, "build")
	goRoot := filepath.Join(buildRoot, "go")
	dir := filepath.Join(goRoot, toolDigestHex)
	if err := ensureGoBuildCacheDir(buildRoot); err != nil {
		return "", err
	}
	if err := ensureGoBuildCacheDir(goRoot); err != nil {
		return "", err
	}
	if err := ensureGoBuildCacheDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureGoBuildCacheDir 创建或核对单个缓存目录：0700、无符号链接、root 所有、组和其他不可写、祖先受信。
// 必须先做 checkRootOwnedDir 再收紧权限，防止已有组/其他可写目录被静默 chmod 掩盖篡改窗口。
func ensureGoBuildCacheDir(dir string) error {
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
	}
	if err := checkRootOwnedDir("Go 构建缓存 ", dir); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// planGoBuildCache 给出 dry-run / 收据用的计划路径（不创建目录）。
func planGoBuildCache(cache, toolDigestHex string) string {
	if cache == "" || !hex64.MatchString(toolDigestHex) {
		return ""
	}
	return filepath.Join(cache, goBuildCacheRel(toolDigestHex))
}

// decideAssembly 按装配摘要判定某服务是否需要重新装配镜像。
// 非首装、非 --full 且装配摘要与上一版一致、旧镜像仍在本机时直接跳过，不调用 assembleImage，
// 也不受层缓存产出的镜像 ID 是否变化影响。
func decideAssembly(first, full bool, svc string, prev *deployRecord, parts assemblyParts, hasLocal func(string) bool) (bool, string) {
	if first {
		return true, "首次安装"
	}
	if full {
		return true, "--full 强制重建"
	}
	if prev != nil && prev.Status != recordOK {
		return true, fmt.Sprintf("上一版 r%d 状态为 %s，重新装配", prev.Seq, prev.Status)
	}
	if prev != nil && prev.Assembly[svc] == parts.Total {
		if hasLocal(prev.Images[imageKey(svc)]) {
			return false, fmt.Sprintf("装配摘要与 r%d 相同，跳过切换", prev.Seq)
		}
		return true, "装配摘要相同但旧镜像已不在本机"
	}
	var oldTotal string
	var oldParts *assemblyParts
	if prev != nil {
		oldTotal = prev.Assembly[svc]
		if p, ok := prev.AssemblyParts[svc]; ok {
			oldParts = &p
		}
	}
	return true, assemblyChangeReason(oldTotal, oldParts, parts)
}
