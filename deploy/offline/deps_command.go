package main

// auriga deps status|fetch|import（设计 D5、D10；统一现场部署第 1 步）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// 可注入点：测试替换缓存锁和 Docker 环境检查。
var (
	depsCacheLock   = lockDepsCache
	depsDockerReady = buildEnvironmentContext
)

// ensureDepsImage 确认工具镜像已在本机 Docker：缺失时从记录的 image.tar 载入，
// 并核对镜像 ID 与平台。返回运行时引用。
var ensureDepsImage = func(ctx context.Context, img imageHit) (string, error) {
	ref := img.Image.runtimeRef()
	if _, err := captureContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref); err != nil {
		if img.Tar == "" {
			return "", fmt.Errorf("本机 Docker 没有镜像 %s，记录里也没有 image.tar；请用 deps import 导入带归档的镜像", img.Image.Ref)
		}
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "load", "-i", img.Tar); err != nil {
			return "", err
		}
	}
	if img.Image.ID != "" {
		id, err := captureContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", img.Image.ID)
		if err != nil || id != img.Image.ID {
			return "", fmt.Errorf("载入的镜像 ID 与记录不符 %s", img.Image.Ref)
		}
	}
	return ref, checkOnlineTool(ctx, ref)
}

// autoFetchRule 是设计 D5 的按生态补依赖规则（第 8 步后：Java/Python 在锁/哈希就绪时可自动补）。
// source 是源码根；为空时只按配置源判断（测试用），Java/Python 仍要求调用方另行确认锁/哈希。
func autoFetchRule(eco string, d *siteDeps, source string) (bool, string) {
	switch eco {
	case "go":
		if len(d.Sources.Go) > 0 || d.AllowPublic {
			return true, ""
		}
		return false, "未配置 deps.sources.go 且 allowPublic=false，Go 依赖无处可取"
	case "web":
		if d.Sources.NPM != nil || d.AllowPublic {
			return true, ""
		}
		return false, "未配置 deps.sources.npm 且 allowPublic=false，Web 依赖无处可取"
	case "java":
		if source != "" {
			ok, err := mavenLockNonEmpty(source)
			if err != nil {
				return false, "读取 " + mavenDepsLockRel + " 失败：" + err.Error()
			}
			if !ok {
				return false, "Java（Maven）依赖在锁文件 " + mavenDepsLockRel + " 就绪前不能自动补，只能 deps import 并人工审核"
			}
		}
		if d.Sources.Maven != nil || d.AllowPublic {
			return true, ""
		}
		return false, "未配置 deps.sources.maven 且 allowPublic=false，Java 依赖无处可取"
	case "python":
		if source != "" {
			ok, err := pythonRequirementsHashed(source)
			if err != nil {
				return false, "读取 " + pythonRequirementsRel + " 失败：" + err.Error()
			}
			if !ok {
				return false, "Python（operator）依赖在 requirements 全部钉版本并带 --hash=sha256 前不能自动补，只能 deps import"
			}
		}
		if d.Sources.PyPI != nil || d.AllowPublic {
			return true, ""
		}
		return false, "未配置 deps.sources.pypi 且 allowPublic=false，Python 依赖无处可取"
	case "system":
		return false, "系统包由基础镜像和固定 digest 决定，只能 deps import 或套件"
	}
	return false, "未知生态 " + eco
}

func depsSourceLabel(d *siteDeps, eco string) string {
	switch eco {
	case "go":
		if len(d.Sources.Go) > 0 {
			return strings.Join(d.Sources.Go, ", ")
		}
	case "web":
		if d.Sources.NPM != nil {
			return *d.Sources.NPM
		}
	case "java":
		if d.Sources.Maven != nil {
			return *d.Sources.Maven
		}
	case "python":
		if d.Sources.PyPI != nil {
			return *d.Sources.PyPI
		}
	}
	if d.AllowPublic {
		return "公网默认源（allowPublic=true）"
	}
	return "无"
}

// 各生态的公网默认源主机。allowPublic=false 时在准备容器里把它们解析到回环地址，
// 并加进 NO_PROXY，保证即使脚本回落（例如 Go 校验库代理不支持时直连
// sum.golang.org）也取不到，直接失败。
var depsPublicHosts = map[string][]string{
	"go":     {"proxy.golang.org", "sum.golang.org"},
	"web":    {"registry.npmjs.org"},
	"java":   {"repo.maven.apache.org", "repo1.maven.org", "nodejs.org", "registry.npmjs.org"},
	"python": {"pypi.org", "files.pythonhosted.org"},
}

const depsCAMount = "/etc/auriga-deps-ca"

// depsPrepareSettings 由站点配置生成现有准备逻辑使用的环境变量（交给 online.go 的
// prepareDockerArgs 映射为 AURIGA_GO_PREPARE_PROXY、npm_config_registry、
// AURIGA_MAVEN_MIRROR_URL 等）与附加 docker 参数。主机环境只透传 HTTP(S)_PROXY/NO_PROXY，
// GOPROXY 等依赖源一律来自配置。
func depsPrepareSettings(d *siteDeps, eco string, host func(string) (string, bool)) (func(string) (string, bool), []string, error) {
	values := map[string]string{}
	var extra []string
	for _, name := range proxyEnvNames {
		if v, ok := host(name); ok && v != "" {
			values[name] = v
		}
	}
	isPublic := func(u *url.URL) bool {
		for _, hosts := range depsPublicHosts {
			for _, h := range hosts {
				if strings.EqualFold(u.Hostname(), h) {
					return true
				}
			}
		}
		return false
	}
	single := func(name string, value *string) (string, error) {
		if value == nil {
			if !d.AllowPublic {
				return "", fmt.Errorf("未配置 deps.sources.%s 且 allowPublic=false", name)
			}
			return "", nil
		}
		u, err := url.Parse(*value)
		if err != nil {
			return "", err
		}
		if u.Scheme == "file" {
			return "", fmt.Errorf("deps.sources.%s 暂不支持 file:// 地址用于自动准备，请改用 https:// 源或 deps import", name)
		}
		if !d.AllowPublic && isPublic(u) {
			return "", fmt.Errorf("allowPublic=false 时不能把公网默认源 %s 配为 deps.sources.%s", u.Hostname(), name)
		}
		return *value, nil
	}
	switch eco {
	case "go":
		var list []string
		for i, source := range d.Sources.Go {
			if err := checkDepsURL(fmt.Sprintf("deps.sources.go[%d]", i), source); err != nil {
				return nil, nil, err
			}
			u, err := url.Parse(source)
			if err != nil {
				return nil, nil, err
			}
			if u.Scheme == "file" {
				info, err := os.Stat(u.Path)
				if err != nil || !info.IsDir() {
					return nil, nil, fmt.Errorf("deps.sources.go[%d] 的目录不可用 %s", i, u.Path)
				}
				mount := fmt.Sprintf("/auriga-deps-sources/go-%d", i)
				extra = append(extra, mountArg(u.Path, mount, true)...)
				list = append(list, "file://"+mount)
				continue
			}
			if !d.AllowPublic && isPublic(u) {
				return nil, nil, fmt.Errorf("allowPublic=false 时不能把公网默认源 %s 配为 deps.sources.go", u.Hostname())
			}
			list = append(list, strings.TrimRight(source, "/"))
		}
		// 配置校验已拒绝分隔符；这里再逐项断言，保证 GOPROXY 里只有配置的源，
		// 不会出现 direct/off 或被拆出的额外地址。
		for i, item := range list {
			if err := checkGoproxyItem(item); err != nil {
				return nil, nil, fmt.Errorf("deps.sources.go[%d] %w", i, err)
			}
		}
		if len(list) > 0 {
			values["GOPROXY"] = strings.Join(list, ",")
		} else if !d.AllowPublic {
			return nil, nil, errors.New("未配置 deps.sources.go 且 allowPublic=false，不回落 proxy.golang.org")
		}
	case "web":
		v, err := single("npm", d.Sources.NPM)
		if err != nil {
			return nil, nil, err
		}
		if v != "" {
			values["npm_config_registry"], values["COREPACK_NPM_REGISTRY"] = v, v
		}
	case "java":
		v, err := single("maven", d.Sources.Maven)
		if err != nil {
			return nil, nil, err
		}
		if v != "" {
			if err := validateMavenMirrorURL(v); err != nil {
				return nil, nil, errors.New("deps.sources.maven " + strings.TrimPrefix(err.Error(), "AURIGA_MAVEN_MIRROR_URL "))
			}
			values["AURIGA_MAVEN_MIRROR_URL"] = v
		}
		if d.Sources.NPM != nil {
			registry, err := single("npm", d.Sources.NPM)
			if err != nil {
				return nil, nil, err
			}
			values["npm_config_registry"] = registry
		}
		if !d.AllowPublic {
			values["AURIGA_JAVA_NO_PUBLIC"] = "1"
		}
		if d.Sources.Files != nil {
			if err := checkDepsURL("deps.sources.files", *d.Sources.Files); err != nil {
				return nil, nil, err
			}
			files, err := url.Parse(*d.Sources.Files)
			if err != nil {
				return nil, nil, err
			}
			switch {
			case files.Scheme == "file":
				info, err := os.Stat(files.Path)
				if err != nil || !info.IsDir() {
					return nil, nil, fmt.Errorf("deps.sources.files 的目录不可用 %s", files.Path)
				}
				if err := depsTrustedSourceCheck("deps.sources.files ", files.Path); err != nil {
					return nil, nil, err
				}
				const mount = "/auriga-deps-sources/java-files"
				extra = append(extra, mountArg(files.Path, mount, true)...)
				values["AURIGA_JAVA_FILES"] = "file://" + mount
			case !d.AllowPublic && isPublic(files):
				return nil, nil, fmt.Errorf("allowPublic=false 时不能把公网默认源 %s 配为 deps.sources.files", files.Hostname())
			default:
				values["AURIGA_JAVA_FILES"] = *d.Sources.Files
			}
		}
	case "python":
		v, err := single("pypi", d.Sources.PyPI)
		if err != nil {
			return nil, nil, err
		}
		if v != "" {
			values["PIP_INDEX_URL"] = v
			values["AURIGA_PIP_INDEX_URL"] = v
		}
	default:
		return nil, nil, fmt.Errorf("%s 不能自动准备", eco)
	}
	if !d.AllowPublic {
		hosts := depsPublicHosts[eco]
		for _, name := range []string{"NO_PROXY", "no_proxy"} {
			var parts []string
			if v := values[name]; v != "" {
				parts = append(parts, v)
			}
			values[name] = strings.Join(append(parts, hosts...), ",")
		}
		for _, h := range hosts {
			extra = append(extra, "--add-host", h+":127.0.0.1")
		}
	}
	if d.CAFile != nil {
		info, err := os.Stat(*d.CAFile)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("deps.caFile 不可用 %s", *d.CAFile)
		}
		if err := noSymlinks(*d.CAFile); err != nil {
			return nil, nil, err
		}
		if err := depsTrustedSourceCheck("deps.caFile ", *d.CAFile); err != nil {
			return nil, nil, err
		}
		extra = append(extra, mountArg(*d.CAFile, depsCAMount+"/ca.pem", true)...)
		// Go 读 SSL_CERT_DIR 下全部证书并保留系统默认根证书文件；Node/corepack/pnpm
		// 用 NODE_EXTRA_CA_CERTS 追加。Java prepare 用挂载的 PEM 建立临时信任库。
		extra = append(extra, "--env", "SSL_CERT_DIR="+depsCAMount, "--env", "NODE_EXTRA_CA_CERTS="+depsCAMount+"/ca.pem")
		if eco == "java" {
			extra = append(extra, "--env", "AURIGA_JAVA_CA_FILE="+depsCAMount+"/ca.pem")
		}
	}
	lookup := func(name string) (string, bool) { v, ok := values[name]; return v, ok }
	return lookup, extra, nil
}

// checkGoproxyItem 断言 GOPROXY 的单项是 https:// 或 file:// 地址，不含列表分隔符，
// 也不是 direct/off。
func checkGoproxyItem(item string) error {
	if item == "" || strings.ContainsAny(item, ",|") {
		return fmt.Errorf("含 GOPROXY 列表分隔符或为空 %q", item)
	}
	switch strings.ToLower(strings.TrimSpace(item)) {
	case "direct", "off":
		return fmt.Errorf("不能是 %s", item)
	}
	if !strings.HasPrefix(item, "https://") && !strings.HasPrefix(item, "file://") {
		return fmt.Errorf("只允许 https:// 或 file:// 地址 %q", item)
	}
	return nil
}

// webLockMissingIntegrity 列出 pnpm-lock.yaml packages 段里没有 integrity 的条目。
func webLockMissingIntegrity(raw []byte) []string {
	var bad []string
	for _, e := range pnpmLockPackages(raw) {
		if e.Integrity == "" {
			bad = append(bad, e.Name+"@"+e.Version)
		}
	}
	sort.Strings(bad)
	return bad
}

func checkWebIntegrity(source string) error {
	raw, err := os.ReadFile(filepath.Join(source, "web", "pnpm-lock.yaml"))
	if err != nil {
		return err
	}
	if bad := webLockMissingIntegrity(raw); len(bad) > 0 {
		return fmt.Errorf("web/pnpm-lock.yaml 有 %d 个条目没有 integrity，拒绝自动准备：%s", len(bad), limitList(bad, 20))
	}
	return nil
}

func limitList(list []string, n int) string {
	if len(list) <= n {
		return strings.Join(list, "、")
	}
	return strings.Join(list[:n], "、") + fmt.Sprintf(" …共 %d 项", len(list))
}

type depsEcoStatus struct {
	Ecosystem  string   `json:"ecosystem"`
	LockDigest string   `json:"lockDigest"`
	Present    bool     `json:"present"`
	Location   string   `json:"location,omitempty"`
	AutoFetch  bool     `json:"autoFetch"`
	Remedy     string   `json:"remedy,omitempty"`
	Problems   []string `json:"problems,omitempty"`
}

type depsKitStatus struct {
	Path   string `json:"path"`
	Usable bool   `json:"usable"`
	Trust  string `json:"trust,omitempty"`
	Layout string `json:"layout,omitempty"`
	Error  string `json:"error,omitempty"`
}

type depsImageStatus struct {
	Role      string   `json:"role"`
	Available []string `json:"available"`
	Remedy    string   `json:"remedy,omitempty"`
}

type depsStatusReport struct {
	Config      string            `json:"config"`
	Source      string            `json:"source"`
	Cache       string            `json:"cache"`
	CacheExists bool              `json:"cacheExists"`
	AllowPublic bool              `json:"allowPublic"`
	AcceptNew   bool              `json:"acceptNewDeps"`
	Kits        []depsKitStatus   `json:"kits"`
	Ecosystems  []depsEcoStatus   `json:"ecosystems"`
	Images      []depsImageStatus `json:"images"`
	Missing     []string          `json:"missing"`
}

const depsImportHint = "用 auriga deps import <目录或归档> 导入，或在 deps.kits 配置含该锁摘要的套件"

func computeDepsStatus(cfg *siteConfig, cfgPath, source string) (*depsStatusReport, *depsResolver, []ecosystemLock, error) {
	exists, err := ensureDepsCache(cfg.Deps.Cache, false)
	if err != nil {
		return nil, nil, nil, err
	}
	locks, err := sourceEcosystemLocks(source)
	if err != nil {
		return nil, nil, nil, err
	}
	r := newDepsResolver(cfg)
	rep := &depsStatusReport{Config: cfgPath, Source: source, Cache: cfg.Deps.Cache, CacheExists: exists, AllowPublic: cfg.Deps.AllowPublic, AcceptNew: cfg.Deps.AcceptNewDeps, Kits: []depsKitStatus{}, Missing: []string{}}
	for _, k := range r.kits {
		ks := depsKitStatus{Path: k.Conf.Path, Usable: k.Err == nil, Trust: k.Trust, Layout: "依赖缓存布局"}
		if k.devkit != nil {
			ks.Layout = k.devkit.describe()
		}
		if k.Err != nil {
			ks.Error, ks.Layout = k.Err.Error(), ""
		}
		rep.Kits = append(rep.Kits, ks)
	}
	for _, lock := range locks {
		st := depsEcoStatus{Ecosystem: lock.Ecosystem, LockDigest: lock.Digest}
		auto, reason := autoFetchRule(lock.Ecosystem, cfg.Deps, source)
		if auto && lock.Ecosystem == "web" {
			if err := checkWebIntegrity(source); err != nil {
				auto, reason = false, err.Error()
				st.Problems = append(st.Problems, err.Error())
			}
		}
		st.AutoFetch = auto
		if hit, ok := r.findInput(lock.Ecosystem, lock.Digest); ok {
			st.Present, st.Location = true, hit.Location
		} else {
			rep.Missing = append(rep.Missing, lock.Ecosystem)
			if auto {
				st.Remedy = "auriga deps fetch（依赖源：" + depsSourceLabel(cfg.Deps, lock.Ecosystem) + "）"
			} else {
				st.Remedy = reason + "；" + depsImportHint
			}
		}
		rep.Ecosystems = append(rep.Ecosystems, st)
	}
	for _, role := range depsImageRoles {
		st := depsImageStatus{Role: role, Available: []string{}}
		hits, err := r.findImages(role)
		if err != nil {
			st.Remedy = "镜像记录无效：" + err.Error()
		}
		for _, h := range hits {
			st.Available = append(st.Available, h.Location+" "+h.Image.Ref)
		}
		if len(hits) == 0 && err == nil {
			st.Remedy = "只能 deps import 或套件提供（固定 digest）"
		}
		rep.Images = append(rep.Images, st)
	}
	return rep, r, locks, nil
}

func printDepsStatus(w io.Writer, rep *depsStatusReport) {
	fmt.Fprintf(w, "站点配置 %s\n源码 %s\n", rep.Config, rep.Source)
	cache := rep.Cache
	if !rep.CacheExists {
		cache += "（尚不存在，deps fetch/import 时创建）"
	}
	fmt.Fprintf(w, "缓存 %s\n公网 allowPublic=%v；新增依赖 acceptNewDeps=%v\n", cache, rep.AllowPublic, rep.AcceptNew)
	if len(rep.Kits) > 0 {
		fmt.Fprintln(w, "套件：")
		for _, k := range rep.Kits {
			if k.Usable {
				fmt.Fprintf(w, "  %s  可用（%s；%s）\n", k.Path, k.Layout, k.Trust)
			} else {
				fmt.Fprintf(w, "  %s  不可用：%s\n", k.Path, k.Error)
			}
		}
	}
	fmt.Fprintln(w, "依赖输入（按锁摘要整套选取：缓存 → 套件 → 依赖源）：")
	for _, e := range rep.Ecosystems {
		if e.Present {
			fmt.Fprintf(w, "  %-6s %s  命中 %s\n", e.Ecosystem, e.LockDigest, e.Location)
		} else {
			fmt.Fprintf(w, "  %-6s %s  缺失；补法：%s\n", e.Ecosystem, e.LockDigest, e.Remedy)
		}
		for _, p := range e.Problems {
			fmt.Fprintf(w, "         问题：%s\n", p)
		}
	}
	fmt.Fprintln(w, "镜像（固定 digest）：")
	for _, img := range rep.Images {
		if len(img.Available) > 0 {
			fmt.Fprintf(w, "  %-16s %s\n", img.Role, strings.Join(img.Available, "；"))
		} else {
			fmt.Fprintf(w, "  %-16s 缺失；%s\n", img.Role, img.Remedy)
		}
	}
	if len(rep.Missing) == 0 {
		fmt.Fprintln(w, "依赖输入全部命中。")
	} else {
		fmt.Fprintf(w, "缺失生态：%s\n", strings.Join(rep.Missing, "、"))
	}
}

// lockFileChanges 比较两份 source-locks.sha256，列出变化的锁文件。
func lockFileChanges(oldRaw, newRaw []byte) []string {
	parse := func(raw []byte) map[string]string {
		out := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if len(line) > 66 {
				out[line[66:]] = line[:64]
			}
		}
		return out
	}
	oldSet, newSet := parse(oldRaw), parse(newRaw)
	var changed []string
	for name, hash := range newSet {
		if oldSet[name] != hash {
			changed = append(changed, name)
		}
	}
	for name := range oldSet {
		if _, ok := newSet[name]; !ok {
			changed = append(changed, name+"（已删除）")
		}
	}
	sort.Strings(changed)
	return changed
}

// printFetchPlan 打印将要（或需要）补的依赖清单。
func printFetchPlan(w io.Writer, cfg *siteConfig, rep *depsStatusReport, locks []ecosystemLock, source string) {
	fmt.Fprintln(w, "新增依赖清单（缓存与套件均未命中的锁摘要）：")
	byEco := map[string]ecosystemLock{}
	for _, l := range locks {
		byEco[l.Ecosystem] = l
	}
	for _, e := range rep.Ecosystems {
		if e.Present {
			continue
		}
		if e.AutoFetch {
			fmt.Fprintf(w, "  [%s] 锁摘要 %s：可自动补，依赖源 %s\n", e.Ecosystem, e.LockDigest, depsSourceLabel(cfg.Deps, e.Ecosystem))
		} else {
			fmt.Fprintf(w, "  [%s] 锁摘要 %s：不能自动补——%s\n", e.Ecosystem, e.LockDigest, e.Remedy)
		}
		seed, _ := newestCacheEntry(cfg.Deps.Cache, e.Ecosystem, e.LockDigest)
		if seed == "" {
			fmt.Fprintln(w, "      缓存里没有该生态的旧条目，将整套准备")
			continue
		}
		oldLocks, _ := os.ReadFile(filepath.Join(seed, "source-locks.sha256"))
		if changed := lockFileChanges(oldLocks, byEco[e.Ecosystem].Raw); len(changed) > 0 {
			fmt.Fprintf(w, "      相对缓存最近条目变化的锁文件：%s\n", limitList(changed, 20))
		}
		if e.Ecosystem == "web" {
			oldLock, err1 := os.ReadFile(filepath.Join(seed, depsWebLockFile))
			newLock, err2 := os.ReadFile(filepath.Join(source, "web", "pnpm-lock.yaml"))
			if err1 == nil && err2 == nil {
				added, _ := diffDeps(pnpmLockPackages(oldLock), pnpmLockPackages(newLock))
				var names []string
				for _, a := range added {
					names = append(names, a.Name+"@"+a.Version+" "+a.Integrity)
				}
				fmt.Fprintf(w, "      新增 Web 包 %d 个：%s\n", len(names), limitList(names, 50))
			}
		}
		if e.Ecosystem == "go" {
			fmt.Fprintln(w, "      Go 新增模块以准备结果为准（准备后写入 site-dependencies.json，含 SHA-256）")
		}
	}
}

var errNewDepsRejected = errors.New("存在新增依赖，acceptNewDeps=false，未拉取任何依赖；审核上面的清单后在配置中设置 deps.acceptNewDeps=true 或加 --accept-new-deps 重试")

type depsFetchOptions struct {
	Accept bool
	Budget buildBudget
	Out    io.Writer
}

// depsFetch 按规则准备缺失且允许自动补的生态，写回可写缓存。
func depsFetch(ctx context.Context, cfg *siteConfig, cfgPath, source string, o depsFetchOptions) error {
	w := o.Out
	if _, err := ensureDepsCache(cfg.Deps.Cache, true); err != nil {
		return err
	}
	unlock, err := depsCacheLock(cfg.Deps.Cache)
	if err != nil {
		return err
	}
	defer unlock()
	rep, r, locks, err := computeDepsStatus(cfg, cfgPath, source)
	if err != nil {
		return err
	}
	if len(rep.Missing) == 0 {
		fmt.Fprintln(w, "依赖输入全部命中（缓存或套件），无需拉取。")
		return nil
	}
	printFetchPlan(w, cfg, rep, locks, source)
	autoMissing := false
	for _, e := range rep.Ecosystems {
		if !e.Present && e.AutoFetch {
			autoMissing = true
		}
	}
	// 只有存在可自动补的缺失时才提示 acceptNewDeps；只缺 Java/system 等时设置了也补不了，
	// 直接报“不能自动补”。
	if autoMissing && !o.Accept && !cfg.Deps.AcceptNewDeps {
		return errNewDepsRejected
	}
	var refused []string
	dockerChecked := false
	for i, e := range rep.Ecosystems {
		if e.Present {
			continue
		}
		if !e.AutoFetch {
			refused = append(refused, e.Ecosystem)
			continue
		}
		if !dockerChecked {
			if err := depsDockerReady(ctx, o.Budget); err != nil {
				return err
			}
			dockerChecked = true
		}
		if err := fetchEcosystem(ctx, cfg, r, source, locks[i], o.Budget, w); err != nil {
			return fmt.Errorf("%s 依赖准备失败：%w", e.Ecosystem, err)
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("以下生态缺失且不能自动补：%s；%s", strings.Join(refused, "、"), depsImportHint)
	}
	return nil
}

func fetchEcosystem(ctx context.Context, cfg *siteConfig, r *depsResolver, source string, lock ecosystemLock, budget buildBudget, w io.Writer) error {
	eco := lock.Ecosystem
	if eco == "web" {
		if err := checkWebIntegrity(source); err != nil {
			return err
		}
	}
	if eco == "java" {
		ok, err := mavenLockNonEmpty(source)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("缺少非空锁文件 %s", mavenDepsLockRel)
		}
	}
	if eco == "python" {
		ok, err := pythonRequirementsHashed(source)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("requirements 未全部钉版本并带 --hash=sha256")
		}
	}
	lookup, extra, err := depsPrepareSettings(cfg.Deps, eco, lookupOnlineEnv)
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(cfg.Deps.Cache, ".staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	// 套件镜像归档复制到暂存目录、在副本上核对后，docker load 只读副本。
	tool, err := r.toolImage(eco, staging)
	if err != nil {
		return err
	}
	ref, err := ensureDepsImage(ctx, tool)
	if err != nil {
		return err
	}
	seed, _ := newestCacheEntry(cfg.Deps.Cache, eco, lock.Digest)
	seedMount := ""
	seedRW := false
	if seed != "" {
		if seedMount, seedRW, err = prepareCache(ctx, eco, seed, staging); err != nil {
			return err
		}
	}
	out := filepath.Join(staging, "out")
	if err := os.Mkdir(out, 0755); err != nil {
		return err
	}
	fmt.Fprintf(w, "准备 %s 依赖（锁摘要 %s，依赖源 %s）…\n", eco, lock.Digest, depsSourceLabel(cfg.Deps, eco))
	args, env := prepareDockerArgs(prepareRun{Kind: eco, Tool: ref, Source: source, Out: out, Cache: seedMount, CacheRW: seedRW, Budget: budget, Extra: extra}, lookup)
	if err := runOnlineDocker(ctx, env, args...); err != nil {
		return fmt.Errorf("准备容器执行失败（请查看上方容器输出，并核对依赖源、代理、caFile 与缓存目录权限）：%w", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "source-locks.sha256"))
	if err != nil {
		return fmt.Errorf("准备结果缺少 source-locks.sha256: %w", err)
	}
	if !bytes.Equal(got, lock.Raw) {
		return errors.New("准备脚本记录的锁清单与 CLI 计算不一致（CLI 与 *-inputs.sh 锁规则不同步，或源码在准备期间被改动），拒绝写入缓存")
	}
	// 准备容器按自己的 umask 写文件：进缓存前统一去掉组和其他用户的写位。
	if err := dropGroupOtherWrite(out); err != nil {
		return err
	}
	if err := verifyInputEntry(out, eco, lock.Digest); err != nil {
		return err
	}
	// 脚本在容器里已核对过锁文件；入缓存前 CLI 再按源码锁文件独立核对一次。
	if eco == "java" {
		if err := verifyImportedJavaAgainstLock(source, out); err != nil {
			return err
		}
	}
	report, err := fetchReport(ctx, eco, lock, seed, out, source)
	if err != nil {
		return err
	}
	if eco == "web" {
		if err := copySmallFile(filepath.Join(source, "web", "pnpm-lock.yaml"), filepath.Join(out, depsWebLockFile)); err != nil {
			return err
		}
	}
	if err := jsonFile(filepath.Join(out, depsReportFile), report, 0644); err != nil {
		return err
	}
	from := []string{depsSourceLabel(cfg.Deps, eco)}
	if eco == "go" && len(cfg.Deps.Sources.Go) > 0 {
		from = append([]string(nil), cfg.Deps.Sources.Go...)
	}
	if err := writeDepsOrigin(out, depsOrigin{Kind: "fetch", Ecosystem: eco, LockDigest: lock.Digest, From: from}); err != nil {
		return err
	}
	target := r.cacheInputDir(eco, lock.Digest)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		fmt.Fprintf(w, "缓存已有 %s/%s，保留原条目\n", eco, lock.Digest)
		return nil
	}
	if err := os.Rename(out, target); err != nil {
		return err
	}
	printSiteReport(w, report)
	fmt.Fprintf(w, "已写入缓存 %s\n", target)
	return nil
}

// dropGroupOtherWrite 去掉目录树里全部文件和目录的组/其他用户写位（拒绝符号链接）。
func dropGroupOtherWrite(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("准备结果含符号链接 %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if perm := info.Mode().Perm(); perm&0022 != 0 {
			return os.Chmod(path, perm&^0022)
		}
		return nil
	})
}

// fetchReport 生成新条目的 site-dependencies.json（结构同 bundle --online）。
func fetchReport(ctx context.Context, eco string, lock ecosystemLock, seed, out, source string) (*siteReport, error) {
	rep := &siteReport{Schema: 1, Online: true, LockDigest: lock.Digest, Ecosystems: []string{eco}, ChangedLockFiles: []string{}, Added: []depEntry{}, Removed: []depEntry{}, Notice: siteReportNotice}
	if seed != "" {
		if old, err := os.ReadFile(filepath.Join(seed, "source-locks.sha256")); err == nil {
			rep.ChangedLockFiles = append(rep.ChangedLockFiles, lockFileChanges(old, lock.Raw)...)
		}
	}
	old, current := map[string]depEntry{}, map[string]depEntry{}
	var err error
	switch eco {
	case "go":
		if seed != "" {
			if old, err = listGoModules(ctx, seed); err != nil {
				return nil, err
			}
		}
		if current, err = listGoModules(ctx, out); err != nil {
			return nil, err
		}
	case "web":
		if seed != "" {
			if raw, err := os.ReadFile(filepath.Join(seed, depsWebLockFile)); err == nil {
				old = pnpmLockPackages(raw)
			} else {
				rep.Warnings = append(rep.Warnings, "种子条目没有 pnpm-lock 副本，新增清单按全部包列出")
			}
		}
		raw, err := os.ReadFile(filepath.Join(source, "web", "pnpm-lock.yaml"))
		if err != nil {
			return nil, err
		}
		current = pnpmLockPackages(raw)
	case "java":
		if seed != "" {
			if old, err = listMavenArtifacts(ctx, filepath.Join(seed, "repository")); err != nil {
				return nil, err
			}
		}
		if current, err = listMavenArtifacts(ctx, filepath.Join(out, "repository")); err != nil {
			return nil, err
		}
	case "python":
		if seed != "" {
			if old, err = listPythonWheels(ctx, filepath.Join(seed, "packages")); err != nil {
				return nil, err
			}
		}
		if current, err = listPythonWheels(ctx, filepath.Join(out, "packages")); err != nil {
			return nil, err
		}
	}
	added, removed := diffDeps(old, current)
	for i := range added {
		if added[i].path == "" {
			continue
		}
		sum, err := digestContext(ctx, added[i].path)
		if err != nil {
			return nil, err
		}
		added[i].SHA256, added[i].File = sum, filepath.Base(added[i].path)
	}
	rep.Added = append(rep.Added, added...)
	rep.Removed = append(rep.Removed, removed...)
	return rep, nil
}

type importEntry struct {
	Rel        string // inputs/<eco>/<digest> 或 images/<role>/<hex>
	Ecosystem  string
	LockDigest string
	Image      bool
}

// verifyImportLayout 校验待导入的缓存布局。trust 给了 trustFingerprint 或 trustState
// 时必须有独立信任的有效签名；都没有时允许未签名的人工导入（打印清单供人工审核）。
func verifyImportLayout(ctx context.Context, cfg *siteConfig, root string, trust siteKit) ([]importEntry, bool, string, error) {
	top, err := os.ReadDir(root)
	if err != nil {
		return nil, false, "", err
	}
	for _, e := range top {
		switch e.Name() {
		case "SHA256SUMS", "SHA256SUMS.sig", "release-public.pem":
			if !e.Type().IsRegular() {
				return nil, false, "", fmt.Errorf("导入内容的 %s 必须是普通文件", e.Name())
			}
		case "inputs", "images":
			if !e.IsDir() {
				return nil, false, "", fmt.Errorf("导入内容的 %s 必须是目录", e.Name())
			}
		default:
			return nil, false, "", fmt.Errorf("导入内容根目录只允许 SHA256SUMS、SHA256SUMS.sig、release-public.pem、inputs/、images/，发现 %s", e.Name())
		}
	}
	if err := verifySumsContext(ctx, root); err != nil {
		return nil, false, "", fmt.Errorf("导入内容的根 SHA256SUMS 校验失败：%w", err)
	}
	signed, fp := false, ""
	if trust.TrustFingerprint != "" || trust.TrustState {
		_, got, _, err := verifyKitTrust(cfg, root, trust, "--trust-fingerprint")
		if err != nil {
			return nil, false, "", fmt.Errorf("导入内容签名核验失败：%w", err)
		}
		signed, fp = true, got
	}
	var entries []importEntry
	ecoDirs, _ := os.ReadDir(filepath.Join(root, "inputs"))
	known := map[string]bool{}
	for _, eco := range depsEcosystems {
		known[eco] = true
	}
	for _, ecoDir := range ecoDirs {
		if !ecoDir.IsDir() || !known[ecoDir.Name()] {
			return nil, false, "", fmt.Errorf("inputs/ 下只允许生态目录 %s，发现 %s", strings.Join(depsEcosystems, "、"), ecoDir.Name())
		}
		digests, err := os.ReadDir(filepath.Join(root, "inputs", ecoDir.Name()))
		if err != nil {
			return nil, false, "", err
		}
		for _, d := range digests {
			if !d.IsDir() || !hex64.MatchString(d.Name()) {
				return nil, false, "", fmt.Errorf("inputs/%s/ 下只允许以锁摘要命名的目录，发现 %s", ecoDir.Name(), d.Name())
			}
			if err := verifyInputEntry(filepath.Join(root, "inputs", ecoDir.Name(), d.Name()), ecoDir.Name(), d.Name()); err != nil {
				return nil, false, "", err
			}
			entries = append(entries, importEntry{Rel: "inputs/" + ecoDir.Name() + "/" + d.Name(), Ecosystem: ecoDir.Name(), LockDigest: d.Name()})
		}
	}
	roleDirs, _ := os.ReadDir(filepath.Join(root, "images"))
	for _, roleDir := range roleDirs {
		if !roleDir.IsDir() || !depsImageRole.MatchString(roleDir.Name()) {
			return nil, false, "", fmt.Errorf("images/ 下只允许 tool-*/base-*/service-* 用途目录，发现 %s", roleDir.Name())
		}
		hexDirs, err := os.ReadDir(filepath.Join(root, "images", roleDir.Name()))
		if err != nil {
			return nil, false, "", err
		}
		for _, h := range hexDirs {
			if !h.IsDir() || !hex64.MatchString(h.Name()) {
				return nil, false, "", fmt.Errorf("images/%s/ 下只允许以 digest 命名的目录，发现 %s", roleDir.Name(), h.Name())
			}
			files, err := os.ReadDir(filepath.Join(root, "images", roleDir.Name(), h.Name()))
			if err != nil {
				return nil, false, "", err
			}
			for _, f := range files {
				if f.Name() != "image.json" && f.Name() != "image.tar" && f.Name() != depsOriginFile {
					return nil, false, "", fmt.Errorf("镜像记录目录只允许 image.json、image.tar，发现 images/%s/%s/%s", roleDir.Name(), h.Name(), f.Name())
				}
			}
		}
		if _, err := readImageRecords(root, roleDir.Name()); err != nil {
			return nil, false, "", err
		}
		for _, h := range hexDirs {
			entries = append(entries, importEntry{Rel: "images/" + roleDir.Name() + "/" + h.Name(), Image: true})
		}
	}
	if len(entries) == 0 {
		return nil, false, "", errors.New("导入内容里没有任何 inputs/<生态>/<锁摘要>/ 或 images/<用途>/<digest>/ 条目")
	}
	return entries, signed, fp, nil
}

type depsImportOptions struct {
	Fingerprint string
	TrustState  bool
	// Source 返回当前源码目录；只有导入发行开发套件时才需要（按当前锁摘要选取输入）。
	Source func() (string, error)
	Out    io.Writer
}

func (o depsImportOptions) trust() siteKit {
	return siteKit{TrustFingerprint: o.Fingerprint, TrustState: o.TrustState}
}

// checkImportedJavaEntries 对导入的 java 条目：锁摘要须等于当前源码的 java 锁摘要（于是条目准备时
// 用的 services/maven-deps.sha256 就是当前这份），repository/ 须与锁文件逐个一致；否则拒绝。
// 缓存里已有的同名条目不会被导入（下面的循环会跳过），这里也不核对。
func checkImportedJavaEntries(cache string, entries []importEntry, root string, o depsImportOptions) error {
	var source, current string
	for _, e := range entries {
		if e.Image || e.Ecosystem != "java" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(cache, filepath.FromSlash(e.Rel))); err == nil {
			continue
		}
		if source == "" {
			if o.Source == nil {
				return fmt.Errorf("导入 Java 依赖需要当前源码以核对 %s", mavenDepsLockRel)
			}
			src, err := o.Source()
			if err != nil {
				return fmt.Errorf("导入 Java 依赖需要当前源码以核对 %s：%w", mavenDepsLockRel, err)
			}
			locks, err := sourceEcosystemLocks(src)
			if err != nil {
				return err
			}
			for _, l := range locks {
				if l.Ecosystem == "java" {
					current = l.Digest
				}
			}
			source = src
		}
		if e.LockDigest != current {
			return fmt.Errorf("%s：条目的锁摘要与当前源码的 Java 锁摘要 %s 不同，无法按 %s 核对，拒绝导入；请在对应源码版本下导入", e.Rel, current, mavenDepsLockRel)
		}
		if err := verifyImportedJavaAgainstLock(source, filepath.Join(root, filepath.FromSlash(e.Rel))); err != nil {
			return fmt.Errorf("%s：%w", e.Rel, err)
		}
	}
	return nil
}

// depsImport 校验目录或归档后按锁摘要导入缓存，记录来源。先复制/解包到缓存内的
// 暂存目录再校验，避免校验后源目录被改动。
func depsImport(ctx context.Context, cfg *siteConfig, src string, o depsImportOptions) error {
	w := o.Out
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if err := noSymlinks(abs); err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	// 导入源同套件：root 所有、组和其他用户不可写（目录或归档文件）。
	if err := depsTrustedSourceCheck("导入源 ", abs); err != nil {
		return fmt.Errorf("%w%s", err, depsTrustedSourceHint)
	}
	if !info.IsDir() {
		if err := checkImportParts(abs); err != nil {
			return fmt.Errorf("%w%s", err, depsTrustedSourceHint)
		}
	}
	if _, err := ensureDepsCache(cfg.Deps.Cache, true); err != nil {
		return err
	}
	if abs == cfg.Deps.Cache || strings.HasPrefix(abs, cfg.Deps.Cache+string(filepath.Separator)) {
		return errors.New("不能从缓存目录自身导入")
	}
	unlock, err := depsCacheLock(cfg.Deps.Cache)
	if err != nil {
		return err
	}
	defer unlock()
	staging, err := os.MkdirTemp(cfg.Deps.Cache, ".import-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	root := filepath.Join(staging, "kit")
	from := abs
	if info.IsDir() && isDevkitLayout(abs) {
		// 发行开发套件可能很大：不整体复制，只把签名清单与套件清单复制到暂存目录核验，
		// 所需数据边读边算摘要（见 importDevkit）。
		return importDevkit(ctx, cfg, abs, abs, staging, o)
	}
	if info.IsDir() {
		if err := copyTreeContext(ctx, abs, root); err != nil {
			return fmt.Errorf("复制导入目录失败（只接受普通文件和目录）：%w", err)
		}
	} else {
		if err := os.Mkdir(root, 0755); err != nil {
			return err
		}
		sum, err := unpackHashing(ctx, abs, root)
		if err != nil {
			return fmt.Errorf("解包导入归档失败：%w", err)
		}
		from = abs + " (sha256 " + sum + ")"
		// 允许归档只有一层顶级目录。
		if _, err := os.Lstat(filepath.Join(root, "SHA256SUMS")); os.IsNotExist(err) {
			if top, _ := os.ReadDir(root); len(top) == 1 && top[0].IsDir() {
				root = filepath.Join(root, top[0].Name())
			}
		}
	}
	if isDevkitLayout(root) {
		return importDevkit(ctx, cfg, root, from, staging, o)
	}
	entries, signed, fp, err := verifyImportLayout(ctx, cfg, root, o.trust())
	if err != nil {
		return err
	}
	if signed {
		fmt.Fprintf(w, "签名已核验（公钥指纹 SHA256:%s）\n", fp)
	} else {
		fmt.Fprintln(w, "警告：未用 --trust-fingerprint 或 --trust-state 核验签名，按人工导入处理；请人工审核下列条目（SHA-256 记录在各条目 auriga-origin.json）。")
	}
	// Java 条目先全部按源码锁文件核对，再开始写缓存：任何一条不符都整体拒绝，不留半截导入。
	if err := checkImportedJavaEntries(cfg.Deps.Cache, entries, root, o); err != nil {
		return err
	}
	imported, skipped := 0, 0
	for _, e := range entries {
		target := filepath.Join(cfg.Deps.Cache, filepath.FromSlash(e.Rel))
		srcDir := filepath.Join(root, filepath.FromSlash(e.Rel))
		sumsFile := "SHA256SUMS"
		if e.Image {
			sumsFile = "image.json"
		}
		if _, err := os.Lstat(target); err == nil {
			same := false
			if a, err1 := os.ReadFile(filepath.Join(target, sumsFile)); err1 == nil {
				if b, err2 := os.ReadFile(filepath.Join(srcDir, sumsFile)); err2 == nil {
					same = bytes.Equal(a, b)
				}
			}
			if same {
				fmt.Fprintf(w, "  = %s 已在缓存中，跳过\n", e.Rel)
			} else {
				fmt.Fprintf(w, "  ! %s 缓存中已有不同内容的同名条目，保留原条目；如需替换请先人工移走\n", e.Rel)
			}
			skipped++
			continue
		}
		// 先在暂存目录写来源记录再改名进缓存，中途崩溃不会留下没有来源记录的条目。
		origin := depsOrigin{Kind: "import", Ecosystem: e.Ecosystem, LockDigest: e.LockDigest, From: []string{from}, Signed: signed, Fingerprint: fp}
		if err := writeDepsOriginWith(srcDir, sumsFile, origin); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.Rename(srcDir, target); err != nil {
			return err
		}
		sum, _ := digest(filepath.Join(target, sumsFile))
		fmt.Fprintf(w, "  + %s（%s sha256 %s）\n", e.Rel, sumsFile, sum)
		imported++
	}
	fmt.Fprintf(w, "导入完成：新增 %d 个条目，跳过 %d 个；来源 %s\n", imported, skipped, from)
	return nil
}

// checkImportParts：分卷导入时，分卷清单与清单列出的每个分卷都必须 root 所有、组和其他
// 用户不可写（第 1 步审查 N1）；不是分卷归档时什么也不做。
func checkImportParts(archive string) error {
	index := archiveIndex(archive)
	if index == "" {
		return nil
	}
	if err := noSymlinks(index); err != nil {
		return err
	}
	if err := depsTrustedSourceCheck("分卷清单 ", index); err != nil {
		return err
	}
	var manifest partsManifest
	if err := readJSON(index, &manifest); err != nil {
		return fmt.Errorf("分卷清单无效：%w", err)
	}
	for _, p := range manifest.Parts {
		if !safeRelative(p.Name) || filepath.Base(p.Name) != p.Name {
			return fmt.Errorf("分卷名无效 %q", p.Name)
		}
		if err := depsTrustedSourceCheck("分卷 ", filepath.Join(filepath.Dir(index), p.Name)); err != nil {
			return err
		}
	}
	return nil
}

// unpackHashing 解包归档到 root，同时对解包读到的同一份字节算 SHA-256（来源记录里的
// 摘要就是被解包的内容）。分卷归档按原规则解包，记录分卷清单文件的摘要。
func unpackHashing(ctx context.Context, archive, root string) (string, error) {
	if archiveIndex(archive) != "" {
		sum, err := digestContext(ctx, archive)
		if err != nil {
			return "", err
		}
		return sum, unpackContext(ctx, archive, root)
	}
	if err := noSymlinks(archive); err != nil {
		return "", err
	}
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	h := sha256.New()
	in := io.TeeReader(cancellationReader(ctx, f), h)
	if err := unpackReader(in, root); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	if _, err := io.Copy(io.Discard, in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// importDevkit 从发行开发套件导入：用独立信任核验签名（禁止未签名或自证），按当前源码
// 各生态的锁摘要在套件各版本里查找输入，解出、核验后写入 inputs/<生态>/<锁摘要>/；
// 再导入最新版本的镜像（images/<用途>/<digest>/，带归档与镜像 ID）。kitRoot 是套件
// 数据所在目录，staging 是缓存内的暂存目录。
func importDevkit(ctx context.Context, cfg *siteConfig, kitRoot, from, staging string, o depsImportOptions) error {
	w := o.Out
	trust := o.trust()
	if trust.TrustFingerprint == "" && !trust.TrustState {
		return errors.New("导入发行开发套件必须用 --trust-fingerprint（独立核对的发行公钥指纹）或 --trust-state（state 钉定的发行钥）核验签名；不接受未签名导入或套件自带公钥自证")
	}
	if o.Source == nil {
		return errors.New("导入发行开发套件需要当前源码来确定各生态锁摘要")
	}
	source, err := o.Source()
	if err != nil {
		return fmt.Errorf("导入发行开发套件需要当前源码来确定各生态锁摘要：%w", err)
	}
	locks, err := sourceEcosystemLocks(source)
	if err != nil {
		return err
	}
	if err := noSymlinks(kitRoot); err != nil {
		return err
	}
	// 公钥、签名清单与套件清单都只读一次到内存，核验与解析用同一份字节（见
	// verifyKitTrust、openDevkitLayout），数据文件边读边算摘要或先复制到暂存目录再核对。
	trustLabel, fp, sums, err := verifyKitTrust(cfg, kitRoot, trust, "--trust-fingerprint")
	if err != nil {
		return fmt.Errorf("发行开发套件签名核验失败：%w", err)
	}
	d, err := openDevkitLayout(kitRoot, sums)
	if err != nil {
		return fmt.Errorf("发行开发套件无效：%w", err)
	}
	fmt.Fprintf(w, "签名已核验（%s）；%s\n", trustLabel, d.describe())
	imported, skipped := 0, 0
	var missing []string
	for _, lock := range locks {
		target := filepath.Join(cfg.Deps.Cache, "inputs", lock.Ecosystem, lock.Digest)
		rel := "inputs/" + lock.Ecosystem + "/" + lock.Digest
		if _, err := os.Lstat(target); err == nil {
			fmt.Fprintf(w, "  = %s 已在缓存中，跳过\n", rel)
			skipped++
			continue
		}
		release, ok := d.findInput(lock.Ecosystem, lock.Digest)
		if !ok {
			missing = append(missing, lock.Ecosystem)
			fmt.Fprintf(w, "  - %s 套件各版本都没有该锁摘要\n", rel)
			continue
		}
		out := filepath.Join(staging, "input-"+lock.Ecosystem)
		if err := os.Mkdir(out, 0755); err != nil {
			return err
		}
		if err := d.extractInput(release, lock, out); err != nil {
			return fmt.Errorf("%s 输入（版本 %s）：%w", lock.Ecosystem, release.Version, err)
		}
		if lock.Ecosystem == "java" {
			if err := verifyImportedJavaAgainstLock(source, out); err != nil {
				return fmt.Errorf("%s：%w", rel, err)
			}
		}
		origin := depsOrigin{Kind: "import", Ecosystem: lock.Ecosystem, LockDigest: lock.Digest, From: []string{from + " 版本 " + release.Version + " " + release.InputsRel}, Signed: true, Fingerprint: fp}
		if err := writeDepsOriginWith(out, "SHA256SUMS", origin); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.Rename(out, target); err != nil {
			return err
		}
		sum, _ := digest(filepath.Join(target, "SHA256SUMS"))
		fmt.Fprintf(w, "  + %s（版本 %s，SHA256SUMS sha256 %s）\n", rel, release.Version, sum)
		imported++
	}
	images := 0
	for _, role := range depsImageRoles {
		for _, hit := range d.images(role) {
			rel := "images/" + role + "/" + hit.Image.digestHex()
			target := filepath.Join(cfg.Deps.Cache, filepath.FromSlash(rel))
			if _, err := os.Lstat(target); err == nil {
				fmt.Fprintf(w, "  = %s 已在缓存中，跳过\n", rel)
				skipped++
				continue
			}
			out := filepath.Join(staging, "image-"+role)
			if err := os.Mkdir(out, 0755); err != nil {
				return err
			}
			staged := filepath.Join(out, "image.tar")
			if err := copyFileContext(ctx, hit.Tar, staged, 0644); err != nil {
				return err
			}
			// 在暂存副本上核对摘要并算镜像 ID，避免核验后归档被替换。
			if got, err := digest(staged); err != nil || got != sums[hit.rel] {
				return fmt.Errorf("套件镜像归档摘要与签名清单不符 %s", hit.rel)
			}
			id, err := dockerSaveImageID(staged)
			if err != nil {
				return fmt.Errorf("套件镜像 %s: %w", role, err)
			}
			img := hit.Image
			img.ID = id
			if err := validateDepsImage(img, role, img.digestHex(), true); err != nil {
				return err
			}
			if err := jsonFile(filepath.Join(out, "image.json"), img, 0644); err != nil {
				return err
			}
			origin := depsOrigin{Kind: "import", From: []string{from + " 版本 " + d.Latest.Version + " " + hit.rel}, Signed: true, Fingerprint: fp}
			if err := writeDepsOriginWith(out, "image.json", origin); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Rename(out, target); err != nil {
				return err
			}
			fmt.Fprintf(w, "  + %s（%s，镜像 ID %s）\n", rel, img.Ref, id)
			images++
		}
	}
	fmt.Fprintf(w, "导入完成：新增 %d 个依赖输入、%d 个镜像，跳过 %d 个；来源 %s\n", imported, images, skipped, from)
	if len(missing) > 0 {
		fmt.Fprintf(w, "套件未覆盖当前源码的生态：%s（运行 auriga deps status 查看补法）\n", strings.Join(missing, "、"))
	}
	if imported == 0 && images == 0 && skipped == 0 {
		return errors.New("发行开发套件不含当前源码任何生态的锁摘要，未导入任何内容")
	}
	return nil
}

// writeDepsOriginWith 写条目来源记录；sumsFile 是计算来源摘要的文件（输入条目用
// SHA256SUMS，镜像记录用 image.json）。条目原有的来源记录保留在 previous。
func writeDepsOriginWith(dir, sumsFile string, origin depsOrigin) error {
	if prev, err := os.ReadFile(filepath.Join(dir, depsOriginFile)); err == nil && json.Valid(prev) {
		origin.Previous = json.RawMessage(bytes.TrimSpace(prev))
	}
	sum, err := digest(filepath.Join(dir, sumsFile))
	if err != nil {
		return err
	}
	origin.Schema, origin.SumsSHA256 = 1, sum
	if origin.At == "" {
		origin.At = nowRFC3339()
	}
	if origin.From == nil {
		origin.From = []string{}
	}
	path := filepath.Join(dir, depsOriginFile)
	_ = os.Remove(path)
	return jsonFile(path, origin, 0644)
}

// resolveDepsSource：--source 只允许 root 直接运行；否则用配置的 source.dir（root 所有）。
func resolveDepsSource(flagSource string, cfg *siteConfig, inv siteInvocation) (string, error) {
	if flagSource != "" {
		if inv.Sudo {
			return "", errors.New("经 sudo 调用时不接受 --source")
		}
		if !inv.Root {
			return "", errors.New("--source 只允许 root 直接运行时使用")
		}
		abs, err := filepath.Abs(flagSource)
		if err != nil {
			return "", err
		}
		if err := noSymlinks(abs); err != nil {
			return "", err
		}
		return abs, nil
	}
	if cfg.Source != nil && cfg.Source.Dir != nil {
		dir := *cfg.Source.Dir
		if err := checkRootOwnedDir("源码目录 source.dir ", dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	return "", errors.New("没有可用源码：请配置 source.dir（root 所有的源码目录），或由 root 直接运行时加 --source <目录>；从 source.git 远端取源码在第 2a 步实现")
}

func depsSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func depsCommand(args []string) error {
	return depsCommandWith(args, currentInvocation(), os.Stdout)
}

func depsCommandWith(args []string, inv siteInvocation, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：auriga deps status|fetch|import|baseline；运行 auriga deps --help 查看说明")
	}
	switch args[0] {
	case "status":
		f := flags("deps status")
		config := f.String("config", "", "")
		source := f.String("source", "", "")
		asJSON := f.Bool("json", false, "")
		client := f.Bool("client", false, "")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("多余 deps status 参数")
		}
		cfg, path, err := loadSiteConfig(*config, inv)
		if err != nil {
			return err
		}
		if *client {
			src, err := resolveClientSource(*source, cfg, inv)
			if err != nil {
				return err
			}
			ctx, cancel := depsSignalContext()
			defer cancel()
			return runClientDeps(ctx, cfg, src, clientDepsRequest{Action: "status", JSON: *asJSON}, w)
		}
		src, err := resolveDepsSource(*source, cfg, inv)
		if err != nil {
			return err
		}
		rep, _, _, err := computeDepsStatus(cfg, path, src)
		if err != nil {
			return err
		}
		if *asJSON {
			raw, err := json.MarshalIndent(rep, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(w, string(raw))
			return err
		}
		printDepsStatus(w, rep)
		return nil
	case "fetch":
		f := flags("deps fetch")
		config := f.String("config", "", "")
		source := f.String("source", "", "")
		accept := f.Bool("accept-new-deps", false, "")
		memory := f.Int("memory-mib", 4096, "")
		cpus := f.Int("cpus", 2, "")
		client := f.Bool("client", false, "")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("多余 deps fetch 参数")
		}
		cfg, path, err := loadSiteConfig(*config, inv)
		if err != nil {
			return err
		}
		if *client {
			src, err := resolveClientSource(*source, cfg, inv)
			if err != nil {
				return err
			}
			ctx, cancel := depsSignalContext()
			defer cancel()
			return runClientDeps(ctx, cfg, src, clientDepsRequest{Action: "fetch", Accept: *accept}, w)
		}
		src, err := resolveDepsSource(*source, cfg, inv)
		if err != nil {
			return err
		}
		ctx, cancel := depsSignalContext()
		defer cancel()
		return depsFetch(ctx, cfg, path, src, depsFetchOptions{Accept: *accept, Budget: buildBudget{MemoryMiB: *memory, CPUs: *cpus}, Out: w})
	case "import":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("deps import 需要 <目录或归档> 路径，放在参数之前")
		}
		f := flags("deps import")
		config := f.String("config", "", "")
		fp := f.String("trust-fingerprint", "", "")
		trustState := f.Bool("trust-state", false, "")
		source := f.String("source", "", "")
		client := f.Bool("client", false, "")
		if err := f.Parse(args[2:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("多余 deps import 参数")
		}
		if *fp != "" && *trustState {
			return errors.New("--trust-fingerprint 与 --trust-state 只能选一个")
		}
		cfg, _, err := loadSiteConfig(*config, inv)
		if err != nil {
			return err
		}
		ctx, cancel := depsSignalContext()
		defer cancel()
		if *client {
			src, err := resolveClientSource(*source, cfg, inv)
			if err != nil {
				return err
			}
			return runClientDeps(ctx, cfg, src, clientDepsRequest{Action: "import", From: args[1], Fingerprint: *fp, TrustState: *trustState}, w)
		}
		src := func() (string, error) { return resolveDepsSource(*source, cfg, inv) }
		return depsImport(ctx, cfg, args[1], depsImportOptions{Fingerprint: *fp, TrustState: *trustState, Source: src, Out: w})
	case "baseline":
		f := flags("deps baseline")
		config := f.String("config", "", "")
		source := f.String("source", "", "")
		asJSON := f.Bool("json", false, "")
		client := f.Bool("client", false, "")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("多余 deps baseline 参数")
		}
		if !*client {
			return errors.New("deps baseline 仅支持 --client（登记 Windows 系统工具现场基线）")
		}
		cfg, _, err := loadSiteConfig(*config, inv)
		if err != nil {
			return err
		}
		src, err := resolveClientSource(*source, cfg, inv)
		if err != nil {
			return err
		}
		ctx, cancel := depsSignalContext()
		defer cancel()
		return runClientDeps(ctx, cfg, src, clientDepsRequest{Action: "baseline", JSON: *asJSON}, w)
	}
	return fmt.Errorf("未知 deps 子命令 %s；可用 status、fetch、import、baseline", args[0])
}
