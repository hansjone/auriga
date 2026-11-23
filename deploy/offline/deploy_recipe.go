package main

// deploy 的配方与装配（设计 D4）。
//
// deploy 一律使用源码快照里的配方 deploy/nas/offline-build（不再用套件配方覆盖）。配方
// 只提供 Dockerfile.assembly 和数据文件 deploy-recipe.json（暂存清单、镜像固定 digest、
// 能力版本），不在宿主机执行源码里的装配脚本：CLI 按暂存清单自己暂存产物，直接调用
// docker build --network=none --pull=false，构建参数由 CLI 固定。assemble-image.sh 只留给
// bundle 路径，行为不变。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// deployCLICapability 是本 CLI 的 deploy 能力版本；源码配方声明的 cliCapability 必须相等，
// 否则提示先升级 CLI（deploy 不替换自己，设计 D10）。
const deployCLICapability = 2

const (
	deployRecipeRel   = "deploy/nas/offline-build/deploy-recipe.json"
	deployDockerfile  = "deploy/nas/offline-build/Dockerfile.assembly"
	deployUpstreamRel = "deploy/nas/offline-build/upstream-recipes.sha256"
)

// deployServices 是五个装配镜像；镜像键（stack.env 的 AURIGA_IMAGE_<键>）由 CLI 固定。
var deployServices = []string{"platform", "edge", "agent", "bifrost", "operator"}

// deployExtraServices 是 compose 需要、但不由本机构建的镜像。
var deployExtraServices = []string{"postgres", "nats", "registry"}

func imageKey(svc string) string { return strings.ToUpper(svc) }

type deployRecipe struct {
	Schema        int                      `json:"schemaVersion"`
	CLICapability int                      `json:"cliCapability"`
	Images        map[string]string        `json:"images"`
	Services      map[string]recipeService `json:"services"`
	dockerfile    []byte
}

type recipeService struct {
	Stage []stageItem `json:"stage"`
}

type stageItem struct {
	Kind             string            `json:"kind"` // go | web | java | system | python | source | dirs
	Files            []string          `json:"files,omitempty"`
	Dirs             []string          `json:"dirs,omitempty"`
	Sources          map[string]string `json:"sources,omitempty"`
	CheckSourceLocks bool              `json:"checkSourceLocks,omitempty"`
}

var artifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

func recipeImageRoles() []string {
	roles := []string{"tool-go", "tool-web", "tool-java"}
	for _, svc := range deployServices {
		roles = append(roles, "base-"+svc)
	}
	for _, svc := range deployExtraServices {
		roles = append(roles, "service-"+svc)
	}
	return roles
}

// loadDeployRecipe 读取源码快照里的配方：先只看能力版本（新版配方可能有本 CLI 不认识的
// 字段），再严格解析与校验，并检查 Dockerfile.assembly 与上游配方摘要。
func loadDeployRecipe(source string) (*deployRecipe, error) {
	raw, err := smallFile(filepath.Join(source, filepath.FromSlash(deployRecipeRel)), 1<<20)
	if err != nil {
		return nil, fmt.Errorf("源码快照没有 deploy 配方 %s，这个提交不支持 deploy（需要包含统一现场部署第 2a 步的源码）：%w", deployRecipeRel, err)
	}
	var head struct {
		CLICapability int `json:"cliCapability"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("deploy 配方无效：%w", err)
	}
	if head.CLICapability != deployCLICapability {
		return nil, fmt.Errorf("源码配方需要 CLI 能力版本 %d，本 CLI 是 %d：请先升级 /usr/local/bin/auriga（随签名包更新，或由 root 用受信提交手动构建安装）；deploy 不会替换自身", head.CLICapability, deployCLICapability)
	}
	var r deployRecipe
	if err := decodeStrictJSON(raw, &r); err != nil {
		return nil, fmt.Errorf("deploy 配方无效：%w", err)
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	r.dockerfile, err = smallFile(filepath.Join(source, filepath.FromSlash(deployDockerfile)), 1<<20)
	if err != nil {
		return nil, err
	}
	if err := checkDockerfile(r.dockerfile); err != nil {
		return nil, err
	}
	if err := checkUpstreamRecipes(source); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *deployRecipe) validate() error {
	if r.Schema != 1 {
		return fmt.Errorf("deploy 配方 schemaVersion 必须是 1（当前 %d）", r.Schema)
	}
	roles := recipeImageRoles()
	if len(r.Images) != len(roles) {
		return fmt.Errorf("deploy 配方 images 必须恰好声明 %s", strings.Join(roles, "、"))
	}
	for _, role := range roles {
		ref := r.Images[role]
		if !immutable.MatchString(ref) || !strings.Contains(ref, "@sha256:") {
			return fmt.Errorf("deploy 配方镜像 %s 必须是 <仓库>@sha256:<digest> 形式的固定 digest（当前 %q）", role, ref)
		}
	}
	if len(r.Services) != len(deployServices) {
		return fmt.Errorf("deploy 配方 services 必须恰好是 %s", strings.Join(deployServices, "、"))
	}
	for _, svc := range deployServices {
		s, ok := r.Services[svc]
		if !ok || len(s.Stage) == 0 {
			return fmt.Errorf("deploy 配方缺少服务 %s 的暂存清单", svc)
		}
		for i, item := range s.Stage {
			if err := item.validate(); err != nil {
				return fmt.Errorf("deploy 配方 %s.stage[%d]：%w", svc, i, err)
			}
		}
	}
	return nil
}

func (it stageItem) validate() error {
	names := func(list []string, what string) error {
		if len(list) == 0 {
			return fmt.Errorf("%s 不能为空", what)
		}
		for _, n := range list {
			if !artifactName.MatchString(n) {
				return fmt.Errorf("%s 含无效名字 %q", what, n)
			}
		}
		return nil
	}
	switch it.Kind {
	case "go", "java":
		if len(it.Dirs) > 0 || len(it.Sources) > 0 || it.CheckSourceLocks {
			return errors.New(it.Kind + " 只接受 files")
		}
		return names(it.Files, "files")
	case "web":
		if len(it.Files) > 0 || len(it.Sources) > 0 || it.CheckSourceLocks {
			return errors.New("web 只接受 dirs")
		}
		return names(it.Dirs, "dirs")
	case "system", "python":
		if len(it.Files) > 0 || len(it.Dirs) > 0 || len(it.Sources) > 0 {
			return errors.New(it.Kind + " 只接受 checkSourceLocks")
		}
		return nil
	case "source":
		if len(it.Files) > 0 || len(it.Dirs) > 0 || it.CheckSourceLocks || len(it.Sources) == 0 {
			return errors.New("source 只接受非空 sources（暂存名 → 源码相对路径）")
		}
		for name, path := range it.Sources {
			if !artifactName.MatchString(name) || !safeRelative(path) {
				return fmt.Errorf("source 条目无效 %q → %q", name, path)
			}
		}
		return nil
	case "dirs":
		if len(it.Files) > 0 || len(it.Sources) > 0 || it.CheckSourceLocks || len(it.Dirs) == 0 {
			return errors.New("dirs 只接受非空 dirs")
		}
		for _, d := range it.Dirs {
			if !safeRelative(d) {
				return fmt.Errorf("dirs 含不安全路径 %q", d)
			}
		}
		return nil
	}
	return fmt.Errorf("未知暂存种类 %q", it.Kind)
}

// checkUpstreamRecipes 与 assemble-image.sh 一致：上游 Dockerfile 摘要必须与配方登记相符。
func checkUpstreamRecipes(source string) error {
	raw, err := smallFile(filepath.Join(source, filepath.FromSlash(deployUpstreamRel)), 1<<20)
	if err != nil {
		return err
	}
	return verifySumsAgainst(raw, source, "upstream-recipes.sha256")
}

// verifySumsAgainst 按 sha256sum 格式的清单核对 root 下的文件。
func verifySumsAgainst(raw []byte, root, what string) error {
	sums, order, err := parseSums(raw)
	if err != nil {
		return fmt.Errorf("%s：%w", what, err)
	}
	for _, name := range order {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := noSymlinks(path); err != nil {
			return err
		}
		got, err := digest(path)
		if err != nil {
			return fmt.Errorf("%s 列出的文件 %s 无法读取：%w", what, name, err)
		}
		if got != sums[name] {
			return fmt.Errorf("%s 校验失败：%s 摘要不符", what, name)
		}
	}
	return nil
}

// checkArtifactSums 核对构建产物目录的 SHA256SUMS（*-inputs.sh build 写出）。
func checkArtifactSums(dir string) error {
	raw, err := smallFile(filepath.Join(dir, "SHA256SUMS"), 64<<20)
	if err != nil {
		return fmt.Errorf("产物目录 %s 缺少 SHA256SUMS：%w", dir, err)
	}
	return verifySumsAgainst(raw, dir, filepath.Base(dir)+"/SHA256SUMS")
}

// recipeNeeds 返回装配这些服务需要的构建（go、web、java）与依赖输入生态（system、python）。
func (r *deployRecipe) needs(services []string) (builds map[string]bool, needInputs map[string]bool) {
	builds = map[string]bool{"go": true} // go 总要构建：bootstrap 与 runtime-release 进发行目录
	needInputs = map[string]bool{}
	for _, svc := range services {
		for _, it := range r.Services[svc].Stage {
			switch it.Kind {
			case "go", "web", "java":
				builds[it.Kind] = true
			case "system", "python":
				needInputs[it.Kind] = true
			}
		}
	}
	return builds, needInputs
}

// stageService 按暂存清单把产物、系统/Python 输入与源码文件放进 scratch（装配上下文）。
// artifacts 是 go/web/java 构建产物根目录；inputDir 含 system（按服务子目录）与 python（顶层 packages）。
func stageService(ctx context.Context, svc string, s recipeService, source, artifacts string, inputDir map[string]string, scratch string) error {
	checked := map[string]bool{}
	check := func(dir string) error {
		if checked[dir] {
			return nil
		}
		checked[dir] = true
		return checkArtifactSums(dir)
	}
	copyNamed := func(from, to string) error {
		if err := noSymlinks(from); err != nil {
			return err
		}
		info, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("暂存源必须是普通文件 %s", from)
		}
		return copyFileContext(ctx, from, to, normalizedFileMode(info.Mode()))
	}
	for _, it := range s.Stage {
		switch it.Kind {
		case "go", "java":
			dir := filepath.Join(artifacts, it.Kind)
			if err := check(dir); err != nil {
				return err
			}
			for _, f := range it.Files {
				if err := copyNamed(filepath.Join(dir, f), filepath.Join(scratch, it.Kind, f)); err != nil {
					return err
				}
			}
		case "web":
			dir := filepath.Join(artifacts, "web")
			if err := check(dir); err != nil {
				return err
			}
			if err := mkdirAllExact(filepath.Join(scratch, "web"), 0755); err != nil {
				return err
			}
			for _, d := range it.Dirs {
				if err := copyTreeContext(ctx, filepath.Join(dir, d), filepath.Join(scratch, "web", d)); err != nil {
					return err
				}
			}
		case "system":
			dir := filepath.Join(inputDir["system"], svc)
			if err := check(dir); err != nil {
				return err
			}
			if err := copyTreeContext(ctx, dir, filepath.Join(scratch, "system")); err != nil {
				return err
			}
			if it.CheckSourceLocks {
				raw, err := smallFile(filepath.Join(scratch, "system", "source-locks.sha256"), 1<<20)
				if err != nil {
					return fmt.Errorf("%s 系统输入缺少 source-locks.sha256：%w", svc, err)
				}
				if err := verifySumsAgainst(raw, source, svc+" 系统输入的 source-locks.sha256"); err != nil {
					return err
				}
			}
		case "python":
			dir := inputDir["python"]
			if dir == "" {
				return fmt.Errorf("%s 需要 python 依赖输入", svc)
			}
			if err := check(dir); err != nil {
				return err
			}
			// Dockerfile.assembly 的 operator 段挂载 source=system；布局与 system/operator 相同。
			if err := copyTreeContext(ctx, dir, filepath.Join(scratch, "system")); err != nil {
				return err
			}
			if it.CheckSourceLocks {
				raw, err := smallFile(filepath.Join(scratch, "system", "source-locks.sha256"), 1<<20)
				if err != nil {
					return fmt.Errorf("%s python 输入缺少 source-locks.sha256：%w", svc, err)
				}
				if err := verifySumsAgainst(raw, source, svc+" python 输入的 source-locks.sha256"); err != nil {
					return err
				}
			}
		case "source":
			names := make([]string, 0, len(it.Sources))
			for name := range it.Sources {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if err := copyNamed(filepath.Join(source, filepath.FromSlash(it.Sources[name])), filepath.Join(scratch, name)); err != nil {
					return err
				}
			}
		case "dirs":
			for _, d := range it.Dirs {
				if err := mkdirAllExact(filepath.Join(scratch, filepath.FromSlash(d)), 0755); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// assemblyParts 是装配摘要的分项（设计 D6）：任一项变化即切换，收据写明触发项。
type assemblyParts struct {
	Total            string `json:"total"`
	ArtifactsTree    string `json:"artifactsTree"`
	BaseImage        string `json:"baseImage"`
	SystemInputs     string `json:"systemInputs"`
	DockerfileTarget string `json:"dockerfileTarget"`
	StageManifest    string `json:"stageManifest"`
}

// assemblyDigest 计算装配摘要与分项。
func assemblyDigest(ctx context.Context, svc string, r *deployRecipe, scratch string) (assemblyParts, error) {
	var zero assemblyParts
	tree, err := directorySnapshotSHA256(ctx, scratch)
	if err != nil {
		return zero, err
	}
	stage, err := json.Marshal(r.Services[svc])
	if err != nil {
		return zero, err
	}
	sysSum := "none"
	if st, err := os.Lstat(filepath.Join(scratch, "system")); err == nil && st.IsDir() {
		sysSum, err = directorySnapshotSHA256(ctx, filepath.Join(scratch, "system"))
		if err != nil {
			return zero, err
		}
	}
	targetSum := dockerfileTargetDigest(r.dockerfile, svc)
	parts := assemblyParts{
		ArtifactsTree:    tree,
		BaseImage:        r.Images["base-"+svc],
		SystemInputs:     sysSum,
		DockerfileTarget: targetSum,
		StageManifest:    sha256Hex(stage),
	}
	h := sha256.New()
	fmt.Fprintf(h, "service %s\nartifacts %s\nbase %s\nsystem %s\ndockerfile-target %s\nstage %s\n",
		svc, parts.ArtifactsTree, parts.BaseImage, parts.SystemInputs, parts.DockerfileTarget, parts.StageManifest)
	parts.Total = hex.EncodeToString(h.Sum(nil))
	return parts, nil
}

// dockerfileTargetDigest 是 Dockerfile.assembly 中与该服务有关部分的摘要：第一个 FROM 之前的
// 全局段（ARG 默认值）+ “FROM … AS <svc>” 到下一个 FROM 之前的目标段。目标段若引用了其它阶段
// （FROM <阶段>、COPY/RUN --from 或 --mount from=），无法只看本段，退回整个文件的摘要（宁可多切换）。
// 找不到目标段同样用整个文件。
func dockerfileTargetDigest(dockerfile []byte, svc string) string {
	lines := strings.Split(string(dockerfile), "\n")
	isFrom := func(line string) bool {
		f := strings.Fields(line)
		return len(f) > 0 && strings.EqualFold(f[0], "FROM")
	}
	first, start := -1, -1
	for i, line := range lines {
		if !isFrom(line) {
			continue
		}
		if first < 0 {
			first = i
		}
		f := strings.Fields(line)
		if len(f) >= 4 && strings.EqualFold(f[len(f)-2], "AS") && strings.EqualFold(f[len(f)-1], svc) {
			start = i
			break
		}
	}
	if start < 0 {
		return sha256Hex(dockerfile)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if isFrom(lines[i]) {
			end = i
			break
		}
	}
	segment := strings.Join(lines[start:end], "\n")
	from := strings.Fields(lines[start])
	image := from[len(from)-3]
	lower := strings.ToLower(segment)
	if !strings.HasPrefix(image, "${") || strings.Contains(lower, "--from") || strings.Contains(lower, "from=") {
		return sha256Hex(dockerfile)
	}
	return sha256Hex([]byte(strings.Join(lines[:first], "\n") + "\n--target--\n" + segment))
}

// assemblyChangeReason 对比新旧分项，返回中文触发说明。
func assemblyChangeReason(oldTotal string, oldParts *assemblyParts, cur assemblyParts) string {
	if oldTotal == "" {
		return "上一版没有装配摘要"
	}
	if oldParts == nil {
		if oldTotal == cur.Total {
			return ""
		}
		return "装配摘要变化（产物、系统输入、基础镜像、Dockerfile 或暂存清单）"
	}
	if oldParts.Total == cur.Total {
		return ""
	}
	var triggers []string
	if oldParts.ArtifactsTree != cur.ArtifactsTree {
		triggers = append(triggers, "产物树")
	}
	if oldParts.BaseImage != cur.BaseImage {
		triggers = append(triggers, "基础镜像")
	}
	if oldParts.SystemInputs != cur.SystemInputs {
		triggers = append(triggers, "系统输入")
	}
	if oldParts.DockerfileTarget != cur.DockerfileTarget {
		triggers = append(triggers, "Dockerfile 目标段")
	}
	if oldParts.StageManifest != cur.StageManifest {
		triggers = append(triggers, "暂存清单")
	}
	if len(triggers) == 0 {
		return "装配摘要变化"
	}
	return "装配摘要变化：" + strings.Join(triggers, "、")
}

// assembleImage 用 CLI 固定的参数装配一个镜像，返回镜像 ID。
// noCache=true（deploy --full）带回 --no-cache；否则依赖 BuildKit 按指令与 COPY
// 内容校验和的确定性层缓存。切换判定仍只看装配摘要，与镜像 ID 是否因缓存相同无关。
func assembleImage(ctx context.Context, svc, baseRef, dockerfile, scratch, tag string, noCache bool) (string, error) {
	args := []string{"build", "--platform=linux/amd64", "--network=none", "--pull=false"}
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args,
		"--provenance=false", "--sbom=false", "--target", svc,
		"--build-arg", imageKey(svc)+"_BASE="+baseRef, "-f", dockerfile, "-t", tag, scratch)
	env := []string{"DOCKER_BUILDKIT=1", "BUILDX_GIT_INFO=false", "BUILDX_GIT_LABELS=false"}
	if err := deployRun(ctx, runSpec{Name: "docker", Args: args, Env: env}); err != nil {
		return "", fmt.Errorf("装配 %s 镜像失败：%w", svc, err)
	}
	id, err := deployCapture(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil || !imageIDPattern.MatchString(id) {
		return "", fmt.Errorf("读取 %s 镜像 ID 失败：%v", svc, err)
	}
	return id, nil
}

// toolBuildOpts 控制 runToolBuild 的可选挂载（当前仅 Go 构建缓存）。
type toolBuildOpts struct {
	// GoCacheHost 非空时以读写方式挂到容器 /gocache，并设 AURIGA_GO_BUILD_CACHE=/gocache。
	// 仅 kind=go 时生效；--full 时留空，脚本退回 scratch 临时 GOCACHE。
	GoCacheHost string
}

// runToolBuild 在固定 digest 的工具镜像里断网运行源码中的 <生态>-inputs.sh build。
func runToolBuild(ctx context.Context, kind, tool, source, inputs, out string, budget buildBudget, opts toolBuildOpts) error {
	envName := map[string]string{"go": "AURIGA_GO_PROXY_DIR", "web": "AURIGA_WEB_INPUTS_DIR", "java": "AURIGA_JAVA_INPUTS_DIR"}[kind]
	if envName == "" {
		return fmt.Errorf("未知构建 %s", kind)
	}
	args := append([]string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64"}, budget.dockerArgs()...)
	args = append(args,
		"--mount", "type=bind,src="+source+",dst=/source,readonly",
		"--mount", "type=bind,src="+inputs+",dst=/inputs,readonly",
		"--mount", "type=bind,src="+out+",dst=/output")
	if kind == "go" && opts.GoCacheHost != "" {
		args = append(args,
			"--mount", "type=bind,src="+opts.GoCacheHost+",dst="+goBuildCacheMount,
			"--env", "AURIGA_GO_BUILD_CACHE="+goBuildCacheMount)
	}
	args = append(args, "--env", envName+"=/inputs", tool, "sh", "/source/deploy/nas/offline-build/"+kind+"-inputs.sh", "build", "/output")
	if err := deployRun(ctx, runSpec{Name: "docker", Args: args}); err != nil {
		return fmt.Errorf("%s 构建失败：%w", kind, err)
	}
	return nil
}
