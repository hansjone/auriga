package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// helpOut 是帮助输出位置（测试中可替换）。
var helpOut io.Writer = os.Stdout

// flagFailure 记录最近一次参数解析失败的命令名，main 据此追加“查看参数”提示。
var flagFailure string

type failWriter struct {
	failed *bool
	name   string
}

func (w failWriter) Write(p []byte) (int, error) {
	*w.failed = true
	flagFailure = w.name
	return len(p), nil
}

// flags 创建 FlagSet：保持 ContinueOnError；-h/--help 时把用法打印到 stdout 并返回 flag.ErrHelp，
// 其他解析错误仍由调用方返回，main 会追加“运行 ./auriga <命令> --help 查看参数”。
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	failed := false
	f.SetOutput(failWriter{failed: &failed, name: name})
	f.Usage = func() {
		if !failed {
			printFlagHelp(helpOut, name, f)
		}
	}
	return f
}

type helpSpec struct {
	usage   string            // 用法行（不含“用法：”前缀）
	summary string            // 一句话用途
	notes   []string          // 附加说明
	docs    map[string]string // 参数名 -> 中文说明
}

func printFlagHelp(w io.Writer, name string, f *flag.FlagSet) {
	spec, ok := helpSpecs[name]
	if !ok {
		spec = helpSpec{usage: "./auriga " + name + " [参数]"}
	}
	fmt.Fprintf(w, "用法：%s\n", spec.usage)
	if spec.summary != "" {
		fmt.Fprintln(w, spec.summary)
	}
	for _, n := range spec.notes {
		fmt.Fprintln(w, n)
	}
	var all []*flag.Flag
	f.VisitAll(func(fl *flag.Flag) { all = append(all, fl) })
	if len(all) == 0 {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	fmt.Fprintln(w, "\n参数：")
	for _, fl := range all {
		kind, _ := flag.UnquoteUsage(fl)
		head := "  --" + fl.Name
		if kind != "" {
			head += " <" + kind + ">"
		}
		desc := spec.docs[fl.Name]
		if desc == "" {
			desc = fl.Usage
		}
		if desc == "" {
			desc = "（无说明）"
		}
		if fl.DefValue != "" && fl.DefValue != "0" && fl.DefValue != "false" {
			desc += "（默认 " + fl.DefValue + "）"
		}
		fmt.Fprintf(w, "%s\n        %s\n", head, desc)
	}
}

func isHelpArg(s string) bool { return s == "-h" || s == "--help" || s == "-help" }

func hasHelpArg(args []string) bool {
	for _, a := range args {
		if isHelpArg(a) {
			return true
		}
	}
	return false
}

// tryHelp 处理顶层帮助和各命令 --help；done 为 true 表示已处理（err 为 nil 即成功退出）。
func tryHelp(args []string) (done bool, err error) {
	if len(args) == 0 || isHelpArg(args[0]) || args[0] == "help" && len(args) == 1 {
		printTopHelp(helpOut)
		return true, nil
	}
	if args[0] == "help" {
		args = append([]string{args[1]}, "--help")
	}
	if !hasHelpArg(args[1:]) {
		return false, nil
	}
	h := []string{"--help"}
	sub := ""
	if len(args) > 1 {
		sub = args[1]
	}
	switch args[0] {
	case "install":
		_, err = parseInstall(h)
	case "upgrade":
		f, _, _ := newUpgradeFlags()
		err = f.Parse(h)
	case "doctor":
		if sub == "--build" {
			err = buildDoctor(h)
		} else {
			_, err = dataArgs("doctor", h)
		}
	case "backup", "status", "logs", "stop":
		_, err = dataArgs(args[0], h)
	case "bundle":
		err = bundle(h)
	case "devkit":
		switch sub {
		case "verify":
			err = devkitVerify(h)
		case "merge":
			err = devkitMergeCommand([]string{"x", "--help"})
		default:
			err = devkit(h)
		}
	case "unpack":
		err = unpackCommand([]string{"x", "--help"})
	case "verify":
		err = verifyCommand([]string{"x", "--help"})
	case "release":
		if sub == "upload" {
			err = uploadCommand(h)
		} else {
			err = releaseCommand(h)
		}
	case "changes":
		if sub == "export" {
			err = changesCommand([]string{"export", "--help"})
		} else {
			fmt.Fprint(helpOut, changesHelp)
		}
	case "keygen":
		err = keygenCommand([]string{"site", "--help"})
	case "trust":
		err = trustCommand([]string{"add-site-key", "x", "--help"})
	case "runtime":
		if sub == "attach" {
			_, err = parseRuntimeAttach(h)
		} else {
			fmt.Fprint(helpOut, runtimeHelp)
		}
	case "deploy":
		_, err = parseDeploy(h)
	case "adopt":
		_, err = parseAdopt(h)
	case "rollback":
		err = rollbackCommand(h, siteInvocation{})
	case "history":
		err = historyCommand(h, siteInvocation{}, helpOut)
	case "deps":
		switch sub {
		case "status", "fetch", "baseline":
			err = depsCommandWith([]string{sub, "--help"}, siteInvocation{}, helpOut)
		case "import":
			err = depsCommandWith([]string{"import", "x", "--help"}, siteInvocation{}, helpOut)
		default:
			fmt.Fprint(helpOut, depsHelp)
		}
	case "client":
		switch sub {
		case "build":
			err = clientBuildCommand([]string{"--help"}, siteInvocation{}, helpOut)
		default:
			fmt.Fprint(helpOut, clientHelp)
		}
	default:
		return false, nil
	}
	if errors.Is(err, flag.ErrHelp) {
		return true, nil
	}
	return true, err
}

const changesHelp = `用法：./auriga changes export --out <补丁.tar>
      ./auriga changes apply <补丁.tar>
现场源码改动的导出与应用（在源码目录运行）。
  export  把现场对源码的改动导出为补丁归档（--out 必填，必须在源码目录之外）；
          用 ./auriga changes export --help 查看参数。
  apply   把补丁归档应用到当前源码目录（补丁基线与当前源码版本须匹配）。
`

const depsHelp = `用法：./auriga deps status [--json] [--source <目录>] [--config <site.json>] [--client]
      ./auriga deps fetch [--accept-new-deps] [--source <目录>] [--config <site.json>] [--client]
      ./auriga deps import <目录或归档> [--trust-fingerprint <指纹> | --trust-state] [--source <目录>] [--config <site.json>] [--client]
      ./auriga deps baseline --client [--json] [--source <目录>] [--config <site.json>]
站点依赖缓存。Linux 服务端需要 sudo/root，配置读 /etc/auriga/site.json（root 所有、0600 或 0644）。
Windows 客户端加 --client（或 baseline --client）：只读固定的 %ProgramData%\Auriga\site.json，要求它与所在目录、deps.cache 由 Administrators/SYSTEM 所有、普通用户无写（上层祖先不得让普通用户删除或替换子项），须在提权的管理员终端运行；转调 scripts/client-release/site-deps.mjs。
  客户端套件：deps.kits 只用带 trustFingerprint 的发行签名套件（trustState 不适用）；import --client 只接受 --trust-fingerprint。
依赖输入按生态（go、web、java、python、system）的锁摘要整套选取：可写缓存 deps.cache → deps.kits 套件 → 依赖源。
  status  逐生态比较当前源码的锁摘要，列出命中位置（缓存或哪个套件）或缺失及补法；--client 查客户端输入。
  fetch   按规则准备缺失且允许自动补的生态；--client 联网写入现场客户端套件（files/npm 源、allowPublic 规则同配置）。
  import  校验目录或归档后导入缓存；--client 导入客户端输入。
  baseline --client  登记本机系统工具（csc/tar/PowerShell）现场基线；Windows 更新导致摘要变化后需重新登记。
经 sudo 调用时拒绝 --config 与 --source；root 直接运行时可用。各子命令用 --help 查看参数。
`

const runtimeHelp = `用法：./auriga runtime attach --bundle <目录> --root <root.pb> --image <repo@sha256:...> [--data <数据目录>] [--no-restart]
对已安装且尚未登记 DSH 运行时的站点受控追加运行时（需要 root）。
  attach  先用 runtime-release verify 校验，再通过 auriga-nas-bootstrap 写入独立的 config/runtime-source，
          不修改已封存的 config/materials 及 bootstrap.receipt，并持久化快照与 state.json。
          已有 runtime 的站点不能用 attach 换根，请走 runtime-upgrade。
`

const clientHelp = `用法：./auriga client build --version <SemVer> [--channel stable|beta] [--sequence <整数>] [--source <源码根>]
Windows 客户端薄壳（设计 D13）。读 site.json 的 client 段与 deps.cache/kits，解析现场套件后调用同一套 auriga-client 离线构建。
  --version 必填；sequence 取 client.platformOrigin 公开 /api/resource/v1/desktop/latest（stable、beta）可见最大序号 +1，
  各渠道 404 时从 1 起，其他错误直接失败；已撤回的发行不可见，必要时用 --sequence 指定更大的值。
  只产出待签目录，并提示下一步人工 auriga-client sign --review-only；不自动签名。
主入口仍是 scripts/client-release/auriga-client.cmd；本命令为可选薄壳。
`

func printTopHelp(w io.Writer) {
	fmt.Fprint(w, `Auriga 离线部署工具
用法：./auriga <命令> [参数]
查看某个命令的参数和默认值：./auriga <命令> --help（或 ./auriga help <命令>）

部署（需要 sudo/root）
  install   首次安装；重复执行会复核配置并继续启动，同时刷新安装摘要
  upgrade   用新离线包升级（先自动备份，只信任 state 固定的公钥；新发行写入 auriga-releases/<序号>/ 并记入历史）
  doctor    部署体检；doctor --build 检查本机离线构建资源（无需 root）
  status    查看服务状态
  logs      查看最近 100 行服务日志
  stop      停止服务
  backup    停止服务并做一致性备份，写入数据目录同级的 auriga-backups

校验与解包
  verify    校验发行附件（目录、归档或分卷）的完整性与签名
  unpack    校验并把安装包、首卷或 parts.json 解到全新目录

现场开发（在源码目录运行）
  devkit verify   校验源码依赖套件是否覆盖当前源码
  devkit merge    把增量套件合并进已解包的累积套件
  changes export  把现场源码改动导出为补丁（changes apply 应用补丁）
  bundle          bundle --build --offline 在现场断网构建离线包；--online 则对变化的语言依赖联网收集（均须 --sign-key）
  keygen site     生成站点构建密钥对（私钥只留在受控开发机）
  trust add-site-key|remove-site-key   在服务器登记或移除站点构建公钥（需要 root）
  trust add-release-key               为发行钥未钉定的站点（deploy 首装）钉定发行公钥（需要 root）

站点构建（需要 root，读 /etc/auriga/site.json）
  deps status|fetch|import   依赖缓存：看缺什么、按配置的依赖源补 Go/Web 依赖、导入手工缓存或套件
  deps … --client / baseline --client  Windows 客户端依赖与系统工具基线（转调 site-deps.mjs）
  deploy [<提交号>]          本机全量构建并部署（无 state 时首次安装）；失败且数据未被触碰时自动回滚
  runtime attach             对已安装站点受控追加 DSH 运行时（写入独立 config/runtime-source，不破坏原收据）
  adopt --stack-env … --deploy-nas …  接管已有 deploy-nas 站点（建立 state，不重启；不在 sudo 工作流白名单）
  rollback [--to <序号>]     退回保留的历史发行（deploy、upgrade 与 adopt 产生的都可以；数据须与目标运行时相同）
  history                    列出部署历史与当前发行
  经 sudo 调用（工作流）：运行器账户（site.json 的 server.runner）只能 deploy <提交号>、rollback、status、
  history 且参数受白名单限制，deploy 只允许快进；其他管理员经 sudo 运行 deploy/rollback 需加 --operator；
  有 site.json 时经 sudo 的 status 不收参数（数据目录取 server.data）；经 sudo 的 adopt 同样需 --operator。
  sudoers 片段 deploy/site/sudoers/auriga-runner；示例工作流与运行器安装说明 deploy/site/workflows/。
  status 会同时显示安装来源与当前发行；deploy/rollback/adopt 每次写 <logs.dir>/<时间>-<命令>.log 与 .json 收据，
  退出码 0 成功、1 未切换就失败、2 已自动回滚、3 需人工；--json 标准输出只打印脱敏收据

Windows 客户端
  client build    读 site.json 映射为 auriga-client 离线构建（--version 必填；sequence 从平台账本取）

维护者
  release         生成发行附件（标签、签名、Windows 与 DSH 输入见 docs/release/maintainer.zh.md）
  release upload  把发行附件上传到 Forgejo
  devkit          生成累积开发套件（--out、--sign-key，并给 --previous-devkit 或 --new-baseline）

提示
  - verify/unpack/install 必须 --verify-key <批准公钥> 或 --trust-fingerprint <独立核对的 SHA256(SPKI)>；缺签名默认拒绝。
  - 站点构建流程：keygen site --out <私密目录> → 服务器 trust add-site-key <pem> --confirm-fingerprint <sha> → bundle --build --offline --sign-key <私钥>。
  - Windows：auriga.exe 随发行附件分发（见 SHA256SUMS）；SmartScreen 提示见 DOWNLOADS.txt / README-OFFLINE.md。
`)
}

var helpSpecs = map[string]helpSpec{}

func init() {
	trust := map[string]string{
		"verify-key":        "独立渠道批准的发行公钥 PEM 路径",
		"trust-fingerprint": "独立核对过的发行公钥指纹：SHA256(SPKI DER)，64 位小写十六进制",
		"allow-unsigned":    "允许缺少签名的发行包（仅限受控演练，会持续报警，不推荐生产使用）",
	}
	merge := func(a, b map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range a {
			out[k] = v
		}
		for k, v := range b {
			out[k] = v
		}
		return out
	}
	dataDoc := map[string]string{"data": "数据目录；实际使用 <目录>/auriga-data（目录名已以 auriga- 开头且至少三层时直接使用）。须为规范绝对路径"}
	helpSpecs["install"] = helpSpec{
		usage:   "./auriga install (--address <IPv4> | --domain <域名> | --proxy <access.json>) [参数]",
		summary: "安装 Auriga 离线部署：校验发行包、载入镜像、生成密钥与配置并启动核心服务。需要 sudo/root。",
		notes: []string{
			"访问方式 --address、--domain、--proxy 三选一（必填其一）；首次安装还需 --verify-key 或 --trust-fingerprint。",
			"安装完成后会列出必须立即保管的材料（应急账号、资源公钥根、降级私钥等），并写入 <数据目录>/INSTALL-SUMMARY.txt（不含密钥内容）。",
		},
		docs: map[string]string{
			"address":                     "访问地址：服务器可达的 IPv4（与 --domain、--proxy 三选一，必填其一）",
			"domain":                      "访问域名，例如 example.com（与 --address、--proxy 三选一）",
			"proxy":                       "已批准的 access.json 路径，用于反向代理等自定义访问（与 --address、--domain 三选一）",
			"data":                        dataDoc["data"],
			"verify-key":                  trust["verify-key"] + "（首次安装需与 --trust-fingerprint 至少给一个）",
			"trust-fingerprint":           trust["trust-fingerprint"] + "（首次安装需与 --verify-key 至少给一个）",
			"allow-unsigned":              trust["allow-unsigned"],
			"package":                     "离线包 tar、首卷或 parts.json 路径；省略时使用 ./auriga 所在的发行包目录",
			"desktop-public-key":          "桌面发布公钥 PEM（Ed25519）；须与 --desktop-publisher-tenant、--desktop-confirm-fingerprint 一并提供",
			"desktop-publisher-tenant":    "桌面发布者租户标识；三个 desktop 参数须一并提供",
			"desktop-confirm-fingerprint": "桌面公钥的 SHA256(SPKI DER) 64 位小写指纹，须独立核对后填写；三个 desktop 参数须一并提供",
		},
	}
	helpSpecs["upgrade"] = helpSpec{
		usage:   "./auriga upgrade <新离线包.tar> [参数]",
		summary: "用新离线包升级已安装的服务（先自动备份；只信任 state 固定的公钥）。需要 sudo/root。新离线包路径必填，放在参数之前。",
		notes: []string{
			"签名包安装与 deploy 建立的站点都可以 upgrade：新发行写入 auriga-releases/<序号>/（占用新序号，记一条 upgrade 历史），镜像加打 auriga/<服务>:r<序号>，切换当前发行；之后 deploy 与 rollback 都能接着用。",
			"旧布局的签名包安装第一次 upgrade 前，当前安装包会先登记为一条 install 历史（之后可以 rollback 回它）。",
			"冷备份语义不变：先冷备份整站，失败从冷备份恢复并删除本次写入的发行目录。",
			"deploy 首装没有钉定发行钥的站点（status 显示“发行钥未钉定”），第一次 upgrade 前先 trust add-release-key 或 trust add-site-key。",
		},
		docs: map[string]string{
			"data":           dataDoc["data"],
			"allow-unsigned": "允许缺少签名的升级包（仅限受控演练，会持续报警）",
		},
	}
	for _, c := range []struct{ name, summary string }{
		{"doctor", "部署体检：检查服务、管理台与资源密钥状态。需要 sudo/root。另有 doctor --build 检查构建资源。"},
		{"backup", "停止服务并做一致性备份，写入数据目录同级的 auriga-backups。需要 sudo/root。"},
		{"status", "查看服务状态（deploy 安装的站点同时显示安装来源与当前发行）。需要 sudo/root。"},
		{"logs", "查看最近 100 行服务日志（带时间戳）。需要 sudo/root。"},
		{"stop", "停止服务。需要 sudo/root。"},
	} {
		helpSpecs[c.name] = helpSpec{usage: "./auriga " + c.name + " [参数]", summary: c.summary, docs: dataDoc}
	}
	buildDocs := map[string]string{
		"memory-mib": "每个构建工具容器的内存上限（MiB），并为系统保留 1GiB",
		"cpus":       "每个构建工具容器的 CPU 上限",
	}
	helpSpecs["doctor --build"] = helpSpec{
		usage:   "./auriga doctor --build [参数]",
		summary: "诊断本机是否有足够的 Docker 内存/CPU 做离线构建。",
		docs:    buildDocs,
	}
	helpSpecs["bundle"] = helpSpec{
		usage:   "./auriga bundle --build (--offline | --online) --sign-key <站点构建私钥> [参数]",
		summary: "在源码目录用已解包的开发套件构建现场离线包。--offline 完全断网，源码依赖锁须与套件一致；--online 在改了 Go/Web/Java 依赖锁后，只对变化的生态联网收集新增依赖（在套件固定 digest 的工具镜像里运行，源码只读，Go 仍由 go.sum 与 GOSUMDB 校验，Web 由 pnpm-lock integrity 校验），再断网构建，并打印/写入新增依赖清单（安装包 site-dependencies.json，含 SHA-256），doctor 会标注含现场依赖。构建必须 --sign-key（先 keygen site，并在服务器 trust add-site-key 登记）。",
		notes: []string{
			"--online 一期不支持系统包与 Dockerfile 基础镜像变化（会明确报错，请联系维护方）；--online 与 --offline 互斥。",
			"联网收集会透传已设置的 HTTP_PROXY/HTTPS_PROXY/NO_PROXY、GOPROXY、npm_config_registry、AURIGA_MAVEN_MIRROR_URL（Maven 镜像地址）；未设置则不传。",
			"Java 依赖由 services/maven-deps.sha256 钉定（prepare/build 核对）；AURIGA_MAVEN_MIRROR_URL 只接受 https 地址。Python（operator）由 requirements 的 --hash=sha256 钉定。",
		},
		docs: merge(buildDocs, map[string]string{
			"release":  "发行索引 JSON 路径",
			"out":      "输出离线包 tar 路径；留空时为 auriga-offline-<版本>.tar",
			"build":    "从源码构建（--offline 或 --online 必须与它同用）",
			"offline":  "完全断网构建，须与 --build 同用，与 --online 互斥",
			"online":   "套件基础上对改了依赖锁的语言生态联网收集依赖后构建，须与 --build、--sign-key 同用，与 --offline 互斥",
			"version":  "离线包版本号，例如 0.2.0-site.3",
			"sign-key": "已登记的站点构建私钥（--offline/--online 构建必填）；私钥须妥善保管，丢失后需重新生成并登记",
			"install":  "构建后先备份再 upgrade 到本机（仅 Linux，需 --build 加 --offline 或 --online，且需 root）",
			"data":     dataDoc["data"] + "；仅 --install 使用",
		}),
	}
	helpSpecs["devkit"] = helpSpec{
		usage:   "./auriga devkit --out <输出> --sign-key <私钥> (--previous-devkit <上版套件目录> | --new-baseline) [参数]",
		summary: "维护者：生成累积开发套件及增量包。现场校验与合并请用 devkit verify / devkit merge。",
		docs: map[string]string{
			"release":          "发行索引 JSON 路径",
			"out":              "输出路径（必填）",
			"previous-devkit":  "上一版完整累积套件目录（与 --new-baseline 二选一）",
			"delta-out":        "增量包输出路径",
			"sign-key":         "发行签名私钥（必填，需永久保管）",
			"new-baseline":     "以本次为新基线（没有上一版套件时使用，与 --previous-devkit 二选一）",
			"max-devkit-bytes": "开发套件总大小上限（字节）",
		},
	}
	helpSpecs["devkit verify"] = helpSpec{
		usage:   "./auriga devkit verify [<套件目录>] [参数]",
		summary: "校验源码依赖套件是否覆盖当前源码（套件目录可省略，默认取已绑定的套件）。",
		docs: map[string]string{
			"version":    "要核对的上游版本；留空使用源码自身版本",
			"source":     "源码目录",
			"verify-key": "独立批准的发行公钥 PEM 路径",
		},
	}
	helpSpecs["devkit merge"] = helpSpec{
		usage:   "./auriga devkit merge <增量tar或首卷> [参数]",
		summary: "把增量开发套件合并进已解包的累积套件。增量包路径必填，放在参数之前。",
		docs: map[string]string{
			"kit":        "已解包的累积套件目录；留空时使用当前源码绑定的套件",
			"verify-key": "独立批准的发行公钥 PEM 路径",
		},
	}
	helpSpecs["changes export"] = helpSpec{
		usage:   "./auriga changes export --out <补丁.tar>",
		summary: "把现场对源码的改动导出为补丁归档（在源码目录运行）；应用补丁用 changes apply <补丁.tar>。",
		docs:    map[string]string{"out": "补丁归档路径（必填，须在源码目录之外）"},
	}
	helpSpecs["keygen site"] = helpSpec{
		usage:   "./auriga keygen site [--out <全新私密目录>]",
		summary: "生成站点构建密钥对。私钥只留在受控开发机并妥善保管；只把公钥拷到服务器，用 trust add-site-key 登记。",
		docs:    map[string]string{"out": "输出目录，必须是全新目录；私钥保存在其中，请保管好"},
	}
	helpSpecs["trust"] = helpSpec{
		usage:   "./auriga trust add-site-key <公钥.pem> --confirm-fingerprint <指纹> [--data <目录>]\n      ./auriga trust remove-site-key <指纹> [--data <目录>]\n      ./auriga trust add-release-key <发行公钥.pem> --confirm-fingerprint <指纹> [--data <目录>]",
		summary: "登记或移除站点构建公钥（需要 sudo/root）。不能移除安装时固定的发行公钥。add-release-key 只用于发行钥未钉定的站点（deploy 首装没有带 trustFingerprint 的套件），已钉定的不能替换。",
		docs: map[string]string{
			"data":                dataDoc["data"],
			"confirm-fingerprint": "add-site-key、add-release-key 必填：公钥的 SHA256(SPKI DER) 64 位小写指纹，须从独立渠道（keygen site 输出或发行说明）核对",
		},
	}
	depsConfig := "站点配置路径；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认读 /etc/auriga/site.json）"
	depsSource := "源码目录；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认用配置的 source.dir）"
	clientFlag := "客户端模式：转调 scripts/client-release/site-deps.mjs，读写现场客户端套件（Windows 可用）"
	helpSpecs["deps status"] = helpSpec{
		usage:   "./auriga deps status [--json] [--source <目录>] [--config <site.json>] [--client]",
		summary: "逐生态（go、web、java、python、system）比较当前源码锁摘要，列出命中位置（缓存或哪个套件）或缺失及可用补法，并列出工具镜像、基础镜像。服务端需要 sudo/root；--client 走客户端依赖。",
		docs:    map[string]string{"config": depsConfig, "source": depsSource, "json": "只输出机器可读 JSON", "client": clientFlag},
	}
	helpSpecs["deps fetch"] = helpSpec{
		usage:   "./auriga deps fetch [--accept-new-deps] [--source <目录>] [--config <site.json>] [--client]",
		summary: "按配置的依赖源准备缺失且允许自动补的生态（Go、Web；锁/哈希就绪后的 Java、Python），在固定 digest 的工具镜像（Python 用 base-operator）里运行源码中的 <生态>-inputs.sh prepare，写回可写缓存并记录来源与 SHA-256。--client 时联网准备现场客户端套件。",
		notes: []string{
			"allowPublic=false 时 Go 不回落 proxy.golang.org，校验库只经配置的代理，取不到就失败；Web 拒绝没有 integrity 的 lock 条目。",
			"Java 须 services/maven-deps.sha256 非空且配置 deps.sources.maven 或 allowPublic；Python 须 requirements 全部 --hash=sha256 且配置 deps.sources.pypi 或 allowPublic。系统包、基础镜像与工具镜像只能 deps import 或套件提供。",
			"acceptNewDeps=false（且未加 --accept-new-deps）时只打印新增依赖清单并以退出码 1 结束。deps.caFile 会只读挂进准备容器。",
			"--client：files 源映射构建 Node/CPython/DSH/LibreOffice 等文件输入（先 <files>/<URL 路径> 再 <files>/<文件名>，路径按百分号解码），摘要仍按锁核对；pnpm/插件 npm 走 deps.sources.npm。allowPublic=false 时 files 与 npm 源缺一即失败。",
		},
		docs: merge(buildDocs, map[string]string{
			"config":          depsConfig,
			"source":          depsSource,
			"accept-new-deps": "本次允许拉取新增依赖（等同配置 deps.acceptNewDeps=true，仅限可自动补的生态）",
			"client":          clientFlag,
		}),
	}
	helpSpecs["deps import"] = helpSpec{
		usage:   "./auriga deps import <目录或归档> [--trust-fingerprint <指纹> | --trust-state] [--source <目录>] [--config <site.json>] [--client]",
		summary: "校验目录或 tar 归档后按锁摘要导入可写缓存并记录来源。缓存布局：根 SHA256SUMS（覆盖全部文件，可带 SHA256SUMS.sig 与 release-public.pem）、inputs/<生态>/<锁摘要>/、images/<用途>/<digest>/image.json。也直接接受解包后的发行开发套件（devkit.json 或累积 devkit-catalog.json）。路径必填，放在参数之前。--client 导入客户端输入。",
		notes: []string{
			"导入源（目录或归档文件）必须 root 所有、组和其他用户不可写；U 盘等介质请先复制到这样的目录。",
			"缓存布局不加 --trust-fingerprint/--trust-state 时按人工导入处理：只校验完整性并打印条目供人工审核。导入 Java 条目须提供当前源码并对照 services/maven-deps.sha256，构件不在锁内或摘要不符则拒绝。",
			"发行开发套件必须用 --trust-fingerprint 或 --trust-state 核验发行签名（拒绝未签名与套件自带公钥自证）；按当前源码各生态锁摘要在套件各版本里取输入，写入 inputs/<生态>/<锁摘要>/，并导入最新版本的镜像（带归档与镜像 ID）。",
		},
		docs: map[string]string{
			"config":            depsConfig,
			"source":            "源码目录（只有导入发行开发套件时用来算锁摘要）；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认用配置的 source.dir）",
			"trust-fingerprint": "独立核对过的签名公钥指纹（SHA256:<64 位小写十六进制> 或裸十六进制）；给出时必须签名有效且指纹匹配",
			"trust-state":       "用 state 已钉定的发行/站点公钥核验签名（需要配置 server.data）；与 --trust-fingerprint 二选一",
			"client":            clientFlag,
		},
	}
	helpSpecs["deps baseline"] = helpSpec{
		usage:   "./auriga deps baseline --client [--json] [--source <目录>] [--config <site.json>]",
		summary: "登记本机系统工具（csc.exe、System32 tar、PowerShell 5.1 等）的现场基线到可写缓存；构建时摘要须匹配发行清单或该基线。仅 --client。",
		notes: []string{
			"Windows 更新导致系统工具变化时，重新运行本命令；不能用基线绕过非系统工具的摘要。",
		},
		docs: map[string]string{"config": depsConfig, "source": depsSource, "json": "只输出机器可读 JSON", "client": "必填：客户端系统工具基线"},
	}
	helpSpecs["client build"] = helpSpec{
		usage:   "./auriga client build --version <SemVer> [--channel stable|beta] [--sequence <整数>] [--source <源码根>]",
		summary: "Windows 客户端薄壳：读 site.json，把 client 段与 deps.cache/kits 映射为 auriga-client build --offline，sequence 从平台公开桌面账本自动取。",
		notes: []string{
			"--version 必填。基础输入来自现场缓存（deps fetch/import --client），以写入缓存时记录的清单摘要（site-dependencies.json）走 --site-kit --devkit-sha256；deps.kits 套件先按其 trustFingerprint 验签。",
			"client.verifyKey/trustFingerprint 始终传入以核验签名源码。构建 Node 只用套件内 build-node/node.exe，执行前核对其摘要等于套件清单登记值，不用系统 Node。",
			"sequence = stable、beta 两渠道 latest 可见最大序号 +1（404 视为该渠道无发行，其他错误失败；只允许同源 https 重定向）。",
			"签名不自动化：结束后提示 auriga-client sign --review-only。主入口仍是 auriga-client.cmd。",
		},
		docs: map[string]string{
			"version":  "客户端版本（SemVer），必填",
			"channel":  "桌面渠道 stable 或 beta，默认 stable",
			"sequence": "手动指定发布序号（不小于账本可见最大序号 +1；用于已撤回发行导致的冲突）",
			"source":   "源码根（含 scripts/client-release）；默认 source.dir 或当前目录",
			"config":   "站点配置路径；Windows 不接受（只读固定的 %ProgramData%\\Auriga\\site.json）",
		},
	}
	helpSpecs["deploy"] = helpSpec{
		usage:   "./auriga deploy [<40 位提交号>] [--full | --services a,b] [--dry-run] [--json] [参数]",
		summary: "在本机用源码快照构建并部署（设计 docs/dev/site-deploy.zh.md D10）。需要 root：root 直接运行，或经 sudo 的受限调用（工作流）。",
		notes: []string{
			"经 sudo 调用（环境里有 SUDO_UID）：调用者是 site.json 登记的运行器账户 server.runner 时一律受限，不能用 --operator；其他账户（含 sudo -i 的管理员）默认也受限，显式 --operator 才按人工 root 处理（要求已登记 server.runner）。受限时只接受一个 40 位小写十六进制提交号与 --full、--services、--dry-run、--json、--backup-db，其余参数（含 --config、--source、--allow-downgrade、--operator）一律拒绝。",
			"快进：受限调用只允许部署当前运行提交的后代（在 root 自己 fetch 的仓库里 merge-base --is-ancestor，同一提交可重新部署）；首装不限。root 人工部署非后代提交须 --allow-downgrade，先做与 rollback 相同的数据检查（未触碰或 floor+FormatMax 通过，否则退出码 3）。",
			"源码：给了提交号就从 source.git 远端 fetch 到数据目录同级的 auriga-source.git，提交必须在某个 allowedRefs 上，git archive 导出（不读 root 的系统/全局 git 配置）；否则用 source.dir 或 --source（都须 root 所有、组和其他用户不可写）。",
			"签名：source.git.requireSignedCommits=true 时，导出前用 trustedSigners（root 所有的公钥文件：PGP 公钥块或 ssh 公钥行）在隔离环境里校验该提交的 gpg 或 ssh 签名，未签名、签名者不受信、x509 签名一律拒绝（退出码 1）；此时源码目录模式也拒绝。",
			"签名包安装的站点也可以 deploy：第一次 deploy 前当前安装包登记为一条 install 历史，切换失败时可自动回滚到它。adopt 接管的站点（source=adopted）同样可 deploy：无可信指纹/装配摘要时一律重建并切换，第一次通常走完整启动。",
			"配方：一律用源码快照里的 deploy/nas/offline-build（deploy-recipe.json 声明暂存清单、固定 digest 与 CLI 能力版本，能力版本不符时先升级 CLI）；Dockerfile 拒绝 syntax/escape 解析器指令（含 BOM、// 与 JSON 变体），FROM 只能是固定 digest、${<SVC>_BASE} 或已声明阶段，拒绝 ADD <URL> 与引用外部镜像的 --from；构建前比较源码锁清单与依赖输入。",
			"没有 state 时执行首次安装（复用 install 步骤，访问方式取 server.install）；有带 trustFingerprint 的套件就钉定发行钥，否则标记未钉定。已有 deploy-nas 站点用 adopt，不要对非空数据目录直接 deploy。",
			"输入指纹：Go 由源码配方 go-inputs.sh deps 在断网 tool-go 容器里 go list -deps 展开（每次都需要 Go 依赖输入与 tool-go 镜像），Web/Java 用 deploy/site/input-sets 的打包清单，另加依赖锁摘要、工具镜像与构建脚本；--dry-run 不展开 Go 依赖图，Go 产物按将重建列出。",
			"构建缓存（D14）：非 --full 时，Go 产物构建把 deps.cache/build/go/<tool-go digest>/ 以读写方式挂进容器作 GOCACHE（只加速，不改变 -trimpath 产物；模块仍每次从已校验依赖输入展开）；装配去掉 --no-cache，依赖 BuildKit 确定性层缓存。缓存只影响速度；怀疑异常时用 --full。收据与 --dry-run 记录是否使用及路径。bundle 的 assemble-image.sh 仍默认 --no-cache。",
			"再次部署：按输入指纹决定是否构建产物（指纹与当前发行一致则复用 auriga-releases/<序号>/artifacts/，复用前核对该发行目录与 state 锚定的 releaseSums，不过就改为重建）；构建后按装配摘要（产物树、基础镜像、系统输入、Dockerfile 目标段、暂存清单）决定是否换镜像，收据写明触发项。编排摘要未变且仅 edge/platform 变化时只换容器（跳过 panel-prepare、不重建 network-broker；broker 产物也变则完整启动，否则收据写混合版本；之后照 start.sh 重启 agent；有 collab-source 时以离线维护模式走 start-collab-network.sh，不直接 up collab；上一版无编排摘要按编排变化处理）；仅 operator 变化时只改 stack.env 并 preflight；agent/bifrost/编排变化或首装走完整启动（render → preflight → start）。新发行写入 auriga-releases/<序号>/（含 provenance.json、fingerprints.json、artifacts/、versions.txt 与 status 可读的发行标记），镜像加打 auriga/<服务>:r<序号>，保留 server.keepHistory 份（默认 3）。",
			"切换失败时：先停掉新版本会写数据的服务（platform、collab、edge 等），再读迁移与数据目录格式版本；与切换前相同（数据未触碰），或上一版不早于新发行的 schemaCompatFloor 且当前格式不高于上一版的 formatMax，并且上一版是成功记录、其发行目录与 state 锚定的 releaseSums 一致，才自动回滚（退出码 2；判定前已停掉全部写数据服务，故自动回滚一律完整启动上一版），否则退出码 3 交人工；首装失败退出码 1 并保留现场。",
			"每次写 <logs.dir>/<时间>-deploy.log 与同名 .json 收据（脱敏），--json 时标准输出只打印收据（工作流据此上传；logs.dir 仍仅 root 可读）。",
			"sudoers 与示例工作流：deploy/site/sudoers/auriga-runner、deploy/site/workflows/（须先登记 server.runner）。",
		},
		docs: merge(buildDocs, map[string]string{
			"full":            "强制重建全部产物与镜像（忽略输入指纹与装配摘要跳过）；同时不使用 Go 持久化 GOCACHE、装配带回 --no-cache（怀疑缓存污染时的人工兜底）。与 --services 互斥",
			"services":        "只处理这些镜像（逗号分隔：platform,edge,agent,bifrost,operator）；与已选服务共享输入集且指纹已变的服务会自动补上并提示；其余沿用当前；切换按编排摘要决定只换容器或完整启动；首次安装不可用",
			"dry-run":         "只打印计划：按指纹会构建/跳过哪些产物、目标镜像、走只换容器还是完整启动及原因、要补的依赖、镜像来源、迁移与格式版本、Go 构建缓存路径与装配层缓存是否启用；不构建、不切换",
			"json":            "标准输出只打印收据 JSON（进度写标准错误）",
			"backup-db":       "构建后、切换前对 auriga、若存在的 collab-db/audit 分别 pg_dump（不停服），写入 auriga-backups/db-<时间>/（根目录须 root 所有，转储 0600），收据记路径与 sha256；任一库失败则不切换（退出码 1）；不参与自动回滚",
			"config":          "站点配置路径；只允许 root 直接运行时使用（默认 /etc/auriga/site.json）",
			"source":          "人工源码目录（root 人工部署时使用，受限调用拒绝，不能与提交号同用）；默认用 source.dir 或 source.git",
			"operator":        "经 sudo 的人工 root 显式跳过参数白名单（运行器账户不可用，需 site.json 登记 server.runner）；不经 sudo 时无作用",
			"allow-downgrade": "root 人工向不是当前运行提交后代的提交部署（先做与 rollback 相同的数据检查，不通过退出码 3）；受限调用拒绝",
			"accept-new-deps": "本次允许按依赖源补新增依赖（等同 deps.acceptNewDeps=true，仅限可自动补的生态）",
		}),
	}
	helpSpecs["adopt"] = helpSpec{
		usage:   "./auriga adopt --stack-env <路径> --deploy-nas <目录> [--dry-run] [--json] [--config <site.json>] [--operator]",
		summary: "接管已用 deploy-nas 脚本部署的站点：只读核查 stack.env 与运行中容器镜像，建立 source=adopted 的 state 与 auriga-releases/<序号>/，打 :r<序号> 标签，不重启容器、不改数据目录与 stack.env，不伪造信任。需要 root：root 直接运行，或非运行器管理员经 sudo 加 --operator（不在工作流 sudo 白名单）。",
		notes: []string{
			"支持 AURIGA_INSTALL_MODE=host、没有 .oneclick 的站点（例如 NAS 测试栈经 deploy.mjs 部署的布局）。持统一站点锁。",
			"核对固定 compose 项目 auriga-nas-test（与 lib.sh 一致）里每个运行中容器的镜像 ID，含 collab overlay；常驻服务未运行、镜像与 stack.env 不一致、或 AURIGA_INSTALLER_IMAGE 与 POSTGRES 不一致时列出差异，退出码 3，不写任何内容。已有 .oneclick 时拒绝（退出码 1）。",
			"state 标记 trustUnpinned；第一次签名包 upgrade 前须先 trust add-release-key / add-site-key。access 直接引用 stack.env 的 AURIGA_ACCESS_FILE（不复制）。",
			"之后可 deploy：adopt 记录没有可信指纹/装配摘要，第一次 deploy（以及回滚到 adopt 版）一律完整启动；rollback 回 adopt 记录受 floor/格式检查（adopt 版无 floor 声明，数据已变则退出码 3；数据未触碰仍允许）。",
			"序号占 1，或接续已有 auriga-releases 的下一个。提交号只在 .auriga-release 发行标记（deploy-nas 内或其上一级）的镜像 ID 与运行镜像一致时采信，否则记未知；迁移版本读不到时不记迁移基线。",
			"--dry-run 只核查并打印将写入的 state/发行内容（序号、编排摘要、镜像、未知项），不写 state、发行目录或镜像标签。每次写日志与收据。",
		},
		docs: map[string]string{
			"stack-env":  "现场 stack.env 路径（须为 <数据目录>/stack.env）",
			"deploy-nas": "当前使用的 deploy-nas 目录（将被复制进不可变发行目录）",
			"dry-run":    "只核查并打印计划，不写 state、发行目录或镜像标签",
			"json":       "标准输出只打印收据 JSON",
			"config":     "站点配置路径（可选，只允许 root 直接运行时使用；用于日志目录；默认 /etc/auriga/site.json，不存在时用默认日志目录）",
			"operator":   "经 sudo 的非运行器管理员按人工 root 运行 adopt（需 site.json 登记 server.runner）；不经 sudo 时无作用",
		},
	}
	helpSpecs["rollback"] = helpSpec{
		usage:   "./auriga rollback [--to <序号>] [--json] [--config <site.json>]",
		summary: "把站点切回保留的历史发行（默认上一份成功的部署；deploy、upgrade、adopt 与签名包基线产生的历史都可以）；编排未变时按只换容器，否则完整启动并等待健康。需要 root：root 直接运行，或经 sudo 的受限调用。",
		notes: []string{
			"经 sudo 调用时与 deploy 同样判定受限：受限时只接受 --to <序号> 与 --json；非运行器账户加 --operator 按人工 root 处理。",
			"数据与目标运行时记录相同（未触碰）则执行；否则检查当前发行及历史里序号比目标大的每条发行的 schemaCompatFloor（目标提交不得更早；未声明则拒绝）与目标的 formatMax（当前格式不得超过）。缺声明或失败退出码 3。",
			"迁移版本相同不等于行数据语义相同；新版本运行越久，回滚后旧代码读错的风险越大。执行前请备份。",
			"回滚失败不重试，退出码 3。每次写日志与收据。",
		},
		docs: map[string]string{
			"to":       "目标序号（auriga history 查看）；默认当前之前最近一份成功的部署",
			"json":     "标准输出只打印收据 JSON",
			"config":   "站点配置路径；只允许 root 直接运行时使用（默认 /etc/auriga/site.json）",
			"operator": "经 sudo 的人工 root 显式跳过参数白名单（运行器账户不可用，需 site.json 登记 server.runner）；不经 sudo 时无作用",
		},
	}
	helpSpecs["history"] = helpSpec{
		usage:   "./auriga history [--json] [--config <site.json>]",
		summary: "列出保留的部署历史：序号、种类（deploy/upgrade/install/adopt）、状态、源码提交与快照摘要、是否声明 floor，* 标出当前发行。需要 root；经 sudo 的运行器只能加 --json。",
		docs: map[string]string{
			"json":   "输出机器可读 JSON",
			"config": "站点配置路径；只允许 root 直接运行时使用（默认 /etc/auriga/site.json）",
		},
	}
	helpSpecs["verify"] = helpSpec{
		usage:   "./auriga verify <文件或目录> [参数]",
		summary: "校验发行附件（目录、归档或分卷）的完整性与签名。路径必填，放在参数之前。",
		docs:    trust,
	}
	helpSpecs["unpack"] = helpSpec{
		usage:   "./auriga unpack <tar|part001|parts.json> --out <全新目录> [参数]",
		summary: "校验并自动解分卷，把发行包解到全新目录。安装包路径必填，放在参数之前。",
		docs:    merge(trust, map[string]string{"out": "解包目标目录（必填，须是全新目录）"}),
	}
	helpSpecs["release"] = helpSpec{
		usage:   "./auriga release --tag <版本> --sign-key <私钥> [参数]",
		summary: "维护者：生成发行附件（尚未上传；上传用 release upload）。Windows 与 DSH 输入、发布顺序见 docs/release/maintainer.zh.md。",
		docs: map[string]string{
			"tag":                   "发行版本标签（必填）",
			"release":               "发行索引 JSON 路径",
			"outdir":                "输出目录；留空为 .offline-releases/<标签>",
			"cache-dir":             "开发套件缓存目录",
			"sign-key":              "发行签名私钥（必填，需永久保管）",
			"windows-client":        "Windows 签名客户端输入 tar",
			"windows-client-sha256": "Windows 侧确认的完整 tar SHA256",
			"dsh-source":            "批准的 DSH 源码归档文件或 Forgejo HTTPS 附件 URL",
			"attachment-limit":      "附件分卷上限（字节）；0 发布完整归档，正数适用于受限传输介质",
			"build":                 "同时构建离线安装包",
			"source-only":           "只发布源码、开发套件和参考材料，安装包仅用于断网构建验证",
			"previous-devkit":       "上一版完整累积套件目录（与 --new-baseline 二选一）",
			"new-baseline":          "以本次为新基线开发套件",
			"max-devkit-bytes":      "开发套件总大小上限（字节）",
		},
	}
	helpSpecs["runtime attach"] = helpSpec{
		usage:   "./auriga runtime attach --bundle <目录> --root <root.pb> --image <repo@sha256:...> [--data <数据目录>] [--no-restart]",
		summary: "对已安装且尚未登记 DSH 运行时的站点受控追加运行时（写入独立的 config/runtime-source，不修改原始 config/materials 收据）。",
		docs: map[string]string{
			"bundle":     "已签名的运行时公开发布目录（必填）",
			"root":       "维护者独立批准的外部运行时根公钥 root.pb（必填）",
			"image":      "精确的运行时镜像 repo@sha256:<digest>（必填）",
			"data":       "数据目录或父目录",
			"no-restart": "仅登记并写入 runtime-source，不自动执行 render/start",
		},
	}
	helpSpecs["release upload"] = helpSpec{
		usage:   "./auriga release upload --server <地址> --repo <仓库> --dir <发行目录> [参数]",
		summary: "维护者：把发行附件上传到 Forgejo（令牌从环境变量 FORGEJO_TOKEN 读取，不在命令行传入）。",
		docs: map[string]string{
			"server":        "Forgejo 服务地址",
			"internal-http": "仅在受控内网直连 Forgejo HTTP（私有或回环 IP）",
			"repo":          "目标仓库（owner/name）",
			"dir":           "发行附件目录（release 的输出目录）",
			"verify-key":    "独立批准的发行公钥 PEM 路径",
			"execute":       "真正执行上传；不加时只做检查，不上传",
			"publish":       "上传完成后把草稿 Release 发布",
		},
	}
}
