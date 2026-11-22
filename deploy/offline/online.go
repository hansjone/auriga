package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// bundle --build --online：套件仍提供 NAS 基础包、服务镜像、固定 digest 的工具镜像
// 和运行基镜像；只有源码依赖锁相对套件发生变化的语言生态（go/web/java）才联网重新
// 运行 <kind>-inputs.sh prepare，随后沿用 buildRelease 的断网构建路径。

var onlineEcosystems = []string{"go", "web", "java"}

const (
	onlineScriptDir = "/source/deploy/nas/offline-build/"
	onlineCacheDir  = "/prepare-cache"
)

type depEntry struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	// File/SHA256：Go 模块 zip、Java jar（无 jar 时 pom）的文件名与 SHA-256；
	// Integrity：Web 取自 pnpm-lock.yaml 的 tarball integrity。供人工核对。
	File      string `json:"file,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Integrity string `json:"integrity,omitempty"`
	path      string
}

// siteReport 写入安装包根目录 site-dependencies.json（被 SHA256SUMS 覆盖并签名），
// 既是人工审核清单，也是“含现场联网收集依赖”的唯一标记；不写入 offline-release.json
// 或 provenance.json，以免旧版 CLI 严格解码时拒绝升级包。
type siteReport struct {
	Schema           int        `json:"schemaVersion"`
	Online           bool       `json:"online"`
	LockDigest       string     `json:"lockDigest"`
	Ecosystems       []string   `json:"ecosystems"`
	ChangedLockFiles []string   `json:"changedLockFiles"`
	Added            []depEntry `json:"added"`
	Removed          []depEntry `json:"removed"`
	Warnings         []string   `json:"warnings,omitempty"`
	Notice           string     `json:"notice"`
}

const siteReportNotice = "Go 依赖由 go.sum/GOSUMDB 钉定，Web 依赖由 pnpm-lock integrity 钉定；Java 依赖由 services/maven-deps.sha256 钉定；Python（operator）由 requirements 的 --hash=sha256 钉定。"

// 可注入点：测试用假执行器替换 docker、镜像检查和环境读取。
var (
	runOnlineDocker = func(ctx context.Context, env []string, args ...string) error {
		return commandContext(ctx, nil, os.Stdout, env, "docker", args...)
	}
	checkOnlineTool = func(ctx context.Context, tool string) error { return prepareBuildImageContext(ctx, tool, true) }
	lookupOnlineEnv = os.LookupEnv
)

// Maven 镜像地址：只允许 https，字符白名单（不含换行、引号、尖括号、&、空白）。
var mavenMirrorURL = regexp.MustCompile(`^https://[A-Za-z0-9._~:/?%+=,-]+$`)

func validateMavenMirrorURL(s string) error {
	if !mavenMirrorURL.MatchString(s) {
		return errors.New("AURIGA_MAVEN_MIRROR_URL 必须是 https:// 地址，且只含字母数字和 ._~:/?%+=,- 字符")
	}
	return nil
}

// 本机回环代理在 bridge 网络容器里不可达。
func proxyWarnings(lookup func(string) (string, bool)) []string {
	var out []string
	for _, name := range proxyEnvNames {
		if v, ok := lookup(name); ok && (strings.Contains(v, "127.0.0.1") || strings.Contains(v, "localhost") || strings.Contains(v, "[::1]")) {
			out = append(out, name+" 指向本机回环地址，bridge 网络的构建容器无法访问；请改用宿主机可达地址")
		}
	}
	return out
}

func validateOnlineFlags(online, offline, build bool, sign string) error {
	if !online {
		return nil
	}
	if offline {
		return errors.New("--online 与 --offline 互斥：--online 会联网收集变化的语言依赖，--offline 完全不联网")
	}
	if !build {
		return errors.New("--online 须与 --build 同用")
	}
	if sign == "" {
		return errors.New("--online 构建必须 --sign-key <已登记站点构建私钥>；先运行 keygen site 并在服务器 trust add-site-key")
	}
	// 镜像地址在载入任何镜像、启动任何容器之前就校验，错了立即失败。
	if value, ok := lookupOnlineEnv("AURIGA_MAVEN_MIRROR_URL"); ok && value != "" {
		if err := validateMavenMirrorURL(value); err != nil {
			return err
		}
	}
	return nil
}

// 系统包与基础镜像不属于一期：Dockerfile*、system-inputs 使用的 admin-bootstrap-requirements.txt。
func unsupportedOnlineFile(name string) bool {
	base := path.Base(filepath.ToSlash(name))
	return name == "@build-inputs" || strings.HasPrefix(base, "Dockerfile") || base == "admin-bootstrap-requirements.txt"
}

func changedLockFiles(expected, actual lockInventory) []string {
	seen := map[string]bool{}
	for name, hash := range expected.Files {
		if actual.Files[name] != hash {
			seen[name] = true
		}
	}
	for name := range actual.Files {
		if _, ok := expected.Files[name]; !ok {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// checkOnlineScope 拒绝一期不支持的变化（系统包、Dockerfile、基础镜像固定项）。
func checkOnlineScope(expected, actual lockInventory) error {
	var bad []string
	for _, name := range changedLockFiles(expected, actual) {
		if unsupportedOnlineFile(name) {
			bad = append(bad, "  "+name)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("--online 一期只支持 Go/Web/Java 语言依赖变化，不支持系统包与 Dockerfile 基础镜像变化，请联系维护方出新套件。涉及:\n%s", strings.Join(bad, "\n"))
	}
	return nil
}

// ecosystemLocksChanged 比较套件输入里的 source-locks.sha256 与当前源码由同一脚本
// （locks 模式）算出的清单；逐字节相同才算未变化。
func ecosystemLocksChanged(kitLocks, currentLocks string) (bool, error) {
	old, err := os.ReadFile(kitLocks)
	if err != nil {
		return false, fmt.Errorf("套件输入缺少锁清单 %s: %w", filepath.Base(filepath.Dir(kitLocks)), err)
	}
	current, err := os.ReadFile(currentLocks)
	if err != nil {
		return false, err
	}
	if len(current) == 0 {
		return false, errors.New("当前源码锁清单为空")
	}
	return !bytes.Equal(old, current), nil
}

func onlineToolArgs(budget buildBudget) []string {
	return append([]string{"run"}, append(budget.dockerArgs(), "--rm", "--pull", "never", "--platform", "linux/amd64")...)
}

func mountArg(src, dst string, readonly bool) []string {
	value := "type=bind,src=" + src + ",dst=" + dst
	if readonly {
		value += ",readonly"
	}
	return []string{"--mount", value}
}

// locksDockerArgs：无网络、只读源码，只输出 source-locks.sha256。
func locksDockerArgs(kind, tool, source, out string, budget buildBudget) []string {
	args := onlineToolArgs(budget)
	args = append(args, "--network", "none")
	args = append(args, mountArg(source, "/source", true)...)
	args = append(args, mountArg(out, "/output", false)...)
	return append(args, tool, "sh", onlineScriptDir+kind+"-inputs.sh", "locks", "/output")
}

type prepareRun struct {
	Kind, Tool string
	Source     string // 宿主源码快照，只读挂载到 /source
	Out        string // 全新空目录，挂载到 /output
	Cache      string // 旧套件输入（或其可写副本），挂载到 /prepare-cache；空则不用缓存
	CacheRW    bool   // 仅 Java 需要可写（脚本向缓存写 maven/npm 内容）
	Budget     buildBudget
	// Extra 插在工具镜像之前的附加 docker 参数（deps fetch 用于只读挂载 caFile、
	// file:// 依赖源，以及 allowPublic=false 时屏蔽公网 Go 代理与校验库）。
	Extra []string
}

var proxyEnvNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// prepareDockerArgs 构造联网 prepare 的 docker 参数。lookup 通常是 os.LookupEnv。
// 返回的 env 要附加到 docker 进程环境（同名变量用 --env NAME 继承，值不出现在
// 命令行里）。绝不设置 GOSUMDB/GOFLAGS/GOINSECURE 等会放宽校验的变量。
func prepareDockerArgs(run prepareRun, lookup func(string) (string, bool)) (args, env []string) {
	args = onlineToolArgs(run.Budget)
	args = append(args, mountArg(run.Source, "/source", true)...)
	args = append(args, mountArg(run.Out, "/output", false)...)
	if run.Cache != "" {
		args = append(args, mountArg(run.Cache, onlineCacheDir, !run.CacheRW)...)
	}
	pass := func(host, container string) {
		if value, ok := lookup(host); ok && value != "" {
			args = append(args, "--env", container)
			env = append(env, container+"="+value)
		}
	}
	for _, name := range proxyEnvNames {
		pass(name, name)
	}
	switch run.Kind {
	case "go":
		pass("GOPROXY", "AURIGA_GO_PREPARE_PROXY")
		if run.Cache != "" {
			args = append(args, "--env", "AURIGA_GO_PREPARE_CACHE="+onlineCacheDir)
		}
	case "web":
		pass("npm_config_registry", "npm_config_registry")
		pass("COREPACK_NPM_REGISTRY", "COREPACK_NPM_REGISTRY")
		if run.Cache != "" {
			args = append(args, "--env", "AURIGA_WEB_PREPARE_STORE_TAR="+onlineCacheDir+"/web-cache.tar.gz")
		}
	case "java":
		pass("AURIGA_MAVEN_MIRROR_URL", "AURIGA_MAVEN_MIRROR_URL")
		if run.Cache != "" {
			args = append(args, "--env", "AURIGA_JAVA_PREP_CACHE_DIR="+onlineCacheDir)
		}
	case "python":
		pass("PIP_INDEX_URL", "PIP_INDEX_URL")
		pass("AURIGA_PIP_INDEX_URL", "AURIGA_PIP_INDEX_URL")
	}
	args = append(args, run.Extra...)
	args = append(args, run.Tool, "sh", onlineScriptDir+run.Kind+"-inputs.sh", "prepare", "/output")
	return args, env
}

// prepareCache 返回各生态用旧输入作缓存的宿主目录。Java 需要把旧输入摆成脚本期望
// 的布局（repository、npm-cache/_cacache、maven/wrapper/dists），并必须是可写副本。
func prepareCache(ctx context.Context, kind, oldInputs, scratch string) (dir string, writable bool, err error) {
	switch kind {
	case "go":
		return filepath.Join(oldInputs, "download"), false, nil
	case "web":
		return oldInputs, false, nil
	case "java":
		cache := filepath.Join(scratch, "java-cache")
		for _, d := range []string{"npm-cache", "maven/wrapper"} {
			if err := os.MkdirAll(filepath.Join(cache, filepath.FromSlash(d)), 0755); err != nil {
				return "", false, err
			}
		}
		for from, to := range map[string]string{"repository": "repository", "npm-cacache": "npm-cache/_cacache", "wrapper/dists": "maven/wrapper/dists"} {
			if err := copyTreeContext(ctx, filepath.Join(oldInputs, filepath.FromSlash(from)), filepath.Join(cache, filepath.FromSlash(to))); err != nil {
				return "", false, err
			}
		}
		return cache, true, nil
	case "python":
		return "", false, nil // 无种子缓存；每次按 requirements 全量下载
	}
	return "", false, fmt.Errorf("未知生态 %s", kind)
}

// collectOnlineInputs 先逐生态用 locks 模式判定变化，再对变化的生态联网 prepare 并
// 替换 stage/inputs/<kind>。任一步失败都返回错误，已失败生态的旧输入保持不变；
// scratch 目录总会清理。
func collectOnlineInputs(ctx context.Context, stage string, r release, options buildOptions) (*siteReport, error) {
	source := filepath.Join(stage, "source")
	inputs := filepath.Join(stage, "inputs")
	scratch := filepath.Join(stage, "online")
	if value, ok := lookupOnlineEnv("AURIGA_MAVEN_MIRROR_URL"); ok && value != "" {
		if err := validateMavenMirrorURL(value); err != nil {
			return nil, err
		}
	}
	if err := os.Mkdir(scratch, 0755); err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	var changed []string
	for _, kind := range onlineEcosystems {
		tool := r.Build.Tools[kind]
		if !immutable.MatchString(tool) {
			return nil, errors.New("构建工具镜像必须固定 digest")
		}
		if err := checkOnlineTool(ctx, tool); err != nil {
			return nil, err
		}
		out := filepath.Join(scratch, "locks-"+kind)
		if err := os.Mkdir(out, 0755); err != nil {
			return nil, err
		}
		if err := runOnlineDocker(ctx, nil, locksDockerArgs(kind, tool, source, out, options.Budget)...); err != nil {
			return nil, err
		}
		diff, err := ecosystemLocksChanged(filepath.Join(inputs, kind, "source-locks.sha256"), filepath.Join(out, "source-locks.sha256"))
		if err != nil {
			return nil, err
		}
		if diff {
			changed = append(changed, kind)
		}
	}
	if len(changed) == 0 {
		fmt.Println("在线构建：Go/Web/Java 依赖锁与套件一致，沿用套件输入。")
		return nil, nil
	}
	report := &siteReport{Schema: 1, Online: true, Ecosystems: changed, Added: []depEntry{}, Removed: []depEntry{}, Notice: siteReportNotice}
	report.Warnings = append(report.Warnings, proxyWarnings(lookupOnlineEnv)...)
	for _, kind := range changed {
		fmt.Printf("在线构建：%s 依赖锁已变化，联网收集依赖（以套件输入为缓存）…\n", kind)
		old := filepath.Join(inputs, kind)
		var oldDeps map[string]depEntry
		webBaselineKnown := true
		if kind == "web" {
			var err error
			oldDeps, err = baselineWebPackages(ctx, options)
			if err != nil {
				webBaselineKnown = false
				report.Warnings = append(report.Warnings, "无法取得套件基线的 web/pnpm-lock.yaml（"+err.Error()+"），未能列出新增 Web 包，请人工比对锁文件")
			}
		} else {
			var err error
			if oldDeps, err = listInputDeps(ctx, kind, old); err != nil {
				return nil, err
			}
		}
		cache, writable, err := prepareCache(ctx, kind, old, scratch)
		if err != nil {
			return nil, err
		}
		out := filepath.Join(scratch, "new-"+kind)
		if err := os.Mkdir(out, 0755); err != nil {
			return nil, err
		}
		args, env := prepareDockerArgs(prepareRun{Kind: kind, Tool: r.Build.Tools[kind], Source: source, Out: out, Cache: cache, CacheRW: writable, Budget: options.Budget}, lookupOnlineEnv)
		if err := runOnlineDocker(ctx, env, args...); err != nil {
			return nil, fmt.Errorf("%s 依赖收集失败（检查网络、代理或镜像源；Maven 只校验镜像自己提供的校验和，不能防镜像篡改）: %w", kind, err)
		}
		var newDeps map[string]depEntry
		if kind == "web" {
			raw, err := os.ReadFile(filepath.Join(source, "web", "pnpm-lock.yaml"))
			if err != nil {
				return nil, err
			}
			newDeps = pnpmLockPackages(raw)
		} else if newDeps, err = listInputDeps(ctx, kind, out); err != nil {
			return nil, err
		}
		if kind == "web" && !webBaselineKnown {
			oldDeps = newDeps
		}
		added, removed := diffDeps(oldDeps, newDeps)
		if kind == "web" && webBaselineKnown && len(added) == 0 {
			report.Warnings = append(report.Warnings, "Web 依赖锁已变化，但从 pnpm-lock.yaml 解析出的新增包为 0 项（可能只是删除或版本回退，也可能解析失败），请人工核对锁文件差异")
		}
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
		report.Added = append(report.Added, added...)
		report.Removed = append(report.Removed, removed...)
		if err := os.RemoveAll(old); err != nil {
			return nil, err
		}
		if err := os.Rename(out, old); err != nil {
			return nil, err
		}
		if writable { // 仅删除 scratch 里的 Java 缓存副本；Go/Web 的缓存就是旧输入目录本身
			_ = os.RemoveAll(cache)
		}
	}
	printSiteReport(os.Stdout, report)
	return report, nil
}

// baselineWebPackages 从套件基线提交取旧 web/pnpm-lock.yaml（归档源码没有 git 时返回错误）。
func baselineWebPackages(ctx context.Context, options buildOptions) (map[string]depEntry, error) {
	if options.BaselineCommit == "" || options.Source == "" {
		return nil, errors.New("没有基线提交")
	}
	raw, err := gitFilesContext(ctx, options.Source, "show", options.BaselineCommit+":web/pnpm-lock.yaml")
	if err != nil {
		return nil, err
	}
	return pnpmLockPackages([]byte(raw)), nil
}

func listInputDeps(ctx context.Context, kind, dir string) (map[string]depEntry, error) {
	switch kind {
	case "go":
		return listGoModules(ctx, dir)
	case "java":
		return listMavenArtifacts(ctx, filepath.Join(dir, "repository"))
	}
	return nil, fmt.Errorf("未知生态 %s", kind)
}

func depKey(e depEntry) string { return e.Ecosystem + "\x00" + e.Name + "\x00" + e.Version }

func diffDeps(old, current map[string]depEntry) (added, removed []depEntry) {
	for key, e := range current {
		if _, ok := old[key]; !ok {
			added = append(added, e)
		}
	}
	for key, e := range old {
		if _, ok := current[key]; !ok {
			removed = append(removed, e)
		}
	}
	sortDeps(added)
	sortDeps(removed)
	return added, removed
}

func sortDeps(list []depEntry) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version < b.Version
	})
}

func printSiteReport(w io.Writer, report *siteReport) {
	_, _ = fmt.Fprintf(w, "现场联网收集依赖：%s；新增 %d 项，不再需要 %d 项（Java 缓存为超集，不统计移除）。请人工审核新增依赖（同样写入安装包 site-dependencies.json，含 SHA-256）：\n", strings.Join(report.Ecosystems, "/"), len(report.Added), len(report.Removed))
	for _, e := range report.Added {
		extra := e.SHA256
		if extra == "" {
			extra = e.Integrity
		}
		_, _ = fmt.Fprintf(w, "  + [%s] %s %s %s\n", e.Ecosystem, e.Name, e.Version, extra)
	}
	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(w, "  警告：%s\n", warning)
	}
	_, _ = fmt.Fprintln(w, "  注意："+report.Notice)
}

// goUnescape 还原 Go 模块缓存路径里的大写转义（!x -> X）。
func goUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '!' && i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z' {
			b.WriteByte(s[i+1] - 'a' + 'A')
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// listGoModules：<dir>/download/<module>/@v/<version>.zip。
func listGoModules(ctx context.Context, dir string) (map[string]depEntry, error) {
	root := filepath.Join(dir, "download")
	out := map[string]depEntry{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".zip") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		module, file, ok := strings.Cut(filepath.ToSlash(rel), "/@v/")
		if !ok {
			return nil
		}
		e := depEntry{Ecosystem: "go", Name: goUnescape(module), Version: strings.TrimSuffix(file, ".zip"), path: p}
		out[depKey(e)] = e
		return nil
	})
	return out, err
}

// pnpm 11 的 store 把索引放在 SQLite，无法从文件名列出包，所以改读 pnpm-lock.yaml
// 的顶层 packages: 段：键 `name@version`（可带引号），其后 resolution 行含 integrity。
var pnpmIntegrity = regexp.MustCompile(`integrity:\s*([^,}\s]+)`)

func pnpmLockPackages(raw []byte) map[string]depEntry {
	out := map[string]depEntry{}
	inPackages := false
	var current *depEntry
	flush := func() {
		if current != nil {
			out[depKey(*current)] = *current
			current = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' {
			flush()
			inPackages = strings.TrimRight(line, " ") == "packages:"
			continue
		}
		if !inPackages {
			continue
		}
		if strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] != ' ' && strings.HasSuffix(strings.TrimRight(line, " "), ":") {
			flush()
			key := strings.Trim(strings.TrimSuffix(strings.TrimSpace(line), ":"), "'\"")
			if i := strings.Index(key, "("); i > 0 {
				key = key[:i]
			}
			at := strings.LastIndex(key, "@")
			if at <= 0 {
				continue
			}
			current = &depEntry{Ecosystem: "web", Name: key[:at], Version: key[at+1:]}
			continue
		}
		if current != nil && strings.Contains(line, "resolution:") {
			if m := pnpmIntegrity.FindStringSubmatch(line); m != nil {
				current.Integrity = m[1]
			}
		}
	}
	flush()
	return out
}

// listMavenArtifacts：repository/<group 路径>/<artifact>/<version>/ 下有 .pom 或 .jar；
// 同一构件优先记录 jar。
func listMavenArtifacts(ctx context.Context, repo string) (map[string]depEntry, error) {
	out := map[string]depEntry{}
	err := filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if d.IsDir() || !(strings.HasSuffix(d.Name(), ".pom") || strings.HasSuffix(d.Name(), ".jar")) {
			return nil
		}
		rel, err := filepath.Rel(repo, filepath.Dir(p))
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 3 {
			return nil
		}
		version, artifact := parts[len(parts)-1], parts[len(parts)-2]
		if !strings.HasPrefix(d.Name(), artifact+"-"+version) {
			return nil
		}
		e := depEntry{Ecosystem: "java", Name: strings.Join(parts[:len(parts)-2], ".") + ":" + artifact, Version: version, path: p}
		if prior, ok := out[depKey(e)]; ok && strings.HasSuffix(prior.path, ".jar") && !strings.HasSuffix(p, ".jar") {
			return nil
		}
		out[depKey(e)] = e
		return nil
	})
	return out, err
}

// siteDependenciesInfo 供 doctor 使用：读取已安装包目录里的 site-dependencies.json；
// 没有该文件（旧包或未联网收集）时返回空串。
func siteDependenciesInfo(packageDir string) string {
	var report struct {
		Online     bool     `json:"online"`
		Ecosystems []string `json:"ecosystems"`
		LockDigest string   `json:"lockDigest"`
	}
	if err := readJSONLoose(filepath.Join(packageDir, "site-dependencies.json"), &report); err != nil || !report.Online {
		return ""
	}
	return fmt.Sprintf("INFO 含现场联网收集的依赖（生态 %s，依赖锁摘要 %s），未经维护方套件审核；清单见安装包 site-dependencies.json", strings.Join(report.Ecosystems, "/"), report.LockDigest)
}
