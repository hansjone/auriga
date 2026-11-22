package main

// CI 覆盖：每个受 git 跟踪的文件要么属于某个输入集展开，要么在 services.json 的 ignore 里。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func pathMatchesIgnore(path string, patterns []string) bool {
	path = filepath.ToSlash(path)
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		if matchGlob(p, path) {
			return true
		}
	}
	return false
}

// matchGlob 支持 ** 与 * 的简单 glob（路径用 /）。
func matchGlob(pattern, name string) bool {
	if pattern == "**" || pattern == "**/*" {
		return true
	}
	if strings.HasPrefix(pattern, "**/") {
		suf := strings.TrimPrefix(pattern, "**/")
		if matchGlob(suf, name) {
			return true
		}
		parts := strings.Split(name, "/")
		for i := range parts {
			if matchGlob(suf, strings.Join(parts[i:], "/")) {
				return true
			}
		}
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		pre := strings.TrimSuffix(pattern, "/**")
		return name == pre || strings.HasPrefix(name, pre+"/")
	}
	return globMatch(pattern, name)
}

func globMatch(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	if err == nil && ok {
		return true
	}
	// 也尝试只匹配最后一段（如 *.md）
	if strings.Contains(pattern, "/") {
		return false
	}
	base := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		base = name[i+1:]
	}
	ok, err = filepath.Match(pattern, base)
	return err == nil && ok
}

func checkCoverage(files []string, covered map[string]bool, ignore []string) error {
	var missing []string
	for _, f := range files {
		f = filepath.ToSlash(f)
		if covered[f] || pathMatchesIgnore(f, ignore) {
			continue
		}
		missing = append(missing, f)
		if len(missing) >= 20 {
			break
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("以下受跟踪文件未归入任何输入集，也不在 services.json ignore 中（最多列 20 个）：%s", strings.Join(missing, "、"))
}

// expandInputCoverage 展开 services.json 输入集覆盖的相对路径（尽量全面；go-program 用包目录）。
func expandInputCoverage(source string, spec *siteServices, programs *goProgramsFile) (map[string]bool, error) {
	out := map[string]bool{}
	mark := func(rel string) {
		rel = filepath.ToSlash(rel)
		if rel != "" {
			out[rel] = true
		}
	}
	markTree := func(rel string) error {
		path := filepath.Join(source, filepath.FromSlash(rel))
		st, err := os.Lstat(path)
		if err != nil {
			return nil // 缺失不阻断覆盖收集
		}
		if st.IsDir() {
			return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				r, err := filepath.Rel(source, p)
				if err != nil {
					return err
				}
				mark(r)
				return nil
			})
		}
		mark(rel)
		return nil
	}
	for _, is := range spec.InputSets {
		switch is.Kind {
		case "pack-list":
			entries, err := readPackList(source, is.File)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				_ = markTree(e)
			}
			mark(is.File)
		case "go-mod-files":
			_ = filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					switch d.Name() {
					case ".git", "node_modules", "target", "dist":
						return filepath.SkipDir
					}
					return nil
				}
				switch d.Name() {
				case "go.mod", "go.sum", "go.work", "go.work.sum":
					r, _ := filepath.Rel(source, p)
					mark(r)
				}
				return nil
			})
		case "go-program":
			if programs == nil {
				continue
			}
			prog, ok := programs.Programs[is.Program]
			if !ok {
				continue
			}
			_ = markTree(prog.Directory)
			if prog.Workspace != "off" {
				mark(prog.Workspace)
			}
		case "assemble":
			// 装配输入：配方、Dockerfile.assembly 与配方 source 项声明的源码文件（与 digestAssembleInputs 同源）。
			mark("deploy/nas/offline-build/deploy-recipe.json")
			mark("deploy/nas/offline-build/Dockerfile.assembly")
			mark("deploy/nas/offline-build/upstream-recipes.sha256")
			recipe, err := loadDeployRecipe(source)
			if err != nil {
				return nil, err
			}
			for _, it := range recipe.Services[is.Service].Stage {
				for _, rel := range it.Sources {
					mark(rel)
				}
			}
		case "tool-image":
			mark("deploy/nas/offline-build/" + strings.TrimPrefix(is.Role, "tool-") + "-inputs.sh")
		}
	}
	mark(siteServicesRel)
	mark(siteGoProgramsRel)
	mark("deploy/site/input-sets/web-pack.list")
	mark("deploy/site/input-sets/java-pack.list")
	return out, nil
}

func gitTrackedFiles(source string) ([]string, error) {
	cmd := exec.Command("git", "-C", source, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			files = append(files, filepath.ToSlash(name))
		}
	}
	return files, nil
}
