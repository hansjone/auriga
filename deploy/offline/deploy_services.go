package main

// deploy/site/services.json：镜像 → 产物、产物 → 输入集（设计 D6）。不列源码路径；
// 路径由 input-sets 共享常量与 go list -deps 展开。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	siteServicesRel   = "deploy/site/services.json"
	siteGoProgramsRel = "deploy/site/input-sets/go-programs.json"
)

type siteServices struct {
	Schema    int                     `json:"schemaVersion"`
	Images    map[string]siteImage    `json:"images"`
	Artifacts map[string]siteArtifact `json:"artifacts"`
	InputSets map[string]siteInputSet `json:"inputSets"`
	Ignore    []string                `json:"ignore"`
}

type siteImage struct {
	Pseudo    bool     `json:"pseudo,omitempty"`
	Artifacts []string `json:"artifacts"`
}

type siteArtifact struct {
	InputSets []string `json:"inputSets"`
}

type siteInputSet struct {
	Kind      string `json:"kind"` // go-program | go-mod-files | pack-list | deps-lock | tool-image | assemble
	Program   string `json:"program,omitempty"`
	File      string `json:"file,omitempty"`
	Ecosystem string `json:"ecosystem,omitempty"`
	Role      string `json:"role,omitempty"`
	Service   string `json:"service,omitempty"`
}

type goProgramsFile struct {
	Schema   int                      `json:"schemaVersion"`
	Programs map[string]goProgramSpec `json:"programs"`
}

type goProgramSpec struct {
	Directory string   `json:"directory"`
	Workspace string   `json:"workspace"` // 相对源码根，或 "off"
	Packages  []string `json:"packages"`
}

func loadSiteServices(source string) (*siteServices, error) {
	raw, err := smallFile(filepath.Join(source, filepath.FromSlash(siteServicesRel)), 2<<20)
	if err != nil {
		return nil, fmt.Errorf("缺少服务划分文件 %s：%w", siteServicesRel, err)
	}
	var s siteServices
	if err := decodeStrictJSON(raw, &s); err != nil {
		return nil, fmt.Errorf("services.json 无效：%w", err)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *siteServices) validate() error {
	if s.Schema != 1 {
		return fmt.Errorf("services.json schemaVersion 必须是 1（当前 %d）", s.Schema)
	}
	wantImages := append(append([]string{}, deployServices...), "auriga-nas-bootstrap", "runtime-release")
	if len(s.Images) != len(wantImages) {
		return fmt.Errorf("services.json images 必须恰好是 %s", strings.Join(wantImages, "、"))
	}
	for _, name := range wantImages {
		img, ok := s.Images[name]
		if !ok {
			return fmt.Errorf("services.json 缺少镜像 %s", name)
		}
		pseudo := name == "auriga-nas-bootstrap" || name == "runtime-release"
		if img.Pseudo != pseudo {
			return fmt.Errorf("services.json 镜像 %s 的 pseudo 应为 %v", name, pseudo)
		}
		if len(img.Artifacts) == 0 {
			return fmt.Errorf("services.json 镜像 %s 的 artifacts 不能为空", name)
		}
		for _, a := range img.Artifacts {
			if _, ok := s.Artifacts[a]; !ok {
				return fmt.Errorf("services.json 镜像 %s 引用未知产物 %s", name, a)
			}
		}
	}
	for name, a := range s.Artifacts {
		if len(a.InputSets) == 0 {
			return fmt.Errorf("services.json 产物 %s 的 inputSets 不能为空", name)
		}
		for _, set := range a.InputSets {
			is, ok := s.InputSets[set]
			if !ok {
				return fmt.Errorf("services.json 产物 %s 引用未知输入集 %s", name, set)
			}
			if err := is.validate(); err != nil {
				return fmt.Errorf("services.json 输入集 %s：%w", set, err)
			}
		}
	}
	for name, is := range s.InputSets {
		if err := is.validate(); err != nil {
			return fmt.Errorf("services.json 输入集 %s：%w", name, err)
		}
	}
	return nil
}

func (is siteInputSet) validate() error {
	switch is.Kind {
	case "go-program":
		if is.Program == "" || is.File != "" || is.Ecosystem != "" || is.Role != "" || is.Service != "" {
			return fmt.Errorf("go-program 只接受 program")
		}
	case "go-mod-files":
		if is.Program != "" || is.File != "" || is.Ecosystem != "" || is.Role != "" || is.Service != "" {
			return fmt.Errorf("go-mod-files 不接受其它字段")
		}
	case "pack-list":
		if is.File == "" || !safeRelative(is.File) || is.Program != "" || is.Ecosystem != "" || is.Role != "" || is.Service != "" {
			return fmt.Errorf("pack-list 只接受安全相对路径 file")
		}
	case "deps-lock":
		if is.Ecosystem == "" || is.Program != "" || is.File != "" || is.Role != "" || is.Service != "" {
			return fmt.Errorf("deps-lock 只接受 ecosystem")
		}
	case "tool-image":
		if is.Role == "" || is.Program != "" || is.File != "" || is.Ecosystem != "" || is.Service != "" {
			return fmt.Errorf("tool-image 只接受 role")
		}
	case "assemble":
		if is.Service == "" || is.Program != "" || is.File != "" || is.Ecosystem != "" || is.Role != "" {
			return fmt.Errorf("assemble 只接受 service")
		}
	default:
		return fmt.Errorf("未知 kind %q", is.Kind)
	}
	return nil
}

func loadGoPrograms(source string) (*goProgramsFile, error) {
	raw, err := smallFile(filepath.Join(source, filepath.FromSlash(siteGoProgramsRel)), 1<<20)
	if err != nil {
		return nil, fmt.Errorf("缺少 Go 程序表 %s：%w", siteGoProgramsRel, err)
	}
	var g goProgramsFile
	if err := decodeStrictJSON(raw, &g); err != nil {
		return nil, fmt.Errorf("go-programs.json 无效：%w", err)
	}
	if g.Schema != 1 {
		return nil, fmt.Errorf("go-programs.json schemaVersion 必须是 1（当前 %d）", g.Schema)
	}
	if len(g.Programs) == 0 {
		return nil, fmt.Errorf("go-programs.json programs 不能为空")
	}
	for name, p := range g.Programs {
		if !safeRelative(p.Directory) || len(p.Packages) == 0 {
			return nil, fmt.Errorf("go-programs.json 程序 %s 无效", name)
		}
		if p.Workspace != "off" && !safeRelative(p.Workspace) {
			return nil, fmt.Errorf("go-programs.json 程序 %s 的 workspace 无效", name)
		}
	}
	return &g, nil
}

// readPackList 读取共享打包清单（忽略空行与 # 注释）。
func readPackList(source, rel string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !safeRelative(line) {
			return nil, fmt.Errorf("打包清单 %s 含不安全路径 %q", rel, line)
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("打包清单 %s 为空", rel)
	}
	return out, nil
}

func (s *siteServices) imageArtifacts(name string) []string {
	img := s.Images[name]
	out := append([]string{}, img.Artifacts...)
	sort.Strings(out)
	return out
}

func (s *siteServices) artifactInputSets(name string) []string {
	a := s.Artifacts[name]
	out := append([]string{}, a.InputSets...)
	sort.Strings(out)
	return out
}

// inputSetUsers 返回使用该输入集的产物名（排序）。
func (s *siteServices) inputSetUsers(set string) []string {
	var out []string
	for name, a := range s.Artifacts {
		for _, x := range a.InputSets {
			if x == set {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// imageInputSets 返回镜像全部产物的输入集并集。
func (s *siteServices) imageInputSets(image string) map[string]bool {
	sets := map[string]bool{}
	for _, a := range s.Images[image].Artifacts {
		for _, set := range s.Artifacts[a].InputSets {
			sets[set] = true
		}
	}
	return sets
}
