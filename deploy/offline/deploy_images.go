package main

// deploy 的依赖输入与镜像解析：复用第 1 步的 depsResolver（缓存 → 套件），在使用前调用
// verifyInput 完整校验（第 1 步审查 L8），并逐字节比较源码锁清单与依赖输入的
// source-locks.sha256（设计 D4）。工具镜像、基础镜像、postgres/nats/registry 一律按配方
// 声明的固定 digest 选取：缓存/套件记录 → 本机 Docker 已有 → deps.registry 按 digest 拉取。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type resolvedImage struct {
	Role     string `json:"role"`
	Ref      string `json:"ref"`
	ID       string `json:"id"`
	Location string `json:"location"`
	Loaded   bool   `json:"loaded,omitempty"`
}

type resolvedInput struct {
	Ecosystem  string `json:"ecosystem"`
	LockDigest string `json:"lockDigest"`
	Location   string `json:"location"`
	Fetched    bool   `json:"fetched,omitempty"`
	Dir        string `json:"-"`
}

// localImage 返回本机 Docker 里某引用的镜像 ID 与平台。
func localImage(ctx context.Context, ref string) (string, string, bool) {
	out, err := deployProbe(ctx, "docker", "image", "inspect", "--format", "{{.Id}} {{.Os}}/{{.Architecture}}", ref)
	if err != nil {
		return "", "", false
	}
	id, platform, ok := strings.Cut(out, " ")
	if !ok || !imageIDPattern.MatchString(id) {
		return "", "", false
	}
	return id, platform, true
}

func digestOf(ref string) string {
	return ref[strings.LastIndex(ref, "sha256:")+7:]
}

// registryPullRef 把配方引用改写到 deps.registry：去掉原仓库主机与标签，保留路径与 digest。
func registryPullRef(registry, ref string) string {
	name := ref[:strings.Index(ref, "@")]
	if slash := strings.LastIndex(name, "/"); strings.LastIndex(name, ":") > slash {
		name = name[:strings.LastIndex(name, ":")]
	}
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		name = rest
	}
	return registry + "/" + name + "@sha256:" + digestOf(ref)
}

// resolveRecipeImage 确认配方声明的镜像在本机 Docker 可用，返回镜像 ID。
func resolveRecipeImage(ctx context.Context, r *depsResolver, cfg *siteConfig, role, ref, stage string) (resolvedImage, error) {
	out := resolvedImage{Role: role, Ref: ref}
	want := digestOf(ref)
	hits, err := r.findImages(role)
	if err != nil {
		return out, err
	}
	for _, h := range hits {
		if h.Image.digestHex() != want {
			continue
		}
		staged, err := stageImageHit(h, stage)
		if err != nil {
			return out, err
		}
		out.Location = staged.Location
		probe := staged.Image.ID
		if probe == "" {
			probe = ref
		}
		id, platform, ok := localImage(ctx, probe)
		if !ok && staged.Tar != "" {
			if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"load", "-i", staged.Tar}}); err != nil {
				return out, fmt.Errorf("载入 %s 失败：%w", role, err)
			}
			out.Loaded = true
			id, platform, ok = localImage(ctx, probe)
		}
		if !ok {
			return out, fmt.Errorf("%s 记录了 %s，但本机 Docker 没有该镜像，记录里也没有 image.tar；用 deps import 导入带归档的镜像", out.Location, ref)
		}
		if staged.Image.ID != "" && id != staged.Image.ID {
			return out, fmt.Errorf("%s 的镜像 ID 与记录不符", role)
		}
		if platform != "linux/amd64" {
			return out, fmt.Errorf("%s 不是 linux/amd64 镜像（%s）", role, platform)
		}
		out.ID = id
		return out, nil
	}
	if id, platform, ok := localImage(ctx, ref); ok {
		if platform != "linux/amd64" {
			return out, fmt.Errorf("%s 不是 linux/amd64 镜像（%s）", role, platform)
		}
		out.ID, out.Location = id, "本机 Docker"
		return out, nil
	}
	if cfg.Deps.Registry != nil {
		pull := registryPullRef(*cfg.Deps.Registry, ref)
		if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"pull", "--platform", "linux/amd64", pull}}); err != nil {
			return out, fmt.Errorf("从 deps.registry 按 digest 拉取 %s 失败：%w", pull, err)
		}
		id, platform, ok := localImage(ctx, pull)
		if !ok || platform != "linux/amd64" {
			return out, fmt.Errorf("拉取的 %s 不可用或不是 linux/amd64", pull)
		}
		out.ID, out.Location, out.Loaded = id, "registry "+*cfg.Deps.Registry, true
		return out, nil
	}
	return out, fmt.Errorf("缺少镜像 %s（%s）：用 auriga deps import 导入含 images/%s/%s/ 的缓存或套件，或配置 deps.registry", role, ref, role, want)
}

// resolveDeployInputs 为需要的生态选取依赖输入：缓存 → 套件；缺失时按第 1 步规则补
// （只有允许自动补、且 acceptNewDeps 时）。使用前完整校验并比较锁清单。
// plan=true（--dry-run）只报告命中位置与缺失，不准备、不校验、不解出。
func resolveDeployInputs(ctx context.Context, cfg *siteConfig, cfgPath, source string, needed []string, accept, plan bool, budget buildBudget, stage string, w io.Writer) ([]resolvedInput, []string, error) {
	rep, r, locks, err := computeDepsStatus(cfg, cfgPath, source)
	if err != nil {
		return nil, nil, err
	}
	want := map[string]bool{}
	for _, eco := range needed {
		want[eco] = true
	}
	byEco := map[string]ecosystemLock{}
	for _, l := range locks {
		byEco[l.Ecosystem] = l
	}
	filtered := *rep
	filtered.Ecosystems = nil
	var missing, refused []string
	for _, e := range rep.Ecosystems {
		if !want[e.Ecosystem] {
			continue
		}
		filtered.Ecosystems = append(filtered.Ecosystems, e)
		if !e.Present {
			missing = append(missing, e.Ecosystem)
			if !e.AutoFetch {
				refused = append(refused, e.Ecosystem+"（"+e.Remedy+"）")
			}
		}
	}
	if plan {
		var out []resolvedInput
		for _, e := range filtered.Ecosystems {
			loc := e.Location
			if !e.Present {
				loc = "缺失：" + e.Remedy
			}
			out = append(out, resolvedInput{Ecosystem: e.Ecosystem, LockDigest: e.LockDigest, Location: loc})
		}
		if len(missing) > 0 {
			printFetchPlan(w, cfg, &filtered, locks, source)
		}
		return out, missing, nil
	}
	if len(missing) > 0 {
		printFetchPlan(w, cfg, &filtered, locks, source)
		if len(refused) > 0 {
			return nil, missing, fmt.Errorf("依赖输入缺失且不能自动补：%s", strings.Join(refused, "；"))
		}
		if !accept && !cfg.Deps.AcceptNewDeps {
			return nil, missing, errNewDepsRejected
		}
		if _, err := ensureDepsCache(cfg.Deps.Cache, true); err != nil {
			return nil, missing, err
		}
		unlock, err := depsCacheLock(cfg.Deps.Cache)
		if err != nil {
			return nil, missing, err
		}
		for _, eco := range missing {
			if err := fetchEcosystem(ctx, cfg, r, source, byEco[eco], budget, w); err != nil {
				unlock()
				return nil, missing, fmt.Errorf("%s 依赖准备失败：%w", eco, err)
			}
		}
		unlock()
	}
	fetched := map[string]bool{}
	for _, eco := range missing {
		fetched[eco] = true
	}
	var out []resolvedInput
	sorted := append([]string(nil), needed...)
	sort.Strings(sorted)
	for _, eco := range sorted {
		lock := byEco[eco]
		hit, ok := r.findInput(eco, lock.Digest)
		if !ok {
			return nil, missing, fmt.Errorf("%s 依赖输入（锁摘要 %s）仍未命中；%s", eco, lock.Digest, depsImportHint)
		}
		if err := r.verifyInput(hit, eco, lock.Digest); err != nil {
			return nil, missing, fmt.Errorf("%s 依赖输入完整校验失败（%s）：%w", eco, hit.Location, err)
		}
		dir := hit.Dir
		if hit.release != nil {
			dir = filepath.Join(stage, "inputs", eco)
			if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
				return nil, missing, err
			}
			if err := hit.kit.devkit.extractInput(hit.release, lock, dir); err != nil {
				return nil, missing, fmt.Errorf("从 %s 解出 %s 依赖输入失败：%w", hit.Location, eco, err)
			}
		}
		if err := compareSourceLocks(dir, lock); err != nil {
			return nil, missing, err
		}
		out = append(out, resolvedInput{Ecosystem: eco, LockDigest: lock.Digest, Location: hit.Location, Fetched: fetched[eco], Dir: dir})
	}
	return out, missing, nil
}

// compareSourceLocks：构建前逐字节比较源码锁清单与依赖输入的 source-locks.sha256。
func compareSourceLocks(dir string, lock ecosystemLock) error {
	got, err := smallFile(filepath.Join(dir, "source-locks.sha256"), 16<<20)
	if err != nil {
		return fmt.Errorf("%s 依赖输入缺少 source-locks.sha256：%w", lock.Ecosystem, err)
	}
	if !bytes.Equal(got, lock.Raw) {
		return fmt.Errorf("%s 依赖输入的 source-locks.sha256 与源码锁清单不一致，拒绝构建", lock.Ecosystem)
	}
	return nil
}
