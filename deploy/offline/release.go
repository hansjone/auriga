package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type releaseOptions struct {
	Source, Tag, Index, Out, Cache, Key  string
	WindowsClient, WindowsSHA, DSHSource string
	Limit                                int64
	Build                                bool
	SourceOnly                           bool
	Previous                             string
	NewBaseline                          bool
	MaxBytes                             int64
}
type releaseBuilders struct {
	Bundle     func([]string, string) error
	Devkit     func([]string) error
	Executable string
	Gate       func(gateRequest) (buildGate, error)
}
type cachedKit struct {
	Asset        assetInfo     `json:"asset"`
	Locks        lockInventory `json:"locks"`
	SourceCommit string        `json:"sourceCommit"`
	SourceTag    string        `json:"sourceTag"`
}
type distributionManifest struct {
	Schema      int                  `json:"schemaVersion"`
	Tag         string               `json:"tag"`
	Commit      string               `json:"commit"`
	Locks       lockInventory        `json:"locks"`
	Devkit      cachedKit            `json:"devkit"`
	Assets      []assetInfo          `json:"assets"`
	Archives    []assetInfo          `json:"archives"`
	Baseline    string               `json:"baseline"`
	Versions    map[string]string    `json:"supportedVersions"`
	ResetReason string               `json:"resetReason,omitempty"`
	Gate        buildGate            `json:"offlineBuildGate"`
	Windows     *windowsReleaseInput `json:"windowsClient,omitempty"`
	SourceOnly  bool                 `json:"sourceOnly,omitempty"`
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
	f.StringVar(&o.WindowsClient, "windows-client", "", "Windows 签名客户端输入 tar")
	f.StringVar(&o.WindowsSHA, "windows-client-sha256", "", "Windows 侧确认的完整 tar SHA256")
	f.StringVar(&o.DSHSource, "dsh-source", "", "批准的 DSH 源码归档文件或 Forgejo HTTPS 附件 URL")
	f.Int64Var(&o.Limit, "attachment-limit", 0, "附件分卷上限（字节）；0 发布完整归档，正数适用于受限传输介质")
	f.BoolVar(&o.Build, "build", false, "")
	f.BoolVar(&o.SourceOnly, "source-only", false, "只发布源码、开发套件和参考材料，安装包仅用于断网构建验证")
	f.StringVar(&o.Previous, "previous-devkit", "", "")
	f.BoolVar(&o.NewBaseline, "new-baseline", false, "")
	f.Int64Var(&o.MaxBytes, "max-devkit-bytes", 64<<30, "")
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
	return generateRelease(o, releaseBuilders{Gate: offlineBuildGate})
}

func releaseIdentity(source, tag string) (string, error) {
	return releaseIdentityContext(context.Background(), source, tag)
}

func releaseIdentityContext(ctx context.Context, source, tag string) (string, error) {
	if !regexpVersion.MatchString(tag) {
		return "", errors.New("需要明确且有效的 --tag")
	}
	if _, err := os.Lstat(filepath.Join(source, "auriga-source.json")); err == nil {
		m, err := readSourceManifestContext(ctx, source)
		if err != nil {
			return "", err
		}
		names, err := archiveSourceNamesContext(ctx, source)
		if err != nil {
			return "", err
		}
		_, hash, err := fileInventoryContext(ctx, source, names, m.Files)
		if err != nil {
			return "", err
		}
		if m.Source.UpstreamVersion != tag || hash != m.Source.SnapshotSHA256 {
			return "", errors.New("发行要求精确且未修改的源码归档")
		}
		return m.Source.Commit, nil
	}
	head, err := gitAtContext(ctx, source, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	target, err := gitAtContext(ctx, source, "rev-parse", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return "", errors.Join(errors.New("发行标签不存在"), ctx.Err())
	}
	if target != head {
		return "", errors.New("HEAD 必须精确对应发行标签，不自动切分支")
	}
	status, err := gitAtContext(ctx, source, "status", "--porcelain", "--untracked-files=all")
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

func devkitByLocksContext(ctx context.Context, o releaseOptions, locks lockInventory, commit, index, public string, build func([]string) error) (string, cachedKit, error) {
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
		actual, err := describeAssetContext(ctx, file)
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
	asset, err := describeAssetContext(ctx, file)
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
	if err := ctx.Err(); err != nil {
		return "", cached, err
	}
	if err := os.Rename(stage, cache); err != nil {
		return "", cached, err
	}
	return filepath.Join(cache, asset.Name), cached, nil
}

func generateRelease(o releaseOptions, builders releaseBuilders) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if builders.Devkit == nil {
		builders.Devkit = func(args []string) error { return devkitSingleContext(ctx, args) }
	}
	if builders.Bundle == nil {
		builders.Bundle = func(args []string, commit string) error { return bundleExpectedContext(ctx, args, commit) }
	}
	if o.Key == "" || o.Out == "" || o.Cache == "" || o.Limit < 0 {
		return errors.New("release 需要 --sign-key、输出目录、缓存目录和非负附件上限")
	}
	if o.SourceOnly && o.Build {
		return errors.New("--source-only 不接受用于选择安装附件的 --build；断网构建验证自动运行")
	}
	if o.WindowsClient == "" || !hex64.MatchString(o.WindowsSHA) || o.DSHSource == "" {
		return errors.New("release 需要 --windows-client、--windows-client-sha256 和 --dsh-source")
	}
	if o.Previous == "" && !o.NewBaseline {
		return errors.New("累积发行必须 --previous-devkit 上版完整套件目录；首次或主动收拢需 --new-baseline")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 64 << 30
	}
	var pathErr error
	o.Key, pathErr = filepath.Abs(o.Key)
	if pathErr != nil {
		return pathErr
	}
	commit, err := releaseIdentityContext(ctx, o.Source, o.Tag)
	if err != nil {
		return err
	}
	var r release
	if err := readJSON(o.Index, &r); err != nil {
		return err
	}
	r.Version = o.Tag
	r.Source = nil
	if r.Build == nil {
		return errors.New("离线发行需要build依赖与Linux构建配方")
	}
	locks, err := dependencyLocksContext(ctx, o.Source, r)
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
	windowsTar := filepath.Join(stage, filepath.Base(o.WindowsClient))
	if err := noSymlinks(o.WindowsClient); err != nil {
		return err
	}
	if err := copyFileContext(ctx, o.WindowsClient, windowsTar, 0644); err != nil {
		return err
	}
	windows, err := verifyWindowsRelease(ctx, windowsTar, o.WindowsSHA, o.Source, public, scratch)
	if err != nil {
		return fmt.Errorf("Windows 客户端附件: %w", err)
	}
	dshArchive, extraFiles, err := stageDSHSource(ctx, o.Source, o.DSHSource, stage)
	if err != nil {
		return fmt.Errorf("DSH 上游源码附件: %w", err)
	}
	if err := jsonFile(index, r, 0644); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if builders.Executable != "" {
		exe = builders.Executable
	}
	sourceRoot := filepath.Join(scratch, "source")
	if err := prepareSourceArchiveContext(ctx, o.Source, sourceRoot, r, o.Key, exe); err != nil {
		return err
	}
	kit, cached, err := devkitByLocksContext(ctx, o, locks, commit, index, public, builders.Devkit)
	if err != nil {
		return err
	}
	kitTarget := filepath.Join(stage, "auriga-devkit-"+o.Tag+".tar")
	single := filepath.Join(scratch, "single")
	if err := extractWithTrustContext(ctx, kit, single, trustOptions{Key: public}, false); err != nil {
		return err
	}
	if err := refreshReleaseInputsContext(ctx, single, r, o.Key); err != nil {
		return err
	}
	if err := writeSumsContext(ctx, sourceRoot); err != nil {
		return err
	}
	if err := signSums(sourceRoot, o.Key); err != nil {
		return err
	}
	sourceTar := filepath.Join(stage, "auriga-source-"+o.Tag+".tar")
	if err := packContext(ctx, sourceRoot, sourceTar); err != nil {
		return err
	}
	sourceAsset, err := describeAssetContext(ctx, sourceTar)
	if err != nil {
		return err
	}
	cumulative := filepath.Join(scratch, "cumulative")
	cat, err := composeDevkitContext(ctx, o.Previous, single, o.Tag, commit, cumulative, o.Key, o.MaxBytes, o.NewBaseline)
	if err != nil {
		return err
	}
	if err := packContext(ctx, cumulative, kitTarget); err != nil {
		return err
	}
	releaseArchives := []string{kitTarget, sourceTar, windowsTar, dshArchive}
	if o.Previous != "" && cat.Baseline != o.Tag {
		deltaTarget := filepath.Join(stage, "auriga-devkit-"+o.Tag+"-delta.tar")
		if err := writeDevkitDeltaContext(ctx, o.Previous, cumulative, deltaTarget, o.Key); err != nil {
			return err
		}
		releaseArchives = append(releaseArchives, deltaTarget)
	}
	catalogSHA, err := digestContext(ctx, filepath.Join(cumulative, "devkit-catalog.json"))
	if err != nil {
		return err
	}
	gateOut := filepath.Join(scratch, "gate.tar")
	kitSHA, err := digestContext(ctx, kitTarget)
	if err != nil {
		return err
	}
	if builders.Gate == nil {
		return errors.New("缺少断网构建门禁")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gate, err := builders.Gate(gateRequest{Context: ctx, Source: sourceRoot, Kit: cumulative, Out: gateOut, Executable: exe, Key: o.Key, Tag: o.Tag, Commit: commit, LockDigest: locks.Digest, SourceSHA: sourceAsset.SHA256, CatalogSHA: catalogSHA, DevkitSHA: kitSHA})
	if err != nil {
		return fmt.Errorf("断网构建门禁失败；补齐devkit后重新发版: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !gate.Passed || gate.Commit != commit || gate.LockDigest != locks.Digest || gate.SourceSHA != sourceAsset.SHA256 || gate.CatalogSHA != catalogSHA || gate.DevkitSHA != kitSHA {
		return errors.New("断网构建门禁未通过或来源不符")
	}
	if !o.SourceOnly {
		offline := filepath.Join(stage, "auriga-offline-"+o.Tag+".tar")
		if o.Build {
			if err := copyFileContext(ctx, gateOut, offline, 0644); err != nil {
				return err
			}
		} else if err := builders.Bundle([]string{"--release", index, "--out", offline, "--sign-key", o.Key}, commit); err != nil {
			return err
		}
		releaseArchives = append(releaseArchives, offline)
	}
	var assets []assetInfo
	var archives []assetInfo
	partLimit := effectivePartLimit(o.Limit)
	for _, file := range releaseArchives {
		original, err := describeAssetContext(ctx, file)
		if err != nil {
			return err
		}
		archives = append(archives, original)
		if partLimit == 0 || original.Size <= partLimit {
			assets = append(assets, original)
		} else {
			names, err := splitAssetContext(ctx, file, partLimit, o.Key)
			if err != nil {
				return err
			}
			for _, name := range names {
				info, err := describeAssetContext(ctx, filepath.Join(stage, name))
				if err != nil {
					return err
				}
				assets = append(assets, info)
			}
		}
		// Legacy bundle/devkit write a detached hash for human copy verification.
		// Distribution uses the single signed SHA256SUMS for all release assets.
		if err := os.Remove(file + ".sha256"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, file := range extraFiles {
		info, err := describeAssetContext(ctx, file)
		if err != nil {
			return err
		}
		assets = append(assets, info)
	}
	if err := copyFileContext(ctx, exe, filepath.Join(stage, "auriga"), 0755); err != nil {
		return err
	}
	cli, err := describeAssetContext(ctx, filepath.Join(stage, "auriga"))
	if err != nil {
		return err
	}
	assets = append(assets, cli)
	if err := signingPublic(o.Key, filepath.Join(stage, "release-public.pem")); err != nil {
		return err
	}
	pubAsset, err := describeAssetContext(ctx, filepath.Join(stage, "release-public.pem"))
	if err != nil {
		return err
	}
	assets = append(assets, pubAsset)
	manifest := distributionManifest{Schema: 1, Tag: o.Tag, Commit: commit, Locks: locks, Devkit: cached, Assets: assets, Archives: archives, Baseline: cat.Baseline, ResetReason: cat.ResetReason, Gate: gate, Windows: &windows, SourceOnly: o.SourceOnly}
	if err := writeDownloads(stage, manifest, cat, assets); err != nil {
		return err
	}
	downloads, err := describeAssetContext(ctx, filepath.Join(stage, "DOWNLOADS.txt"))
	if err != nil {
		return err
	}
	assets = append(assets, downloads)
	versions := map[string]string{}
	for tag, entry := range cat.Versions {
		versions[tag] = entry.Kit.Locks.Digest
	}
	manifest.Assets = assets
	manifest.Versions = versions
	if err := finalizeDistribution(ctx, stage, manifest, cat, o.Key); err != nil {
		return err
	}
	if partLimit > 0 {
		names, err := regularFilesContext(ctx, stage)
		if err != nil {
			return err
		}
		for _, name := range names {
			info, err := os.Stat(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			if info.Size() > partLimit {
				return fmt.Errorf("附件上限太小，元数据或引导程序 %s 超限，请增大 --attachment-limit", name)
			}
		}
	}
	// Refuse a checkout or lock edit made during a long release build.
	if head, err := releaseIdentityContext(ctx, o.Source, o.Tag); err != nil || head != commit {
		return errors.Join(errors.New("发行期间源码发生变化，拒绝发布输出"), ctx.Err())
	}
	finalLocks, err := dependencyLocksContext(ctx, o.Source, r)
	if err != nil {
		return err
	}
	if finalLocks.Digest != locks.Digest {
		return errors.New("发行期间依赖锁发生变化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(stage, o.Out); err != nil {
		return err
	}
	fmt.Printf("发行附件已生成 %s\n标签 %s\n提交 %s\n锁 SHA256 %s\n尚未上传；仅由主控运行 publish-forgejo.sh。\n", o.Out, o.Tag, commit, locks.Digest)
	return nil
}
