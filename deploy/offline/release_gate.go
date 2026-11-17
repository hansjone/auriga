package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type gateRequest struct {
	Context    context.Context
	Source     string
	Kit        string
	Out        string
	Executable string
	Key        string
	Tag        string
	Commit     string
	LockDigest string
	SourceSHA  string
	CatalogSHA string
	DevkitSHA  string
}
type buildGate struct {
	Passed     bool   `json:"passed"`
	Commit     string `json:"commit"`
	LockDigest string `json:"lockDigest"`
	SourceSHA  string `json:"sourceArchiveSha256"`
	CatalogSHA string `json:"catalogSha256"`
	DevkitSHA  string `json:"devkitArchiveSha256"`
	OutputSHA  string `json:"buildOutputSha256"`
	Network    string `json:"network"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

func offlineBuildGate(o gateRequest) (proof buildGate, gateErr error) {
	proof = buildGate{Commit: o.Commit, LockDigest: o.LockDigest, SourceSHA: o.SourceSHA, CatalogSHA: o.CatalogSHA, DevkitSHA: o.DevkitSHA, Network: gateNetwork, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	parent := o.Context
	if parent == nil {
		parent = context.Background()
	}
	interrupted, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, gateBuildTimeout)
	defer cancel()
	if err := verifyCumulative(o.Kit, o.Source, o.Tag, ""); err != nil {
		return proof, err
	}
	if err := ctx.Err(); err != nil {
		return proof, err
	}
	workspace, err := newGateWorkspace()
	if err != nil {
		return proof, err
	}
	defer func() {
		closeErr := workspace.close()
		gateErr = errors.Join(gateErr, closeErr, ctx.Err())
		if gateErr != nil {
			proof.Passed = false
		}
	}()
	cmd := exec.CommandContext(ctx, "unshare", "--net", "--mount", "--propagation", "private", "--pid", "--fork", "--mount-proc", "--kill-child=KILL", o.Executable, "internal-offline-build", "--source", o.Source, "--version", o.Tag, "--out", o.Out, "--sign-key", o.Key, "--workdir", workspace.root, "--runtime-dir", workspace.runtime)
	cmd.Dir = o.Source
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setGateProcessGroup(cmd)
	cmd.WaitDelay = 45 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return proof, fmt.Errorf("断网构建门禁已取消（完整构建时限 %s）: %w", gateBuildTimeout, ctx.Err())
		}
		return proof, fmt.Errorf("必须具有Linux unshare网络命名空间权限且所有依赖齐备（无在线回退）: %w", err)
	}
	extracted, err := os.MkdirTemp("", "auriga-gate-result-")
	if err != nil {
		return proof, err
	}
	defer func() { gateErr = errors.Join(gateErr, os.RemoveAll(extracted)) }()
	result := filepath.Join(extracted, "package")
	if err := extractWithTrustContext(ctx, o.Out, result, trustOptions{}, false); err != nil {
		return proof, err
	}
	var info provenance
	if err := readJSON(filepath.Join(result, "provenance.json"), &info); err != nil {
		return proof, err
	}
	if info.Commit != o.Commit || info.UpstreamVersion != o.Tag || info.Version != o.Tag || info.SiteChanges || info.Dirty {
		return proof, errors.New("门禁产物不是精确的干净目标源码")
	}
	proof.OutputSHA, err = digestContext(ctx, o.Out)
	if err != nil {
		return proof, err
	}
	if err := ctx.Err(); err != nil {
		return proof, err
	}
	proof.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	proof.Passed = true
	return proof, nil
}

// Dependency-only cache keys exclude versioned deployment files and optional
// release payloads. Refresh those from the current approved index on cache reuse.
func refreshReleaseInputs(root string, r release, key string) error {
	kit, err := readDevkit(root)
	if err != nil {
		return err
	}
	for name, current := range map[string]*artifact{"nas": &r.NAS, "desktop": r.Desktop, "seeds": r.Seeds} {
		var target **artifact
		switch name {
		case "desktop":
			target = &kit.Release.Desktop
		case "seeds":
			target = &kit.Release.Seeds
		}
		if current == nil {
			if target != nil {
				*target = nil
			}
			if err := os.Remove(filepath.Join(root, "cache", name+".tar")); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		cached := filepath.Join(root, "cache", name+".tar")
		hash, _ := digest(cached)
		if hash != current.SHA256 {
			if err := os.Remove(cached); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := fetch(*current, cached); err != nil {
				return fmt.Errorf("刷新发行依赖 %s: %w", name, err)
			}
		}
		updated := *current
		updated.URL = name + ".tar"
		if name == "nas" {
			kit.Release.NAS = updated
		} else {
			*target = &updated
		}
	}
	kit.Release.Version = r.Version
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		return err
	}
	if err := signingPublic(key, filepath.Join(root, "release-public.pem")); err != nil {
		return err
	}
	if err := writeSums(root); err != nil {
		return err
	}
	return signSums(root, key)
}

func writeDownloads(root, tag string, cat devkitCatalog, assets []assetInfo) error {
	var b strings.Builder
	fingerprint, err := publicFingerprint(filepath.Join(root, "release-public.pem"))
	if err != nil {
		return err
	}
	fmt.Fprintf(&b, "发行公钥指纹 SHA256(SPKI DER)：%s\n从独立核准的Forgejo Release正文/组织文档核对本指纹；所有verify/unpack/install示例必须追加 --trust-fingerprint %s（或--verify-key批准公钥）。\n仅包内公钥只能校验完整性，未核验身份会非零退出。\n", fingerprint, fingerprint)
	fmt.Fprintf(&b, "Auriga %s 浏览器下载说明（字节为精确大小）\n只从本版本 Forgejo Release 手动下载，再拷入现场；现场无需Git或网络。\n只部署：未分卷时只需 auriga-offline-%s.tar 和 SHA256SUMS 两个文件。\n先系统tar解包，进入包目录运行 ./auriga verify .，再 sudo ./auriga install --address 192.0.2.10。\n改代码：再下载 auriga-source-%s.tar 和 auriga-devkit-%s.tar。源码包自带Linux amd64 CLI、版本基线和公钥。\n分卷：下载同名前缀全部 .partNNN、.parts.json及.sig；另下载auriga引导程序，用 ./auriga unpack 首卷 --out 新目录。\n累积基线 %s，最新 %s，支持%d版；已有精确上版套件可只下载delta代替完整devkit。\n新基线/跨大版本/超阈值时下载完整套件解包到新目录；旧基线在原Forgejo Release保留。\n校验全离线，必须用已独立核对的 --trust-fingerprint 或 --verify-key 固定发行身份。\n\n", tag, tag, tag, tag, cat.Baseline, cat.Latest, len(cat.Versions))
	for _, a := range assets {
		purpose := "可选：引导程序/公钥"
		switch {
		case strings.HasPrefix(a.Name, "auriga-offline-"):
			purpose = "只部署必下：安装包或其分卷元数据"
		case strings.HasPrefix(a.Name, "auriga-source-"):
			purpose = "改代码必下：带版本清单的源码"
		case strings.HasPrefix(a.Name, "auriga-devkit-") && strings.Contains(a.Name, "-delta.tar"):
			purpose = "已有上版套件可选：相对上版增量（新基线需完整套件）"
		case strings.HasPrefix(a.Name, "auriga-devkit-"):
			purpose = "首次改代码必下：累积依赖/工具镜像"
		case a.Name == "SHA256SUMS":
			purpose = "必下：全部附件摘要"
		case a.Name == "SHA256SUMS.sig":
			purpose = "签名；包内也自带摘要/签名"
		case a.Name == "release-manifest.json":
			purpose = "可选审计：版本/锁/断网构建门禁"
		case a.Name == "DOWNLOADS.txt":
			purpose = "本下载说明"
		}
		fmt.Fprintf(&b, "%s  %d B  %s\n", a.Name, a.Size, purpose)
	}
	b.WriteString("SHA256SUMS：必下，全部附件摘要。SHA256SUMS.sig：签名（包内也自带摘要/签名）。\nrelease-manifest.json：可选审计，支持版本/每版锁摘要及真实断网构建门禁结果。\nDOWNLOADS.txt：本说明；所有文件均只在Forgejo Release发布。\n")
	return os.WriteFile(filepath.Join(root, "DOWNLOADS.txt"), []byte(b.String()), 0644)
}

// Metadata sizes include this one-page guide itself. A short fixed point
// iteration stabilizes those decimal sizes without hashing large assets again.
func finalizeDistribution(ctx context.Context, root string, m distributionManifest, cat devkitCatalog, key string) error {
	for attempt := 0; attempt < 12; attempt++ {
		if err := jsonFile(filepath.Join(root, "release-manifest.json"), m, 0644); err != nil {
			return err
		}
		if err := writeSumsContext(ctx, root); err != nil {
			return err
		}
		if err := signSums(root, key); err != nil {
			return err
		}
		listing := append([]assetInfo(nil), m.Assets...)
		for _, name := range []string{"release-manifest.json", "SHA256SUMS", "SHA256SUMS.sig"} {
			a, err := describeAssetContext(ctx, filepath.Join(root, name))
			if err != nil {
				return err
			}
			listing = append(listing, a)
		}
		if err := writeDownloads(root, m.Tag, cat, listing); err != nil {
			return err
		}
		current, err := describeAssetContext(ctx, filepath.Join(root, "DOWNLOADS.txt"))
		if err != nil {
			return err
		}
		for i, a := range m.Assets {
			if a.Name == current.Name {
				if a == current {
					return nil
				}
				m.Assets[i] = current
			}
		}
	}
	return errors.New("下载说明元数据大小未收敛")
}

func validateBuildGate(root string, m distributionManifest) error {
	g := m.Gate
	if !g.Passed || g.Commit != m.Commit || g.LockDigest != m.Locks.Digest || !hex64.MatchString(g.OutputSHA) || !hex64.MatchString(g.CatalogSHA) || g.Network != gateNetwork {
		return errors.New("发行缺少有效的目标源码断网构建门禁")
	}
	sourceName := "auriga-source-" + m.Tag + ".tar"
	kitName := "auriga-devkit-" + m.Tag + ".tar"
	foundSource, foundKit := false, false
	for _, original := range m.Archives {
		if filepath.Base(original.Name) != original.Name || !hex64.MatchString(original.SHA256) {
			return errors.New("发行原始归档清单无效")
		}
		actual, err := describeAsset(filepath.Join(root, original.Name))
		if os.IsNotExist(err) {
			var parts partsManifest
			if err := readJSON(filepath.Join(root, original.Name+".parts.json"), &parts); err != nil {
				return err
			}
			actual = parts.Original
		} else if err != nil {
			return err
		}
		if actual != original {
			return errors.New("发行原始归档与附件不符")
		}
		if original.Name == sourceName {
			foundSource = original.SHA256 == g.SourceSHA
		}
		if original.Name == kitName {
			foundKit = original.SHA256 == g.DevkitSHA
		}
	}
	if !foundSource || !foundKit {
		return errors.New("门禁未绑定本次源码/完整devkit归档")
	}
	return nil
}
