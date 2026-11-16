package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type devkitVersion struct {
	Commit string            `json:"commit"`
	Kit    devkitManifest    `json:"kit"`
	Files  map[string]string `json:"files"`
}
type devkitCatalog struct {
	Schema      int                      `json:"schemaVersion"`
	Baseline    string                   `json:"baseline"`
	Latest      string                   `json:"latest"`
	ResetReason string                   `json:"resetReason,omitempty"`
	Versions    map[string]devkitVersion `json:"versions"`
	Blobs       map[string]int64         `json:"blobs"`
}
type devkitDelta struct {
	Schema       int      `json:"schemaVersion"`
	Baseline     string   `json:"baseline"`
	BaseSHA256   string   `json:"baseSha256"`
	TargetSHA256 string   `json:"targetSha256"`
	Reset        bool     `json:"reset"`
	Added        []string `json:"added"`
}

func validateCatalog(c devkitCatalog) error {
	if c.Schema != 1 || !regexpVersion.MatchString(c.Baseline) || !regexpVersion.MatchString(c.Latest) || len(c.Versions) == 0 {
		return errors.New("累积devkit清单无效")
	}
	if _, ok := c.Versions[c.Baseline]; !ok {
		return errors.New("累积devkit缺少基线版本")
	}
	if _, ok := c.Versions[c.Latest]; !ok {
		return errors.New("累积devkit缺少当前版本")
	}
	for version, entry := range c.Versions {
		if !regexpVersion.MatchString(version) || len(entry.Commit) != 40 || entry.Kit.Locks == nil {
			return errors.New("累积devkit版本/锁清单无效")
		}
		if err := validateDevkit(entry.Kit); err != nil {
			return err
		}
		for name, hash := range entry.Files {
			if !safeRelative(name) || !hex64.MatchString(hash) {
				return errors.New("累积devkit依赖路径无效")
			}
			if _, ok := c.Blobs[hash]; !ok {
				return fmt.Errorf("累积devkit缺少依赖块 %s", hash)
			}
		}
		needed := []string{entry.Kit.Release.NAS.URL, entry.Kit.Release.Build.Inputs.URL, entry.Kit.Release.Build.Recipes.URL}
		for _, image := range entry.Kit.Images {
			needed = append(needed, image.File)
		}
		if entry.Kit.Release.Desktop != nil {
			needed = append(needed, entry.Kit.Release.Desktop.URL)
		}
		if entry.Kit.Release.Seeds != nil {
			needed = append(needed, entry.Kit.Release.Seeds.URL)
		}
		for _, name := range needed {
			if entry.Files[name] == "" {
				return fmt.Errorf("版本 %s 缺少依赖 %s", version, name)
			}
		}
	}
	for hash, size := range c.Blobs {
		if !hex64.MatchString(hash) || size < 0 || size > 128<<30 {
			return errors.New("累积devkit数据块无效")
		}
	}
	return nil
}

func readCatalog(root, key string) (devkitCatalog, error) {
	var cat devkitCatalog
	if err := verifySignature(root, key); err != nil {
		return cat, err
	}
	if err := verifyInventoryFile(root, "devkit-catalog.json"); err != nil {
		return cat, err
	}
	if err := readJSON(filepath.Join(root, "devkit-catalog.json"), &cat); err != nil {
		return cat, err
	}
	if err := validateCatalog(cat); err != nil {
		return cat, err
	}
	for hash, size := range cat.Blobs {
		var uses []string
		for version, entry := range cat.Versions {
			for name, value := range entry.Files {
				if value == hash {
					uses = append(uses, version+":"+name)
				}
			}
		}
		sort.Strings(uses)
		info, err := describeAsset(filepath.Join(root, "blobs", hash))
		if err != nil {
			return cat, fmt.Errorf("缺少离线依赖 %s（块 %s）: %w", strings.Join(uses, ", "), hash, err)
		}
		if info.Size != size || info.SHA256 != hash {
			return cat, fmt.Errorf("离线依赖摘要不符 %s（块 %s）", strings.Join(uses, ", "), hash)
		}
	}
	if err := verifySums(root); err != nil {
		return cat, err
	}
	return cat, nil
}

func catalogVersion(cat devkitCatalog, version string) (devkitVersion, error) {
	entry, ok := cat.Versions[version]
	if !ok {
		return entry, fmt.Errorf("当前套件（基线 %s，最新 %s）未覆盖版本 %s；请从该版本Forgejo Release浏览器下载相应基线的完整devkit。", cat.Baseline, cat.Latest, version)
	}
	return entry, nil
}
func versionMajor(version string) string {
	return strings.Split(strings.TrimPrefix(version, "v"), ".")[0]
}

func composeDevkit(previous, single, version, commit, target, key string, maxBytes int64, newBaseline bool) (devkitCatalog, error) {
	cat := devkitCatalog{Schema: 1, Baseline: version, Latest: version, Versions: map[string]devkitVersion{}, Blobs: map[string]int64{}}
	if !regexpVersion.MatchString(version) || maxBytes <= 0 {
		return cat, errors.New("devkit版本或大小阈值无效")
	}
	if previous != "" {
		prior, err := readCatalog(previous, "")
		if err != nil {
			return cat, err
		}
		if !newBaseline && versionMajor(prior.Baseline) == versionMajor(version) {
			cat = prior
			cat.Latest = version
			cat.ResetReason = ""
		} else {
			cat.ResetReason = "新基线或跨大版本"
		}
	}
	kit, err := readDevkit(single)
	if err != nil {
		return cat, err
	}
	if kit.Locks == nil {
		return cat, errors.New("单版本套件缺少锁清单")
	}
	names, err := regularFiles(filepath.Join(single, "cache"))
	if err != nil {
		return cat, err
	}
	entry := devkitVersion{Commit: commit, Kit: kit, Files: map[string]string{}}
	currentBlobs := map[string]string{}
	for _, name := range names {
		path := filepath.Join(single, "cache", name)
		info, err := describeAsset(path)
		if err != nil {
			return cat, err
		}
		entry.Files[name] = info.SHA256
		currentBlobs[info.SHA256] = path
		cat.Blobs[info.SHA256] = info.Size
	}
	if old, ok := cat.Versions[version]; ok && !reflect.DeepEqual(old, entry) {
		return cat, errors.New("同版本devkit不能替换已有源码或锁")
	}
	cat.Versions[version] = entry
	total := int64(0)
	for _, size := range cat.Blobs {
		if size > maxBytes-total {
			total = maxBytes + 1
			break
		}
		total += size
	}
	if total > maxBytes {
		if previous != "" && len(cat.Versions) > 1 {
			rebuilt, err := composeDevkit("", single, version, commit, target, key, maxBytes, true)
			if err != nil {
				return rebuilt, err
			}
			rebuilt.ResetReason = "超过累计大小阈值"
			if err := jsonFile(filepath.Join(target, "devkit-catalog.json"), rebuilt, 0644); err != nil {
				return rebuilt, err
			}
			if err := writeSums(target); err != nil {
				return rebuilt, err
			}
			if err := signSums(target, key); err != nil {
				return rebuilt, err
			}
			return rebuilt, nil
		}
		return cat, errors.New("单版本依赖已超过devkit阈值，需增大 --max-devkit-bytes")
	}
	if err := validateCatalog(cat); err != nil {
		return cat, err
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return cat, err
	}
	for hash := range cat.Blobs {
		path, ok := currentBlobs[hash]
		if !ok {
			path = filepath.Join(previous, "blobs", hash)
		}
		if err := copyFile(path, filepath.Join(target, "blobs", hash), 0644); err != nil {
			return cat, err
		}
	}
	if err := jsonFile(filepath.Join(target, "devkit-catalog.json"), cat, 0644); err != nil {
		return cat, err
	}
	if key != "" {
		if err := signingPublic(key, filepath.Join(target, "release-public.pem")); err != nil {
			return cat, err
		}
	}
	if err := writeSums(target); err != nil {
		return cat, err
	}
	if key != "" {
		if err := signSums(target, key); err != nil {
			return cat, err
		}
	}
	return cat, nil
}

func writeDevkitDelta(previous, target, out, key string) error {
	cat, err := readCatalog(target, "")
	if err != nil {
		return err
	}
	delta := devkitDelta{Schema: 1, Baseline: cat.Baseline, Reset: previous == ""}
	delta.TargetSHA256, err = digest(filepath.Join(target, "devkit-catalog.json"))
	if err != nil {
		return err
	}
	old := devkitCatalog{Blobs: map[string]int64{}}
	if previous != "" {
		old, err = readCatalog(previous, "")
		if err != nil {
			return err
		}
		delta.BaseSHA256, err = digest(filepath.Join(previous, "devkit-catalog.json"))
		if err != nil {
			return err
		}
		delta.Reset = old.Baseline != cat.Baseline
	}
	stage, err := os.MkdirTemp("", "auriga-devkit-delta-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for hash := range cat.Blobs {
		if _, ok := old.Blobs[hash]; ok && !delta.Reset {
			continue
		}
		if err := copyFile(filepath.Join(target, "blobs", hash), filepath.Join(stage, "blobs", hash), 0644); err != nil {
			return err
		}
		delta.Added = append(delta.Added, hash)
	}
	sort.Strings(delta.Added)
	for source, dest := range map[string]string{"devkit-catalog.json": "devkit-catalog.json", "SHA256SUMS": "TARGET-SHA256SUMS", "SHA256SUMS.sig": "TARGET-SHA256SUMS.sig", "release-public.pem": "release-public.pem"} {
		if err := copyFile(filepath.Join(target, source), filepath.Join(stage, dest), 0644); err != nil {
			return err
		}
	}
	if err := jsonFile(filepath.Join(stage, "delta.json"), delta, 0644); err != nil {
		return err
	}
	if err := writeSums(stage); err != nil {
		return err
	}
	if err := signSums(stage, key); err != nil {
		return err
	}
	return pack(stage, out)
}

func mergeDevkit(root, archive, key string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absolute
	if root == filepath.Dir(root) {
		return errors.New("不能合并到文件系统根目录")
	}
	if key == "" {
		key = filepath.Join(root, "release-public.pem")
	}
	old, err := readCatalog(root, key)
	if err != nil {
		return err
	}
	lock := root + ".merge-lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		return errors.New("devkit已有合并或遗留锁")
	}
	defer func() { _ = os.Remove(lock) }()
	scratch, err := os.MkdirTemp(filepath.Dir(root), ".auriga-devkit-merge-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	deltaRoot := filepath.Join(scratch, "delta")
	if err := extractVerified(archive, deltaRoot, key); err != nil {
		return err
	}
	var delta devkitDelta
	if err := readJSON(filepath.Join(deltaRoot, "delta.json"), &delta); err != nil {
		return err
	}
	current, err := digest(filepath.Join(root, "devkit-catalog.json"))
	if err != nil {
		return err
	}
	if delta.Schema != 1 || !hex64.MatchString(delta.TargetSHA256) {
		return errors.New("devkit增量清单无效")
	}
	if current == delta.TargetSHA256 {
		fmt.Println("PASS 此增量已合并")
		return nil
	}
	if delta.Reset || old.Baseline != delta.Baseline {
		return errors.New("增量属于新基线，请下载完整套件解包到新目录；旧基线保留")
	}
	if current != delta.BaseSHA256 {
		return errors.New("增量缺少精确前置版本，请依次合并增量或下载最新完整套件")
	}
	targetHash, err := digest(filepath.Join(deltaRoot, "devkit-catalog.json"))
	if err != nil {
		return err
	}
	if targetHash != delta.TargetSHA256 {
		return errors.New("增量目标清单摘要不符")
	}
	var cat devkitCatalog
	if err := readJSON(filepath.Join(deltaRoot, "devkit-catalog.json"), &cat); err != nil {
		return err
	}
	if err := validateCatalog(cat); err != nil {
		return err
	}
	for version, entry := range old.Versions {
		next, ok := cat.Versions[version]
		if !ok || !reflect.DeepEqual(next, entry) {
			return errors.New("增量不能删除或替换旧版本")
		}
	}
	target := filepath.Join(scratch, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		return err
	}
	for hash := range cat.Blobs {
		path := filepath.Join(root, "blobs", hash)
		if _, ok := old.Blobs[hash]; !ok {
			path = filepath.Join(deltaRoot, "blobs", hash)
		}
		if err := copyFile(path, filepath.Join(target, "blobs", hash), 0644); err != nil {
			return fmt.Errorf("增量缺少数据块 %s: %w", hash, err)
		}
	}
	for source, dest := range map[string]string{"devkit-catalog.json": "devkit-catalog.json", "TARGET-SHA256SUMS": "SHA256SUMS", "TARGET-SHA256SUMS.sig": "SHA256SUMS.sig", "release-public.pem": "release-public.pem"} {
		if err := copyFile(filepath.Join(deltaRoot, source), filepath.Join(target, dest), 0644); err != nil {
			return err
		}
	}
	if _, err := readCatalog(target, key); err != nil {
		return err
	}
	backup := filepath.Join(scratch, "previous")
	if err := renamePortable(root, backup); err != nil {
		return err
	}
	if err := renamePortable(target, root); err != nil {
		return errors.Join(err, renamePortable(backup, root))
	}
	fmt.Printf("PASS devkit已合并，基线 %s，覆盖 %d 版，最新 %s\n", cat.Baseline, len(cat.Versions), cat.Latest)
	return nil
}

func materializeVersion(root string, entry devkitVersion) (string, error) {
	stage, err := os.MkdirTemp("", "auriga-devkit-selected-")
	if err != nil {
		return "", err
	}
	for name, hash := range entry.Files {
		if err := copyFile(filepath.Join(root, "blobs", hash), filepath.Join(stage, "cache", name), 0644); err != nil {
			_ = os.RemoveAll(stage)
			return "", err
		}
	}
	return stage, nil
}
func locateCumulative(source, root string) (string, *devkitManifest, error) {
	cat, err := readCatalog(root, "")
	if err != nil {
		return "", nil, err
	}
	version := sourceVersionName(source)
	entry, err := catalogVersion(cat, version)
	if err != nil {
		return "", nil, err
	}
	kit := entry.Kit
	kit.Release.Version = version
	if err := checkKitLocks(source, kit); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(source, "auriga-source.json")); err == nil {
		m, err := readSourceManifest(source)
		if err != nil {
			return "", nil, err
		}
		kit.Source = &m.Source
		if m.Source.Commit != entry.Commit {
			return "", nil, errors.New("源码提交与套件版本清单不符")
		}
	} else {
		kit.Source = &provenance{Version: version, UpstreamVersion: version, Commit: entry.Commit}
	}
	stage, err := materializeVersion(root, entry)
	if err != nil {
		return "", nil, err
	}
	kit.cleanup = func() { _ = os.RemoveAll(stage) }
	return stage, &kit, nil
}
func verifyCumulative(root, source, version, key string) error {
	cat, err := readCatalog(root, key)
	if err != nil {
		return err
	}
	if version == "" {
		version = sourceVersionName(source)
	}
	entry, err := catalogVersion(cat, version)
	if err != nil {
		return err
	}
	if sourceVersionName(source) == version {
		if err := checkKitLocks(source, entry.Kit); err != nil {
			return err
		}
		baseline := provenance{Version: version, UpstreamVersion: version, Commit: entry.Commit}
		if _, err := os.Stat(filepath.Join(source, "auriga-source.json")); err == nil {
			m, err := readSourceManifest(source)
			if err != nil {
				return err
			}
			if m.Source.Commit != entry.Commit {
				return errors.New("源码提交与套件版本清单不符")
			}
			baseline = m.Source
		}
		if err := jsonFile(filepath.Join(source, ".auriga-devkit.json"), devkitBinding{Root: root, Digest: entry.Kit.Locks.Digest, Baseline: baseline}, 0644); err != nil {
			return err
		}
	}
	fmt.Printf("PASS 累积devkit覆盖 %s；基线 %s，最新 %s，支持%d个发行版本\n锁SHA256 %s\n", version, cat.Baseline, cat.Latest, len(cat.Versions), entry.Kit.Locks.Digest)
	return nil
}
func devkitMergeCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("devkit merge 需要增量包")
	}
	f := flags("devkit merge")
	root := f.String("kit", "", "")
	key := f.String("verify-key", "", "")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余merge参数")
	}
	if *root == "" {
		source, err := os.Getwd()
		if err != nil {
			return err
		}
		var binding devkitBinding
		if err := readJSON(filepath.Join(source, ".auriga-devkit.json"), &binding); err != nil {
			return errors.New("请用 --kit 指定已解包的累积套件")
		}
		*root = binding.Root
	}
	return mergeDevkit(*root, args[0], *key)
}
