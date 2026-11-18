package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type assetInfo struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type partsManifest struct {
	Schema   int         `json:"schemaVersion"`
	Original assetInfo   `json:"original"`
	Parts    []assetInfo `json:"parts"`
}

func effectivePartLimit(limit int64) int64 {
	if limit > (4<<30)-1 {
		return (4 << 30) - 1
	}
	return limit
}

func describeAsset(file string) (assetInfo, error) {
	return describeAssetContext(context.Background(), file)
}

func describeAssetContext(ctx context.Context, file string) (assetInfo, error) {
	st, err := os.Lstat(file)
	if err != nil {
		return assetInfo{}, err
	}
	if !st.Mode().IsRegular() {
		return assetInfo{}, errors.New("附件必须是普通文件")
	}
	hash, err := digestContext(ctx, file)
	return assetInfo{Name: filepath.Base(file), Size: st.Size(), SHA256: hash}, err
}

// Read at most one part into the kernel's page cache at a time. No joined tar is
// written, so extraction also works on a FAT32 transfer medium.
func splitAsset(file string, limit int64, key string) ([]string, error) {
	return splitAssetContext(context.Background(), file, limit, key)
}

func splitAssetContext(ctx context.Context, file string, limit int64, key string) ([]string, error) {
	limit = effectivePartLimit(limit)
	if limit <= 0 {
		return nil, errors.New("附件上限必须大于零")
	}
	info, err := describeAssetContext(ctx, file)
	if err != nil {
		return nil, err
	}
	if info.Size <= limit {
		return []string{filepath.Base(file)}, nil
	}
	if (info.Size-1)/limit+1 > 10000 {
		return nil, errors.New("分卷数超过10000，请增大附件上限")
	}
	in, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	manifest := partsManifest{Schema: 1, Original: info}
	var names []string
	for remaining := info.Size; remaining > 0; {
		name := fmt.Sprintf("%s.part%03d", file, len(manifest.Parts)+1)
		out, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		n, copyErr := io.CopyN(out, cancellationReader(ctx, in), min(limit, remaining))
		closeErr := out.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		part, err := describeAssetContext(ctx, name)
		if err != nil {
			return nil, err
		}
		manifest.Parts = append(manifest.Parts, part)
		names = append(names, part.Name)
		remaining -= n
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := file + ".parts.json"
	if err := jsonFile(index, manifest, 0644); err != nil {
		return nil, err
	}
	names = append(names, filepath.Base(index))
	if key != "" {
		if err := signFile(index, key); err != nil {
			return nil, err
		}
		names = append(names, filepath.Base(index)+".sig")
	}
	if err := in.Close(); err != nil {
		return nil, err
	}
	if err := os.Remove(file); err != nil {
		return nil, err
	}
	return names, nil
}

var partSuffix = regexp.MustCompile(`\.part[0-9]{3,5}$`)

type partReader struct {
	root    string
	parts   []assetInfo
	current *os.File
}

func (r *partReader) Read(p []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.parts) == 0 {
				return 0, io.EOF
			}
			f, err := os.Open(filepath.Join(r.root, r.parts[0].Name))
			if err != nil {
				return 0, err
			}
			r.current = f
			r.parts = r.parts[1:]
		}
		n, err := r.current.Read(p)
		if err != io.EOF {
			return n, err
		}
		if err := r.current.Close(); err != nil {
			return n, err
		}
		r.current = nil
		if n > 0 {
			return n, nil
		}
	}
}
func (r *partReader) Close() error {
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}

func archiveIndex(file string) string {
	if partSuffix.MatchString(file) {
		return partSuffix.ReplaceAllString(file, "") + ".parts.json"
	}
	if strings.HasSuffix(file, ".parts.json") {
		return file
	}
	return ""
}

func openArchive(file string) (io.ReadCloser, error) {
	return openArchiveContext(context.Background(), file)
}

func openArchiveContext(ctx context.Context, file string) (io.ReadCloser, error) {
	index := archiveIndex(file)
	if index == "" {
		if err := noSymlinks(file); err != nil {
			return nil, err
		}
		return os.Open(file)
	}
	if err := noSymlinks(index); err != nil {
		return nil, err
	}
	var manifest partsManifest
	if err := readJSON(index, &manifest); err != nil {
		return nil, err
	}
	original := manifest.Original
	if manifest.Schema != 1 || !safeRelative(original.Name) || filepath.Base(original.Name) != original.Name || filepath.Base(index) != original.Name+".parts.json" || !hex64.MatchString(original.SHA256) || original.Size <= 0 || original.Size > 128<<30 || len(manifest.Parts) < 2 || len(manifest.Parts) > 10000 {
		return nil, errors.New("无效分卷清单")
	}
	root := filepath.Dir(index)
	total := int64(0)
	h := sha256.New()
	for i, p := range manifest.Parts {
		if p.Name != fmt.Sprintf("%s.part%03d", original.Name, i+1) || p.Size <= 0 || p.Size > (4<<30)-1 || p.Size > original.Size-total || !hex64.MatchString(p.SHA256) {
			return nil, errors.New("分卷名称、大小或顺序无效")
		}
		path := filepath.Join(root, p.Name)
		if err := noSymlinks(path); err != nil {
			return nil, err
		}
		actual, err := describeAssetContext(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("缺少分卷 %s: %w", p.Name, err)
		}
		if p != actual {
			return nil, fmt.Errorf("分卷摘要或大小不匹配 %s", p.Name)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(h, cancellationReader(ctx, f))
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		total += p.Size
	}
	if total != original.Size || hex.EncodeToString(h.Sum(nil)) != original.SHA256 {
		return nil, errors.New("分卷合并后的总摘要不匹配")
	}
	return &partReader{root: root, parts: manifest.Parts}, nil
}

func extractVerified(file, destination, key string) error {
	return extractWithTrust(file, destination, trustOptions{Key: key}, false)
}

func extractWithTrust(file, destination string, o trustOptions, identity bool) error {
	return extractWithTrustContext(context.Background(), file, destination, o, identity)
}

func extractWithTrustContext(ctx context.Context, file, destination string, o trustOptions, identity bool) error {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("解包目标已存在或不可访问")
	}
	if err := noSymlinks(filepath.Dir(destination)); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".auriga-unpack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := unpackContext(ctx, file, stage); err != nil {
		return err
	}
	if err := verifySumsContext(ctx, stage); err != nil {
		return err
	}
	key, unsigned := o.Key, false
	if identity {
		var err error
		key, unsigned, err = verifyRootTrust(stage, o)
		if err != nil {
			return err
		}
	} else if err := verifySignature(stage, key); err != nil {
		return err
	}
	if key == "" {
		key = filepath.Join(stage, "release-public.pem")
	}
	if index := archiveIndex(file); index != "" && !unsigned {
		if err := verifyFileSignature(index, key); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(stage, destination)
}

func unpackCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("unpack 需要安装包、part001 或 parts.json")
	}
	f := flags("unpack")
	out := f.String("out", "", "")
	var o trustOptions
	o.flags(f)
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if *out == "" || f.NArg() != 0 {
		return errors.New("unpack 需要 --out 全新目录")
	}
	return extractWithTrust(args[0], *out, o, true)
}

func verifyCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("verify 需要发行附件目录")
	}
	f := flags("verify")
	var o trustOptions
	o.flags(f)
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 verify 参数")
	}
	info, err := os.Stat(args[0])
	if err != nil {
		return err
	}
	if !info.IsDir() {
		stage, err := os.MkdirTemp("", "auriga-verify-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err := extractWithTrust(args[0], filepath.Join(stage, "package"), o, true); err != nil {
			return err
		}
		fmt.Println("PASS 归档/分卷校验（无签名例外见警告）")
		return nil
	}
	if err := verifySums(args[0]); err != nil {
		return err
	}
	if _, _, err := verifyRootTrust(args[0], o); err != nil {
		return err
	}
	fmt.Println("PASS 全部发行附件校验（无签名例外见警告）")
	return nil
}
