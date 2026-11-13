package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

var unifiedReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)

func unifiedRename(source, target string) error {
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("resource rename target already exists or cannot be inspected")
	}
	if err := unifiedReplace(source, target); err != nil {
		return err
	}
	if filepath.Dir(source) != filepath.Dir(target) {
		return unifiedSyncDirectory(filepath.Dir(source))
	}
	return nil
}

func unifiedAtomic(file string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return unifiedAtomicBytes(file, raw)
}
func unifiedAtomicBytes(file string, raw []byte) error {
	return unifiedAtomicMode(file, raw, 0600)
}
func unifiedAtomicMode(file string, raw []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(file); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe resource state file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(file), ".resource-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err = temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return unifiedReplace(name, file)
}
func unifiedRead(file string, value any) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 8*1024*1024 {
		return fmt.Errorf("unsafe resource state file")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if _, err := DecodeUnifiedJSON(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func unifiedArchivePath(name string) (string, error) {
	if !utf8.ValidString(name) || len(name) > 240 || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe skill archive path")
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" || path.Clean(name) != name || len(strings.Split(name, "/")) > 8 {
		return "", fmt.Errorf("unsafe skill archive path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "." || part == ".." || part == "" || strings.TrimRight(part, " .") != part || unifiedReserved.MatchString(part) {
			return "", fmt.Errorf("unsafe skill archive path")
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return "", fmt.Errorf("unsafe skill archive path")
			}
		}
	}
	if parts[0] != "SKILL.md" && parts[0] != "references" && parts[0] != "assets" {
		return "", fmt.Errorf("skill archive root refused")
	}
	return name, nil
}

// ExtractUnifiedSkill accepts only the frozen text-only ustar+gzip profile.
// It extracts into a fresh node-owned staging directory, never the active view.
func ExtractUnifiedSkill(raw []byte, directory string) error {
	if len(raw) == 0 || len(raw) > 5*1024*1024 {
		return fmt.Errorf("skill compressed budget exceeded")
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	compressed := bytes.NewReader(raw)
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	reader.Multistream(false)
	inflated, err := io.ReadAll(io.LimitReader(reader, 20*1024*1024+1))
	if err != nil {
		return err
	}
	if len(inflated) > 20*1024*1024 || len(inflated) > len(raw)*100 {
		return fmt.Errorf("skill expansion budget exceeded")
	}
	if compressed.Len() != 0 || len(inflated)%512 != 0 {
		return fmt.Errorf("trailing or misaligned skill archive")
	}
	stream := bytes.NewReader(inflated)
	archive := tar.NewReader(stream)
	seen := map[string]bool{}
	entry := false
	count := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 512 || header.Format != tar.FormatUSTAR || header.Mode&07000 != 0 || header.Typeflag != tar.TypeDir && header.Mode&0111 != 0 || header.Size > 2*1024*1024 || header.Size < 0 || header.Linkname != "" || len(header.PAXRecords) > 0 {
			return fmt.Errorf("unsafe skill archive entry")
		}
		name, err := unifiedArchivePath(header.Name)
		if err != nil {
			return err
		}
		key := norm.NFC.String(strings.ToLower(strings.ToUpper(norm.NFC.String(name))))
		if seen[key] {
			return fmt.Errorf("skill path collision")
		}
		seen[key] = true
		target, err := storage.SafeJoin(directory, filepath.FromSlash(name))
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 || name == "SKILL.md" {
				return fmt.Errorf("unsafe skill directory")
			}
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return fmt.Errorf("skill links/devices refused")
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".md", ".txt", ".json", ".yaml", ".yml":
		default:
			return fmt.Errorf("skill attachment is not text-only")
		}
		content, err := io.ReadAll(io.LimitReader(archive, 2*1024*1024+1))
		if err != nil {
			return err
		}
		if int64(len(content)) != header.Size || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return fmt.Errorf("skill attachment bytes refused")
		}
		if name == "SKILL.md" {
			if len(content) > 256*1024 {
				return fmt.Errorf("skill entry budget exceeded")
			}
			if err := unifiedSkillFrontMatter(content); err != nil {
				return err
			}
			entry = true
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if !entry {
		return fmt.Errorf("missing SKILL.md")
	}
	if trailing, err := io.ReadAll(stream); err != nil || len(bytes.Trim(trailing, "\x00")) != 0 {
		return fmt.Errorf("trailing skill tar data")
	}
	return os.Chmod(directory, 0755)
}
func unifiedSkillFrontMatter(content []byte) error {
	if !bytes.HasPrefix(content, []byte("---\n")) && !bytes.HasPrefix(content, []byte("---\r\n")) {
		return fmt.Errorf("skill front matter required")
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return fmt.Errorf("unterminated skill front matter")
	}
	raw := normalized[4 : 4+end]
	if len(raw) > 8192 {
		return fmt.Errorf("skill metadata budget exceeded")
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &node); err != nil {
		return err
	}
	var visit func(*yaml.Node, int) error
	visit = func(n *yaml.Node, depth int) error {
		if depth > 8 || n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
			return fmt.Errorf("skill metadata tags/aliases refused")
		}
		if n.Style&yaml.TaggedStyle != 0 {
			return fmt.Errorf("explicit skill YAML tag refused")
		}
		if n.Kind == yaml.MappingNode {
			keys := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i].Value
				if keys[key] {
					return fmt.Errorf("duplicate skill metadata key")
				}
				keys[key] = true
				if key == "whenToUse" && len([]rune(n.Content[i+1].Value)) > 2048 {
					return fmt.Errorf("skill whenToUse budget exceeded")
				}
			}
		}
		for _, child := range n.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(&node, 0)
}

func unifiedSkillBody(raw []byte) string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return ""
	}
	rest := text[4+end+4:]
	return strings.TrimSpace(rest)
}
