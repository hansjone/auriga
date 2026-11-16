package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type buildOptions struct {
	Offline       bool
	Cache, Source string
	Info          *provenance
}
type kitImage struct {
	Role string `json:"role"`
	Ref  string `json:"ref"`
	Tag  string `json:"tag"`
	File string `json:"file"`
}
type devkitManifest struct {
	Locks   *lockInventory `json:"locks,omitempty"`
	Schema  int            `json:"schemaVersion"`
	Release release        `json:"release"`
	Images  []kitImage     `json:"images"`
	Source  *provenance    `json:"source"`
}

func buildImageRefs(r release) (map[string]string, error) {
	if r.Schema != 1 || !regexpVersion.MatchString(r.Version) {
		return nil, errors.New("开发套件发行版本无效")
	}
	if r.Build == nil {
		return nil, errors.New("发行索引缺少 build 依赖和 Forgejo 配方")
	}
	for _, name := range []string{"go", "web", "java"} {
		if !immutable.MatchString(r.Build.Tools[name]) {
			return nil, fmt.Errorf("缺少固定摘要的构建工具镜像 %s", name)
		}
	}
	for _, name := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		if !immutable.MatchString(r.Build.Bases[name]) {
			return nil, fmt.Errorf("缺少固定摘要的构建基础镜像 %s", name)
		}
	}
	for _, name := range []string{"postgres", "nats", "registry"} {
		if !immutable.MatchString(r.Images[name].Ref) {
			return nil, fmt.Errorf("缺少固定摘要的服务镜像 %s", name)
		}
	}
	refs := map[string]string{}
	for name, entry := range r.Images {
		refs["service-"+name] = entry.Ref
	}
	for name, ref := range r.Build.Tools {
		refs["tool-"+name] = ref
	}
	for name, ref := range r.Build.Bases {
		refs["base-"+name] = ref
	}
	for role, ref := range refs {
		_, name, ok := strings.Cut(role, "-")
		if !ok || !regexpName.MatchString(name) || !immutable.MatchString(ref) {
			return nil, fmt.Errorf("构建镜像用途或摘要无效 %s", role)
		}
	}
	return refs, nil
}

func kitImageTag(role, ref string) string {
	return "auriga/devkit-" + role + ":" + ref[strings.LastIndex(ref, "sha256:")+7:][:24]
}

func validateDevkit(kit devkitManifest) error {
	if kit.Schema != 1 || kit.Source == nil {
		return errors.New("离线开发套件缺少版本来源")
	}
	refs, err := buildImageRefs(kit.Release)
	if err != nil {
		return err
	}
	if len(kit.Images) != len(refs) {
		return errors.New("开发套件镜像清单不完整")
	}
	seen := map[string]bool{}
	for _, entry := range kit.Images {
		ref, ok := refs[entry.Role]
		if !ok || seen[entry.Role] || ref != entry.Ref || entry.File != "images/"+entry.Role+".tar" || entry.Tag != kitImageTag(entry.Role, ref) {
			return errors.New("开发套件镜像用途、路径或摘要不符")
		}
		seen[entry.Role] = true
	}
	return nil
}

func fetchInput(a artifact, target string, options buildOptions) error {
	if !options.Offline {
		return fetch(a, target)
	}
	if !safeRelative(a.URL) || a.URL == "SHA256SUMS" {
		return fmt.Errorf("离线依赖必须使用套件缓存相对路径 %s", a.URL)
	}
	source := filepath.Join(options.Cache, filepath.FromSlash(a.URL))
	if err := noSymlinks(source); err != nil {
		return err
	}
	if err := copyFile(source, target, 0644); err != nil {
		return fmt.Errorf("缺少离线依赖 %s %w", a.URL, err)
	}
	hash, err := digest(target)
	if err != nil {
		return err
	}
	if hash != a.SHA256 {
		return fmt.Errorf("离线依赖摘要不符 %s", a.URL)
	}
	return nil
}

// Source and Git state may change at the site. All original cache bytes and the
// manifest remain bound to the devkit's original complete checksum inventory.
func verifyDevkitCache(root string) error {
	if err := noSymlinks(root); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(f)
	seen := map[string]bool{}
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 67 || line[64:66] != "  " || !hex64.MatchString(line[:64]) || !safeRelative(line[66:]) {
			return errors.New("devkit 摘要清单无效")
		}
		name := line[66:]
		if seen[name] {
			return errors.New("devkit 摘要条目重复")
		}
		seen[name] = true
		if name != "devkit.json" && !strings.HasPrefix(name, "cache/") {
			continue
		}
		if err := noSymlinks(filepath.Join(root, name)); err != nil {
			return err
		}
		hash, err := digest(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("缺少离线依赖 %s %w", name, err)
		}
		if hash != line[:64] {
			return fmt.Errorf("离线依赖摘要不符 %s", name)
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if !seen["devkit.json"] {
		return errors.New("devkit.json 未受校验")
	}
	names, err := regularFiles(filepath.Join(root, "cache"))
	if err != nil {
		return err
	}
	for _, name := range names {
		if !seen["cache/"+name] {
			return fmt.Errorf("未批准的离线依赖 %s", name)
		}
	}
	return nil
}

func locateDevkit(source string) (string, *devkitManifest, error) {
	root := filepath.Dir(source)
	var binding devkitBinding
	bound := false
	if _, err := os.Lstat(filepath.Join(source, ".auriga-devkit.json")); err == nil {
		if err := readJSON(filepath.Join(source, ".auriga-devkit.json"), &binding); err != nil {
			return "", nil, err
		}
		root = binding.Root
		bound = true
	}
	if err := verifyDevkitCache(root); err != nil {
		return "", nil, err
	}
	var kit devkitManifest
	if err := readJSON(filepath.Join(root, "devkit.json"), &kit); err != nil {
		return "", nil, err
	}
	if err := validateDevkit(kit); err != nil {
		return "", nil, err
	}
	if kit.Locks != nil {
		if err := checkKitLocks(source, kit); err != nil {
			return "", nil, err
		}
	}
	if bound {
		if kit.Locks == nil || binding.Digest != kit.Locks.Digest {
			return "", nil, errors.New("devkit 绑定已过期，重新运行 devkit verify")
		}
		kit.Source = &binding.Baseline
		kit.Release.Version = binding.Baseline.Version
	}
	return root, &kit, nil
}

func devkit(args []string) error {
	if len(args) > 0 && args[0] == "verify" {
		return devkitVerify(args[1:])
	}
	f := flags("devkit")
	index := f.String("release", "auriga-release.json", "")
	out := f.String("out", "", "")
	verify := f.String("verify", "", "")
	sign := f.String("sign-key", "", "")
	key := f.String("verify-key", "", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 devkit 参数")
	}
	if *verify != "" {
		if err := verifySums(*verify); err != nil {
			return err
		}
		return verifySignature(*verify, *key)
	}
	if *out == "" {
		return errors.New("devkit 需要 --out 套件.tar")
	}
	var r release
	if err := readJSON(*index, &r); err != nil {
		return err
	}
	refs, err := buildImageRefs(r)
	if err != nil {
		return err
	}
	if err := dockerReady(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "auriga-devkit-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	root := filepath.Join(stage, "kit")
	if err := os.Mkdir(root, 0755); err != nil {
		return err
	}
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(cache, 0755); err != nil {
		return err
	}
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	snapshot := filepath.Join(stage, "snapshot")
	info, err := snapshotSource(source, snapshot, *sign)
	if err != nil {
		return err
	}
	if err := sourceVersion(info, r, "", false); err != nil {
		return err
	}
	kit := devkitManifest{Schema: 1, Release: r, Source: info}
	locks, err := dependencyLocks(source, r)
	if err != nil {
		return err
	}
	kit.Locks = &locks
	for name, a := range map[string]*artifact{"nas": &kit.Release.NAS, "inputs": &kit.Release.Build.Inputs, "recipes": &kit.Release.Build.Recipes, "desktop": kit.Release.Desktop, "seeds": kit.Release.Seeds} {
		if a == nil {
			continue
		}
		file := name + ".tar"
		if err := fetch(*a, filepath.Join(cache, file)); err != nil {
			return fmt.Errorf("devkit 依赖 %s %w", name, err)
		}
		a.URL = file
	}
	// A source bundle carries reachable history and upstream version tags without
	// worktree pointers, global Git config, hooks or remote credential material.
	history := filepath.Join(cache, "source-history.bundle")
	if err := command(nil, os.Stdout, nil, "git", "-C", source, "bundle", "create", history, "HEAD", "--tags"); err != nil {
		return err
	}
	checkout := filepath.Join(root, "source")
	if err := command(nil, os.Stdout, nil, "git", "clone", "--no-checkout", history, checkout); err != nil {
		return err
	}
	branch := info.Branch
	if branch == "HEAD" {
		branch = "site/local"
	}
	if err := command(nil, os.Stdout, nil, "git", "-C", checkout, "checkout", "-B", branch, info.Commit); err != nil {
		return err
	}
	tracked, err := gitFiles(checkout, "ls-files", "-z")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(tracked, "\x00") {
		if name == "" {
			continue
		}
		if err := os.Remove(filepath.Join(checkout, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	names, err := regularFiles(snapshot)
	if err != nil {
		return err
	}
	for _, name := range names {
		st, err := os.Stat(filepath.Join(snapshot, name))
		if err != nil {
			return err
		}
		if err := copyFile(filepath.Join(snapshot, name), filepath.Join(checkout, name), st.Mode().Perm()); err != nil {
			return err
		}
	}
	if _, err := gitAt(checkout, "add", "--all"); err != nil {
		return err
	}
	if _, err := gitAt(checkout, "remote", "remove", "origin"); err != nil {
		return err
	}
	bootRoot := filepath.Join(stage, "nas")
	if err := os.Mkdir(bootRoot, 0755); err != nil {
		return err
	}
	if err := unpack(filepath.Join(cache, "nas.tar"), bootRoot); err != nil {
		return err
	}
	boot := filepath.Join(bootRoot, "deploy-nas/bin/auriga-nas-bootstrap")
	if err := command(nil, os.Stdout, nil, boot, "image-check", bootRoot); err != nil {
		return err
	}
	roles := make([]string, 0, len(refs))
	for role := range refs {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		ref := refs[role]
		if strings.Contains(ref, "@sha256:") {
			if err := command(nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", ref); err != nil {
				return err
			}
		}
		tag := kitImageTag(role, ref)
		file := "images/" + role + ".tar"
		if err := os.MkdirAll(filepath.Join(cache, "images"), 0755); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "docker", "tag", ref, tag); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "docker", "save", "--platform", "linux/amd64", "-o", filepath.Join(cache, file), tag); err != nil {
			return err
		}
		if _, err := capture(boot, "image-extra-resolve", filepath.Join(cache, file), tag); err != nil {
			return err
		}
		kit.Images = append(kit.Images, kitImage{Role: role, Ref: ref, Tag: tag, File: file})
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := copyFile(exe, filepath.Join(checkout, "bin/auriga-cli"), 0755); err != nil {
		return err
	}
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		return err
	}
	if err := writeSums(root); err != nil {
		return err
	}
	if *sign != "" {
		if err := signSums(root, *sign); err != nil {
			return err
		}
	}
	if err := pack(root, *out); err != nil {
		return err
	}
	hash, err := digest(*out)
	if err != nil {
		return err
	}
	fmt.Printf("离线开发套件 %s\nSHA256 %s\n解包后先在 source 中运行 ./auriga devkit --verify ..，再修改源码。\n", *out, hash)
	return os.WriteFile(*out+".sha256", []byte(hash+"\n"), 0644)
}

func loadDevkitImages(root string, kit devkitManifest, boot string) (release, error) {
	if err := validateDevkit(kit); err != nil {
		return release{}, err
	}
	raw, err := json.Marshal(kit.Release)
	if err != nil {
		return release{}, err
	}
	var r release
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	for _, entry := range kit.Images {
		file := filepath.Join(root, "cache", entry.File)
		if err := command(nil, os.Stdout, nil, "docker", "load", "-i", file); err != nil {
			return r, err
		}
		id, err := capture(boot, "image-extra-resolve", file, entry.Tag)
		if err != nil {
			return r, err
		}
		kind, name, ok := strings.Cut(entry.Role, "-")
		if !ok {
			return r, errors.New("套件镜像用途无效")
		}
		switch kind {
		case "service":
			image := r.Images[name]
			image.Ref = id
			r.Images[name] = image
		case "tool":
			r.Build.Tools[name] = id
		case "base":
			r.Build.Bases[name] = entry.Tag
		default:
			return r, errors.New("未知套件镜像用途")
		}
	}
	return r, nil
}
