package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type provenance struct {
	Version         string `json:"version"`
	UpstreamVersion string `json:"upstreamVersion"`
	Commit          string `json:"commit"`
	Branch          string `json:"branch"`
	Dirty           bool   `json:"dirty"`
	SiteChanges     bool   `json:"siteChanges"`
	SnapshotSHA256  string `json:"snapshotSha256"`
	BuiltAt         string `json:"builtAt"`
}

func gitAt(root string, args ...string) (string, error) {
	return capture("git", append([]string{"-C", root}, args...)...)
}

func gitFiles(root string, args ...string) (string, error) {
	var out bytes.Buffer
	err := command(nil, &out, nil, "git", append([]string{"-C", root}, args...)...)
	return out.String(), err
}

// Snapshot exactly the source visible to Git, including staged, unstaged and
// non-ignored new files. Ignored credentials, caches and worktree metadata stay
// outside the snapshot. Index executable bits remain portable across DrvFS.
func snapshotSource(root, target string, excluded ...string) (*provenance, error) {
	if _, err := os.Lstat(filepath.Join(root, "auriga-source.json")); err == nil {
		return snapshotArchiveSource(root, target, excluded...)
	}
	return snapshotGitSource(root, target, excluded...)
}

func snapshotGitSource(root, target string, excluded ...string) (*provenance, error) {
	info := &provenance{BuiltAt: time.Now().UTC().Format(time.RFC3339)}
	var err error
	info.Commit, err = gitAt(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	info.Branch, err = gitAt(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	status, err := gitAt(root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	info.Dirty = status != ""
	info.SiteChanges = info.Dirty
	excludedNames := map[string]bool{}
	for _, file := range excluded {
		if file == "" {
			continue
		}
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		check := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", ":(literal)"+rel)
		if _, err := check.Output(); err == nil {
			return nil, errors.New("签名私钥已被 Git 跟踪，不能将其历史打入源码套件")
		}
		excludedNames[rel] = true
	}
	// A checkout without an upstream tag uses the approved release version later.
	tagCmd := exec.Command("git", "-C", root, "describe", "--tags", "--abbrev=0")
	if raw, err := tagCmd.Output(); err == nil {
		info.UpstreamVersion = strings.TrimSpace(string(raw))
	}
	raw, err := gitFiles(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	index, err := gitFiles(root, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	executable := map[string]bool{}
	for _, entry := range strings.Split(index, "\x00") {
		header, name, ok := strings.Cut(entry, "\t")
		if ok {
			executable[name] = strings.HasPrefix(header, "100755 ")
		}
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return nil, err
	}
	names := strings.Split(raw, "\x00")
	sort.Strings(names)
	seen := map[string]bool{}
	hash := sha256.New()
	for _, name := range names {
		if name == "" || seen[name] || excludedNames[name] {
			continue
		}
		seen[name] = true
		if !safeRelative(name) {
			return nil, fmt.Errorf("源码路径无效 %q", name)
		}
		source := filepath.Join(root, filepath.FromSlash(name))
		if err := noSymlinks(source); err != nil {
			return nil, err
		}
		st, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("源码含非普通文件 %s", name)
		}
		mode := os.FileMode(0644)
		isExecutable, indexed := executable[name]
		if isExecutable || !indexed && st.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		if err := copyFile(source, filepath.Join(target, filepath.FromSlash(name)), mode); err != nil {
			return nil, err
		}
		sum, err := digest(filepath.Join(target, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(hash, "%o %s %s\n", mode, sum, name)
	}
	info.SnapshotSHA256 = hex.EncodeToString(hash.Sum(nil))
	return info, nil
}

func sourceVersion(info *provenance, r release, version string, build bool) error {
	if info.UpstreamVersion == "" {
		info.UpstreamVersion = r.Version
	}
	info.Version = r.Version
	if version != "" {
		info.Version = version
	} else if build && info.SiteChanges {
		info.Version = r.Version + "-site." + info.SnapshotSHA256[:12]
	}
	if !regexpVersion.MatchString(info.Version) {
		return errors.New("版本名无效，请用 --version 指定版本及现场后缀")
	}
	return nil
}

func printProvenance(info *provenance) string {
	if info == nil {
		return "版本来源未记录（旧发行）"
	}
	return fmt.Sprintf("版本 %s\n上游版本 %s\n源码提交 %s\n现场分支 %s\n工作区未提交改动 %t\n含现场改动 %t\n源码摘要 %s\n构建时间 %s", info.Version, info.UpstreamVersion, info.Commit, info.Branch, info.Dirty, info.SiteChanges, info.SnapshotSHA256, info.BuiltAt)
}
