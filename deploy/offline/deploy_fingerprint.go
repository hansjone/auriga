package main

// 输入指纹与按产物构建（设计 D6 / 第 2c 步）。
//
// 指纹由配方声明的输入集展开：Go 用 go list -deps；Web/Java 读共享 pack list；
// 再加依赖锁摘要、工具镜像、装配源文件等。指纹与当前发行一致则跳过该产物构建，
// 从上一版发行目录 artifacts/ 复用；--full 全量重建；--services 会补上共享输入变化的服务。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// goDepsRunner 可注入：在断网工具容器里跑源码配方 go-inputs.sh deps（go list -deps，
// GOFLAGS=-mod=readonly、GOPROXY 只指向已校验的 Go 依赖输入、GOOS/GOARCH 与构建一致），
// 返回 程序 → 编译到的包目录（容器内绝对路径，源码挂在 goDepsSourceMount）。
// 程序与包的对应关系只在 go-inputs.sh 的 record_run 表里维护一处。
var goDepsRunner = runGoDeps

const goDepsSourceMount = "/source"

func runGoDeps(ctx context.Context, tool, source, inputs, scratch string, budget buildBudget) (map[string][]string, error) {
	out, err := os.MkdirTemp(scratch, "go-deps-")
	if err != nil {
		return nil, err
	}
	args := append([]string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64"}, budget.dockerArgs()...)
	args = append(args,
		"--mount", "type=bind,src="+source+",dst="+goDepsSourceMount+",readonly",
		"--mount", "type=bind,src="+inputs+",dst=/inputs,readonly",
		"--mount", "type=bind,src="+out+",dst=/output",
		"--env", "AURIGA_GO_PROXY_DIR=/inputs", tool, "sh", goDepsSourceMount+"/deploy/nas/offline-build/go-inputs.sh", "deps", "/output")
	if err := deployRun(ctx, runSpec{Name: "docker", Args: args}); err != nil {
		return nil, fmt.Errorf("go list -deps（断网工具容器）失败：%w", err)
	}
	raw, err := smallFile(filepath.Join(out, "deps.txt"), 64<<20)
	if err != nil {
		return nil, fmt.Errorf("go-inputs.sh deps 没有写出 deps.txt：%w", err)
	}
	return parseGoDeps(raw)
}

// parseGoDeps 解析 “程序 包目录” 行。
func parseGoDeps(raw []byte) (map[string][]string, error) {
	out := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		prog, dir, ok := strings.Cut(line, " ")
		if !ok || prog == "" || !strings.HasPrefix(dir, "/") {
			return nil, fmt.Errorf("go list -deps 输出行无效：%q", line)
		}
		out[prog] = append(out[prog], dir)
	}
	if len(out) == 0 {
		return nil, errors.New("go list -deps 没有输出任何程序")
	}
	return out, nil
}

type fingerprintContext struct {
	Source   string
	Services *siteServices
	Recipe   *deployRecipe
	// GoDeps 是 go-inputs.sh deps 的结果；nil 表示未展开（--dry-run），Go 产物一律按“将重建”计。
	GoDeps map[string][]string
	Locks  map[string]string // ecosystem → lock digest
	Tools  map[string]string // role → image ref (含 digest)
}

type artifactFingerprint struct {
	Name       string            `json:"name"`
	Digest     string            `json:"digest"`
	InputSets  []string          `json:"inputSets"`
	SetDigests map[string]string `json:"setDigests"`
}

type fingerprintReport struct {
	Artifacts map[string]artifactFingerprint `json:"artifacts"`
	Computed  string                         `json:"computedAt"`
}

// computeAllFingerprints 计算全部产物指纹与各输入集摘要。
func computeAllFingerprints(ctx context.Context, fc fingerprintContext) (*fingerprintReport, error) {
	setCache := map[string]string{}
	report := &fingerprintReport{Artifacts: map[string]artifactFingerprint{}, Computed: time.Now().UTC().Format(time.RFC3339)}
	names := make([]string, 0, len(fc.Services.Artifacts))
	for name := range fc.Services.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fp, err := fingerprintArtifact(ctx, fc, name, setCache)
		if err != nil {
			return nil, err
		}
		report.Artifacts[name] = fp
	}
	return report, nil
}

func fingerprintArtifact(ctx context.Context, fc fingerprintContext, name string, setCache map[string]string) (artifactFingerprint, error) {
	sets := fc.Services.artifactInputSets(name)
	setDigests := map[string]string{}
	h := sha256.New()
	fmt.Fprintf(h, "artifact %s\n", name)
	for _, set := range sets {
		d, err := inputSetDigest(ctx, fc, set, setCache)
		if err != nil {
			return artifactFingerprint{}, fmt.Errorf("产物 %s 输入集 %s：%w", name, set, err)
		}
		setDigests[set] = d
		fmt.Fprintf(h, "set %s %s\n", set, d)
	}
	return artifactFingerprint{
		Name:       name,
		Digest:     hex.EncodeToString(h.Sum(nil)),
		InputSets:  sets,
		SetDigests: setDigests,
	}, nil
}

func inputSetDigest(ctx context.Context, fc fingerprintContext, name string, cache map[string]string) (string, error) {
	if d, ok := cache[name]; ok {
		return d, nil
	}
	is, ok := fc.Services.InputSets[name]
	if !ok {
		return "", fmt.Errorf("未知输入集")
	}
	var d string
	var err error
	switch is.Kind {
	case "go-program":
		d, err = digestGoProgram(ctx, fc, is.Program)
	case "go-mod-files":
		d, err = digestGoModFiles(ctx, fc.Source)
	case "pack-list":
		d, err = digestPackList(ctx, fc.Source, is.File)
	case "deps-lock":
		d = fc.Locks[is.Ecosystem]
		if d == "" {
			d = "missing"
		}
		d = sha256Hex([]byte("deps-lock " + is.Ecosystem + " " + d + "\n"))
	case "tool-image":
		ref := fc.Tools[is.Role]
		if ref == "" && fc.Recipe != nil {
			ref = fc.Recipe.Images[is.Role]
		}
		if ref == "" {
			err = fmt.Errorf("配方没有声明工具镜像 %s", is.Role)
			break
		}
		// 构建脚本本身（构建参数、打包方式）也是该生态产物的输入；deploy/nas/** 整体在 ignore 里，
		// 不在这里计入就会出现“改了构建脚本却跳过构建”。
		script := "deploy/nas/offline-build/" + strings.TrimPrefix(is.Role, "tool-") + "-inputs.sh"
		var sum string
		sum, err = digestContext(ctx, filepath.Join(fc.Source, filepath.FromSlash(script)))
		if err != nil {
			err = fmt.Errorf("构建脚本 %s：%w", script, err)
			break
		}
		d = sha256Hex([]byte("tool-image " + is.Role + " " + ref + "\nscript " + script + " " + sum + "\n"))
	case "assemble":
		d, err = digestAssembleInputs(ctx, fc, is.Service)
	default:
		err = fmt.Errorf("未知 kind %s", is.Kind)
	}
	if err != nil {
		return "", err
	}
	cache[name] = d
	return d, nil
}

func digestGoProgram(ctx context.Context, fc fingerprintContext, program string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "go-program %s\n", program)
	if fc.GoDeps == nil {
		// --dry-run 不跑工具容器：给一个不可能与真实指纹相同的值，计划里显示为“将重建”。
		fmt.Fprintln(h, "unexpanded")
		return "unexpanded-" + hex.EncodeToString(h.Sum(nil)), nil
	}
	dirs, ok := fc.GoDeps[program]
	if !ok || len(dirs) == 0 {
		return "", fmt.Errorf("go list -deps 没有返回程序 %s（services.json 与 go-inputs.sh 的程序表不一致）", program)
	}
	seen := map[string]bool{}
	var paths []string
	for _, dir := range dirs {
		dir = path.Clean(dir)
		var rel string
		switch {
		case dir == goDepsSourceMount:
			rel = "."
		case strings.HasPrefix(dir, goDepsSourceMount+"/"):
			rel = strings.TrimPrefix(dir, goDepsSourceMount+"/")
			if !safeRelative(rel) {
				return "", fmt.Errorf("go list -deps 返回了异常目录 %s", dir)
			}
		default:
			// GOROOT 与模块缓存里的包不进源码指纹：由 deps-go（锁摘要）、tool-go（工具镜像 digest）覆盖。
			continue
		}
		if !seen[rel] {
			seen[rel] = true
			paths = append(paths, rel)
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("程序 %s 没有源码树内的包", program)
	}
	sort.Strings(paths)
	for _, p := range paths {
		// 包目录整棵树进指纹（含子目录与嵌入文件；嵌入文件只能在包目录之下）。
		sum, err := hashPathUnder(ctx, fc.Source, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %s\n", p, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestGoModFiles(ctx context.Context, source string) (string, error) {
	h := sha256.New()
	fmt.Fprintln(h, "go-mod-files")
	var names []string
	err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "target" || base == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	for _, name := range names {
		sum, err := digestContext(ctx, filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %s\n", name, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestPackList(ctx context.Context, source, rel string) (string, error) {
	entries, err := readPackList(source, rel)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "pack-list %s\n", rel)
	var files []string
	for _, e := range entries {
		path := filepath.Join(source, filepath.FromSlash(e))
		st, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if st.IsDir() {
			names, err := regularFilesContext(ctx, path)
			if err != nil {
				return "", err
			}
			for _, n := range names {
				// 打包脚本排除 node_modules/dist/target
				if strings.Contains("/"+n+"/", "/node_modules/") || strings.Contains("/"+n+"/", "/dist/") || strings.Contains("/"+n+"/", "/target/") {
					continue
				}
				files = append(files, filepath.ToSlash(filepath.Join(e, n)))
			}
		} else if st.Mode().IsRegular() {
			files = append(files, e)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sum, err := digestContext(ctx, filepath.Join(source, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %s\n", f, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestAssembleInputs(ctx context.Context, fc fingerprintContext, svc string) (string, error) {
	if fc.Recipe == nil {
		return "", fmt.Errorf("装配输入需要配方")
	}
	s, ok := fc.Recipe.Services[svc]
	if !ok {
		return "", fmt.Errorf("配方无服务 %s", svc)
	}
	h := sha256.New()
	fmt.Fprintf(h, "assemble %s\n", svc)
	stage, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(h, "stage %s\n", sha256Hex(stage))
	for _, it := range s.Stage {
		if it.Kind != "source" {
			continue
		}
		names := make([]string, 0, len(it.Sources))
		for name := range it.Sources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rel := it.Sources[name]
			sum, err := digestContext(ctx, filepath.Join(fc.Source, filepath.FromSlash(rel)))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "source %s %s %s\n", name, rel, sum)
		}
	}
	// 系统输入：指纹侧用「需要 system」标记 + 锁摘要占位；实际字节进装配摘要。
	needSystem := false
	for _, it := range s.Stage {
		if it.Kind == "system" {
			needSystem = true
			break
		}
	}
	fmt.Fprintf(h, "system %t lock %s\n", needSystem, fc.Locks["system"])
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashPathUnder(ctx context.Context, source, rel string) (string, error) {
	path := filepath.Join(source, filepath.FromSlash(rel))
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		sum, err := directorySnapshotSHA256(ctx, path)
		if err != nil {
			return "", err
		}
		return "dir:" + sum, nil
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("非普通文件 %s", rel)
	}
	return digestContext(ctx, path)
}

// expandSelectedServices：手工 --services 时，补上与已选服务共享输入集且指纹已变的服务。
func expandSelectedServices(spec *siteServices, selected []string, report *fingerprintReport, prev map[string]string) (chosen []string, added []string) {
	set := map[string]bool{}
	for _, s := range selected {
		set[s] = true
	}
	artifactChanged := func(a string) bool {
		cur := report.Artifacts[a].Digest
		return prev[a] == "" || prev[a] != cur
	}
	imageChanged := func(img string) bool {
		for _, a := range spec.Images[img].Artifacts {
			if artifactChanged(a) {
				return true
			}
		}
		return false
	}
	changed := true
	for changed {
		changed = false
		selectedSets := map[string]bool{}
		for img := range set {
			for s := range spec.imageInputSets(img) {
				selectedSets[s] = true
			}
		}
		for _, img := range deployServices {
			if set[img] || !imageChanged(img) {
				continue
			}
			share := false
			for s := range spec.imageInputSets(img) {
				if selectedSets[s] {
					share = true
					break
				}
			}
			if share {
				set[img] = true
				added = append(added, img)
				changed = true
			}
		}
	}
	for _, img := range deployServices {
		if set[img] {
			chosen = append(chosen, img)
		}
	}
	sort.Strings(added)
	return chosen, added
}

// artifactBuildKind：产物属于哪类工具构建（go/web/java），空表示无工具构建。
func artifactBuildKind(name string) string {
	switch name {
	case "web":
		return "web"
	case "platform.jar":
		return "java"
	case "operator-runtime":
		return ""
	default:
		// Go 程序名
		return "go"
	}
}

// kindsToBuild 根据需要重建的产物决定跑哪些 *-inputs.sh。
func kindsToBuild(spec *siteServices, report *fingerprintReport, prev map[string]string, full bool, images []string) map[string]bool {
	need := map[string]bool{}
	consider := map[string]bool{}
	for _, img := range images {
		for _, a := range spec.Images[img].Artifacts {
			consider[a] = true
		}
	}
	// 伪服务始终随 go 构建进发行目录。
	consider["auriga-nas-bootstrap"] = true
	consider["runtime-release"] = true
	for a := range consider {
		kind := artifactBuildKind(a)
		if kind == "" {
			continue
		}
		if full || prev[a] == "" || prev[a] != report.Artifacts[a].Digest {
			need[kind] = true
		}
	}
	return need
}

// artifactsNeedingBuild 列出需要重建的产物。
func artifactsNeedingBuild(report *fingerprintReport, prev map[string]string, full bool, names []string) []string {
	var out []string
	for _, a := range names {
		if full || prev[a] == "" || prev[a] != report.Artifacts[a].Digest {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func fingerprintMap(report *fingerprintReport) map[string]string {
	m := map[string]string{}
	for name, a := range report.Artifacts {
		m[name] = a.Digest
	}
	return m
}

func writeFingerprintReport(path string, report *fingerprintReport) error {
	return jsonFile(path, report, 0644)
}

// reusableArtifacts 列出本次要用、且指纹与上一版记录一致（可从上一版 artifacts/ 复用）的产物。
func reusableArtifacts(report *fingerprintReport, prev map[string]string, images []string, spec *siteServices) []string {
	needed := map[string]bool{"auriga-nas-bootstrap": true, "runtime-release": true}
	for _, img := range images {
		for _, a := range spec.Images[img].Artifacts {
			needed[a] = true
		}
	}
	var out []string
	for a := range needed {
		if artifactBuildKind(a) == "" || prev[a] == "" || prev[a] != report.Artifacts[a].Digest {
			continue
		}
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// reuseArtifactsFromRelease 把上一版发行目录中指纹未变的产物拷到 out（out/<生态> 尚不存在或为空）。
// 复用前按回滚同样的规则核对上一版发行目录：路径与属主（checkReleaseDir：发行根 root 所有、
// 组和其他用户不可写）、SHA256SUMS 与 state 锚定的 releaseSums 一致、逐文件摘要相符且没有
// 符号链接；再核对各生态目录自己的 SHA256SUMS。任何一步不过都返回错误，由调用方改为重建。
func reuseArtifactsFromRelease(ctx context.Context, data string, prevRec *deployRecord, out string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	artRoot := filepath.Join(prevRec.Release, "artifacts")
	if st, err := os.Lstat(artRoot); err != nil || !st.IsDir() {
		return fmt.Errorf("上一版发行 r%d 没有 artifacts/，无法按指纹复用", prevRec.Seq)
	}
	if err := checkReleaseDir(data, prevRec); err != nil {
		return err
	}
	checked := map[string]bool{}
	for _, a := range names {
		kind := artifactBuildKind(a)
		dir := filepath.Join(artRoot, kind)
		if !checked[kind] {
			if err := checkArtifactSums(dir); err != nil {
				return fmt.Errorf("上一版 artifacts/%s：%w", kind, err)
			}
			checked[kind] = true
		}
		switch kind {
		case "go":
			if err := copyFileContext(ctx, filepath.Join(dir, a), filepath.Join(out, "go", a), 0755); err != nil {
				return fmt.Errorf("复用 Go 产物 %s：%w", a, err)
			}
		case "web":
			target := filepath.Join(out, "web")
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if err := copyTreeContext(ctx, dir, target); err != nil {
				return fmt.Errorf("复用 web 产物：%w", err)
			}
		case "java":
			if err := copyFileContext(ctx, filepath.Join(dir, "platform.jar"), filepath.Join(out, "java", "platform.jar"), 0644); err != nil {
				return fmt.Errorf("复用 java 产物：%w", err)
			}
		}
	}
	return nil
}

// artifactPresent：产物是否在 out（本次构建或复用）里。
func artifactPresent(out, name string) bool {
	switch artifactBuildKind(name) {
	case "go":
		st, err := os.Lstat(filepath.Join(out, "go", name))
		return err == nil && st.Mode().IsRegular()
	case "web":
		return dirHasFiles(filepath.Join(out, "web"))
	case "java":
		st, err := os.Lstat(filepath.Join(out, "java", "platform.jar"))
		return err == nil && st.Mode().IsRegular()
	default:
		return true // operator-runtime 没有工具构建，指纹只描述装配输入
	}
}

// recordedFingerprints 只保留本次发行 artifacts/ 里真有的产物的指纹：--services 没选中、
// 也没被顺带构建的产物不记录，下次部署时视为“上一版没有”而重建，不会拿缺失的产物去复用。
func recordedFingerprints(report *fingerprintReport, out string) *fingerprintReport {
	kept := &fingerprintReport{Artifacts: map[string]artifactFingerprint{}, Computed: report.Computed}
	for name, a := range report.Artifacts {
		if artifactPresent(out, name) {
			kept.Artifacts[name] = a
		}
	}
	return kept
}

func dirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

func rewriteArtifactSums(dir string) error {
	names, err := regularFiles(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, n := range names {
		if n == "SHA256SUMS" {
			continue
		}
		files = append(files, n)
	}
	if len(files) == 0 {
		return fmt.Errorf("产物目录 %s 为空", dir)
	}
	f, err := os.OpenFile(filepath.Join(dir, "SHA256SUMS"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, name := range files {
		sum, err := digest(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(f, "%s  %s\n", sum, name); err != nil {
			return err
		}
	}
	return nil
}
