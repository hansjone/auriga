package main

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// The runtime tool is the existing installer pin, separate from build Corepack.
// Preserve original registry TGZ bytes; never repack the installed payload tree.
func verifyRuntimeToolingInput(ctx context.Context, source, file string) error {
	var upstream struct {
		Version   string `json:"pnpm"`
		Integrity string `json:"pnpmIntegrity"`
	}
	raw, err := smallFileContext(ctx, filepath.Join(source, "clients/installer/upstream.json"), 64<<10)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return err
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	raw, err = smallFileContext(ctx, filepath.Join(source, "deploy/compose/real-dsh/tooling/package.json"), 64<<10)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return err
	}
	var lock struct {
		Packages map[string]struct {
			Version      string            `json:"version"`
			Integrity    string            `json:"integrity"`
			Resolved     string            `json:"resolved"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	raw, err = smallFileContext(ctx, filepath.Join(source, "deploy/compose/real-dsh/tooling/package-lock.json"), 64<<10)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return err
	}
	pin := lock.Packages["node_modules/pnpm"]
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(upstream.Version) ||
		len(pkg.Dependencies) != 1 || pkg.Dependencies["pnpm"] != upstream.Version || len(lock.Packages) != 2 ||
		lock.Packages[""].Dependencies["pnpm"] != upstream.Version || pin.Version != upstream.Version ||
		pin.Integrity != upstream.Integrity || pin.Resolved != "https://registry.npmjs.org/pnpm/-/pnpm-"+upstream.Version+".tgz" {
		return errors.New("运行 pnpm 输入与现有 installer pin/lock 不符")
	}
	decoded, err := base64.StdEncoding.DecodeString(trimSHA512(upstream.Integrity))
	if err != nil || len(decoded) != 64 {
		return errors.New("运行 pnpm SHA512 pin 无效")
	}
	if err := noSymlinks(file); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 64<<20 {
		return errors.New("运行 pnpm TGZ 类型或长度无效")
	}
	h := sha512.New()
	if _, err := io.Copy(h, cancellationReader(ctx, f)); err != nil {
		return err
	}
	if "sha512-"+base64.StdEncoding.EncodeToString(h.Sum(nil)) != upstream.Integrity {
		return errors.New("运行 pnpm TGZ 完整性不符")
	}
	return nil
}

func trimSHA512(value string) string {
	if len(value) > 7 && value[:7] == "sha512-" {
		return value[7:]
	}
	return ""
}
