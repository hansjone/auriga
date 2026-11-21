package main

// 按生态的源码锁清单（source-locks.sha256）与锁摘要。
//
// 内容逐字节复现 deploy/nas/offline-build/<生态>-inputs.sh 的 locks 模式输出
// （`sha256sum` 格式，LC_ALL=C 排序），这样 deps status 不需要 Docker 和工具镜像。
// 锁摘要 = 该清单文件内容的 SHA-256，也就是缓存目录 inputs/<生态>/<锁摘要>/ 的名字。
// 规则与脚本不同步时不会放过错误依赖：deps fetch 在准备后逐字节比较脚本写出的
// source-locks.sha256，构建时脚本自己还会 cmp；deps_locks_linux_test.go 直接拿脚本
// 片段在真实源码上对照。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// depsEcosystems 是缓存里按锁摘要整套存放的依赖输入生态。
var depsEcosystems = []string{"go", "web", "java", "system"}

// emptySHA256Line 复现 GNU xargs 在没有输入时仍运行一次 sha256sum（读空 stdin）的输出。
const emptySHA256Line = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  -\n"

type lockRule struct {
	roots   []string        // find 起点
	names   map[string]bool // find -name
	exclude bool            // 是否排除 node_modules、dist、target（web/java 的 tar --exclude）
	fixed   []string        // 追加的固定文件（缺失即失败）
	// rootsRequired：web/java 先 tar 复制工作区，起点缺失会失败；go 的 find 缺失起点被忽略。
	rootsRequired bool
}

var lockRules = map[string]lockRule{
	"go": {
		roots: []string{"infra", "deploy/nas/bootstrap", "deploy/bootstrap", "third_party/bifrost/core"},
		names: map[string]bool{"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true},
	},
	"web": {
		roots: []string{"web", "clients"}, names: map[string]bool{"package.json": true}, exclude: true, rootsRequired: true,
		fixed: []string{"package.json", "pnpm-workspace.yaml", ".npmrc", "web/pnpm-lock.yaml"},
	},
	"java": {
		roots: []string{"services"}, names: map[string]bool{"pom.xml": true}, exclude: true, rootsRequired: true,
		fixed: []string{"pom.xml", "mvnw", ".mvn/wrapper/maven-wrapper.properties", ".mvn/wrapper/maven-wrapper.jar", "services/contracts/package.json", "services/contracts/package-lock.json"},
	},
	// system 没有 locks 模式：系统包由基础镜像决定，只能导入；以配方脚本与 operator 的
	// requirements 作为锁清单，二者任一变化都要求导入新的系统输入。
	"system": {
		fixed: []string{"deploy/nas/offline-build/system-inputs.sh", "deploy/nas/test/admin-bootstrap-requirements.txt"},
	},
}

var lockExcluded = map[string]bool{"node_modules": true, "dist": true, "target": true}

// ecosystemLocks 返回某生态的 source-locks.sha256 内容。
func ecosystemLocks(source, eco string) ([]byte, error) {
	rule, ok := lockRules[eco]
	if !ok {
		return nil, fmt.Errorf("未知生态 %s", eco)
	}
	var found []string
	for _, root := range rule.roots {
		dir := filepath.Join(source, filepath.FromSlash(root))
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) && !rule.rootsRequired {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s 锁清单缺少目录 %s: %w", eco, root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s 锁清单起点不是目录 %s", eco, root)
		}
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if rule.exclude && p != dir && lockExcluded[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !rule.names[d.Name()] {
				return nil
			}
			rel, err := filepath.Rel(source, p)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if len(rule.roots) > 0 {
		sort.Strings(found)
		if len(found) == 0 {
			out.WriteString(emptySHA256Line)
		}
		for _, name := range found {
			if err := appendLockLine(&out, source, name); err != nil {
				return nil, err
			}
		}
	}
	for _, name := range rule.fixed {
		if err := appendLockLine(&out, source, name); err != nil {
			return nil, fmt.Errorf("%s 锁清单缺少 %s: %w", eco, name, err)
		}
	}
	return out.Bytes(), nil
}

func appendLockLine(out *bytes.Buffer, source, name string) error {
	if strings.ContainsAny(name, "\\\n\r") {
		return fmt.Errorf("锁文件路径含不支持的字符 %q", name)
	}
	path := filepath.Join(source, filepath.FromSlash(name))
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("锁文件不是普通文件 %s", name)
	}
	hash, err := digest(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s  %s\n", hash, name)
	return nil
}

func lockDigestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type ecosystemLock struct {
	Ecosystem string
	Digest    string
	Raw       []byte
}

// sourceEcosystemLocks 逐生态计算锁清单与摘要。
func sourceEcosystemLocks(source string) ([]ecosystemLock, error) {
	if err := noSymlinks(source); err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("源码路径不是目录 " + source)
	}
	var out []ecosystemLock
	for _, eco := range depsEcosystems {
		raw, err := ecosystemLocks(source, eco)
		if err != nil {
			return nil, err
		}
		out = append(out, ecosystemLock{Ecosystem: eco, Digest: lockDigestOf(raw), Raw: raw})
	}
	return out, nil
}
