package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type releaseOptions struct {
	Source, Tag, Index, Out, Cache, Key string
	Limit                               int64
	Build                               bool
}
type releaseBuilders struct {
	Bundle     func([]string, string) error
	Devkit     func([]string) error
	Executable string
}
type cachedKit struct {
	Asset        assetInfo     `json:"asset"`
	Locks        lockInventory `json:"locks"`
	SourceCommit string        `json:"sourceCommit"`
	SourceTag    string        `json:"sourceTag"`
}
type distributionManifest struct {
	Schema int           `json:"schemaVersion"`
	Tag    string        `json:"tag"`
	Commit string        `json:"commit"`
	Locks  lockInventory `json:"locks"`
	Devkit cachedKit     `json:"devkit"`
	Assets []assetInfo   `json:"assets"`
}

func releaseCommand(args []string) error {
	if len(args) > 0 && args[0] == "upload" {
		return uploadCommand(args[1:])
	}
	f := flags("release")
	var o releaseOptions
	f.StringVar(&o.Tag, "tag", "", "")
	f.StringVar(&o.Index, "release", "auriga-release.json", "")
	f.StringVar(&o.Out, "outdir", "", "")
	f.StringVar(&o.Cache, "cache-dir", ".offline-devkit-cache", "")
	f.StringVar(&o.Key, "sign-key", "", "")
	f.Int64Var(&o.Limit, "attachment-limit", (4<<30)-1, "")
	f.BoolVar(&o.Build, "build", false, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 release 参数")
	}
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	o.Source = source
	if o.Out == "" {
		o.Out = filepath.Join(".offline-releases", o.Tag)
	}
	return generateRelease(o, releaseBuilders{Bundle: bundleExpected, Devkit: devkit})
}

func releaseIdentity(source, tag string) (string, error) {
	if !regexpVersion.MatchString(tag) {
		return "", errors.New("需要明确且有效的 --tag")
	}
	head, err := gitAt(source, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	target, err := gitAt(source, "rev-parse", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return "", errors.New("发行标签不存在")
	}
	if target != head {
		return "", errors.New("HEAD 必须精确对应发行标签，不自动切分支")
	}
	status, err := gitAt(source, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", errors.New("标签发行要求源码干净；先提交并标记版本，构建输出请放 Git 忽略目录或源码之外")
	}
	return head, nil
}

func signingPublic(privatePath, target string) error {
	raw, err := smallFile(privatePath, 64<<10)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return errors.New("需要 Ed25519 PKCS8 PEM 私钥")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return errors.New("签名需要 Ed25519")
	}
	der, err := x509.MarshalPKIXPublicKey(private.Public())
	if err != nil {
		return err
	}
	return os.WriteFile(target, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0644)
}

func devkitByLocks(o releaseOptions, locks lockInventory, commit, index, public string, build func([]string) error) (string, cachedKit, error) {
	pubBytes, err := os.ReadFile(public)
	if err != nil {
		return "", cachedKit{}, err
	}
	signer := sha256.Sum256(pubBytes)
	cache := filepath.Join(o.Cache, locks.Digest+"-"+hex.EncodeToString(signer[:8]))
	record := filepath.Join(cache, "devkit-cache.json")
	var cached cachedKit
	if _, err := os.Lstat(cache); err == nil {
		if err := noSymlinks(cache); err != nil {
			return "", cached, err
		}
		if err := verifyFileSignature(record, public); err != nil {
			return "", cached, fmt.Errorf("缓存签名无效，拒绝复用: %w", err)
		}
		if err := readJSON(record, &cached); err != nil {
			return "", cached, err
		}
		if cached.Locks.Digest != locks.Digest || cached.Asset.Name != "auriga-devkit-"+locks.Digest+".tar" {
			return "", cached, errors.New("devkit 缓存锁清单无效")
		}
		file := filepath.Join(cache, cached.Asset.Name)
		if err := noSymlinks(file); err != nil {
			return "", cached, err
		}
		actual, err := describeAsset(file)
		if err != nil {
			return "", cached, err
		}
		if actual != cached.Asset {
			return "", cached, errors.New("devkit 缓存字节被修改，拒绝复用")
		}
		fmt.Printf("复用 devkit 锁 SHA256 %s，原始版本 %s\n", locks.Digest, cached.SourceTag)
		return file, cached, nil
	} else if !os.IsNotExist(err) {
		return "", cached, err
	}
	if err := os.MkdirAll(o.Cache, 0755); err != nil {
		return "", cached, err
	}
	stage, err := os.MkdirTemp(o.Cache, ".devkit-")
	if err != nil {
		return "", cached, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	file := filepath.Join(stage, "auriga-devkit-"+locks.Digest+".tar")
	if err := build([]string{"--release", index, "--out", file, "--sign-key", o.Key}); err != nil {
		return "", cached, err
	}
	asset, err := describeAsset(file)
	if err != nil {
		return "", cached, err
	}
	cached = cachedKit{Asset: asset, Locks: locks, SourceCommit: commit, SourceTag: o.Tag}
	record = filepath.Join(stage, "devkit-cache.json")
	if err := jsonFile(record, cached, 0644); err != nil {
		return "", cached, err
	}
	if err := signFile(record, o.Key); err != nil {
		return "", cached, err
	}
	if err := os.Rename(stage, cache); err != nil {
		return "", cached, err
	}
	return filepath.Join(cache, asset.Name), cached, nil
}

func generateRelease(o releaseOptions, builders releaseBuilders) error {
	if o.Key == "" || o.Out == "" || o.Cache == "" || o.Limit <= 0 {
		return errors.New("release 需要 --sign-key、输出目录、缓存目录和正数附件上限")
	}
	commit, err := releaseIdentity(o.Source, o.Tag)
	if err != nil {
		return err
	}
	var r release
	if err := readJSON(o.Index, &r); err != nil {
		return err
	}
	r.Version = o.Tag
	r.Source = nil
	locks, err := dependencyLocks(o.Source, r)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(o.Out); !os.IsNotExist(err) {
		return errors.New("发行输出目录已存在或不可访问，不覆盖已有附件")
	}
	if err := os.MkdirAll(filepath.Dir(o.Out), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(o.Out), ".auriga-release-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	// Keep the index, verification key and temporary artifacts out of the source
	// snapshot and out of the published inventory.
	scratch, err := os.MkdirTemp("", "auriga-release-input-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := filepath.Join(scratch, "release.json")
	public := filepath.Join(scratch, "public.pem")
	if err := signingPublic(o.Key, public); err != nil {
		return err
	}
	if err := jsonFile(index, r, 0644); err != nil {
		return err
	}
	offline := filepath.Join(stage, "auriga-offline-"+o.Tag+".tar")
	args := []string{"--release", index, "--out", offline, "--sign-key", o.Key}
	if o.Build {
		args = append(args, "--build", "--version", o.Tag)
	}
	if err := builders.Bundle(args, commit); err != nil {
		return err
	}
	kit, cached, err := devkitByLocks(o, locks, commit, index, public, builders.Devkit)
	if err != nil {
		return err
	}
	kitTarget := filepath.Join(stage, "auriga-devkit-"+o.Tag+".tar")
	if err := copyFile(kit, kitTarget, 0644); err != nil {
		return err
	}
	var assets []assetInfo
	for _, file := range []string{offline, kitTarget} {
		names, err := splitAsset(file, o.Limit, o.Key)
		if err != nil {
			return err
		}
		for _, name := range names {
			info, err := describeAsset(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			assets = append(assets, info)
		}
		// Legacy bundle/devkit write a detached hash for human copy verification.
		// Distribution uses the single signed SHA256SUMS for all release assets.
		if err := os.Remove(file + ".sha256"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if builders.Executable != "" {
		exe = builders.Executable
	}
	if err := copyFile(exe, filepath.Join(stage, "auriga"), 0755); err != nil {
		return err
	}
	cli, err := describeAsset(filepath.Join(stage, "auriga"))
	if err != nil {
		return err
	}
	assets = append(assets, cli)
	manifest := distributionManifest{Schema: 1, Tag: o.Tag, Commit: commit, Locks: locks, Devkit: cached, Assets: assets}
	if err := jsonFile(filepath.Join(stage, "release-manifest.json"), manifest, 0644); err != nil {
		return err
	}
	if err := writeSums(stage); err != nil {
		return err
	}
	if err := signSums(stage, o.Key); err != nil {
		return err
	}
	names, err := regularFiles(stage)
	if err != nil {
		return err
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(stage, name))
		if err != nil {
			return err
		}
		if info.Size() > effectivePartLimit(o.Limit) {
			return fmt.Errorf("附件上限太小，元数据或引导程序 %s 超限，请增大 --attachment-limit", name)
		}
	}
	// Refuse a checkout or lock edit made during a long release build.
	if head, err := releaseIdentity(o.Source, o.Tag); err != nil || head != commit {
		return errors.New("发行期间源码发生变化，拒绝发布输出")
	}
	finalLocks, err := dependencyLocks(o.Source, r)
	if err != nil {
		return err
	}
	if finalLocks.Digest != locks.Digest {
		return errors.New("发行期间依赖锁发生变化")
	}
	if err := os.Rename(stage, o.Out); err != nil {
		return err
	}
	fmt.Printf("发行附件已生成 %s\n标签 %s\n提交 %s\n锁 SHA256 %s\n尚未上传；仅由主控运行 publish-forgejo.sh。\n", o.Out, o.Tag, commit, locks.Digest)
	return nil
}
