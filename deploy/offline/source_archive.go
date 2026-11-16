package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sourceFile struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type sourceManifest struct {
	Schema int                   `json:"schemaVersion"`
	Source provenance            `json:"source"`
	Tree   string                `json:"tree"`
	Locks  lockInventory         `json:"locks"`
	Files  map[string]sourceFile `json:"files"`
}

func sourcePathAllowed(name string) bool {
	if !safeRelative(name) {
		return false
	}
	name = strings.ToLower(name)
	for _, part := range strings.Split(name, "/") {
		switch part {
		case ".git", ".agents", ".agent-forgejo", ".idea", ".vscode", "node_modules", "target", "dist", ".pnpm-store", ".offline-releases", ".offline-devkit-cache", "__pycache__", ".dev":
			return false
		}
	}
	if name == "bin" || strings.HasPrefix(name, "bin/") || name == "data" || strings.HasPrefix(name, "data/") || strings.HasPrefix(name, "deploy/compose/var/") {
		return false
	}
	base := filepath.Base(name)
	if !strings.Contains(name, "/") && (strings.HasPrefix(base, "auriga-offline-") || strings.HasPrefix(base, "auriga-devkit-") || strings.HasPrefix(base, "auriga-changes-")) && (strings.Contains(base, ".tar") || strings.HasSuffix(base, ".sha256")) {
		return false
	}
	switch base {
	case "auriga-source.json", "sha256sums", "sha256sums.sig", "release-public.pem", ".auriga-devkit.json", ".agent-task.md", ".agent-report.md":
		return false
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") && base != ".env.example" {
		return false
	}
	for _, suffix := range []string{".pem", ".key", ".out", ".test", ".exe", ".tsbuildinfo"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return true
}

func archiveSourceNames(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !sourcePathAllowed(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("源码含链接或特殊文件 %s", name)
		}
		names = append(names, name)
		return nil
	})
	sort.Strings(names)
	return names, err
}

func sourceNames(root string) ([]string, error) {
	if _, err := os.Lstat(filepath.Join(root, "auriga-source.json")); err == nil {
		names, err := archiveSourceNames(root)
		if err != nil {
			return nil, err
		}
		manifest, err := readSourceManifest(root)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, name := range names {
			seen[name] = true
		}
		for name := range manifest.Files {
			if !seen[name] && isDependencyFile(name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names, nil
	}
	raw, err := gitFiles(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	return strings.Split(raw, "\x00"), err
}

func fileInventory(root string, names []string, baseline map[string]sourceFile) (map[string]sourceFile, string, error) {
	if err := portablePaths(names); err != nil {
		return nil, "", err
	}
	files := map[string]sourceFile{}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := files[name]; ok {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := noSymlinks(path); err != nil {
			return nil, "", err
		}
		st, err := os.Lstat(path)
		if err != nil {
			return nil, "", err
		}
		if !st.Mode().IsRegular() {
			return nil, "", errors.New("源码只接受普通文件")
		}
		mode := uint32(0644)
		if st.Mode().Perm()&0111 != 0 || strings.HasSuffix(name, ".sh") || filepath.Base(name) == "mvnw" || filepath.Base(name) == "auriga" {
			mode = 0755
		}
		if previous, ok := baseline[name]; ok {
			mode = previous.Mode
		}
		hash, err := sourceDigest(path, name, baseline[name].SHA256)
		if err != nil {
			return nil, "", err
		}
		files[name] = sourceFile{SHA256: hash, Mode: mode}
		_, _ = fmt.Fprintf(h, "%o %s %s\n", mode, hash, name)
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

func readSourceManifest(root string) (sourceManifest, error) {
	var m sourceManifest
	for _, name := range []string{"auriga-source.json", "SHA256SUMS", "SHA256SUMS.sig", "release-public.pem"} {
		if err := noSymlinks(filepath.Join(root, name)); err != nil {
			return m, err
		}
	}
	_, signatureErr := os.Lstat(filepath.Join(root, "SHA256SUMS.sig"))
	_, publicErr := os.Lstat(filepath.Join(root, "release-public.pem"))
	if signatureErr == nil || publicErr == nil {
		if _, err := os.Stat(filepath.Join(root, "release-public.pem")); err != nil {
			return m, errors.New("签名源码归档缺少包内公钥")
		}
		if err := verifySignature(root, ""); err != nil {
			return m, err
		}
		if err := verifyInventoryFile(root, "auriga-source.json"); err != nil {
			return m, err
		}
	}
	if err := readJSON(filepath.Join(root, "auriga-source.json"), &m); err != nil {
		return m, err
	}
	if m.Schema != 1 || !regexpVersion.MatchString(m.Source.UpstreamVersion) || len(m.Source.Commit) != 40 || !hex64.MatchString(m.Source.SnapshotSHA256) || !hex64.MatchString(m.Locks.Digest) || len(m.Tree) != 40 || len(m.Files) == 0 {
		return m, errors.New("源码归档版本清单无效")
	}
	for name, file := range m.Files {
		if !sourcePathAllowed(name) || !hex64.MatchString(file.SHA256) || file.Mode != 0644 && file.Mode != 0755 {
			return m, errors.New("源码归档文件清单无效")
		}
	}
	return m, nil
}

// Verify immutable metadata against the signed original inventory without
// rejecting deliberate edits to ordinary source files.
func verifyInventoryFile(root, name string) error {
	raw, err := smallFile(filepath.Join(root, "SHA256SUMS"), 4<<20)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) >= 67 && line[64:66] == "  " && line[66:] == name {
			if expected != "" || !hex64.MatchString(line[:64]) {
				return errors.New("重复或无效元数据摘要")
			}
			expected = line[:64]
		}
	}
	hash, err := digest(filepath.Join(root, name))
	if err != nil {
		return err
	}
	if expected == "" || hash != expected {
		return fmt.Errorf("不可修改发行基线元数据 %s；重新解包原始源码归档", name)
	}
	return nil
}

func writeSourceManifest(root string, info provenance, tree string, r release) error {
	names, err := archiveSourceNames(root)
	if err != nil {
		return err
	}
	files, hash, err := fileInventory(root, names, nil)
	if err != nil {
		return err
	}
	locks, err := locksFromNames(root, r, names)
	if err != nil {
		return err
	}
	info.SnapshotSHA256 = hash
	info.Dirty = false
	info.SiteChanges = false
	if info.UpstreamVersion == "" {
		info.UpstreamVersion = r.Version
	}
	if info.Version == "" {
		info.Version = r.Version
	}
	return jsonFile(filepath.Join(root, "auriga-source.json"), sourceManifest{Schema: 1, Source: info, Tree: tree, Files: files, Locks: locks}, 0644)
}

func snapshotArchiveSource(root, target string, excluded ...string) (*provenance, error) {
	m, err := readSourceManifest(root)
	if err != nil {
		return nil, err
	}
	names, err := archiveSourceNames(root)
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, name := range excluded {
		if name == "" {
			continue
		}
		absolute, err := filepath.Abs(name)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := m.Files[rel]; ok {
			return nil, errors.New("签名私钥不能属于源码基线")
		}
		skip[rel] = true
	}
	filtered := names[:0]
	for _, name := range names {
		if !skip[name] {
			filtered = append(filtered, name)
		}
	}
	files, hash, err := fileInventory(root, filtered, m.Files)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return nil, err
	}
	for name, file := range files {
		if err := copySourceFile(filepath.Join(root, name), filepath.Join(target, name), name, fs.FileMode(file.Mode)); err != nil {
			return nil, err
		}
	}
	_, copiedHash, err := fileInventory(target, names, m.Files)
	if err != nil {
		return nil, err
	}
	if copiedHash != hash {
		return nil, errors.New("生成快照时源码变化，请停止编辑后重试")
	}
	info := m.Source
	info.BuiltAt = time.Now().UTC().Format(time.RFC3339)
	info.Branch = "site/archive"
	info.SnapshotSHA256 = hash
	info.SiteChanges = hash != m.Source.SnapshotSHA256
	info.Dirty = info.SiteChanges
	return &info, nil
}

func sourceVersionName(root string) string {
	if _, err := os.Lstat(filepath.Join(root, "auriga-source.json")); err == nil {
		m, err := readSourceManifest(root)
		if err == nil {
			return m.Source.UpstreamVersion
		}
		return ""
	}
	tag, _ := gitAt(root, "describe", "--tags", "--abbrev=0")
	return tag
}

func sourceTree(root string) (string, error) {
	if _, err := os.Lstat(filepath.Join(root, "auriga-source.json")); err == nil {
		m, err := readSourceManifest(root)
		return m.Tree, err
	}
	return gitAt(root, "rev-parse", "HEAD^{tree}")
}

func prepareSourceArchive(source, target string, r release, key, exe string) error {
	info, err := snapshotSource(source, target, key)
	if err != nil {
		return err
	}
	// Several approved tags may refer to one commit. The explicitly selected
	// release tag is authoritative, rather than git describe's tie-breaking.
	info.UpstreamVersion = r.Version
	if err := sourceVersion(info, r, r.Version, false); err != nil {
		return err
	}
	tree, err := sourceTree(source)
	if err != nil {
		return err
	}
	if err := writeSourceManifest(target, *info, tree, r); err != nil {
		return err
	}
	if err := copyFile(exe, filepath.Join(target, "bin/auriga-cli"), 0755); err != nil {
		return err
	}
	if key != "" {
		if err := signingPublic(key, filepath.Join(target, "release-public.pem")); err != nil {
			return err
		}
	}
	if err := writeSums(target); err != nil {
		return err
	}
	if key != "" {
		return signSums(target, key)
	}
	return nil
}
