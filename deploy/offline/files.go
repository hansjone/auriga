package main

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func digest(file string) (string, error) {
	return digestContext(context.Background(), file)
}

func digestContext(ctx context.Context, file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	h := sha256.New()
	if _, err := io.Copy(h, cancellationReader(ctx, f)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func safeRelative(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "\\\r\n\t:")
}

func regularFiles(root string) ([]string, error) {
	return regularFilesContext(context.Background(), root)
}

func regularFilesContext(ctx context.Context, root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("拒绝非普通文件 %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !safeRelative(name) {
			return fmt.Errorf("不安全路径 %s", name)
		}
		names = append(names, name)
		return nil
	})
	sort.Strings(names)
	return names, err
}

func writeSums(root string) error {
	return writeSumsContext(context.Background(), root)
}

func writeSumsContext(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	names, err := regularFilesContext(ctx, root)
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, name := range names {
		if name == "SHA256SUMS" || name == "SHA256SUMS.sig" {
			continue
		}
		hash, err := digestContext(ctx, filepath.Join(root, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", hash, name)
	}
	sumPath := filepath.Join(root, "SHA256SUMS")
	if err := os.WriteFile(sumPath, []byte(sums.String()), 0644); err != nil {
		return err
	}
	return os.Chmod(sumPath, 0644)
}

func verifySums(root string) error {
	return verifySumsContext(context.Background(), root)
}

func verifySumsContext(ctx context.Context, root string) error {
	if err := noSymlinks(root); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	names, err := regularFilesContext(ctx, root)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	seen := map[string]bool{"SHA256SUMS": true, "SHA256SUMS.sig": true}
	s := bufio.NewScanner(io.LimitReader(f, 4<<20))
	count := 0
	for s.Scan() {
		line := s.Text()
		if len(line) < 67 || line[64:66] != "  " || !hex64.MatchString(line[:64]) || !safeRelative(line[66:]) {
			return errors.New("无效 SHA256SUMS")
		}
		name := line[66:]
		if seen[name] {
			return errors.New("重复摘要条目")
		}
		seen[name] = true
		actual, err := digestContext(ctx, filepath.Join(root, name))
		if err != nil {
			return err
		}
		if actual != line[:64] {
			return fmt.Errorf("摘要不匹配 %s，重新拷贝原包", name)
		}
		count++
	}
	if err := s.Err(); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("空摘要清单")
	}
	for _, name := range names {
		if !seen[name] {
			return fmt.Errorf("未列入摘要清单 %s", name)
		}
	}
	return nil
}

// Extraction is deliberately limited to regular files/directories in a fresh
// directory. No archive-supplied ownership, special bits, links or devices apply.
func unpack(archive, destination string) error {
	return unpackContext(context.Background(), archive, destination)
}

func unpackContext(ctx context.Context, archive, destination string) error {
	f, err := openArchiveContext(ctx, archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if file, ok := f.(*os.File); ok {
		stop := context.AfterFunc(ctx, func() { _ = file.Close() })
		defer stop()
	}
	err = unpackReader(cancellationReader(ctx, f), destination)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func unpackReader(input io.Reader, destination string) error {
	r := tar.NewReader(input)
	seen := map[string]bool{}
	portable := map[string]string{}
	var size int64
	for {
		h, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !safeRelative(name) || seen[name] {
			return errors.New("归档含非法或重复路径")
		}
		seen[name] = true
		if err := portablePath(portable, name); err != nil {
			return err
		}
		if len(seen) > 100000 || h.Size < 0 || h.Size > (128<<30)-size {
			return errors.New("归档超出大小限制")
		}
		size += h.Size
		path := filepath.Join(destination, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllExact(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := mkdirAllExact(filepath.Dir(path), 0755); err != nil {
				return err
			}
			mode := fs.FileMode(0644)
			if h.Mode&0111 != 0 {
				mode = 0755
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, r, h.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := os.Chmod(path, mode); err != nil {
				return err
			}
		default:
			return errors.New("归档包含链接或特殊文件")
		}
	}
}

func pack(root, archive string) error {
	return packContext(context.Background(), root, archive)
}

func packContext(ctx context.Context, root, archive string) error {
	names, err := regularFilesContext(ctx, root)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil {
			_ = tw.Close()
			_ = f.Close()
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			_ = tw.Close()
			_ = f.Close()
			return err
		}
		h.Name = name
		if err := tw.WriteHeader(h); err != nil {
			_ = tw.Close()
			_ = f.Close()
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			_ = tw.Close()
			_ = f.Close()
			return err
		}
		_, err = io.Copy(tw, cancellationReader(ctx, in))
		_ = in.Close()
		if err != nil {
			_ = tw.Close()
			_ = f.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// mkdirAllExact 按指定权限逐级创建不存在的目录，并对新建的每一级目录显式 Chmod，
// 避免受调用者 umask（如 0077）影响；已存在的父目录权限保持不变。
func mkdirAllExact(dir string, mode fs.FileMode) error {
	clean := filepath.Clean(dir)
	if st, err := os.Stat(clean); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s 已存在且不是目录", clean)
		}
		return nil
	}
	var missing []string
	cur := clean
	for {
		if _, err := os.Stat(cur); err == nil {
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		p := missing[i]
		if err := os.Mkdir(p, mode); err != nil && !os.IsExist(err) {
			return err
		}
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}

// normalizedFileMode 把文件权限规范化为发行/装配公开权限：
// 含任意执行位则为 0755，否则为 0644，避免把源文件的 0600/0700 透传进镜像或发行目录。
func normalizedFileMode(mode fs.FileMode) fs.FileMode {
	if mode&0111 != 0 {
		return 0755
	}
	return 0644
}

func copyFile(source, target string, mode fs.FileMode) error {
	return copyFileContext(context.Background(), source, target, mode)
}

func copyFileContext(ctx context.Context, source, target string, mode fs.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("复制源必须是普通文件")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = in.Close() })
	defer stop()
	if err := mkdirAllExact(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, cancellationReader(ctx, in))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(target, mode)
}

func jsonFile(path string, value any, mode fs.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func resourceKeys(root string) error {
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return errors.New("资源密钥目录已存在或不可访问；拒绝覆盖，残缺目录请从备份恢复或隔离后重试")
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".auriga-resource-keys-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := writeResourceKeys(stage); err != nil {
		return err
	}
	return os.Rename(stage, root)
}

func writeResourceKeys(root string) error {
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	for _, part := range []string{"platform", "offline"} {
		if err := os.Mkdir(filepath.Join(root, part), 0700); err != nil {
			return err
		}
	}
	roots := map[string]map[string]string{}
	for _, role := range []string{"release", "revocation", "downgrade"} {
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return err
		}
		part := "platform"
		if role == "downgrade" {
			part = "offline"
		}
		if err := os.WriteFile(filepath.Join(root, part, role+".pk8"), der, 0600); err != nil {
			return err
		}
		spki, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return err
		}
		h := sha256.Sum256(pub)
		roots[role] = map[string]string{"kid": "site-" + hex.EncodeToString(h[:8]) + "-" + role, "publicKeySpkiBase64": base64.StdEncoding.EncodeToString(spki)}
	}
	return jsonFile(filepath.Join(root, "resource-roots.json"), roots, 0644)
}
