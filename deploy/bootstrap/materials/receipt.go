package materials

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaterialSet is a recipe's fixed ordered material list, never installation input.
// ManagedDirectories must be empty real directories before the first generation.
type MaterialSet struct {
	PublicFiles        []string
	PrivateFiles       []string
	ManagedDirectories []string
}

func (s MaterialSet) validate() error {
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, s.PublicFiles...), s.ManagedDirectories...) {
		if name == "" || strings.ContainsAny(name, "\\\x00:") || filepath.IsAbs(name) || seen[name] {
			return fmt.Errorf("invalid installation material manifest")
		}
		for _, part := range strings.Split(name, "/") {
			if !validLeaf(part) {
				return fmt.Errorf("invalid installation material manifest")
			}
		}
		seen[name] = true
	}
	for _, name := range s.PrivateFiles {
		if !validLeaf(name) || name == "bootstrap.receipt" || seen["iam-installation/"+name] {
			return fmt.Errorf("invalid private material manifest")
		}
		seen["iam-installation/"+name] = true
	}
	return nil
}

func materialDigest(out string, set MaterialSet) (string, error) {
	h := sha256.New()
	p, err := Open(out)
	if err != nil {
		return "", err
	}
	defer p.Close()
	for _, name := range set.PrivateFiles {
		raw, err := p.Read(name)
		if err != nil {
			return "", fmt.Errorf("incomplete private installation")
		}
		sum := sha256.Sum256(raw)
		clear(raw)
		_, _ = fmt.Fprintf(h, "%s:%x\n", name, sum)
	}
	if err := p.Validate(); err != nil {
		return "", err
	}
	for _, name := range set.PublicFiles {
		path := filepath.Join(out, filepath.FromSlash(name))
		// Inspect every material parent, including nested template directories.
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("invalid identity material directory")
			}
			if parent == filepath.Clean(out) {
				break
			}
			if parent == filepath.Dir(parent) {
				return "", fmt.Errorf("invalid material root")
			}
		}
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 {
			return "", fmt.Errorf("identity materials incomplete or changed")
		}
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read identity material")
		}
		opened, statErr := f.Stat()
		raw, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		after, finalErr := f.Stat()
		_ = f.Close()
		final, pathErr := os.Lstat(path)
		if statErr != nil || readErr != nil || finalErr != nil || pathErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) || !os.SameFile(after, final) || before.Size() != int64(len(raw)) || after.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) || !after.ModTime().Equal(final.ModTime()) {
			clear(raw)
			return "", fmt.Errorf("changed identity material")
		}
		sum := sha256.Sum256(raw)
		clear(raw)
		_, _ = fmt.Fprintf(h, "%s:%x\n", name, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func SealReceipt(out, inputDigest string, set MaterialSet) error {
	if err := set.validate(); err != nil {
		return err
	}
	digest, err := materialDigest(out, set)
	if err != nil {
		return err
	}
	p, err := Open(out)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := p.Create("bootstrap.receipt", []byte(inputDigest+"\n"+digest+"\n")); err != nil {
		return err
	}
	return p.Validate()
}

func CheckReceipt(out, inputDigest string, set MaterialSet) (bool, error) {
	if err := set.validate(); err != nil {
		return false, err
	}
	p, err := Open(out)
	if err != nil {
		return false, err
	}
	defer p.Close()
	raw, err := p.Read("bootstrap.receipt")
	if os.IsNotExist(err) {
		for _, name := range set.ManagedDirectories {
			path := filepath.Join(out, filepath.FromSlash(name))
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return false, fmt.Errorf("unreceipted %s must be an empty real directory", name)
			}
			entries, readErr := os.ReadDir(path)
			if readErr != nil || len(entries) != 0 {
				return false, fmt.Errorf("unreceipted %s must be an empty real directory", name)
			}
		}
		for _, name := range set.PublicFiles {
			if _, err := os.Lstat(filepath.Join(out, filepath.FromSlash(name))); !os.IsNotExist(err) {
				return false, fmt.Errorf("existing identity materials require a complete bootstrap receipt; use a fresh installation directory")
			}
		}
		for _, name := range set.PrivateFiles {
			if _, err := p.Read(name); !os.IsNotExist(err) {
				return false, fmt.Errorf("existing installation requires a complete bootstrap receipt")
			}
		}
		return false, p.Validate()
	}
	digest, digestErr := materialDigest(out, set)
	if err != nil || digestErr != nil || string(raw) != inputDigest+"\n"+digest+"\n" {
		return false, fmt.Errorf("installation inputs or identity materials changed; refusing automatic rotation")
	}
	for _, name := range set.PrivateFiles {
		if _, err := p.Read(name); err != nil {
			return false, fmt.Errorf("incomplete private installation")
		}
	}
	return true, p.Validate()
}
