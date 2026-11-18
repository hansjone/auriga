package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

const windowsClientPrefix = "auriga-client-windows-"
const windowsBuildNodePath = "build-node/node.exe"

type windowsReleaseInput struct {
	Schema       int    `json:"schemaVersion"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	LockSHA      string `json:"lockSha256"`
	ManifestSHA  string `json:"manifestSha256"`
	BuildNodeSHA string `json:"buildNodeSha256"`
}

type windowsClientManifest struct {
	Schema    int               `json:"schema"`
	Platform  string            `json:"platform"`
	BuildNode string            `json:"buildNode"`
	LockSHA   string            `json:"lockSha256"`
	Locks     map[string]string `json:"locks"`
	Files     []struct {
		Path string `json:"path"`
		Size int64  `json:"bytes"`
		SHA  string `json:"sha256"`
	} `json:"files"`
	Bytes int64 `json:"bytes"`
}

// Same lock paths and canonical key order as scripts/client-release/inputs.mjs.
// Hash the raw files because the Windows tool checks the exact checkout bytes.
func windowsReleaseLocks(ctx context.Context, root, dshVersion string) (map[string]string, error) {
	names := []string{
		"package.json", "pnpm-workspace.yaml", ".npmrc", "web/pnpm-lock.yaml",
		"clients/dsh-plugin/package.json", "clients/dsh-plugin/package-lock.json",
		"clients/installer/upstream.json", "clients/installer/locks/python-win-x64.json",
		"clients/installer/locks/dsh-" + dshVersion + ".package-lock.json",
	}
	for _, base := range []string{"web", "clients"} {
		entries, err := os.ReadDir(filepath.Join(root, base))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := base + "/" + entry.Name() + "/package.json"
			_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
			if err == nil {
				names = append(names, name)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	locks := make(map[string]string, len(names))
	for _, name := range names {
		digest, err := digestContext(ctx, filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		locks[name] = digest
	}
	return locks, nil
}

func verifyWindowsRelease(ctx context.Context, archive, expectedSHA, source, public, scratch string) (windowsReleaseInput, error) {
	var release windowsReleaseInput
	base := filepath.Base(archive)
	stem := strings.TrimSuffix(base, ".tar")
	lockSHA, ok := strings.CutPrefix(stem, windowsClientPrefix)
	if !strings.HasSuffix(base, ".tar") || !ok || !hex64.MatchString(lockSHA) || !hex64.MatchString(expectedSHA) {
		return release, errors.New("需要 auriga-client-windows-<锁SHA256>.tar 及批准的完整归档摘要")
	}

	// Read the transferred tar without creating its tens of thousands of files
	// on the maintainer's disk. Keep only the signed metadata and file hashes.
	meta, err := os.MkdirTemp(scratch, "windows-metadata-")
	if err != nil {
		return release, err
	}
	defer func() { _ = os.RemoveAll(meta) }()
	in, err := os.Open(archive)
	if err != nil {
		return release, err
	}
	defer func() { _ = in.Close() }()
	archiveHash := sha256.New()
	stream := io.TeeReader(cancellationReader(ctx, in), archiveHash)
	tarReader := tar.NewReader(stream)
	limits := map[string]int64{
		"SHA256SUMS": 4 << 20, "SHA256SUMS.sig": 1024, "release-public.pem": 64 << 10,
		"client-inputs-release.json": 64 << 10, "windows-client.json": 32 << 20,
	}
	seen := map[string]bool{}
	portable := map[string]string{}
	payload := map[string]assetInfo{}
	var size int64
	for {
		h, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return release, err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == stem && h.Typeflag == tar.TypeDir && !seen[name] {
			seen[name] = true
			continue
		}
		if seen[name] || len(seen) >= 200000 || h.Size < 0 || h.Size > (128<<30)-size {
			return release, errors.New("Windows tar 的根目录、重复路径或大小无效")
		}
		seen[name] = true
		name, ok := strings.CutPrefix(name, stem+"/")
		if !ok {
			return release, errors.New("Windows tar 的根目录与归档名称不一致")
		}
		if err := portablePath(portable, name); err != nil {
			return release, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return release, errors.New("Windows tar 含链接或特殊文件")
		}
		if limit, ok := limits[name]; ok {
			if h.Size > limit {
				return release, fmt.Errorf("Windows 发行元数据超限 %s", name)
			}
			raw, err := io.ReadAll(tarReader)
			if err != nil {
				return release, err
			}
			if err := os.WriteFile(filepath.Join(meta, name), raw, 0600); err != nil {
				return release, err
			}
			continue
		}
		hash := sha256.New()
		if _, err := io.CopyN(hash, tarReader, h.Size); err != nil {
			return release, err
		}
		payload[name] = assetInfo{Name: name, Size: h.Size, SHA256: fmt.Sprintf("%x", hash.Sum(nil))}
		size += h.Size
	}
	// System tar may pad the final record. Reject hidden entries appended
	// after the terminator while still hashing the exact transferred bytes.
	var padding [32 << 10]byte
	for {
		n, err := stream.Read(padding[:])
		if len(bytes.Trim(padding[:n], "\x00")) != 0 {
			return release, errors.New("Windows tar 结束标记后包含数据")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return release, err
		}
	}
	if fmt.Sprintf("%x", archiveHash.Sum(nil)) != expectedSHA {
		return release, errors.New("完整 Windows tar 摘要与转运批准值不符")
	}
	if err := verifySignature(meta, public); err != nil {
		return release, err
	}
	expectedKey, err := publicFingerprint(public)
	if err != nil {
		return release, err
	}
	if actual, err := publicFingerprint(filepath.Join(meta, "release-public.pem")); err != nil || actual != expectedKey {
		return release, errors.New("Windows tar 公钥与本次发行公钥不一致")
	}
	manifestBytes, err := smallFileContext(ctx, filepath.Join(meta, "windows-client.json"), limits["windows-client.json"])
	if err != nil {
		return release, err
	}
	releaseFile := filepath.Join(meta, "client-inputs-release.json")
	releaseSHA, err := digestContext(ctx, releaseFile)
	if err != nil {
		return release, err
	}
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(manifestBytes))
	sums, err := smallFileContext(ctx, filepath.Join(meta, "SHA256SUMS"), limits["SHA256SUMS"])
	if err != nil {
		return release, err
	}
	if string(sums) != fmt.Sprintf("%s  client-inputs-release.json\n%s  windows-client.json\n", releaseSHA, manifestSHA) {
		return release, errors.New("Windows 签名清单与内部元数据不一致")
	}
	if err := readJSON(releaseFile, &release); err != nil {
		return release, err
	}
	if release.Schema != 1 || release.Platform != "win-x64" || !regexpVersion.MatchString(release.Version) ||
		release.LockSHA != lockSHA || release.ManifestSHA != manifestSHA || release.BuildNodeSHA != payload[windowsBuildNodePath].SHA256 ||
		!hex64.MatchString(release.BuildNodeSHA) {
		return release, errors.New("Windows 签名发行、归档名称或引导 Node 摘要不一致")
	}
	var m windowsClientManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return release, err
	}
	return release, verifyWindowsClientManifest(ctx, source, release, m, payload, size)
}

func verifyWindowsClientManifest(ctx context.Context, source string, release windowsReleaseInput, m windowsClientManifest, payload map[string]assetInfo, size int64) error {
	if m.Schema != 1 || m.Platform != release.Platform || m.LockSHA != release.LockSHA || m.Bytes != size || len(m.Files) != len(payload) {
		return errors.New("Windows 输入清单和归档不一致")
	}
	for _, row := range m.Files {
		actual, ok := payload[row.Path]
		if !ok || actual.Size != row.Size || actual.SHA256 != row.SHA {
			return fmt.Errorf("Windows 输入未登记、重复或摘要不符 %s", row.Path)
		}
		delete(payload, row.Path)
	}
	upstream, err := readDSHInstallerPin(source)
	if err != nil {
		return err
	}
	locks, err := windowsReleaseLocks(ctx, source, upstream.Version)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(struct {
		BuildNode string            `json:"buildNode"`
		Locks     map[string]string `json:"locks"`
		Platform  string            `json:"platform"`
		Schema    int               `json:"schema"`
	}{m.BuildNode, locks, m.Platform, m.Schema})
	if err != nil {
		return err
	}
	if !maps.Equal(locks, m.Locks) || fmt.Sprintf("%x", sha256.Sum256(canonical)) != m.LockSHA {
		return errors.New("Windows 输入依赖锁与目标源码不匹配")
	}
	return nil
}
