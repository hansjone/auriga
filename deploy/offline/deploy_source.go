package main

// deploy 的源码获取（设计 D3）。
//
//   - git 远端模式：root 把配置的 source.git.url 的 allowedRefs fetch 到数据目录同级、
//     root 所有的裸仓库 auriga-source.git，再用 git archive <提交>^{tree} 导出快照；
//     不在运行器目录里执行 git，也不读取运行器仓库的 .git/config。dirty=false 由来源决定。
//   - 人工源码目录模式：配置 source.dir（root 所有）或 root 直接运行时的 --source，仍按原
//     snapshotSourceContext 复制并检查 git 状态，记录 dirty 标志。
//
// 两种模式都得到导出目录与 SnapshotSHA256（directorySnapshotSHA256 与原函数同一口径）。
// 提交必须在某个 allowedRefs 上（是其祖先或本身），否则拒绝。requireSignedCommits=true 时，
// git 远端模式在导出前按 trustedSigners 校验提交签名（deploy_signature.go），目录模式直接拒绝。
// git 调用隔离 root 的系统/全局配置与继承的 GIT_* 环境，快照只由提交决定。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// runSpec 是一次外部命令（docker、git）调用。
type runSpec struct {
	Name   string
	Args   []string
	Env    []string
	Stdout io.Writer
	Limit  time.Duration
	Quiet  bool // 丢弃标准错误（只用于探测，例如镜像是否存在）
	Stdin  io.Reader
	// DropEnvPrefix 非空时，继承的环境里去掉以它开头的变量（git 用 "GIT_"）。
	DropEnvPrefix string
}

// runEnv 是一次调用的完整环境：继承的环境（按 DropEnvPrefix 过滤）加 r.Env。
func runEnv(base []string, r runSpec) []string {
	env := make([]string, 0, len(base)+len(r.Env))
	for _, kv := range base {
		if r.DropEnvPrefix != "" && strings.HasPrefix(kv, r.DropEnvPrefix) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, r.Env...)
}

// deployRun 是 deploy 调用 docker、git 的唯一出口（测试注入假执行器）。
var deployRun = func(ctx context.Context, r runSpec) error {
	out := r.Stdout
	if out == nil {
		out = os.Stdout
	}
	limit := r.Limit
	if limit == 0 {
		limit = 120 * time.Minute
	}
	if r.Quiet {
		probe, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		c := exec.CommandContext(probe, r.Name, r.Args...)
		c.Stdin, c.Stdout, c.Stderr, c.Env = r.Stdin, out, io.Discard, runEnv(os.Environ(), r)
		return c.Run()
	}
	return commandEnvLimit(ctx, limit, r.Stdin, out, runEnv(os.Environ(), r), r.Name, r.Args...)
}

func deployCapture(ctx context.Context, name string, args ...string) (string, error) {
	var b bytes.Buffer
	err := deployRun(ctx, withGitEnv(runSpec{Name: name, Args: args, Stdout: &b}))
	return strings.TrimSpace(b.String()), err
}

// deployProbe 同 deployCapture，但丢弃标准错误（预期可能失败的探测）。
func deployProbe(ctx context.Context, name string, args ...string) (string, error) {
	var b bytes.Buffer
	err := deployRun(ctx, withGitEnv(runSpec{Name: name, Args: args, Stdout: &b, Quiet: true}))
	return strings.TrimSpace(b.String()), err
}

// withGitEnv：git 调用一律带隔离环境。
func withGitEnv(r runSpec) runSpec {
	if r.Name == "git" {
		r.Env, r.DropEnvPrefix = append(gitEnv(), r.Env...), "GIT_"
	}
	return r
}

// requireSigned：site.json 要求部署签名提交。
func requireSigned(cfg *siteConfig) bool {
	return cfg != nil && cfg.Source != nil && cfg.Source.Git != nil && cfg.Source.Git.RequireSignedCommits
}

// dirModeSigningPolicy：要求签名提交时不能用源码目录模式（目录快照无法证明来自签名提交）。
func dirModeSigningPolicy(cfg *siteConfig) error {
	if requireSigned(cfg) {
		return errors.New("site.json 要求签名提交（source.git.requireSignedCommits=true）：源码目录模式（source.dir 或 --source）无法校验签名，拒绝；请按提交号从 source.git 部署")
	}
	return nil
}

// deploySource 记录本次部署的源码来源（进收据和历史）。
type deploySource struct {
	Mode           string `json:"mode"` // git | dir
	URL            string `json:"url,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Commit         string `json:"commit,omitempty"`
	Dir            string `json:"-"` // 导出目录
	SnapshotSHA256 string `json:"snapshotSha256"`
	Dirty          bool   `json:"dirty"`
	// Signature：requireSignedCommits=true 时的签名校验结果（签名类型与签名者）。
	Signature string `json:"signature,omitempty"`
}

// 可注入点：人工源码目录模式的快照（真实实现调用原 snapshotSourceContext）。
var snapshotDirSource = func(ctx context.Context, dir, target string) (*provenance, error) {
	return snapshotSourceContext(ctx, dir, target)
}

// resolveDeploySource 按参数与配置选择源码模式并导出快照到 stage/source。
//   - 给了提交号：必须配置 source.git，走 git 远端模式；
//   - --source（只允许 root 直接运行）：人工源码目录模式，不能与提交号同用；
//   - 都没给：有 source.dir 用目录模式，否则用 source.git 第一个 allowedRefs 的最新提交。
func resolveDeploySource(ctx context.Context, cfg *siteConfig, data, flagSource, commit, stage string) (*deploySource, error) {
	target := filepath.Join(stage, "source")
	if commit != "" && !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("提交号必须是 40 位小写十六进制（当前 %q）", commit)
	}
	if flagSource != "" {
		if commit != "" {
			return nil, errors.New("--source 与提交号不能同用")
		}
		abs, err := filepath.Abs(flagSource)
		if err != nil {
			return nil, err
		}
		if err := noSymlinks(abs); err != nil {
			return nil, err
		}
		if err := depsTrustedSourceCheck("源码目录 --source ", abs); err != nil {
			return nil, err
		}
		if err := dirModeSigningPolicy(cfg); err != nil {
			return nil, err
		}
		return dirSnapshot(ctx, abs, target)
	}
	var git *siteGit
	var dir *string
	if cfg.Source != nil {
		git, dir = cfg.Source.Git, cfg.Source.Dir
	}
	if commit == "" && dir != nil {
		if err := depsTrustedSourceCheck("源码目录 source.dir ", *dir); err != nil {
			return nil, err
		}
		if err := dirModeSigningPolicy(cfg); err != nil {
			return nil, err
		}
		return dirSnapshot(ctx, *dir, target)
	}
	if git == nil {
		if commit != "" {
			return nil, errors.New("按提交号部署需要配置 source.git（受信 git 远端）")
		}
		return nil, errors.New("没有可用源码：配置 source.git 或 source.dir，或由 root 直接运行时加 --source <目录>")
	}
	return gitSnapshot(ctx, git, sourceRepo(data), commit, stage, target)
}

func dirSnapshot(ctx context.Context, dir, target string) (*deploySource, error) {
	info, err := snapshotDirSource(ctx, dir, target)
	if err != nil {
		return nil, fmt.Errorf("复制源码目录 %s 失败：%w", dir, err)
	}
	// 复核：导出目录按同一口径重算，必须与原函数一致。
	sum, err := directorySnapshotSHA256(ctx, target)
	if err != nil {
		return nil, err
	}
	if info.SnapshotSHA256 != "" && info.SnapshotSHA256 != sum {
		return nil, errors.New("源码快照摘要复核不一致，拒绝构建")
	}
	return &deploySource{Mode: "dir", URL: dir, Commit: info.Commit, Dir: target, SnapshotSHA256: sum, Dirty: info.Dirty}, nil
}

// gitRefLocal 把 refs/heads/main 映射到裸仓库里的 refs/auriga/heads/main。
func gitRefLocal(ref string) string {
	return "refs/auriga/" + strings.TrimPrefix(ref, "refs/")
}

func gitSnapshot(ctx context.Context, g *siteGit, repo, commit, stage, target string) (*deploySource, error) {
	if err := noSymlinks(repo); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(repo); os.IsNotExist(err) {
		if err := os.Mkdir(repo, 0700); err != nil {
			return nil, err
		}
		if err := deployRun(ctx, withGitEnv(runSpec{Name: "git", Args: []string{"init", "--bare", "--quiet", repo}})); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := depsTrustedSourceCheck("源码暂存仓库 ", repo); err != nil {
		return nil, err
	}
	gitDir := "--git-dir=" + repo
	args := []string{gitDir, "fetch", "--no-tags", "--prune", "--quiet", g.URL}
	for _, ref := range g.AllowedRefs {
		args = append(args, "+"+ref+":"+gitRefLocal(ref))
	}
	if err := deployRun(ctx, withGitEnv(runSpec{Name: "git", Args: args})); err != nil {
		return nil, fmt.Errorf("从 %s 获取源码失败：%w", g.URL, err)
	}
	src := &deploySource{Mode: "git", URL: g.URL}
	if commit == "" {
		src.Ref = g.AllowedRefs[0]
		out, err := deployCapture(ctx, "git", gitDir, "rev-parse", "--verify", "--quiet", gitRefLocal(src.Ref)+"^{commit}")
		if err != nil || !commitPattern.MatchString(out) {
			return nil, fmt.Errorf("远端没有 %s：%v", src.Ref, err)
		}
		commit = out
	} else {
		// 提交必须在某个 allowedRefs 上：刚 fetch 的引用里，至少一个以它为祖先（或就是它）。
		// 不在任何 allowedRefs 上的提交不按提交号另取，直接拒绝。
		if _, err := deployProbe(ctx, "git", gitDir, "cat-file", "-e", commit+"^{commit}"); err != nil {
			return nil, fmt.Errorf("提交 %s 不在 allowedRefs（%s）上，拒绝部署", commit, strings.Join(g.AllowedRefs, "、"))
		}
		onRef := ""
		for _, ref := range g.AllowedRefs {
			if _, err := deployProbe(ctx, "git", gitDir, "merge-base", "--is-ancestor", commit, gitRefLocal(ref)); err == nil {
				onRef = ref
				break
			}
		}
		if onRef == "" {
			return nil, fmt.Errorf("提交 %s 不在 allowedRefs（%s）上，拒绝部署", commit, strings.Join(g.AllowedRefs, "、"))
		}
		src.Ref = onRef
	}
	src.Commit = commit
	if g.RequireSignedCommits {
		sig, err := verifyCommitSignature(ctx, repo, commit, g.TrustedSigners, filepath.Join(stage, "signature"))
		if err != nil {
			return nil, err
		}
		src.Signature = sig
	}
	// 导出提交的树（不是提交本身）：git archive 不写 pax 全局头，导出只含普通文件与目录。
	archive := filepath.Join(stage, "source.tar")
	if err := deployRun(ctx, withGitEnv(runSpec{Name: "git", Args: []string{gitDir, "archive", "--format=tar", "-o", archive, commit + "^{tree}"}})); err != nil {
		return nil, fmt.Errorf("导出提交 %s 失败：%w", commit, err)
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return nil, err
	}
	if err := unpackContext(ctx, archive, target); err != nil {
		return nil, fmt.Errorf("解开源码快照失败（只接受普通文件和目录）：%w", err)
	}
	_ = os.Remove(archive)
	sum, err := directorySnapshotSHA256(ctx, target)
	if err != nil {
		return nil, err
	}
	src.Dir, src.SnapshotSHA256, src.Dirty = target, sum, false
	return src, nil
}

// gitEnv：禁止交互式询问凭据；不读系统与全局 git 配置、系统 gitattributes（HOME、
// XDG_CONFIG_HOME 指向不存在的目录）。继承的 GIT_* 变量由 DropEnvPrefix 去掉。
func gitEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1",
		"HOME=/nonexistent/auriga-deploy-git", "XDG_CONFIG_HOME=/nonexistent/auriga-deploy-git"}
}
