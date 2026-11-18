package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func portablePaths(names []string) error {
	seen := map[string]string{}
	for _, name := range names {
		if err := portablePath(seen, name); err != nil {
			return err
		}
	}
	return nil
}
func portablePath(seen map[string]string, name string) error {
	if !safeRelative(name) {
		return fmt.Errorf("不安全路径 %q", name)
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		stem := strings.ToUpper(strings.TrimRight(strings.Split(part, ".")[0], " "))
		device := ""
		if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
			device = stem[3:]
		}
		reservedDevice := device != "" && strings.Contains("|1|2|3|4|5|6|7|8|9|¹|²|³|", "|"+device+"|")
		if strings.TrimRight(part, " .") != part || strings.ContainsAny(part, "<>\"|?*") || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || reservedDevice {
			return fmt.Errorf("当前Windows不支持的文件名 %q；请改名后重新打包", name)
		}
		prefix := strings.Join(parts[:i+1], "/")
		fold := strings.ToUpper(strings.ToLower(prefix))
		if prior, ok := seen[fold]; ok && prior != prefix {
			return fmt.Errorf("大小写不敏感路径冲突 %q 与 %q", prior, prefix)
		}
		seen[fold] = prefix
	}
	return nil
}

func sourceText(name string) bool {
	base := filepath.Base(name)
	switch base {
	case "auriga", "mvnw", "go.mod", "go.sum", "go.work", "go.work.sum", ".npmrc", ".gitattributes", ".gitignore":
		return true
	}
	if strings.HasPrefix(base, "Dockerfile") {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".sh", ".go", ".mod", ".sum", ".yaml", ".yml", ".json", ".xml", ".properties", ".md", ".txt", ".vue", ".ts", ".js", ".mjs", ".cjs", ".tsx", ".jsx", ".css", ".html", ".toml", ".config", ".lock", ".ps1", ".cmd", ".bat":
		return true
	}
	return false
}

func canonicalSourceContext(ctx context.Context, path, name string) ([]byte, error) {
	raw, err := smallFileContext(ctx, path, 64<<20)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, fmt.Errorf("源码文本必须为UTF-8（可带BOM）: %s", name)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if strings.HasSuffix(name, ".sh") || filepath.Base(name) == "auriga" || filepath.Base(name) == "mvnw" {
		raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	}
	if bytes.Contains(raw, []byte("\r")) {
		return nil, fmt.Errorf("不支持单独CR换行 %s，请在编辑器另存UTF-8/LF", name)
	}
	return raw, nil
}

func bytesDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func sourceDigest(path, name, expected string) (string, error) {
	return sourceDigestContext(context.Background(), path, name, expected)
}

func sourceDigestContext(ctx context.Context, path, name, expected string) (string, error) {
	if !sourceText(name) {
		return digestContext(ctx, path)
	}
	raw, err := canonicalSourceContext(ctx, path, name)
	if err != nil {
		return "", err
	}
	hash := bytesDigest(raw)
	// Older signed baselines may contain CRLF bytes. Preserve their identity
	// when only the editor's line endings changed, without changing the manifest.
	if expected != "" && bytesDigest(bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))) == expected {
		return expected, nil
	}
	return hash, nil
}

func copySourceFile(source, target, name string, mode fs.FileMode) error {
	return copySourceFileContext(context.Background(), source, target, name, mode)
}

func copySourceFileContext(ctx context.Context, source, target, name string, mode fs.FileMode) error {
	return copySourceTextContext(ctx, source, target, name, mode, true)
}

// Content-addressed patch blobs use canonical bytes; materialized Windows
// scripts regain CRLF at the destination, even when two file kinds share a blob.
func copySourcePayload(source, target, name string, mode fs.FileMode) error {
	return copySourceText(source, target, name, mode, false)
}

func windowsScript(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ps1", ".cmd", ".bat":
		return true
	}
	return false
}

func copySourceText(source, target, name string, mode fs.FileMode, materialize bool) error {
	return copySourceTextContext(context.Background(), source, target, name, mode, materialize)
}

func copySourceTextContext(ctx context.Context, source, target, name string, mode fs.FileMode, materialize bool) error {
	if !sourceText(name) {
		return copyFileContext(ctx, source, target, mode)
	}
	raw, err := canonicalSourceContext(ctx, source, name)
	if err != nil {
		return err
	}
	if materialize && windowsScript(name) {
		raw = bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := out.Write(raw)
	return errors.Join(writeErr, out.Close(), ctx.Err())
}
