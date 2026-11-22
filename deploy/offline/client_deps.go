package main

// 客户端依赖：auriga deps … --client 与 deps baseline --client。
// Go 薄壳负责站点配置、缓存 ACL 与并发锁、导入源与 caFile 的受信校验，再把 site.json
// 映射成 node scripts/client-release/site-deps.mjs 的参数（准备逻辑复用 prepare.mjs，
// 不在 Go 里重写）。联网准备阶段由 PATH 上的 Node 运行脚本；构建阶段只用缓存/套件内
// 摘要核对过的构建 Node（见 client_build.go）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const clientSiteDepsRel = "scripts/client-release/site-deps.mjs"

// 可注入点：测试替换 Node 子进程与缓存校验/加锁。
var (
	clientNodeRunner = runClientNodeScript
	clientCacheGuard = guardClientCache
)

// clientDepsRequest 是一次 site-deps.mjs 调用的参数。
type clientDepsRequest struct {
	Action      string
	From        string
	Fingerprint string
	TrustState  bool
	JSON        bool
	Accept      bool
}

// guardClientCache 校验 deps.cache 的受信权限（Windows：Administrators/SYSTEM 所有、普通
// 用户不可写，祖先同样受信；Linux：root 所有）。写缓存的动作另加排他锁。
func guardClientCache(cfg *siteConfig, write bool) (func(), error) {
	exists, err := ensureDepsCache(cfg.Deps.Cache, write)
	if err != nil {
		return nil, err
	}
	if !write || !exists {
		return func() {}, nil
	}
	return depsCacheLock(cfg.Deps.Cache)
}

func runClientDeps(ctx context.Context, cfg *siteConfig, source string, req clientDepsRequest, w io.Writer) error {
	if cfg == nil || cfg.Deps == nil {
		return errors.New("缺少 deps 段")
	}
	if err := checkClientPaths(cfg); err != nil {
		return err
	}
	if req.TrustState {
		return errors.New("客户端依赖不使用 state 钉定的发行钥（Windows 现场没有 state）；请改用 --trust-fingerprint")
	}
	script, err := findClientSiteDeps(source)
	if err != nil {
		return err
	}
	write := req.Action == "fetch" || req.Action == "import" || req.Action == "baseline"
	args := []string{script, req.Action, "--cache", cfg.Deps.Cache, "--source", source}
	if req.JSON {
		args = append(args, "--json")
	}
	if req.Accept || cfg.Deps.AcceptNewDeps {
		args = append(args, "--accept-new-deps")
	}
	if cfg.Deps.AllowPublic {
		args = append(args, "--allow-public")
	}
	if cfg.Deps.Sources.Files != nil {
		args = append(args, "--files", *cfg.Deps.Sources.Files)
	}
	if cfg.Deps.Sources.NPM != nil {
		args = append(args, "--npm", *cfg.Deps.Sources.NPM)
	}
	if cfg.Deps.CAFile != nil && (req.Action == "fetch" || req.Action == "status") {
		if err := depsTrustedSourceCheck("deps.caFile ", *cfg.Deps.CAFile); err != nil {
			return err
		}
		args = append(args, "--ca-file", *cfg.Deps.CAFile)
	}
	for i, kit := range cfg.Deps.Kits {
		if kit.TrustFingerprint == "" {
			// trustState 依赖 Linux state 钉定的发行钥，客户端无法使用；不传给 Node（不自证）。
			fmt.Fprintf(os.Stderr, "跳过 deps.kits[%d]（%s）：客户端套件需要 trustFingerprint，trustState 不适用\n", i, kit.Path)
			continue
		}
		fp, err := normalizeFingerprint(fmt.Sprintf("deps.kits[%d].trustFingerprint", i), kit.TrustFingerprint)
		if err != nil {
			return err
		}
		args = append(args, "--kit", kit.Path, "--kit-fingerprint", fp)
	}
	if req.Action == "import" {
		abs, err := filepath.Abs(req.From)
		if err != nil {
			return err
		}
		if err := noSymlinks(abs); err != nil {
			return err
		}
		// 导入源同套件：管理员受控（Windows ACL / Linux root 所有），复制后再校验副本。
		if err := depsTrustedSourceCheck("导入源 ", abs); err != nil {
			return fmt.Errorf("%w%s", err, depsTrustedSourceHint)
		}
		if pathsOverlap(abs, cfg.Deps.Cache) {
			return errors.New("不能从缓存目录自身导入，导入源也不能包含缓存")
		}
		args = append(args, "--from", abs)
	}
	if req.Fingerprint != "" {
		fp, err := normalizeFingerprint("--trust-fingerprint", req.Fingerprint)
		if err != nil {
			return err
		}
		args = append(args, "--trust-fingerprint", fp)
	}
	unlock, err := clientCacheGuard(cfg, write)
	if err != nil {
		return err
	}
	defer unlock()
	return clientNodeRunner(ctx, args, w)
}

func findClientSiteDeps(source string) (string, error) {
	p := filepath.Join(source, clientSiteDepsRel)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p, nil
	}
	return "", fmt.Errorf("找不到 %s；请配置 source.dir，或用 --source 指向含 scripts/client-release 的源码树", p)
}

func runClientNodeScript(ctx context.Context, args []string, w io.Writer) error {
	node, err := exec.LookPath("node")
	if err != nil {
		return errors.New("未找到 node：客户端依赖准备需要 Node 运行 scripts/client-release/site-deps.mjs（构建阶段只用缓存/套件内的构建 Node）")
	}
	cmd := exec.CommandContext(ctx, node, args...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("site-deps.mjs 失败：%w", err)
	}
	return nil
}

// clientKit 是 resolve-devkit 选出的客户端套件。
type clientKit struct {
	Location     string `json:"location"`
	Source       string `json:"source"`
	DevkitSHA256 string `json:"devkitSha256"`
	Signed       bool   `json:"signed"`
}

// resolveClientDevkit 调用 site-deps.mjs resolve-devkit --json，返回现场客户端套件与其清单摘要。
func resolveClientDevkit(ctx context.Context, cfg *siteConfig, source string) (clientKit, error) {
	var buf strings.Builder
	if err := runClientDeps(ctx, cfg, source, clientDepsRequest{Action: "resolve-devkit", JSON: true}, &buf); err != nil {
		return clientKit{}, err
	}
	raw := strings.TrimSpace(buf.String())
	// site-deps 只打印一个 JSON 对象；容忍其前有日志行。
	if i := strings.Index(raw, "{"); i >= 0 {
		raw = raw[i:]
	}
	var kit clientKit
	if err := json.Unmarshal([]byte(raw), &kit); err != nil {
		return clientKit{}, fmt.Errorf("resolve-devkit 输出不是 JSON：%w", err)
	}
	switch {
	case kit.Location == "":
		return clientKit{}, errors.New("resolve-devkit 未返回现场客户端套件路径；请先 auriga deps fetch --client")
	case kit.Source != "cache" && kit.Source != "kit":
		return clientKit{}, fmt.Errorf("resolve-devkit 返回未知来源 %q", kit.Source)
	case !hex64.MatchString(kit.DevkitSHA256):
		return clientKit{}, errors.New("resolve-devkit 未返回清单摘要；请删除缓存条目后重新 deps fetch --client")
	case kit.Source == "kit" && !kit.Signed:
		return clientKit{}, errors.New("deps.kits 中的客户端套件必须经发行签名校验")
	}
	return kit, nil
}

func resolveClientSource(flagSource string, cfg *siteConfig, inv siteInvocation) (string, error) {
	if flagSource != "" {
		if inv.Sudo {
			return "", errors.New("经 sudo 调用时不接受 --source")
		}
		return flagSource, nil
	}
	if cfg.Source != nil && cfg.Source.Dir != nil && *cfg.Source.Dir != "" {
		return *cfg.Source.Dir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(cwd, clientSiteDepsRel)); err == nil {
		return cwd, nil
	}
	return "", errors.New("没有可用源码：请配置 source.dir，或加 --source <源码根>，或在含 scripts/client-release 的源码根目录运行")
}
