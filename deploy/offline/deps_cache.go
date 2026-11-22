package main

// 依赖缓存与套件（设计 D5）。缓存和套件同一种布局：
//
//	inputs/<生态>/<锁摘要>/        生态 go、web、java、python、system；锁摘要 = 目录内
//	                               source-locks.sha256 的 SHA-256；目录内 SHA256SUMS
//	                               由 <生态>-inputs.sh prepare 写出，覆盖全部依赖文件
//	images/<用途>/<摘要>/image.json 工具镜像、基础镜像、服务镜像的固定 digest 记录，
//	                               可选 image.tar（docker save 归档）
//
// 套件额外在根目录带 SHA256SUMS（覆盖全部文件）与 SHA256SUMS.sig，可带
// release-public.pem 作为公钥载体；信任只来自配置里的 trustFingerprint 或 state
// 已钉定的公钥，绝不接受套件用自带公钥自证。现有发行开发套件（devkit.json /
// devkit-catalog.json 布局）同样可以直接作为套件，见 deps_devkit.go。
//
// 条目目录里除了 SHA256SUMS 列出的文件，只允许 CLI 自己写的元数据：
// auriga-origin.json（来源与 SHA-256）、site-dependencies.json（新增依赖清单，沿用
// bundle --online 的结构）、auriga-web-lock.yaml（Web 条目对应的 pnpm-lock 副本，
// 用于下次列出新增包）。

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	depsOriginFile  = "auriga-origin.json"
	depsReportFile  = "site-dependencies.json"
	depsWebLockFile = "auriga-web-lock.yaml"
)

var depsEntryMetadata = map[string]bool{depsOriginFile: true, depsReportFile: true, depsWebLockFile: true}

var depsImageRole = regexp.MustCompile(`^(tool|base|service)-[a-z0-9]+$`)

// depsImageRoles 是 deps status 列出的镜像用途（与 buildImageRefs 的用途命名一致）。
var depsImageRoles = []string{
	"tool-go", "tool-web", "tool-java",
	"base-platform", "base-edge", "base-agent", "base-bifrost", "base-operator",
	"service-postgres", "service-nats", "service-registry",
}

// depsOrigin 记录条目来源，写在条目目录的 auriga-origin.json。
type depsOrigin struct {
	Schema      int             `json:"schemaVersion"`
	Kind        string          `json:"kind"` // fetch | import
	Ecosystem   string          `json:"ecosystem,omitempty"`
	LockDigest  string          `json:"lockDigest,omitempty"`
	From        []string        `json:"from"`
	At          string          `json:"at"`
	Signed      bool            `json:"signed"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	SumsSHA256  string          `json:"sumsSha256"`
	Previous    json.RawMessage `json:"previous,omitempty"`
}

type depsImage struct {
	Schema int    `json:"schemaVersion"`
	Role   string `json:"role"`
	Ref    string `json:"ref"`
	ID     string `json:"id,omitempty"`
}

var imageIDPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (img depsImage) digestHex() string {
	return img.Ref[strings.LastIndex(img.Ref, "sha256:")+7:]
}

// runtimeRef 是运行时使用的镜像引用：有镜像 ID 时用 ID（docker load 后不一定保留仓库 digest）。
func (img depsImage) runtimeRef() string {
	if img.ID != "" {
		return img.ID
	}
	return img.Ref
}

func validateDepsImage(img depsImage, role, hexDir string, hasTar bool) error {
	if img.Schema != 1 || img.Role != role || !depsImageRole.MatchString(role) {
		return fmt.Errorf("镜像记录 %s/%s 的 schemaVersion 或用途不符", role, hexDir)
	}
	if !immutable.MatchString(img.Ref) || img.digestHex() != hexDir {
		return fmt.Errorf("镜像记录 %s/%s 的 ref 必须是固定 digest 且与目录名一致", role, hexDir)
	}
	if img.ID != "" && !imageIDPattern.MatchString(img.ID) {
		return fmt.Errorf("镜像记录 %s/%s 的 id 无效", role, hexDir)
	}
	if hasTar && img.ID == "" {
		return fmt.Errorf("镜像记录 %s/%s 带归档时必须记录镜像 ID", role, hexDir)
	}
	return nil
}

// parseSums 解析 sha256sum 格式清单（"<64hex>  <相对路径>"），拒绝重复与不安全路径。
func parseSums(raw []byte) (map[string]string, []string, error) {
	sums := map[string]string{}
	var order []string
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 64<<10), 1<<20)
	for s.Scan() {
		line := s.Text()
		if len(line) < 67 || line[64:66] != "  " || !hex64.MatchString(line[:64]) {
			return nil, nil, errors.New("SHA256SUMS 行格式无效")
		}
		name := line[66:]
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\\r\n") {
			return nil, nil, fmt.Errorf("SHA256SUMS 含不安全路径 %q", name)
		}
		if _, dup := sums[name]; dup {
			return nil, nil, fmt.Errorf("SHA256SUMS 条目重复 %s", name)
		}
		sums[name] = line[:64]
		order = append(order, name)
	}
	if err := s.Err(); err != nil {
		return nil, nil, err
	}
	if len(sums) == 0 {
		return nil, nil, errors.New("SHA256SUMS 为空")
	}
	return sums, order, nil
}

// verifyInputEntry 完整校验一个依赖输入条目：锁摘要与目录名一致，条目 SHA256SUMS
// 覆盖的文件全部匹配，未覆盖的只能是 CLI 元数据。
func verifyInputEntry(dir, eco, lockDigest string) error {
	if err := noSymlinks(dir); err != nil {
		return err
	}
	if !hex64.MatchString(lockDigest) {
		return fmt.Errorf("依赖条目目录名不是锁摘要 %s", lockDigest)
	}
	locks, err := smallFile(filepath.Join(dir, "source-locks.sha256"), 16<<20)
	if err != nil {
		return fmt.Errorf("依赖条目 %s/%s 缺少 source-locks.sha256: %w", eco, lockDigest, err)
	}
	if lockDigestOf(locks) != lockDigest {
		return fmt.Errorf("依赖条目 %s/%s 的锁清单与目录名不符", eco, lockDigest)
	}
	raw, err := smallFile(filepath.Join(dir, "SHA256SUMS"), 64<<20)
	if err != nil {
		return fmt.Errorf("依赖条目 %s/%s 缺少 SHA256SUMS: %w", eco, lockDigest, err)
	}
	sums, _, err := parseSums(raw)
	if err != nil {
		return fmt.Errorf("依赖条目 %s/%s：%w", eco, lockDigest, err)
	}
	if sums["source-locks.sha256"] != lockDigest {
		return fmt.Errorf("依赖条目 %s/%s 的 SHA256SUMS 未覆盖锁清单", eco, lockDigest)
	}
	names, err := regularFiles(dir)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, name := range names {
		present[name] = true
		if name == "SHA256SUMS" {
			continue
		}
		want, listed := sums[name]
		if !listed {
			if depsEntryMetadata[name] {
				continue
			}
			return fmt.Errorf("依赖条目 %s/%s 含未列入 SHA256SUMS 的文件 %s", eco, lockDigest, name)
		}
		hash, err := digest(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if hash != want {
			return fmt.Errorf("依赖条目 %s/%s 文件摘要不符 %s", eco, lockDigest, name)
		}
	}
	for name := range sums {
		if !present[name] {
			return fmt.Errorf("依赖条目 %s/%s 缺少文件 %s", eco, lockDigest, name)
		}
	}
	return nil
}

// quickInputHit 只核对锁清单（status 用，避免每次读全部依赖字节）。
func quickInputHit(dir, lockDigest string) bool {
	if noSymlinks(dir) != nil {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, "source-locks.sha256"))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	hash, err := digest(filepath.Join(dir, "source-locks.sha256"))
	return err == nil && hash == lockDigest
}

// readImageRecords 列出缓存下某用途的镜像记录。
func readImageRecords(root, role string) ([]imageHit, error) {
	return readImageRecordsSigned(root, role, nil)
}

// readImageRecordsSigned 列出某布局根目录下某用途的镜像记录。sums 非空（套件）时，
// image.json 读入内存后先核对摘要等于已验签清单，再用同一份字节解析；未受签名覆盖的
// 记录跳过。
func readImageRecordsSigned(root, role string, sums map[string]string) ([]imageHit, error) {
	dir := filepath.Join(root, "images", role)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []imageHit
	for _, e := range entries {
		if !e.IsDir() || !hex64.MatchString(e.Name()) {
			continue
		}
		entry := filepath.Join(dir, e.Name())
		if err := noSymlinks(entry); err != nil {
			return nil, err
		}
		rel := "images/" + role + "/" + e.Name()
		if sums != nil {
			if _, ok := sums[rel+"/image.json"]; !ok {
				continue // 未受签名覆盖的记录不可用
			}
		}
		raw, err := smallFile(filepath.Join(entry, "image.json"), 1<<20)
		if err != nil {
			return nil, fmt.Errorf("镜像记录 %s/%s 无效: %w", role, e.Name(), err)
		}
		if sums != nil && sha256Hex(raw) != sums[rel+"/image.json"] {
			return nil, fmt.Errorf("套件文件摘要不符 %s/image.json", rel)
		}
		var img depsImage
		if err := decodeStrictJSON(raw, &img); err != nil {
			return nil, fmt.Errorf("镜像记录 %s/%s 无效: %w", role, e.Name(), err)
		}
		_, tarErr := os.Lstat(filepath.Join(entry, "image.tar"))
		if err := validateDepsImage(img, role, e.Name(), tarErr == nil); err != nil {
			return nil, err
		}
		hit := imageHit{Image: img, Dir: entry}
		if tarErr == nil {
			hit.Tar = filepath.Join(entry, "image.tar")
			hit.rel = rel + "/image.tar"
		}
		out = append(out, hit)
	}
	return out, nil
}

// decodeStrictJSON 同 readJSON 的规则解析内存字节：拒绝未知字段与尾部数据。
func decodeStrictJSON(raw []byte, output any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return errors.New("JSON 尾部有多余数据")
	}
	return nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type imageHit struct {
	Image    depsImage
	Dir, Tar string
	Location string
	kit      *depsKit
	rel      string // 套件里镜像归档相对套件根目录的路径（受签名清单覆盖）
}

// depsKit 是已核验签名的套件。
type depsKit struct {
	Conf   siteKit
	Root   string
	Trust  string // 说明信任来源
	sums   map[string]string
	devkit *devkitLayout // 发行开发套件布局时非空
	Err    error
}

func (k *depsKit) label() string { return "套件 " + k.Conf.Path }

// depsStateKeys 返回 state 已钉定的发行/站点公钥（trustState 套件使用）；可注入。
var depsStateKeys = func(cfg *siteConfig) ([]string, error) {
	if cfg.Server == nil {
		return nil, errors.New("trustState 套件需要 server.data 指向已安装站点的数据目录")
	}
	data, err := dataPath(cfg.Server.Data)
	if err != nil {
		return nil, err
	}
	s, err := readState(data)
	if err != nil {
		return nil, err
	}
	return s.trustKeys()
}

// openDepsKit 核验套件签名。trustFingerprint：套件根目录的 release-public.pem 只是
// 公钥载体，指纹必须与配置一致；trustState：只用 state 钉定的公钥验签。
func openDepsKit(cfg *siteConfig, conf siteKit) *depsKit {
	k := &depsKit{Conf: conf, Root: conf.Path}
	k.Err = k.open(cfg)
	return k
}

func (k *depsKit) open(cfg *siteConfig) error {
	if err := noSymlinks(k.Root); err != nil {
		return err
	}
	info, err := os.Stat(k.Root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("套件路径不是目录")
	}
	// 套件与 deps.cache 同为保护边界：root 所有、组和其他用户不可写。
	if err := depsTrustedSourceCheck("套件目录 deps.kits ", k.Root); err != nil {
		return err
	}
	trust, _, sums, err := verifyKitTrust(cfg, k.Root, k.Conf, "trustFingerprint")
	if err != nil {
		return err
	}
	k.Trust, k.sums = trust, sums
	if isDevkitLayout(k.Root) {
		d, err := openDevkitLayout(k.Root, sums)
		if err != nil {
			return fmt.Errorf("发行开发套件无效：%w", err)
		}
		k.devkit = d
	}
	return nil
}

// kitSumsLimit 与 verifyFileSignature 的签名清单上限一致。
const kitSumsLimit = 4 << 20

// readKitFile 读套件根目录下的小文件到内存（拒绝符号链接）。
func readKitFile(path string, limit int64) ([]byte, error) {
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	return smallFile(path, limit)
}

// parseEd25519PEM 解析单个 Ed25519 SPKI PEM 公钥，指纹规则同 publicFingerprint。
func parseEd25519PEM(raw []byte) (ed25519.PublicKey, string, error) {
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, "", errors.New("需要单个SPKI PEM公钥")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", err
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, "", errors.New("签名公钥需要Ed25519")
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, "", err
	}
	return public, sha256Hex(der), nil
}

// verifyKitTrust 用独立信任核验 root 下 SHA256SUMS 的签名，返回信任说明、公钥指纹与
// 已验签的清单。公钥、SHA256SUMS 与签名都只读一次到内存，验签、算指纹和解析用的是
// 同一份字节，读完之后文件再被替换也不影响结果。
// trustFingerprint：root 的 release-public.pem 只是公钥载体，指纹必须与给定值一致；
// trustState：只用 state 钉定的公钥。两者都没有时拒绝。field 是报错里的参数名。
func verifyKitTrust(cfg *siteConfig, root string, conf siteKit, field string) (string, string, map[string]string, error) {
	var sumsRaw, signature []byte
	loadSigned := func() error {
		if sumsRaw != nil {
			return nil
		}
		raw, err := readKitFile(filepath.Join(root, "SHA256SUMS"), kitSumsLimit)
		if err != nil {
			return err
		}
		sigRaw, err := readKitFile(filepath.Join(root, "SHA256SUMS.sig"), 1024)
		if err != nil {
			return err
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
		if err != nil {
			return err
		}
		sumsRaw, signature = raw, sig
		return nil
	}
	verifyWith := func(public ed25519.PublicKey) error {
		if err := loadSigned(); err != nil {
			return err
		}
		if !ed25519.Verify(public, sumsRaw, signature) {
			return errors.New("签名不匹配")
		}
		return nil
	}
	var label, fp string
	switch {
	case conf.TrustFingerprint != "":
		want, err := normalizeFingerprint(field, conf.TrustFingerprint)
		if err != nil {
			return "", "", nil, err
		}
		keyRaw, err := readKitFile(filepath.Join(root, "release-public.pem"), 64<<10)
		if err != nil {
			return "", "", nil, fmt.Errorf("套件缺少可用的 release-public.pem: %w", err)
		}
		public, got, err := parseEd25519PEM(keyRaw)
		if err != nil {
			return "", "", nil, fmt.Errorf("套件缺少可用的 release-public.pem: %w", err)
		}
		if got != want {
			return "", "", nil, fmt.Errorf("套件公钥指纹 %s 与 %s 不符，拒绝套件自带公钥自证", got, field)
		}
		if err := verifyWith(public); err != nil {
			return "", "", nil, fmt.Errorf("套件签名无效: %w", err)
		}
		label, fp = "trustFingerprint SHA256:"+want, want
	case conf.TrustState:
		keys, err := depsStateKeys(cfg)
		if err != nil {
			return "", "", nil, fmt.Errorf("trustState 套件无法取得 state 钉定公钥: %w", err)
		}
		for _, key := range keys {
			keyRaw, err := readKitFile(key, 64<<10)
			if err != nil {
				continue
			}
			public, got, err := parseEd25519PEM(keyRaw)
			if err != nil {
				continue
			}
			if verifyWith(public) == nil {
				label, fp = "state 钉定公钥 SHA256:"+got, got
				break
			}
		}
		if label == "" {
			return "", "", nil, errors.New("套件签名不属于 state 钉定的公钥，拒绝套件自带公钥替换信任")
		}
	default:
		return "", "", nil, errors.New("套件没有独立信任（trustFingerprint 或 trustState），拒绝")
	}
	sums, _, err := parseSums(sumsRaw)
	if err != nil {
		return "", "", nil, err
	}
	return label, fp, sums, nil
}

// hasInput：缓存布局要求套件根 SHA256SUMS 里锁清单的摘要恰好等于锁摘要且文件实际
// 字节一致；发行开发套件布局在各版本 inputs.tar 里按锁摘要查找。
func (k *depsKit) hasInput(eco, lockDigest string) (inputHit, bool) {
	if k.Err != nil {
		return inputHit{}, false
	}
	if k.devkit != nil {
		rel, ok := k.devkit.findInput(eco, lockDigest)
		if !ok {
			return inputHit{}, false
		}
		return inputHit{Location: k.label() + " 版本 " + rel.Version, kit: k, release: rel}, true
	}
	rel := "inputs/" + eco + "/" + lockDigest
	if k.sums[rel+"/source-locks.sha256"] != lockDigest {
		return inputHit{}, false
	}
	dir := filepath.Join(k.Root, filepath.FromSlash(rel))
	return inputHit{Dir: dir, Location: k.label(), kit: k}, quickInputHit(dir, lockDigest)
}

// verifyKitPrefix 核对套件中某个子目录下的全部文件都受根 SHA256SUMS 覆盖且摘要一致。
func (k *depsKit) verifyKitPrefix(rel string) error {
	dir := filepath.Join(k.Root, filepath.FromSlash(rel))
	names, err := regularFiles(dir)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, name := range names {
		full := rel + "/" + name
		present[full] = true
		want, ok := k.sums[full]
		if !ok {
			return fmt.Errorf("套件文件未受签名清单覆盖 %s", full)
		}
		hash, err := digest(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if hash != want {
			return fmt.Errorf("套件文件摘要不符 %s", full)
		}
	}
	for name := range k.sums {
		if strings.HasPrefix(name, rel+"/") && !present[name] {
			return fmt.Errorf("套件缺少文件 %s", name)
		}
	}
	return nil
}

// depsResolver 按“可写缓存 → deps.kits（按配置顺序）”整套选取依赖输入。
type depsResolver struct {
	cfg   *siteConfig
	cache string
	kits  []*depsKit
}

func newDepsResolver(cfg *siteConfig) *depsResolver {
	r := &depsResolver{cfg: cfg, cache: cfg.Deps.Cache}
	for _, conf := range cfg.Deps.Kits {
		r.kits = append(r.kits, openDepsKit(cfg, conf))
	}
	return r
}

type inputHit struct {
	Dir      string // 缓存布局的条目目录；发行开发套件命中时为空
	Location string
	kit      *depsKit
	release  *devkitRelease // 发行开发套件命中的版本
}

func (r *depsResolver) cacheInputDir(eco, lockDigest string) string {
	return filepath.Join(r.cache, "inputs", eco, lockDigest)
}

func (r *depsResolver) findInput(eco, lockDigest string) (inputHit, bool) {
	if dir := r.cacheInputDir(eco, lockDigest); quickInputHit(dir, lockDigest) {
		return inputHit{Dir: dir, Location: "缓存"}, true
	}
	for _, k := range r.kits {
		if hit, ok := k.hasInput(eco, lockDigest); ok {
			return hit, true
		}
	}
	return inputHit{}, false
}

// verifyInput 在真正使用前完整校验命中的条目。
func (r *depsResolver) verifyInput(hit inputHit, eco, lockDigest string) error {
	if hit.release != nil {
		return checkSummed(hit.kit.devkit.sums, hit.kit.devkit.Root, hit.release.InputsRel)
	}
	if hit.kit != nil {
		if err := hit.kit.verifyKitPrefix("inputs/" + eco + "/" + lockDigest); err != nil {
			return err
		}
	}
	return verifyInputEntry(hit.Dir, eco, lockDigest)
}

// findImages 返回某用途的镜像，第一个有记录的位置优先（缓存先于套件）。
func (r *depsResolver) findImages(role string) ([]imageHit, error) {
	var all []imageHit
	hits, err := readImageRecords(r.cache, role)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		hits[i].Location = "缓存"
	}
	all = append(all, hits...)
	for _, k := range r.kits {
		if k.Err != nil {
			continue
		}
		if k.devkit != nil {
			for _, h := range k.devkit.images(role) {
				h.Location, h.kit = k.label()+" 版本 "+k.devkit.Latest.Version, k
				all = append(all, h)
			}
			continue
		}
		hits, err := readImageRecordsSigned(k.Root, role, k.sums)
		if err != nil {
			return nil, err
		}
		for i := range hits {
			hits[i].Location, hits[i].kit = k.label(), k
			all = append(all, hits[i])
		}
	}
	return all, nil
}

// toolImage 选取准备依赖用的工具镜像：取第一个有该用途记录的位置；同一位置有多个
// digest 时拒绝猜测（固定 digest 由第 2a 步源码配方声明）。来自套件且带归档的镜像，
// 先把归档复制到 stage（缓存内的暂存目录），在副本上核对签名清单摘要并算出镜像 ID，
// 返回的 Tar 指向副本，docker load 只读副本，不再按套件原路径读取。
func (r *depsResolver) toolImage(eco, stage string) (imageHit, error) {
	// python 准备在 base-operator（python:3.13）镜像里跑 python-inputs.sh，没有独立 tool-python。
	role := "tool-" + eco
	if eco == "python" {
		role = "base-operator"
	}
	hits, err := r.findImages(role)
	if err != nil {
		return imageHit{}, err
	}
	if len(hits) == 0 {
		return imageHit{}, fmt.Errorf("缺少工具镜像 %s：用 auriga deps import 导入含 images/%s/<digest>/ 的缓存或套件", role, role)
	}
	first := hits[0].Location
	var same []string
	for _, h := range hits {
		if h.Location == first {
			same = append(same, h.Image.Ref)
		}
	}
	if len(same) > 1 {
		return imageHit{}, fmt.Errorf("%s 中有多个 %s 镜像（%s），无法确定用哪一个；请只保留一个", first, role, strings.Join(same, "、"))
	}
	return stageImageHit(hits[0], stage)
}

// stageImageHit：来自套件且带归档的镜像先复制到 stage，在副本上核对签名清单摘要并
// 算出镜像 ID；缓存里的镜像原样返回。
func stageImageHit(hit imageHit, stage string) (imageHit, error) {
	role := hit.Image.Role
	if hit.kit == nil || hit.Tar == "" {
		return hit, nil
	}
	if stage == "" {
		return imageHit{}, errors.New("套件镜像归档需要暂存目录")
	}
	sums := hit.kit.sums
	if hit.kit.devkit != nil {
		sums = hit.kit.devkit.sums
	}
	staged, id, err := stageKitImage(hit, sums, stage)
	if err != nil {
		return imageHit{}, err
	}
	if hit.Image.ID != "" && hit.Image.ID != id {
		return imageHit{}, fmt.Errorf("套件镜像 %s 归档的镜像 ID 与 image.json 记录不符", role)
	}
	hit.Image.ID, hit.Tar = id, staged
	return hit, nil
}

// stageKitImage 把套件镜像归档复制到 stage，在副本上核对摘要等于签名清单，再从副本
// 算出镜像 ID。返回副本路径与镜像 ID。
func stageKitImage(hit imageHit, sums map[string]string, stage string) (string, string, error) {
	want, ok := sums[hit.rel]
	if !ok {
		return "", "", fmt.Errorf("套件文件未受签名清单覆盖 %s", hit.rel)
	}
	if err := noSymlinks(hit.Tar); err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(stage, "image-")
	if err != nil {
		return "", "", err
	}
	staged := filepath.Join(dir, "image.tar")
	if err := copyFile(hit.Tar, staged, 0644); err != nil {
		return "", "", err
	}
	got, err := digest(staged)
	if err != nil {
		return "", "", err
	}
	if got != want {
		return "", "", fmt.Errorf("套件文件摘要不符 %s", hit.rel)
	}
	id, err := dockerSaveImageID(staged)
	if err != nil {
		return "", "", fmt.Errorf("套件镜像 %s: %w", hit.Image.Role, err)
	}
	return staged, id, nil
}

// newestCacheEntry 返回缓存中某生态最近写入且校验通过的条目（作为准备时的种子缓存）。
func newestCacheEntry(cache, eco, except string) (string, string) {
	dir := filepath.Join(cache, "inputs", eco)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	type cand struct {
		name string
		at   time.Time
	}
	var list []cand
	for _, e := range entries {
		if !e.IsDir() || !hex64.MatchString(e.Name()) || e.Name() == except {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, cand{e.Name(), info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].at.Equal(list[j].at) {
			return list[i].at.After(list[j].at)
		}
		return list[i].name < list[j].name
	})
	for _, c := range list {
		path := filepath.Join(dir, c.name)
		if verifyInputEntry(path, eco, c.name) == nil {
			return path, c.name
		}
	}
	return "", ""
}

func writeDepsOrigin(dir string, origin depsOrigin) error {
	return writeDepsOriginWith(dir, "SHA256SUMS", origin)
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// ensureDepsCache 确认缓存目录 root 所有、组和其他用户不可写；不存在时在受信父目录下创建。
func ensureDepsCache(cache string, create bool) (bool, error) {
	if _, err := os.Lstat(cache); os.IsNotExist(err) {
		if !create {
			parent := filepath.Dir(cache)
			if _, err := os.Stat(parent); err == nil {
				if err := siteParentCheck(parent); err != nil {
					return false, fmt.Errorf("依赖缓存的父目录不受信：%w", err)
				}
			}
			return false, nil
		}
		if err := os.MkdirAll(filepath.Dir(cache), 0755); err != nil {
			return false, err
		}
		if err := siteParentCheck(filepath.Dir(cache)); err != nil {
			return false, fmt.Errorf("依赖缓存的父目录不受信：%w", err)
		}
		if err := os.Mkdir(cache, 0755); err != nil && !os.IsExist(err) {
			return false, err
		}
	}
	if err := checkRootOwnedDir("依赖缓存 deps.cache ", cache); err != nil {
		return false, err
	}
	return true, nil
}

// copyStream 复制文件到新文件（O_EXCL），供写入 Web 锁副本等小文件。
func copySmallFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
