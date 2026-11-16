package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sourceChange struct {
	Path   string      `json:"path"`
	Before *sourceFile `json:"before"`
	After  *sourceFile `json:"after"`
}
type changeManifest struct {
	Schema      int            `json:"schemaVersion"`
	BaseVersion string         `json:"baseVersion"`
	BaseCommit  string         `json:"baseCommit"`
	Changes     []sourceChange `json:"changes"`
}

func sameFile(a, b *sourceFile) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func filePointer(files map[string]sourceFile, name string) *sourceFile {
	if file, ok := files[name]; ok {
		return &file
	}
	return nil
}

func exportChanges(source, out string) error {
	m, err := readSourceManifest(source)
	if err != nil {
		return err
	}
	names, err := archiveSourceNames(source)
	if err != nil {
		return err
	}
	files, _, err := fileInventory(source, names, m.Files)
	if err != nil {
		return err
	}
	all := map[string]bool{}
	for name := range files {
		all[name] = true
	}
	for name := range m.Files {
		all[name] = true
	}
	ordered := make([]string, 0, len(all))
	for name := range all {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	stage, err := os.MkdirTemp("", "auriga-changes-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	changes := changeManifest{Schema: 1, BaseVersion: m.Source.UpstreamVersion, BaseCommit: m.Source.Commit}
	copied := map[string]bool{}
	for _, name := range ordered {
		before, after := filePointer(m.Files, name), filePointer(files, name)
		if sameFile(before, after) {
			continue
		}
		changes.Changes = append(changes.Changes, sourceChange{Path: name, Before: before, After: after})
		if after != nil && !copied[after.SHA256] {
			if err := copySourcePayload(filepath.Join(source, name), filepath.Join(stage, "files", after.SHA256), name, 0644); err != nil {
				return err
			}
			copied[after.SHA256] = true
		}
	}
	if err := jsonFile(filepath.Join(stage, "changes.json"), changes, 0644); err != nil {
		return err
	}
	if err := writeSums(stage); err != nil {
		return err
	}
	if err := pack(stage, out); err != nil {
		return err
	}
	fmt.Printf("已导出 %d 项现场改动，基线 %s，补丁 %s\n", len(changes.Changes), changes.BaseVersion, out)
	return nil
}

func replaceSourceFile(source, target string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".auriga-change-")
	if err != nil {
		return err
	}
	temp := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(temp); err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	var copyErr error
	if windowsScript(target) {
		copyErr = copySourceFile(source, temp, filepath.Base(target), mode)
	} else {
		copyErr = copyFile(source, temp, mode)
	}
	if copyErr != nil {
		return copyErr
	}
	return os.Rename(temp, target)
}

func applyChanges(source, archive string) error {
	m, err := readSourceManifest(source)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "auriga-apply-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := unpack(archive, stage); err != nil {
		return err
	}
	if err := verifySums(stage); err != nil {
		return err
	}
	var patch changeManifest
	if err := readJSON(filepath.Join(stage, "changes.json"), &patch); err != nil {
		return err
	}
	if patch.Schema != 1 || !regexpVersion.MatchString(patch.BaseVersion) {
		return errors.New("现场补丁清单无效")
	}
	names, err := archiveSourceNames(source)
	if err != nil {
		return err
	}
	current, _, err := fileInventory(source, names, m.Files)
	if err != nil {
		return err
	}
	portableNames := append([]string(nil), names...)
	for _, change := range patch.Changes {
		portableNames = append(portableNames, change.Path)
	}
	if err := portablePaths(portableNames); err != nil {
		return err
	}
	seen := map[string]bool{}
	var conflicts []string
	var plan []sourceChange
	for _, change := range patch.Changes {
		if !sourcePathAllowed(change.Path) || seen[change.Path] || change.Before == nil && change.After == nil {
			return errors.New("现场补丁含非法或重复路径")
		}
		seen[change.Path] = true
		if err := noSymlinks(filepath.Join(source, change.Path)); err != nil {
			return err
		}
		for _, record := range []*sourceFile{change.Before, change.After} {
			if record != nil && (!hex64.MatchString(record.SHA256) || record.Mode != 0644 && record.Mode != 0755) {
				return errors.New("现场补丁文件摘要或模式无效")
			}
		}
		if change.After != nil {
			actual, err := sourceDigest(filepath.Join(stage, "files", change.After.SHA256), change.Path, change.After.SHA256)
			if err != nil {
				return err
			}
			if actual != change.After.SHA256 {
				return errors.New("现场补丁内容摘要不符")
			}
		}
		actual := filePointer(current, change.Path)
		if sameFile(actual, change.After) {
			continue
		}
		if !sameFile(actual, change.Before) {
			conflicts = append(conflicts, change.Path)
			continue
		}
		plan = append(plan, change)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("补丁与当前上游或现场修改冲突，尚未应用任何文件:\n%s\n请人工对照旧源码与新源码处理。", strings.Join(conflicts, "\n"))
	}
	backup := filepath.Join(stage, "backup")
	for _, change := range plan {
		if previous := current[change.Path]; previous.SHA256 != "" {
			if err := copyFile(filepath.Join(source, change.Path), filepath.Join(backup, change.Path), fs.FileMode(previous.Mode)); err != nil {
				return err
			}
		}
	}
	applied := []sourceChange{}
	rollback := func(cause error) error {
		for i := len(applied) - 1; i >= 0; i-- {
			change := applied[i]
			target := filepath.Join(source, change.Path)
			if change.Before == nil {
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					cause = errors.Join(cause, err)
				}
			} else if err := replaceSourceFile(filepath.Join(backup, change.Path), target, fs.FileMode(change.Before.Mode)); err != nil {
				cause = errors.Join(cause, err)
			}
		}
		return cause
	}
	for _, change := range plan {
		applied = append(applied, change)
		target := filepath.Join(source, change.Path)
		if change.After == nil {
			if err := os.Remove(target); err != nil {
				return rollback(err)
			}
		} else if err := replaceSourceFile(filepath.Join(stage, "files", change.After.SHA256), target, fs.FileMode(change.After.Mode)); err != nil {
			return rollback(err)
		}
	}
	fmt.Printf("已应用 %d 项改动（补丁基线 %s，当前源码 %s）。\n", len(plan), patch.BaseVersion, m.Source.UpstreamVersion)
	return nil
}

func changesCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("changes 需要 export 或 apply")
	}
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	if args[0] == "apply" {
		if len(args) != 2 {
			return errors.New("changes apply 需要补丁归档")
		}
		return applyChanges(source, args[1])
	}
	if args[0] == "export" {
		f := flags("changes export")
		out := f.String("out", "", "")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" || f.NArg() != 0 {
			return errors.New("changes export 需要 --out 源码目录外的补丁.tar")
		}
		return exportChanges(source, *out)
	}
	return errors.New("changes 仅支持 export/apply")
}
