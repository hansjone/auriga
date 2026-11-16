package main

import (
	"archive/tar"
	"bufio"
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
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func safeRelative(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "\\\r\n\t:")
}

func regularFiles(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
	names, err := regularFiles(root)
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, name := range names {
		if name == "SHA256SUMS" || name == "SHA256SUMS.sig" {
			continue
		}
		hash, err := digest(filepath.Join(root, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", hash, name)
	}
	return os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(sums.String()), 0644)
}

func verifySums(root string) error {
	if err := noSymlinks(root); err != nil {
		return err
	}
	names, err := regularFiles(root)
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
		actual, err := digest(filepath.Join(root, name))
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
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := tar.NewReader(f)
	seen := map[string]bool{}
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
		if len(seen) > 100000 || h.Size < 0 || h.Size > (128<<30)-size {
			return errors.New("归档超出大小限制")
		}
		size += h.Size
		path := filepath.Join(destination, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
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
		default:
			return errors.New("归档包含链接或特殊文件")
		}
	}
}

func pack(root, archive string) error {
	names, err := regularFiles(root)
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
		_, err = io.Copy(tw, in)
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

func copyFile(source, target string, mode fs.FileMode) error {
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
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func jsonFile(path string, value any, mode fs.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), mode)
}

func resourceKeys(root string) error {
	if err := os.Mkdir(root, 0700); err != nil {
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
