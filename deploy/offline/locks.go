package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type lockInventory struct {
	Digest string            `json:"digest"`
	Files  map[string]string `json:"files"`
}
type devkitBinding struct {
	Root     string     `json:"root"`
	Digest   string     `json:"lockDigest"`
	Baseline provenance `json:"baseline"`
}

func isDependencyFile(name string) bool {
	base := filepath.Base(name)
	switch base {
	case "go.mod", "go.sum", "go.work", "go.work.sum", "pom.xml", "package.json", "package-lock.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "yarn.lock", "bun.lock", "uv.lock", "poetry.lock", "pyproject.toml", "requirements.txt", ".npmrc", ".nvmrc", ".node-version", "maven-wrapper.properties", "maven.config", "jvm.config":
		return true
	}
	return strings.HasPrefix(base, "Dockerfile") || strings.HasSuffix(base, "-requirements.txt")
}

// Git supplies the inventory, so ignored module caches and private settings
// cannot accidentally alter the dependency key. Deleted tracked locks fail.
func dependencyLocks(source string, r release) (lockInventory, error) {
	return dependencyLocksContext(context.Background(), source, r)
}

func dependencyLocksContext(ctx context.Context, source string, r release) (lockInventory, error) {
	inv := lockInventory{Files: map[string]string{}}
	names, err := sourceNamesContext(ctx, source)
	if err != nil {
		return inv, err
	}
	return locksFromNamesContext(ctx, source, r, names)
}

func locksFromNamesContext(ctx context.Context, source string, r release, names []string) (lockInventory, error) {
	inv := lockInventory{Files: map[string]string{}}
	for _, name := range names {
		if name == "" || !isDependencyFile(name) {
			continue
		}
		if !safeRelative(name) {
			return inv, errors.New("依赖清单路径无效")
		}
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := noSymlinks(path); err != nil {
			return inv, err
		}
		hash, err := sourceDigestContext(ctx, path, name, "")
		if os.IsNotExist(err) {
			inv.Files[name] = "MISSING"
			continue
		}
		if err != nil {
			return inv, err
		}
		inv.Files[name] = hash
	}
	if r.Build == nil {
		return inv, errors.New("缺少批准的构建依赖清单")
	}
	// URLs may change between release tags; their approved bytes and immutable
	// image references determine dependency identity. NAS contains versioned code
	// and is intentionally excluded from this dependency-only key.
	pins := struct {
		Inputs, Recipes     string
		Tools, Bases        map[string]string
		Images              map[string]string
		RuntimeTooling      string            `json:",omitempty"`
		RuntimeDependencies map[string]string `json:",omitempty"`
	}{Inputs: r.Build.Inputs.SHA256, Recipes: r.Build.Recipes.SHA256, Tools: r.Build.Tools, Bases: r.Build.Bases, Images: map[string]string{}}
	if r.Build.RuntimeTooling != nil {
		pins.RuntimeTooling = r.Build.RuntimeTooling.SHA256
	}
	if r.Build.RuntimeDependencies != nil {
		pins.RuntimeDependencies = map[string]string{}
		for name, a := range r.Build.RuntimeDependencies.artifacts() {
			pins.RuntimeDependencies[name] = a.SHA256
		}
	}
	for name, entry := range r.Images {
		pins.Images[name] = entry.Ref
	}
	raw, err := json.Marshal(pins)
	if err != nil {
		return inv, err
	}
	hash := sha256.Sum256(raw)
	inv.Files["@build-inputs"] = hex.EncodeToString(hash[:])
	raw, err = json.Marshal(inv.Files)
	if err != nil {
		return inv, err
	}
	hash = sha256.Sum256(raw)
	inv.Digest = hex.EncodeToString(hash[:])
	return inv, nil
}

func lockDifferences(expected, actual lockInventory) string {
	var diffs []string
	for name, hash := range expected.Files {
		value, ok := actual.Files[name]
		if !ok {
			diffs = append(diffs, "缺少 "+name)
		} else if value != hash {
			diffs = append(diffs, "不同 "+name)
		}
	}
	for name := range actual.Files {
		if _, ok := expected.Files[name]; !ok {
			diffs = append(diffs, "新增 "+name)
		}
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "\n")
}

func checkKitLocks(source string, kit devkitManifest) error {
	return checkKitLocksContext(context.Background(), source, kit)
}

func checkKitLocksContext(ctx context.Context, source string, kit devkitManifest) error {
	if kit.Locks == nil {
		return errors.New("旧 devkit 未记录锁摘要，请从当前源码标签的 Forgejo Release 下载 auriga-devkit-<版本>.tar")
	}
	current, err := dependencyLocksContext(ctx, source, kit.Release)
	if err != nil {
		return err
	}
	if current.Digest == kit.Locks.Digest {
		return nil
	}
	version := sourceVersionName(source)
	if !regexpVersion.MatchString(version) {
		version = "<当前上游版本>"
	}
	return fmt.Errorf("devkit 与当前源码依赖不匹配:\n%s\n请下载 Forgejo Release %s 的 auriga-devkit-%s.tar（或全部分卷）；自改依赖需维护者重新生成 devkit。", lockDifferences(*kit.Locks, current), version, version)
}

func readDevkit(root string) (devkitManifest, error) {
	return readDevkitContext(context.Background(), root)
}

func readDevkitContext(ctx context.Context, root string) (devkitManifest, error) {
	var kit devkitManifest
	if err := verifyDevkitCacheContext(ctx, root); err != nil {
		return kit, err
	}
	if err := readJSON(filepath.Join(root, "devkit.json"), &kit); err != nil {
		return kit, err
	}
	return kit, validateDevkit(kit)
}

func devkitVerify(args []string) error {
	root := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		root = args[0]
		args = args[1:]
	}
	f := flags("devkit verify")
	version := f.String("version", "", "")
	source := f.String("source", ".", "")
	key := f.String("verify-key", "", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 verify 参数")
	}
	sourceAbs, err := filepath.Abs(*source)
	if err != nil {
		return err
	}
	if root == "" {
		var binding devkitBinding
		if err := readJSON(filepath.Join(sourceAbs, ".auriga-devkit.json"), &binding); err == nil {
			root = binding.Root
		} else {
			root = filepath.Dir(sourceAbs)
		}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if *key == "" {
		*key, err = sourceVerificationKey(sourceAbs)
		if err != nil {
			return err
		}
	}
	if _, err := os.Lstat(filepath.Join(rootAbs, "devkit-catalog.json")); err == nil {
		return verifyCumulative(rootAbs, sourceAbs, *version, *key)
	}
	kit, err := readDevkit(rootAbs)
	if err != nil {
		return err
	}
	if err := verifySignature(rootAbs, *key); err != nil {
		return err
	}
	if err := checkKitLocks(sourceAbs, kit); err != nil {
		return err
	}
	// Reused kits retain their original source history. The checkout's upstream
	// tag is the baseline for this build; committed site changes remain visible.
	tag := sourceVersionName(sourceAbs)
	if tag == "" {
		return errors.New("当前源码需要上游版本标签")
	}
	var baseline provenance
	if _, err := os.Lstat(filepath.Join(sourceAbs, "auriga-source.json")); err == nil {
		m, err := readSourceManifest(sourceAbs)
		if err != nil {
			return err
		}
		baseline = m.Source
	} else {
		commit, err := gitAt(sourceAbs, "rev-parse", tag+"^{commit}")
		if err != nil {
			return err
		}
		baseline = provenance{Version: tag, UpstreamVersion: tag, Commit: commit}
	}
	binding := devkitBinding{Root: rootAbs, Digest: kit.Locks.Digest, Baseline: baseline}
	if err := jsonFile(filepath.Join(sourceAbs, ".auriga-devkit.json"), binding, 0644); err != nil {
		return err
	}
	fmt.Printf("PASS devkit 与当前源码依赖匹配\n锁 SHA256 %s\n上游 %s\n套件原始源码 %s\n", kit.Locks.Digest, tag, kit.Source.Commit)
	return nil
}
