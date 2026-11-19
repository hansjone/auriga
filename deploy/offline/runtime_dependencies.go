package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var runtimeDependencySources = []string{"package.json", "pnpm-workspace.yaml", ".npmrc", "web/pnpm-lock.yaml", "clients/dsh-plugin/package.json", "clients/dsh-plugin/package-lock.json", "deploy/compose/real-dsh/package.json", "deploy/compose/real-dsh/package-lock.json", "deploy/compose/real-dsh/versions.json"}

func verifyRuntimeDependenciesInput(ctx context.Context, source, directory string, inputs *runtimeDependencyInputs, runtimeImage string) error {
	var m struct {
		Schema      int    `json:"schemaVersion"`
		Platform    string `json:"platform"`
		NodeVersion string `json:"nodeVersion"`
		NpmVersion  string `json:"npmVersion"`
		NodeImage   struct {
			Ref    string `json:"ref"`
			Config string `json:"config"`
		} `json:"nodeImage"`
		PackageManager string            `json:"packageManager"`
		Locks          map[string]string `json:"locks"`
		WebSeed        string            `json:"webSourceLocksSha256"`
		Files          map[string]struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	raw, err := smallFileContext(ctx, filepath.Join(directory, "runtime-dependencies.json"), 1<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	var v struct {
		NodeVersion string `json:"nodeVersion"`
		NpmVersion  string `json:"npmVersion"`
		NodeImage   string `json:"nodeImage"`
	}
	raw, err = smallFileContext(ctx, filepath.Join(source, "deploy/compose/real-dsh/versions.json"), 64<<10)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	var p struct {
		Manager string `json:"packageManager"`
	}
	raw, err = smallFileContext(ctx, filepath.Join(source, "package.json"), 1<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	config, ok := strings.CutPrefix(m.NodeImage.Config, "sha256:")
	imageIdentity := runtimeImage
	if at := strings.LastIndex(imageIdentity, "@"); at >= 0 {
		imageIdentity = imageIdentity[at+1:]
	}
	sourceIdentity := v.NodeImage
	if at := strings.LastIndex(sourceIdentity, "@"); at >= 0 {
		sourceIdentity = sourceIdentity[at+1:]
	}
	if m.Schema != 1 || m.Platform != "linux/amd64" || m.NodeVersion != v.NodeVersion || m.NpmVersion != v.NpmVersion || m.NodeImage.Ref != v.NodeImage || !ok || !hex64.MatchString(config) || m.PackageManager != p.Manager || !hex64.MatchString(m.WebSeed) || len(m.Locks) != len(runtimeDependencySources) || (imageIdentity != sourceIdentity && imageIdentity != m.NodeImage.Config) {
		return errors.New("DSH冷构建输入工具链/镜像映射/源码锁不符")
	}
	for _, name := range runtimeDependencySources {
		value, err := digestContext(ctx, filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if m.Locks[name] != value {
			return errors.New("DSH冷构建输入源码依赖锁不符: " + name)
		}
	}
	files, err := regularFilesContext(ctx, directory)
	if err != nil {
		return err
	}
	if len(files) != 4 || len(m.Files) != 3 {
		return errors.New("DSH冷构建输入必须只含四个固定文件")
	}
	keys := map[string]string{"corepack": "corepack.tgz", "webCache": "web-cache.tar.gz", "npmCache": "npm-cache.tar.gz"}
	for name, a := range inputs.artifacts() {
		actual, err := digestContext(ctx, filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if a.SHA256 != actual || a.URL != "runtime-dependencies/"+name {
			return errors.New("DSH冷构建发行输入摘要不符")
		}
	}
	for key, name := range keys {
		r, ok := m.Files[key]
		if !ok || r.Path != name || !hex64.MatchString(r.SHA256) {
			return errors.New("DSH冷构建缓存清单无效")
		}
		path := filepath.Join(directory, name)
		stat, err := os.Stat(path)
		if err != nil {
			return err
		}
		actual, err := digestContext(ctx, path)
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || r.Bytes != stat.Size() || r.Bytes <= 0 || actual != r.SHA256 {
			return errors.New("DSH冷构建缓存字节不符")
		}
	}
	return nil
}
