package main

// 站点配置 site.json（设计 docs/dev/site-deploy.zh.md D9）。
//
// 第 1 步只实现解析、严格校验和权限校验；client 段只解析与校验，不接线；
// server、sign、logs、source.git 也只做格式校验，由后续步骤使用。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const siteConfigSchema = 1

const siteConfigLimit = 1 << 20

type siteConfig struct {
	SchemaVersion int         `json:"schemaVersion"`
	Source        *siteSource `json:"source"`
	Deps          *siteDeps   `json:"deps"`
	Server        *siteServer `json:"server"`
	Sign          *siteSign   `json:"sign"`
	Client        *siteClient `json:"client"`
	Logs          *siteLogs   `json:"logs"`
}

type siteSource struct {
	Git *siteGit `json:"git"`
	Dir *string  `json:"dir"`
}

type siteGit struct {
	URL                  string   `json:"url"`
	AllowedRefs          []string `json:"allowedRefs"`
	RequireSignedCommits bool     `json:"requireSignedCommits"`
	TrustedSigners       []string `json:"trustedSigners"`
}

type siteDeps struct {
	Cache         string      `json:"cache"`
	Kits          []siteKit   `json:"kits"`
	Sources       siteSources `json:"sources"`
	Registry      *string     `json:"registry"`
	CAFile        *string     `json:"caFile"`
	AllowPublic   bool        `json:"allowPublic"`
	AcceptNewDeps bool        `json:"acceptNewDeps"`
}

// siteKit：套件必须有独立信任——trustFingerprint（独立核对的发行公钥指纹），
// 或 trustState=true（使用 state 已钉定的发行/站点公钥验签）。二者恰选其一。
type siteKit struct {
	Path             string `json:"path"`
	TrustFingerprint string `json:"trustFingerprint,omitempty"`
	TrustState       bool   `json:"trustState,omitempty"`
}

// siteSources：只有 go 允许多个依赖源（数组）；npm、maven、pypi、files 为单个地址。
// pypi 为第 8 步相对设计 D9 示例新增（设计原文无 pip 源字段），规则与 npm/maven 相同。
type siteSources struct {
	Go    []string `json:"go"`
	NPM   *string  `json:"npm"`
	Maven *string  `json:"maven"`
	PyPI  *string  `json:"pypi"`
	Files *string  `json:"files"`
}

type siteServer struct {
	Data          string       `json:"data"`
	Install       *siteInstall `json:"install"`
	HealthTimeout *string      `json:"healthTimeout"`
	Smoke         *bool        `json:"smoke"`
	KeepHistory   *int         `json:"keepHistory"`
	// Runner 是工作流运行器账户名（设计 D12、遗留 P3）：经 sudo 调用且调用者是该账户时，
	// 一律按受限调用执行参数白名单，不能用 --operator 跳过。
	Runner *string `json:"runner"`
}

type siteInstall struct {
	Address                   *string `json:"address"`
	Domain                    *string `json:"domain"`
	Proxy                     *string `json:"proxy"`
	DesktopPublicKey          *string `json:"desktopPublicKey"`
	DesktopPublisherTenant    *string `json:"desktopPublisherTenant"`
	DesktopConfirmFingerprint *string `json:"desktopConfirmFingerprint"`
}

type siteSign struct {
	KeyFile string `json:"keyFile"`
}

type siteClient struct {
	Out               string  `json:"out"`
	PlatformOrigin    string  `json:"platformOrigin"`
	VerifyKey         *string `json:"verifyKey"`
	TrustFingerprint  *string `json:"trustFingerprint"`
	ReleasePublicKey  *string `json:"releasePublicKey"`
	ResourceTrustJSON *string `json:"resourceTrustJson"`
}

type siteLogs struct {
	Dir string `json:"dir"`
}

// siteInvocation 描述调用方式：经 sudo（环境里有 SUDO_UID）或 root 直接运行。
// SudoUID 是 sudo 记录的调用者 uid，用来核对是否为登记的运行器账户（privilege.go）。
type siteInvocation struct {
	Sudo    bool
	Root    bool
	SudoUID string
}

var lookupSudoUID = os.LookupEnv

func currentInvocation() siteInvocation {
	uid, sudo := lookupSudoUID("SUDO_UID")
	return siteInvocation{Sudo: sudo, Root: os.Geteuid() == 0, SudoUID: uid}
}

func defaultSiteConfigPath() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Auriga", "site.json")
	}
	return "/etc/auriga/site.json"
}

// 可注入点：测试在非 root 环境下用它们模拟属主和祖先目录校验。
var (
	siteOwnerOf      = fileOwnerUID
	siteParentCheck  = trustedParent
	openSiteFileFunc = openSiteFile
)

// loadSiteConfig 读取并校验站点配置。经 sudo 调用时拒绝 --config；root 直接运行
// 可用 --config 指向同样满足权限要求的文件。
func loadSiteConfig(flagPath string, inv siteInvocation) (*siteConfig, string, error) {
	path := defaultSiteConfigPath()
	if flagPath != "" {
		if inv.Sudo {
			return nil, "", errors.New("经 sudo 调用时不接受 --config，只读固定配置 " + path)
		}
		if !inv.Root {
			return nil, "", errors.New("--config 只允许 root 直接运行时使用")
		}
		path = flagPath
	}
	raw, err := readSiteConfigFile(path)
	if err != nil {
		return nil, path, err
	}
	cfg, err := parseSiteConfig(raw)
	if err != nil {
		return nil, path, fmt.Errorf("%s：%w", path, err)
	}
	return cfg, path, nil
}

// readSiteConfigFile 以不跟随符号链接的方式打开文件，对已打开的文件描述符核对
// 属主与权限后再读取，避免检查与读取之间被替换。
func readSiteConfigFile(path string) ([]byte, error) {
	if runtime.GOOS != "windows" && (!pathpkg.IsAbs(path) || pathpkg.Clean(path) != path) {
		return nil, fmt.Errorf("站点配置路径必须是规范绝对路径 %s", path)
	}
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	f, err := openSiteFileFunc(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := checkSiteConfigInfo(path, info); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, siteConfigLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > siteConfigLimit {
		return nil, fmt.Errorf("站点配置超过 %d 字节上限", siteConfigLimit)
	}
	return raw, nil
}

// checkSiteConfigInfo：root 所有、普通文件、权限恰为 0600 或 0644，
// 祖先目录不可被组或其他用户写（复用 trustedParent）。
func checkSiteConfigInfo(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("站点配置必须是普通文件 %s", path)
	}
	uid, err := siteOwnerOf(info)
	if err != nil {
		return err
	}
	if uid != 0 {
		return fmt.Errorf("站点配置必须由 root 所有 %s（当前属主 uid %d）", path, uid)
	}
	if perm := info.Mode().Perm(); perm != 0600 && perm != 0644 {
		return fmt.Errorf("站点配置权限必须是 0600 或 0644 %s（当前 %04o）", path, perm)
	}
	if err := siteParentCheck(filepath.Dir(path)); err != nil {
		return fmt.Errorf("站点配置所在目录不受信：%w", err)
	}
	return nil
}

// checkRootOwnedDir：目录本身 root 所有、组和其他用户不可写，祖先目录同 trustedParent。
func checkRootOwnedDir(what, dir string) error {
	if err := noSymlinks(dir); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s不是目录 %s", what, dir)
	}
	uid, err := siteOwnerOf(info)
	if err != nil {
		return err
	}
	if uid != 0 {
		return fmt.Errorf("%s必须由 root 所有 %s（当前属主 uid %d）", what, dir, uid)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s不能被组或其他用户写 %s（当前 %04o）", what, dir, info.Mode().Perm())
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := siteParentCheck(parent); err != nil {
			return fmt.Errorf("%s的祖先目录不受信：%w", what, err)
		}
	}
	return nil
}

// checkRootOwnedPath：目录同 checkRootOwnedDir；普通文件要求 root 所有、组和其他用户
// 不可写，所在目录同 trustedParent。用于套件目录、导入源（目录或归档）与 deps.caFile。
func checkRootOwnedPath(what, path string) error {
	if err := noSymlinks(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return checkRootOwnedDir(what, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s不是普通文件或目录 %s", what, path)
	}
	uid, err := siteOwnerOf(info)
	if err != nil {
		return err
	}
	if uid != 0 {
		return fmt.Errorf("%s必须由 root 所有 %s（当前属主 uid %d）", what, path, uid)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s不能被组或其他用户写 %s（当前 %04o）", what, path, info.Mode().Perm())
	}
	if err := siteParentCheck(filepath.Dir(path)); err != nil {
		return fmt.Errorf("%s所在目录不受信：%w", what, err)
	}
	return nil
}

// depsTrustedSourceCheck 校验套件目录、导入源与 caFile 的属主和权限；测试可注入。
var depsTrustedSourceCheck = checkRootOwnedPath

const depsTrustedSourceHint = "；请先复制到 root 所有、组和其他用户不可写的目录（如 /srv/auriga/kits）再使用"

// parseSiteConfig 严格解析：未知字段、重复键、尾部多余数据、类型不符都报错。
func parseSiteConfig(raw []byte) (*siteConfig, error) {
	if len(raw) > siteConfigLimit {
		return nil, fmt.Errorf("站点配置超过 %d 字节上限", siteConfigLimit)
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, err
	}
	if err := checkSourceShapes(raw); err != nil {
		return nil, err
	}
	var cfg siteConfig
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, fmt.Errorf("站点配置含未知字段（%v）；schemaVersion %d 不认识该字段，请删除或改用对应版本的 auriga", err, siteConfigSchema)
		}
		return nil, fmt.Errorf("站点配置不是有效 JSON 或字段类型不符：%w", err)
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, errors.New("站点配置 JSON 尾部有多余数据")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// rejectDuplicateKeys 逐个 token 扫描，任何对象里出现重复键都拒绝（encoding/json
// 默认取最后一个值，配置里重复键容易被误读）。
func rejectDuplicateKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("站点配置不是有效 JSON：%w", err)
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				keyTok, err := d.Token()
				if err != nil {
					return fmt.Errorf("站点配置不是有效 JSON：%w", err)
				}
				key, _ := keyTok.(string)
				if seen[key] {
					return fmt.Errorf("站点配置 %s 中键 %q 重复", strings.TrimPrefix(path, "."), key)
				}
				seen[key] = true
				if err := walk(path + "." + key); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for i := 0; d.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	return walk("")
}

// checkSourceShapes 给“只有 go 允许数组”一条能看懂的报错。
func checkSourceShapes(raw []byte) error {
	var top struct {
		Deps *struct {
			Sources map[string]json.RawMessage `json:"sources"`
		} `json:"deps"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || top.Deps == nil {
		return nil // 交给严格解码报具体错误
	}
	for name, value := range top.Deps.Sources {
		v := bytes.TrimSpace(value)
		if len(v) == 0 || string(v) == "null" {
			continue
		}
		switch name {
		case "go":
			if v[0] != '[' {
				return errors.New("deps.sources.go 必须是地址数组，例如 [\"https://goproxy.example\"]")
			}
		case "npm", "maven", "pypi", "files":
			if v[0] != '"' {
				return fmt.Errorf("deps.sources.%s 只能是单个地址字符串（只有 go 支持多个依赖源）", name)
			}
		}
	}
	return nil
}

var (
	gitRefPattern   = regexp.MustCompile(`^refs/(heads|tags)/[A-Za-z0-9._/-]+$`)
	registryPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*(?::([0-9]{1,5}))?$`)
	// 不允许 “,” 与 “|”：GOPROXY 把它们当列表分隔符，混进 direct/off 或公网源会绕过
	// allowPublic=false。
	urlCharPattern    = regexp.MustCompile(`^[A-Za-z0-9._~:/%+=@-]+$`)
	runnerNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	windowsAbsPath    = regexp.MustCompile(`^(?:[A-Za-z]:\\|\\\\[^\\/:*?"<>|\r\n]+\\)[^*?"<>|\r\n]*$`)
)

func (c *siteConfig) validate() error {
	if c.SchemaVersion != siteConfigSchema {
		return fmt.Errorf("schemaVersion 必须是 %d（当前 %d）；新版本配置请改用对应版本的 auriga", siteConfigSchema, c.SchemaVersion)
	}
	if c.Source != nil {
		if err := c.Source.validate(); err != nil {
			return err
		}
	}
	if c.Deps == nil {
		return errors.New("缺少 deps 段（至少要有 deps.cache）")
	}
	if err := c.Deps.validate(); err != nil {
		return err
	}
	if c.Server != nil {
		if err := c.Server.validate(); err != nil {
			return err
		}
	}
	if c.Sign != nil {
		if err := checkConfigPath("sign.keyFile", c.Sign.KeyFile); err != nil {
			return err
		}
	}
	if c.Client != nil {
		if err := c.Client.validate(); err != nil {
			return err
		}
	}
	if c.Logs != nil {
		if err := checkConfigPath("logs.dir", c.Logs.Dir); err != nil {
			return err
		}
	}
	return nil
}

// checkConfigPath：Linux 规范绝对路径，不含空白控制字符和引号。
func checkConfigPath(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s 不能为空", name)
	}
	if !pathpkg.IsAbs(value) || pathpkg.Clean(value) != value || strings.ContainsAny(value, "\x00\n\r\t\"'\\") {
		return fmt.Errorf("%s 必须是规范绝对路径（当前 %q）", name, value)
	}
	return nil
}

func checkWindowsPath(name, value string) error {
	if !windowsAbsPath.MatchString(value) {
		return fmt.Errorf("%s 必须是 Windows 绝对路径，例如 D:\\\\auriga-client-out（当前 %q）", name, value)
	}
	return nil
}

func (s *siteSource) validate() error {
	if s.Dir != nil {
		if err := checkConfigPath("source.dir", *s.Dir); err != nil {
			return err
		}
	}
	if s.Git == nil {
		return nil
	}
	g := s.Git
	if err := checkGitURL(g.URL); err != nil {
		return err
	}
	if len(g.AllowedRefs) == 0 {
		return errors.New("source.git.allowedRefs 至少需要一个引用，例如 refs/heads/main")
	}
	seen := map[string]bool{}
	for _, ref := range g.AllowedRefs {
		if !gitRefPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".lock") {
			return fmt.Errorf("source.git.allowedRefs 含无效引用 %q（只接受 refs/heads/... 或 refs/tags/...，不支持通配）", ref)
		}
		if seen[ref] {
			return fmt.Errorf("source.git.allowedRefs 重复 %q", ref)
		}
		seen[ref] = true
	}
	for i, signer := range g.TrustedSigners {
		if err := checkConfigPath(fmt.Sprintf("source.git.trustedSigners[%d]", i), signer); err != nil {
			return err
		}
	}
	if g.RequireSignedCommits && len(g.TrustedSigners) == 0 {
		return errors.New("source.git.requireSignedCommits=true 时必须配置 trustedSigners（受信签名者公钥文件路径）")
	}
	return nil
}

func checkGitURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || value == "" || !urlCharPattern.MatchString(value) {
		return fmt.Errorf("source.git.url 无效 %q", value)
	}
	switch u.Scheme {
	case "https", "ssh":
		if u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("source.git.url 需要主机且不带查询或片段 %q", value)
		}
		if _, hasPassword := u.User.Password(); hasPassword || u.Scheme == "https" && u.User != nil {
			return errors.New("source.git.url 不能内嵌凭据；密钥只写路径")
		}
	case "file":
		if err := checkFileURL("source.git.url", u); err != nil {
			return err
		}
	default:
		return fmt.Errorf("source.git.url 只允许 https://、ssh:// 或 file://（当前 %q）", value)
	}
	return nil
}

func checkFileURL(name string, u *url.URL) error {
	if u.Host != "" && u.Host != "localhost" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%s 的 file:// 地址必须形如 file:///绝对路径", name)
	}
	return checkConfigPath(name, u.Path)
}

// checkDepsURL：依赖源只允许 https:// 与 file://，不内嵌凭据，不带查询或片段。
func checkDepsURL(name, value string) error {
	u, err := url.Parse(value)
	if strings.ContainsAny(value, ",|") {
		return fmt.Errorf("%s 不能含 “,” 或 “|”（GOPROXY 列表分隔符），多个 Go 依赖源请写成数组的多个元素（当前 %q）", name, value)
	}
	if err != nil || value == "" || !urlCharPattern.MatchString(value) {
		return fmt.Errorf("%s 不是有效地址 %q（只允许字母数字和 ._~:/%%+=@- 字符）", name, value)
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("%s 需要主机名且不带查询或片段 %q", name, value)
		}
		if u.User != nil {
			return fmt.Errorf("%s 不能内嵌用户名或口令；密钥只写路径", name)
		}
		host := u.Hostname()
		if host == "" || strings.HasPrefix(host, "-") {
			return fmt.Errorf("%s 主机名无效 %q", name, value)
		}
		return nil
	case "file":
		return checkFileURL(name, u)
	}
	return fmt.Errorf("%s 只允许 https:// 或 file:// 地址（当前 %q）", name, value)
}

func checkRegistry(value string) error {
	m := registryPattern.FindStringSubmatch(value)
	if m == nil || len(value) > 253 {
		return fmt.Errorf("deps.registry 必须是主机名[:端口]，不带协议和路径（当前 %q）", value)
	}
	if m[1] != "" {
		port := 0
		_, _ = fmt.Sscanf(m[1], "%d", &port)
		if port < 1 || port > 65535 {
			return fmt.Errorf("deps.registry 端口无效（当前 %q）", value)
		}
	}
	return nil
}

// normalizeFingerprint 接受 "SHA256:<64位小写十六进制>" 或裸 64 位小写十六进制。
func normalizeFingerprint(name, value string) (string, error) {
	fp := strings.TrimPrefix(value, "SHA256:")
	if !hex64.MatchString(fp) {
		return "", fmt.Errorf("%s 需要 SHA256:<64 位小写十六进制>（公钥 SPKI DER 的 SHA-256）", name)
	}
	return fp, nil
}

func (d *siteDeps) validate() error {
	if err := checkConfigPath("deps.cache", d.Cache); err != nil {
		return err
	}
	if len(strings.Split(strings.Trim(d.Cache, "/"), "/")) < 2 {
		return errors.New("deps.cache 至少需要两层路径，例如 /srv/auriga/cache")
	}
	seen := map[string]bool{}
	for i, kit := range d.Kits {
		name := fmt.Sprintf("deps.kits[%d]", i)
		if err := checkConfigPath(name+".path", kit.Path); err != nil {
			return err
		}
		if seen[kit.Path] {
			return fmt.Errorf("%s.path 重复 %s", name, kit.Path)
		}
		seen[kit.Path] = true
		if kit.Path == d.Cache || strings.HasPrefix(kit.Path, d.Cache+"/") || strings.HasPrefix(d.Cache, kit.Path+"/") {
			return fmt.Errorf("%s.path 不能与 deps.cache 重叠", name)
		}
		switch {
		case kit.TrustFingerprint != "" && kit.TrustState:
			return fmt.Errorf("%s 的 trustFingerprint 与 trustState 只能选一个", name)
		case kit.TrustFingerprint != "":
			if _, err := normalizeFingerprint(name+".trustFingerprint", kit.TrustFingerprint); err != nil {
				return err
			}
		case !kit.TrustState:
			return fmt.Errorf("%s 必须有独立信任：trustFingerprint（独立核对的发行公钥指纹）或 trustState=true（用 state 已钉定的发行钥验签）；不接受套件用自带公钥自证", name)
		}
	}
	for i, source := range d.Sources.Go {
		if err := checkDepsURL(fmt.Sprintf("deps.sources.go[%d]", i), source); err != nil {
			return err
		}
	}
	for name, value := range map[string]*string{"npm": d.Sources.NPM, "maven": d.Sources.Maven, "pypi": d.Sources.PyPI, "files": d.Sources.Files} {
		if value != nil {
			if err := checkDepsURL("deps.sources."+name, *value); err != nil {
				return err
			}
		}
	}
	if d.Registry != nil {
		if err := checkRegistry(*d.Registry); err != nil {
			return err
		}
	}
	if d.CAFile != nil {
		if err := checkConfigPath("deps.caFile", *d.CAFile); err != nil {
			return err
		}
	}
	return nil
}

func (s *siteServer) validate() error {
	if err := checkConfigPath("server.data", s.Data); err != nil {
		return err
	}
	if s.HealthTimeout != nil {
		d, err := time.ParseDuration(*s.HealthTimeout)
		if err != nil || d <= 0 || d > 24*time.Hour {
			return fmt.Errorf("server.healthTimeout 需要 1s 到 24h 之间的时长，例如 10m（当前 %q）", *s.HealthTimeout)
		}
	}
	if s.KeepHistory != nil && (*s.KeepHistory < 1 || *s.KeepHistory > 100) {
		return fmt.Errorf("server.keepHistory 需要 1 到 100（当前 %d）", *s.KeepHistory)
	}
	if s.Runner != nil {
		if !runnerNamePattern.MatchString(*s.Runner) || *s.Runner == "root" {
			return fmt.Errorf("server.runner 需要运行器的本机账户名（小写字母、数字、_、-，以字母或 _ 开头，不超过 32 位，不能是 root），当前 %q", *s.Runner)
		}
	}
	if s.Install == nil {
		return nil
	}
	in := s.Install
	if in.Address != nil {
		ip := net.ParseIP(*in.Address)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("server.install.address 需要可达的 IPv4")
		}
	}
	if in.Domain != nil && (!domainName.MatchString(*in.Domain) || strings.Contains(*in.Domain, "..")) {
		return errors.New("server.install.domain 需要规范域名，例如 example.com")
	}
	if in.Proxy != nil {
		if err := checkConfigPath("server.install.proxy", *in.Proxy); err != nil {
			return err
		}
	}
	set := 0
	for _, v := range []*string{in.DesktopPublicKey, in.DesktopPublisherTenant, in.DesktopConfirmFingerprint} {
		if v != nil {
			set++
		}
	}
	if set != 0 && set != 3 {
		return errors.New("server.install 的 desktopPublicKey、desktopPublisherTenant、desktopConfirmFingerprint 必须一并提供或都为 null")
	}
	if in.DesktopPublicKey != nil {
		if err := checkConfigPath("server.install.desktopPublicKey", *in.DesktopPublicKey); err != nil {
			return err
		}
		if strings.TrimSpace(*in.DesktopPublisherTenant) == "" {
			return errors.New("server.install.desktopPublisherTenant 不能为空")
		}
		if !hex64.MatchString(*in.DesktopConfirmFingerprint) {
			return errors.New("server.install.desktopConfirmFingerprint 需要 64 位小写 SHA256（SPKI DER）")
		}
	}
	return nil
}

func (c *siteClient) validate() error {
	if err := checkWindowsPath("client.out", c.Out); err != nil {
		return err
	}
	u, err := url.Parse(c.PlatformOrigin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("client.platformOrigin 必须是 https:// 源（不带路径），当前 %q", c.PlatformOrigin)
	}
	for name, value := range map[string]*string{"client.verifyKey": c.VerifyKey, "client.releasePublicKey": c.ReleasePublicKey, "client.resourceTrustJson": c.ResourceTrustJSON} {
		if value != nil {
			if err := checkWindowsPath(name, *value); err != nil {
				return err
			}
		}
	}
	if c.TrustFingerprint != nil {
		if _, err := normalizeFingerprint("client.trustFingerprint", *c.TrustFingerprint); err != nil {
			return err
		}
	}
	if c.VerifyKey == nil && c.TrustFingerprint == nil {
		return errors.New("client 段需要 verifyKey 或 trustFingerprint 之一，用于核验发行套件")
	}
	return nil
}
