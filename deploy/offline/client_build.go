package main

// auriga client build 薄壳（设计 D13）：读 site.json，映射为 auriga-client 离线构建参数，
// 走与 auriga-client.cmd 相同的 cli.mjs build --offline 流程（网络守卫、offline-evidence.json、
// 空 network-denials.log 均由该流程产出）。
//
// 信任链：
//   - 源码：client.verifyKey / client.trustFingerprint（独立批准的发行钥）核签名源码；Git 源码
//     按 Git 身份。
//   - 现场缓存套件（deps fetch/import --client 写入）：无发行签名，以写入缓存时记录在
//     site-dependencies.json 的 windows-client.json 摘要为准（缓存受管理员 ACL 保护），
//     以 --site-kit --devkit-sha256 传入；不重新对当前文件自算摘要充当“批准摘要”。
//   - deps.kits 中的发行套件：先用该套件配置的 trustFingerprint 验签（site-deps.mjs），
//     构建时再用 client 段的发行钥验签。
//   - 构建 Node：只执行套件内 build-node/node.exe，执行前核对其摘要等于上述清单登记值
//     （等价于 auriga-client.cmd 对 AURIGA_CLIENT_NODE_SHA256 的门禁），不用系统 Node。
// sequence 从 platformOrigin 的公开 /api/resource/v1/desktop/latest 取（stable、beta 取最大值）+1；
// 签名不自动化，只提示人工 sign --review-only。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	clientBuildCLIRel = "scripts/client-release/cli.mjs"
	desktopLatestPath = "/api/resource/v1/desktop/latest"
	clientBuildNode   = "build-node/node.exe"
)

// desktopChannels 是平台资源服务接受的全部渠道；发布序号在渠道间全局递增。
var desktopChannels = []string{"stable", "beta"}

// 可注入点：测试替换 HTTP 取序号与实际启动子进程。
var (
	clientLatestSequence = fetchDesktopLatestSequence
	clientBuildRunner    = runAurigaClientBuild
	clientDevkitResolver = resolveClientDevkit
	// desktopTransport 为 nil 时用默认传输；测试注入信任自签证书的传输。
	desktopTransport http.RoundTripper
)

// clientOfflineBuildArgs 是映射后的 auriga-client build --offline 参数（不含可执行文件）。
type clientOfflineBuildArgs struct {
	Devkit            string
	DevkitSHA256      string // 套件 windows-client.json 的摘要（缓存记录或签名元数据）
	SiteKit           bool   // 现场缓存套件：--site-kit --devkit-sha256
	Out               string
	Version           string
	Sequence          int64
	PlatformOrigin    string
	VerifyKey         string
	TrustFingerprint  string
	ReleasePublicKey  string
	ResourceTrustJSON string
	Channel           string
	ToolsBaseline     string // deps.cache，供构建核对系统工具现场基线
}

func mapClientBuildArgs(cfg *siteConfig, version, channel string, kit clientKit, sequence int64) (clientOfflineBuildArgs, error) {
	if cfg == nil || cfg.Client == nil {
		return clientOfflineBuildArgs{}, errors.New("缺少 client 段；请在 site.json 配置 client.out、platformOrigin、verifyKey/trustFingerprint、releasePublicKey 与 resourceTrustJson")
	}
	c := cfg.Client
	if strings.TrimSpace(version) == "" {
		return clientOfflineBuildArgs{}, errors.New("client build 需要 --version（SemVer，例如 1.0.2-site.1）")
	}
	if sequence < 1 {
		return clientOfflineBuildArgs{}, errors.New("发布账本序号无效")
	}
	if c.VerifyKey == nil && c.TrustFingerprint == nil {
		return clientOfflineBuildArgs{}, errors.New("client 段需要 verifyKey 或 trustFingerprint 之一，用于核验发行源码与套件")
	}
	if kit.Location == "" {
		return clientOfflineBuildArgs{}, errors.New("缺少现场客户端套件路径；请先 auriga deps fetch --client")
	}
	if !hex64.MatchString(kit.DevkitSHA256) {
		return clientOfflineBuildArgs{}, errors.New("现场客户端套件缺少记录的清单摘要")
	}
	out := clientOfflineBuildArgs{
		Devkit:         kit.Location,
		DevkitSHA256:   kit.DevkitSHA256,
		Out:            c.Out,
		Version:        version,
		Sequence:       sequence,
		PlatformOrigin: strings.TrimRight(c.PlatformOrigin, "/"),
		Channel:        channel,
	}
	if cfg.Deps != nil {
		out.ToolsBaseline = cfg.Deps.Cache
	}
	if c.ReleasePublicKey != nil {
		out.ReleasePublicKey = *c.ReleasePublicKey
	}
	if c.ResourceTrustJSON != nil {
		out.ResourceTrustJSON = *c.ResourceTrustJSON
	}
	if out.ReleasePublicKey == "" || out.ResourceTrustJSON == "" {
		return clientOfflineBuildArgs{}, errors.New("client 段需要 releasePublicKey 与 resourceTrustJson（桌面签发公钥与资源信任根）")
	}
	// 发行钥始终传入：签名源码归档必须用它核验（Git 源码时不影响）。
	if c.VerifyKey != nil {
		out.VerifyKey = *c.VerifyKey
	} else {
		fp, err := normalizeFingerprint("client.trustFingerprint", *c.TrustFingerprint)
		if err != nil {
			return clientOfflineBuildArgs{}, err
		}
		out.TrustFingerprint = fp
	}
	switch kit.Source {
	case "cache":
		out.SiteKit = true
	case "kit":
		if !kit.Signed {
			return clientOfflineBuildArgs{}, errors.New("deps.kits 中的客户端套件必须经发行签名校验")
		}
	default:
		return clientOfflineBuildArgs{}, fmt.Errorf("未知套件来源 %q", kit.Source)
	}
	return out, nil
}

func (a clientOfflineBuildArgs) argv() []string {
	args := []string{
		"build", "--offline",
		"--devkit", a.Devkit,
		"--out", a.Out,
		"--version", a.Version,
		"--sequence", strconv.FormatInt(a.Sequence, 10),
		"--platform-origin", a.PlatformOrigin,
		"--release-public-key", a.ReleasePublicKey,
		"--resource-trust-json", a.ResourceTrustJSON,
	}
	if a.VerifyKey != "" {
		args = append(args, "--verify-key", a.VerifyKey)
	}
	if a.TrustFingerprint != "" {
		args = append(args, "--trust-fingerprint", a.TrustFingerprint)
	}
	if a.SiteKit {
		args = append(args, "--site-kit", "--devkit-sha256", a.DevkitSHA256)
	}
	if a.Channel != "" && a.Channel != "stable" {
		args = append(args, "--channel", a.Channel)
	}
	return args
}

func clientBuildCommand(args []string, inv siteInvocation, w io.Writer) error {
	f := flags("client build")
	version := f.String("version", "", "")
	config := f.String("config", "", "")
	source := f.String("source", "", "")
	channel := f.String("channel", "stable", "")
	sequence := f.Int64("sequence", 0, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余 client build 参数")
	}
	if strings.TrimSpace(*version) == "" {
		return errors.New("client build 需要 --version")
	}
	if *channel != "stable" && *channel != "beta" {
		return fmt.Errorf("--channel 只能是 stable 或 beta（当前 %q）", *channel)
	}
	cfg, _, err := loadSiteConfig(*config, inv)
	if err != nil {
		return err
	}
	if cfg.Client == nil {
		return errors.New("缺少 client 段；请在 site.json 配置 client")
	}
	src, err := resolveClientSource(*source, cfg, inv)
	if err != nil {
		return err
	}
	ctx, cancel := depsSignalContext()
	defer cancel()
	next, err := clientLatestSequence(ctx, cfg.Client.PlatformOrigin)
	if err != nil {
		return err
	}
	seq := next
	if *sequence != 0 {
		if *sequence < next {
			return fmt.Errorf("--sequence %d 小于发布账本可见的下一个序号 %d", *sequence, next)
		}
		seq = *sequence
	}
	kit, err := clientDevkitResolver(ctx, cfg, src)
	if err != nil {
		return err
	}
	mapped, err := mapClientBuildArgs(cfg, *version, *channel, kit, seq)
	if err != nil {
		return err
	}
	// 系统工具基线在 deps.cache 根下：缓存目录已做 ACL 校验，这里再确认基线文件本身没有被
	// 单独放宽权限（它能放宽构建时的系统工具比对）。
	if runtime.GOOS == "windows" && mapped.ToolsBaseline != "" {
		baseline := filepath.Join(mapped.ToolsBaseline, "client-tools-baseline.json")
		if _, err := os.Lstat(baseline); err == nil {
			if err := checkWindowsTrustedPath("系统工具基线 ", baseline); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(w, "现场客户端套件 %s（来源 %s，清单 SHA-256 %s）\n发布序号 %d（%s%s 可见最大序号 +1 为 %d）\n", mapped.Devkit, kit.Source, mapped.DevkitSHA256, mapped.Sequence, strings.TrimRight(cfg.Client.PlatformOrigin, "/"), desktopLatestPath, next)
	fmt.Fprintln(w, "注意：已撤回的发行不在公开接口中，若发布时提示序号不大于平台最高序号，请用 --sequence 指定更大的值重建。")
	if err := clientBuildRunner(ctx, src, mapped, w); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n已产出待签目录 %s（收据 offline-evidence.json，系统工具依据见其 systemTools 字段）\n下一步请人工审阅签名：\n  auriga-client sign --dir %s --review-only\n核对 reviewSha256 后再：\n  auriga-client sign --dir %s --key-file <现场桌面私钥> --confirm-sha256 <审阅摘要>\n", mapped.Out, mapped.Out, mapped.Out)
	return nil
}

// fetchDesktopLatestSequence 读取公开 latest 接口（各渠道），返回下一个可用序号。
// 某渠道 404 表示该渠道尚无可见发行；其他状态、网络错误、非 https 重定向或无效响应一律失败。
func fetchDesktopLatestSequence(ctx context.Context, origin string) (int64, error) {
	base, err := url.Parse(strings.TrimRight(origin, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return 0, fmt.Errorf("client.platformOrigin 必须是 https 源，当前 %q", origin)
	}
	client := &http.Client{
		Transport: desktopTransport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向次数过多")
			}
			if req.URL.Scheme != "https" || req.URL.Host != base.Host {
				return fmt.Errorf("拒绝重定向到 %s（只允许同一 https 源）", req.URL.Redacted())
			}
			return nil
		},
	}
	var highest int64
	for _, channel := range desktopChannels {
		u := base.String() + desktopLatestPath + "?channel=" + channel
		n, found, err := fetchChannelLatest(ctx, client, u)
		if err != nil {
			return 0, err
		}
		if found && n > highest {
			highest = n
		}
	}
	return highest + 1, nil
}

func fetchChannelLatest(ctx context.Context, client *http.Client, u string) (int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("读取发布账本失败（%s）：%w；请确认平台可达或稍后重试", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, false, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var payload struct {
			Sequence json.Number `json:"sequence"`
		}
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.UseNumber()
		if err := dec.Decode(&payload); err != nil {
			return 0, false, fmt.Errorf("发布账本 latest 不是有效 JSON（%s）：%w", u, err)
		}
		n, err := strconv.ParseInt(payload.Sequence.String(), 10, 64)
		if err != nil || n < 1 {
			return 0, false, fmt.Errorf("发布账本 latest.sequence 无效（%q，%s）", payload.Sequence, u)
		}
		return n, true, nil
	case http.StatusNotFound:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("读取发布账本失败 HTTP %d（%s）；取不到序号则无法 client build，请检查平台资源服务", resp.StatusCode, u)
	}
}

// verifyClientBuildNode 核对套件清单摘要与其中登记的构建 Node 摘要，返回 node.exe 路径。
func verifyClientBuildNode(devkit, manifestSHA256 string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(devkit, "windows-client.json"))
	if err != nil {
		return "", fmt.Errorf("读取 windows-client.json：%w", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != manifestSHA256 {
		return "", fmt.Errorf("现场客户端套件清单摘要与记录不符：%s", devkit)
	}
	var manifest struct {
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("windows-client.json 无效：%w", err)
	}
	want := ""
	for _, f := range manifest.Files {
		if f.Path == clientBuildNode {
			want = f.SHA256
		}
	}
	if !hex64.MatchString(want) {
		return "", fmt.Errorf("套件清单未登记 %s", clientBuildNode)
	}
	node := filepath.Join(devkit, filepath.FromSlash(clientBuildNode))
	got, err := digest(node)
	if err != nil {
		return "", fmt.Errorf("现场套件缺少构建 Node %s：%w", node, err)
	}
	if got != want {
		return "", fmt.Errorf("构建 Node 摘要与套件清单不符：%s", node)
	}
	return node, nil
}

// clientBuildEnv 继承当前环境，但去掉会改变构建行为的变量后再按配置设定。
func clientBuildEnv(mapped clientOfflineBuildArgs) []string {
	var env []string
	for _, kv := range os.Environ() {
		name := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		switch name {
		case "PSMODULEPATH", "AURIGA_CLIENT_DEVKIT", "AURIGA_CLIENT_NODE_SHA256", "AURIGA_CLIENT_TOOLS_BASELINE", "NODE_OPTIONS":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "AURIGA_CLIENT_DEVKIT="+mapped.Devkit)
	if mapped.ToolsBaseline != "" {
		env = append(env, "AURIGA_CLIENT_TOOLS_BASELINE="+mapped.ToolsBaseline)
	}
	return env
}

func runAurigaClientBuild(ctx context.Context, source string, mapped clientOfflineBuildArgs, w io.Writer) error {
	cli := filepath.Join(source, filepath.FromSlash(clientBuildCLIRel))
	if st, err := os.Stat(cli); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("找不到 %s", clientBuildCLIRel)
	}
	nodeExe, err := verifyClientBuildNode(mapped.Devkit, mapped.DevkitSHA256)
	if err != nil {
		return err
	}
	args := append([]string{cli}, mapped.argv()...)
	fmt.Fprintf(w, "调用 auriga-client 离线构建：%s %s\n", nodeExe, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, nodeExe, args...)
	cmd.Dir = source
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	cmd.Env = clientBuildEnv(mapped)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("auriga-client build 失败：%w", err)
	}
	return nil
}
