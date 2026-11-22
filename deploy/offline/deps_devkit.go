package main

// 现有发行开发套件直接作为只读依赖来源（设计 D5“缓存和套件同一种格式”的本意）。
//
// 解包后的发行套件有两种布局，都不需要转换：
//
//	单版本  devkit.json + cache/<inputs.tar、recipes.tar、images/<用途>.tar…>
//	累积    devkit-catalog.json + blobs/<sha256>（各版本 files 映射到数据块）
//
// 信任与依赖缓存布局的套件完全相同：根 SHA256SUMS 的签名只用配置（或命令行）给出的
// trustFingerprint、或 state 已钉定的公钥核验，套件自带的 release-public.pem 只是
// 公钥载体，绝不自证。清单文件（devkit.json / devkit-catalog.json）与所用数据文件
// 都必须受已验签的根 SHA256SUMS 覆盖且摘要一致。
//
// 依赖输入：每个发行版本的 inputs.tar 里按生态存放 <生态>/，内容就是
// <生态>-inputs.sh prepare 的输出（含 SHA256SUMS 与 source-locks.sha256），和缓存条目
// inputs/<生态>/<锁摘要>/ 完全一致；锁摘要 = <生态>/source-locks.sha256 的 SHA-256。
// system 没有 locks 模式：按 deps_locks.go 的 system 规则合成锁清单——配方脚本的摘要
// 取自该版本 recipes.tar（构建时正是用它覆盖源码里的配方），其余文件的摘要取自该版本
// 锁清单 locks.files；system 条目导入缓存时补写 source-locks.sha256 与条目级 SHA256SUMS。
//
// 镜像：取最新版本（累积套件的 latest，单版本套件即其版本）的 images 记录；使用前
// 核对归档摘要，并从 docker save 归档的 manifest.json/配置块算出镜像 ID，载入后再核对。

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	devkitSingleFile     = "devkit.json"
	devkitCumulativeFile = "devkit-catalog.json"
)

// isDevkitLayout 判断目录是否为发行开发套件布局。
func isDevkitLayout(root string) bool {
	for _, name := range []string{devkitSingleFile, devkitCumulativeFile} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
}

// devkitRelease 是套件里的一个发行版本（数据文件路径都是相对套件根目录）。
type devkitRelease struct {
	Version    string
	InputsRel  string
	RecipesRel string
	Locks      *lockInventory
	Images     []kitImage // File 已改写为套件根下的相对路径

	locks map[string]ecosystemLock // 生态 → 锁清单（打开套件时计算）
	// prefixes 生态 → inputs.tar 里的子树前缀；缺省为「<生态>/」。第 8 步前的套件没有 python/，
	// operator 的 pip 包在 system/operator/（布局与 python 条目相同），此时 python 取那里。
	prefixes map[string]string
}

type devkitLayout struct {
	Root     string // 数据文件所在目录
	Kind     string // devkit.json 或 devkit-catalog.json
	Latest   *devkitRelease
	Releases []*devkitRelease // 最新版本在前
	sums     map[string]string
}

func (d *devkitLayout) describe() string {
	if d.Kind == devkitCumulativeFile {
		return fmt.Sprintf("累积发行开发套件，%d 个版本，最新 %s", len(d.Releases), d.Latest.Version)
	}
	return "单版本发行开发套件 " + d.Latest.Version
}

// checkSummed 核对受签名清单覆盖的文件摘要（在 dir 下读取）。
func checkSummed(sums map[string]string, dir, rel string) error {
	want, ok := sums[rel]
	if !ok {
		return fmt.Errorf("套件文件未受签名清单覆盖 %s", rel)
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := noSymlinks(path); err != nil {
		return err
	}
	got, err := digest(path)
	if err != nil {
		return fmt.Errorf("套件缺少文件 %s: %w", rel, err)
	}
	if got != want {
		return fmt.Errorf("套件文件摘要不符 %s", rel)
	}
	return nil
}

// readSummedManifest 把清单文件读入内存，核对摘要等于已验签清单后返回同一份字节，
// 调用方用这份字节解析（核对与解析之间没有按路径重读的窗口）。
func readSummedManifest(sums map[string]string, root, rel string) ([]byte, error) {
	want, ok := sums[rel]
	if !ok {
		return nil, fmt.Errorf("套件文件未受签名清单覆盖 %s", rel)
	}
	raw, err := readKitFile(filepath.Join(root, filepath.FromSlash(rel)), devkitJSONLimit)
	if err != nil {
		return nil, fmt.Errorf("套件缺少文件 %s: %w", rel, err)
	}
	if sha256Hex(raw) != want {
		return nil, fmt.Errorf("套件文件摘要不符 %s", rel)
	}
	return raw, nil
}

// openDevkitLayout 读取已验签套件的清单。root 是套件根目录；sums 是已验签的根
// SHA256SUMS（内存中的同一份字节解析所得）。
func openDevkitLayout(root string, sums map[string]string) (*devkitLayout, error) {
	d := &devkitLayout{Root: root, sums: sums}
	covered := func(rel string) (string, error) {
		if !safeRelative(rel) {
			return "", fmt.Errorf("套件数据路径无效 %s", rel)
		}
		if _, ok := sums[rel]; !ok {
			return "", fmt.Errorf("套件数据文件未受签名清单覆盖 %s", rel)
		}
		return rel, nil
	}
	if _, err := os.Lstat(filepath.Join(root, devkitCumulativeFile)); err == nil {
		d.Kind = devkitCumulativeFile
		raw, err := readSummedManifest(sums, root, devkitCumulativeFile)
		if err != nil {
			return nil, err
		}
		var cat devkitCatalog
		if err := decodeDevkitJSON(devkitCumulativeFile, raw, &cat); err != nil {
			return nil, err
		}
		if err := validateCatalog(cat); err != nil {
			return nil, err
		}
		versions := make([]string, 0, len(cat.Versions))
		for v := range cat.Versions {
			versions = append(versions, v)
		}
		// 新版本在前：按语义版本比较（0.10.0 新于 0.9.0），不按字符串。
		sort.Slice(versions, func(i, j int) bool { return compareDevkitVersion(versions[i], versions[j]) > 0 })
		for _, v := range versions {
			entry := cat.Versions[v]
			blob := func(name string) (string, error) {
				hash := entry.Files[name]
				rel, err := covered("blobs/" + hash)
				if err != nil {
					return "", err
				}
				if sums[rel] != hash {
					return "", fmt.Errorf("累积套件数据块摘要与签名清单不符 %s", hash)
				}
				return rel, nil
			}
			rel := &devkitRelease{Version: v, Locks: entry.Kit.Locks}
			var err error
			if rel.InputsRel, err = blob(entry.Kit.Release.Build.Inputs.URL); err != nil {
				return nil, err
			}
			if rel.RecipesRel, err = blob(entry.Kit.Release.Build.Recipes.URL); err != nil {
				return nil, err
			}
			for _, img := range entry.Kit.Images {
				if img.File, err = blob(img.File); err != nil {
					return nil, err
				}
				rel.Images = append(rel.Images, img)
			}
			if v == cat.Latest {
				d.Latest = rel
				d.Releases = append([]*devkitRelease{rel}, d.Releases...)
			} else {
				d.Releases = append(d.Releases, rel)
			}
		}
		return d, d.indexLocks()
	}
	d.Kind = devkitSingleFile
	raw, err := readSummedManifest(sums, root, devkitSingleFile)
	if err != nil {
		return nil, err
	}
	var kit devkitManifest
	if err := decodeDevkitJSON(devkitSingleFile, raw, &kit); err != nil {
		return nil, err
	}
	if err := validateDevkit(kit); err != nil {
		return nil, err
	}
	rel := &devkitRelease{Version: kit.Release.Version, Locks: kit.Locks}
	if rel.InputsRel, err = covered("cache/" + kit.Release.Build.Inputs.URL); err != nil {
		return nil, err
	}
	if rel.RecipesRel, err = covered("cache/" + kit.Release.Build.Recipes.URL); err != nil {
		return nil, err
	}
	for _, img := range kit.Images {
		if img.File, err = covered("cache/" + img.File); err != nil {
			return nil, err
		}
		rel.Images = append(rel.Images, img)
	}
	d.Latest = rel
	d.Releases = []*devkitRelease{rel}
	return d, d.indexLocks()
}

var errStopScan = errors.New("停止扫描")

// 解出套件输入的上限，与 unpackReader 一致（变量便于测试调低）。
var (
	devkitExtractMaxEntries       = 100000
	devkitExtractMaxBytes   int64 = 128 << 30
)

// scanTar 逐个成员回调（名字已去掉 "./" 前缀与末尾 "/"）；回调返回 errStopScan 提前结束。
// 直接读文件时 archive/tar 会用 Seek 跳过不需要的成员内容。
func scanTar(path string, fn func(name string, h *tar.Header, r io.Reader) error) error {
	if err := noSymlinks(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取归档 %s: %w", filepath.Base(path), err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if err := fn(name, h, tr); err != nil {
			if err == errStopScan {
				return nil
			}
			return err
		}
	}
}

// scanTarVerified 与 scanTar 相同地逐个成员回调，但顺序读完整个文件并计算 SHA-256，
// 结束时与 want（已验签清单里的摘要）比对；不一致返回错误，回调得到的结果必须丢弃。
// 回调看到的字节就是参与摘要计算的字节，没有“先核对、后按路径重读”的窗口。
func scanTarVerified(path, rel, want string, fn func(name string, h *tar.Header, r io.Reader) error) error {
	if !hex64.MatchString(want) {
		return fmt.Errorf("套件文件未受签名清单覆盖 %s", rel)
	}
	if err := noSymlinks(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	in := io.TeeReader(f, sum) // 不实现 Seek：tar 读取器逐字节读过每个成员
	tr := tar.NewReader(in)
	stopped := false
	for !stopped {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取归档 %s: %w", filepath.Base(path), err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if err := fn(name, h, tr); err != nil {
			if err != errStopScan {
				return err
			}
			stopped = true
		}
	}
	if _, err := io.Copy(io.Discard, in); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != want {
		return fmt.Errorf("套件文件摘要与签名清单不符 %s", rel)
	}
	return nil
}

// indexLocks 计算各版本各生态的锁清单与锁摘要。inputs.tar 与 recipes.tar 都整档计算
// SHA-256 并与已验签清单比对（scanTarVerified），锁清单只取自核对过的字节。
// 归档读不出来或摘要不符时整个套件不可用。
func (d *devkitLayout) indexLocks() error {
	for _, rel := range d.Releases {
		locks, err := d.computeLocks(rel)
		if err != nil {
			return fmt.Errorf("版本 %s：%w", rel.Version, err)
		}
		rel.locks = locks
	}
	return nil
}

func (d *devkitLayout) computeLocks(rel *devkitRelease) (map[string]ecosystemLock, error) {
	out := map[string]ecosystemLock{}
	want := map[string]string{}
	for _, eco := range []string{"go", "web", "java", "python"} {
		want[eco+"/source-locks.sha256"] = eco
	}
	const pythonCompat = "system/operator/"
	want[pythonCompat+"source-locks.sha256"] = "python-compat"
	err := scanTarVerified(filepath.Join(d.Root, filepath.FromSlash(rel.InputsRel)), rel.InputsRel, d.sums[rel.InputsRel], func(name string, h *tar.Header, r io.Reader) error {
		eco, ok := want[name]
		if !ok || h.Typeflag != tar.TypeReg {
			return nil
		}
		if h.Size > 16<<20 {
			return fmt.Errorf("版本 %s 的 %s 过大", rel.Version, name)
		}
		raw, err := io.ReadAll(io.LimitReader(r, 16<<20))
		if err != nil {
			return err
		}
		out[eco] = ecosystemLock{Ecosystem: eco, Digest: lockDigestOf(raw), Raw: raw}
		delete(want, name)
		if len(want) == 0 {
			return errStopScan
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if compat, ok := out["python-compat"]; ok {
		delete(out, "python-compat")
		if _, has := out["python"]; !has {
			compat.Ecosystem = "python"
			out["python"] = compat
			rel.prefixes = map[string]string{"python": pythonCompat}
		}
	}
	if lock, ok, err := d.systemLock(rel); err != nil {
		return nil, err
	} else if ok {
		out["system"] = lock
	}
	return out, nil
}

// systemLock 按 deps_locks.go 的 system 规则合成该版本的锁清单：配方脚本摘要取自
// recipes.tar（整档摘要必须等于已验签清单），其余文件摘要取自版本锁清单 locks.files；
// 任何一项缺失则该版本不提供 system。
func (d *devkitLayout) systemLock(rel *devkitRelease) (ecosystemLock, bool, error) {
	const recipePrefix = "deploy/nas/offline-build/"
	fixed := lockRules["system"].fixed
	hashes := map[string]string{}
	need := map[string]bool{}
	for _, name := range fixed {
		if strings.HasPrefix(name, recipePrefix) {
			need[name] = true
			continue
		}
		if rel.Locks == nil || !hex64.MatchString(rel.Locks.Files[name]) {
			return ecosystemLock{}, false, nil
		}
		hashes[name] = rel.Locks.Files[name]
	}
	if len(need) > 0 {
		err := scanTarVerified(filepath.Join(d.Root, filepath.FromSlash(rel.RecipesRel)), rel.RecipesRel, d.sums[rel.RecipesRel], func(name string, h *tar.Header, r io.Reader) error {
			if !need[name] || h.Typeflag != tar.TypeReg {
				return nil
			}
			sum := sha256.New()
			if _, err := io.Copy(sum, r); err != nil {
				return err
			}
			hashes[name] = hex.EncodeToString(sum.Sum(nil))
			delete(need, name)
			if len(need) == 0 {
				return errStopScan
			}
			return nil
		})
		if err != nil {
			return ecosystemLock{}, false, err
		}
		if len(need) > 0 {
			return ecosystemLock{}, false, nil
		}
	}
	var raw strings.Builder
	for _, name := range fixed {
		fmt.Fprintf(&raw, "%s  %s\n", hashes[name], name)
	}
	b := []byte(raw.String())
	return ecosystemLock{Ecosystem: "system", Digest: lockDigestOf(b), Raw: b}, true, nil
}

// findInput 在各版本里按锁摘要找该生态的输入（最新版本优先）。
func (d *devkitLayout) findInput(eco, lockDigest string) (*devkitRelease, bool) {
	for _, rel := range d.Releases {
		if l, ok := rel.locks[eco]; ok && l.Digest == lockDigest {
			return rel, true
		}
	}
	return nil, false
}

// extractInput 把版本 inputs.tar 里 <生态>/ 子树解出到全新目录 dst，同时对整个归档
// 计算 SHA-256 并与签名清单比对（解出的就是核验过的字节，没有校验后被替换的窗口），
// 最后按缓存条目规则完整校验。条目数与解出总字节的上限同 unpackReader，防止摘要比对
// 之前恶意归档写满缓存盘。
func (d *devkitLayout) extractInput(rel *devkitRelease, lock ecosystemLock, dst string) error {
	eco := lock.Ecosystem
	path := filepath.Join(d.Root, filepath.FromSlash(rel.InputsRel))
	if err := noSymlinks(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	in := io.TeeReader(f, sum)
	tr := tar.NewReader(in)
	prefix := eco + "/"
	if p, ok := rel.prefixes[eco]; ok {
		prefix = p
	}
	seen := map[string]bool{}
	count := 0
	var written int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取套件输入归档: %w", err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if !safeRelative(name) || seen[name] {
			return errors.New("套件输入归档含非法或重复路径")
		}
		seen[name] = true
		if len(seen) > devkitExtractMaxEntries || h.Size < 0 {
			return errors.New("套件输入归档超出条目数上限")
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if h.Typeflag == tar.TypeReg {
			if h.Size > devkitExtractMaxBytes-written {
				return errors.New("套件输入归档解出内容超出大小上限")
			}
			written += h.Size
		}
		target := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(name, prefix)))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if h.Mode&0111 != 0 {
				mode = 0755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, h.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			count++
		default:
			return errors.New("套件输入归档包含链接或特殊文件")
		}
	}
	if _, err := io.Copy(io.Discard, in); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != d.sums[rel.InputsRel] {
		return fmt.Errorf("套件输入归档摘要与签名清单不符 %s", rel.InputsRel)
	}
	if count == 0 {
		return fmt.Errorf("版本 %s 的输入归档里没有 %s/", rel.Version, eco)
	}
	if eco == "system" {
		for _, name := range []string{"SHA256SUMS", "source-locks.sha256"} {
			if _, err := os.Lstat(filepath.Join(dst, name)); err == nil {
				return fmt.Errorf("版本 %s 的 system/ 顶层出现 %s，布局不符", rel.Version, name)
			}
		}
		if err := os.WriteFile(filepath.Join(dst, "source-locks.sha256"), lock.Raw, 0644); err != nil {
			return err
		}
		if err := writeEntrySums(dst); err != nil {
			return err
		}
	}
	return verifyInputEntry(dst, eco, lock.Digest)
}

// writeEntrySums 为条目目录写覆盖全部文件的 SHA256SUMS（sha256sum 格式，按路径排序）。
func writeEntrySums(dir string) error {
	names, err := regularFiles(dir)
	if err != nil {
		return err
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		if name == "SHA256SUMS" {
			continue
		}
		hash, err := digest(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", hash, name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0644)
}

// images 返回最新版本里某用途的镜像（未核验归档；toolImage 复制到暂存目录后在副本上
// 核对摘要并算镜像 ID，导入时同样在暂存副本上核对）。
func (d *devkitLayout) images(role string) []imageHit {
	var out []imageHit
	for _, img := range d.Latest.Images {
		if img.Role != role || !immutable.MatchString(img.Ref) {
			continue
		}
		out = append(out, imageHit{
			Image: depsImage{Schema: 1, Role: role, Ref: img.Ref},
			Tar:   filepath.Join(d.Root, filepath.FromSlash(img.File)),
			rel:   img.File,
		})
	}
	return out
}

var (
	ociConfigName    = regexp.MustCompile(`^blobs/sha256/([a-f0-9]{64})$`)
	legacyConfigName = regexp.MustCompile(`^([a-f0-9]{64})\.json$`)
)

// dockerSaveImageID 从 docker save 归档算出镜像 ID：manifest.json 只能有一个镜像，
// 其配置块的 SHA-256 必须与配置名一致，镜像 ID = sha256:<配置摘要>。
func dockerSaveImageID(path string) (string, error) {
	var manifest []struct {
		Config string `json:"Config"`
	}
	found := false
	err := scanTar(path, func(name string, h *tar.Header, r io.Reader) error {
		if name != "manifest.json" || h.Typeflag != tar.TypeReg {
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(r, 1<<20))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("镜像归档 manifest.json 无效: %w", err)
		}
		found = true
		return errStopScan
	})
	if err != nil {
		return "", err
	}
	if !found || len(manifest) != 1 {
		return "", errors.New("镜像归档必须恰有一个镜像的 manifest.json")
	}
	config := manifest[0].Config
	m := ociConfigName.FindStringSubmatch(config)
	if m == nil {
		m = legacyConfigName.FindStringSubmatch(config)
	}
	if m == nil {
		return "", fmt.Errorf("镜像归档配置名无效 %q", config)
	}
	got := ""
	err = scanTar(path, func(name string, h *tar.Header, r io.Reader) error {
		if name != config || h.Typeflag != tar.TypeReg {
			return nil
		}
		sum := sha256.New()
		if _, err := io.Copy(sum, io.LimitReader(r, 16<<20)); err != nil {
			return err
		}
		got = hex.EncodeToString(sum.Sum(nil))
		return errStopScan
	})
	if err != nil {
		return "", err
	}
	if got != m[1] {
		return "", errors.New("镜像归档配置块摘要与配置名不符")
	}
	return "sha256:" + m[1], nil
}

// compareDevkitVersion 按语义版本比较 a、b（可带前缀 v）：主体按点分段，数字段按数值、
// 其余按字符串；有预发布后缀（-xxx）的低于同主体的正式版，预发布段同样逐段比较。
// 返回 -1、0、1。
func compareDevkitVersion(a, b string) int {
	split := func(v string) (string, string) {
		v = strings.TrimPrefix(v, "v")
		if i := strings.IndexByte(v, '+'); i >= 0 {
			v = v[:i]
		}
		if i := strings.IndexByte(v, '-'); i >= 0 {
			return v[:i], v[i+1:]
		}
		return v, ""
	}
	fields := func(x, y string) int {
		xs, ys := strings.Split(x, "."), strings.Split(y, ".")
		for i := 0; i < len(xs) || i < len(ys); i++ {
			if i >= len(xs) {
				return -1
			}
			if i >= len(ys) {
				return 1
			}
			if c := compareVersionField(xs[i], ys[i]); c != 0 {
				return c
			}
		}
		return 0
	}
	ma, pa := split(a)
	mb, pb := split(b)
	if c := fields(ma, mb); c != 0 {
		return c
	}
	switch {
	case pa == pb:
		return strings.Compare(a, b)
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	if c := fields(pa, pb); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func compareVersionField(x, y string) int {
	xn, xerr := strconv.ParseUint(x, 10, 64)
	yn, yerr := strconv.ParseUint(y, 10, 64)
	switch {
	case xerr == nil && yerr == nil:
		if xn != yn {
			if xn < yn {
				return -1
			}
			return 1
		}
		return 0
	case xerr == nil:
		return -1 // 数字段低于非数字段（同 semver 预发布规则）
	case yerr == nil:
		return 1
	}
	return strings.Compare(x, y)
}
