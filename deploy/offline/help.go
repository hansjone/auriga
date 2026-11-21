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
	case "deploy":
		_, err = parseDeploy(h)
	case "rollback":
		err = rollbackCommand(h, siteInvocation{})
	case "history":
		err = historyCommand(h, siteInvocation{}, helpOut)
	case "deps":
		switch sub {
		case "status", "fetch":
			err = depsCommandWith([]string{sub, "--help"}, siteInvocation{}, helpOut)
		case "import":
			err = depsCommandWith([]string{"import", "x", "--help"}, siteInvocation{}, helpOut)
		default:
			fmt.Fprint(helpOut, depsHelp)
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

const depsHelp = `用法：./auriga deps status [--json] [--source <目录>] [--config <site.json>]
      ./auriga deps fetch [--accept-new-deps] [--source <目录>] [--config <site.json>]
      ./auriga deps import <目录或归档> [--trust-fingerprint <指纹> | --trust-state] [--source <目录>] [--config <site.json>]
站点依赖缓存（需要 sudo/root）。配置固定读 /etc/auriga/site.json（root 所有、0600 或 0644）。
依赖输入按生态（go、web、java、system）的锁摘要整套选取：可写缓存 deps.cache → deps.kits 套件 → 依赖源。
  status  逐生态比较当前源码的锁摘要，列出命中位置（缓存或哪个套件）或缺失及补法。
  fetch   按规则准备缺失且允许自动补的生态（只有 Go、Web；Java、系统包、Python、镜像只能 import 或套件）；
          acceptNewDeps=false 时只打印清单并以退出码 1 结束。
  import  校验目录或归档（inputs/<生态>/<锁摘要>/、images/<用途>/<digest>/ 布局，或解包后的发行开发套件）后导入缓存并记录来源。
deps.kits 与 import 都直接接受解包后的发行开发套件（devkit.json 或累积 devkit-catalog.json），按当前锁摘要在各版本里取输入。
deps.kits 套件目录、import 的源目录或归档、deps.caFile 都必须 root 所有、组和其他用户不可写（同 deps.cache）。
经 sudo 调用时拒绝 --config 与 --source；root 直接运行时可用。各子命令用 --help 查看参数。
`

func printTopHelp(w io.Writer) {
	fmt.Fprint(w, `Auriga 离线部署工具
用法：./auriga <命令> [参数]
查看某个命令的参数和默认值：./auriga <命令> --help（或 ./auriga help <命令>）

部署（需要 sudo/root）
  install   首次安装；重复执行会复核配置并继续启动，同时刷新安装摘要
  upgrade   用新离线包升级（先自动备份，只信任安装时固定的公钥）
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

站点构建（需要 root，读 /etc/auriga/site.json）
  deps status|fetch|import   依赖缓存：看缺什么、按配置的依赖源补 Go/Web 依赖、导入手工缓存或套件
  deploy [<提交号>]          本机全量构建并部署（无 state 时首次安装）；失败且数据未被触碰时自动回滚
  rollback [--to <序号>]     退回保留的历史发行（数据须与目标运行时相同；跨迁移回滚交人工）
  history                    列出部署历史与当前发行
  status 会同时显示安装来源与当前发行；deploy/rollback 每次写 <logs.dir>/<时间>-<命令>.log 与 .json 收据，
  退出码 0 成功、1 未切换就失败、2 已自动回滚、3 需人工

维护者
  release         生成发行附件（标签、签名、Windows 与 DSH 输入见 docs/release/maintainer.zh.md）
  release upload  把发行附件上传到 Forgejo
  devkit          生成累积开发套件（--out、--sign-key，并给 --previous-devkit 或 --new-baseline）

提示
  - verify/unpack/install 必须 --verify-key <批准公钥> 或 --trust-fingerprint <独立核对的 SHA256(SPKI)>；缺签名默认拒绝。
  - 站点构建流程：keygen site --out <私密目录> → 服务器 trust add-site-key <pem> --confirm-fingerprint <sha> → bundle --build --offline --sign-key <私钥>。
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
		summary: "用新离线包升级已安装的服务（先自动备份；只信任安装时固定的公钥）。需要 sudo/root。新离线包路径必填，放在参数之前。",
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
			"Java 依赖没有锁哈希钉定：Maven 只校验 Central 或镜像自己提供的校验和，不能防镜像篡改，镜像可信性由现场负责；AURIGA_MAVEN_MIRROR_URL 只接受 https 地址。请用 site-dependencies.json 里的 SHA-256 与 Maven Central 人工核对新增构件。",
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
		usage:   "./auriga trust add-site-key <公钥.pem> --confirm-fingerprint <指纹> [--data <目录>]\n      ./auriga trust remove-site-key <指纹> [--data <目录>]",
		summary: "登记或移除站点构建公钥（需要 sudo/root）。不能移除安装时固定的发行公钥。",
		docs: map[string]string{
			"data":                dataDoc["data"],
			"confirm-fingerprint": "add-site-key 必填：站点公钥的 SHA256(SPKI DER) 64 位小写指纹，须与 keygen site 输出核对",
		},
	}
	depsConfig := "站点配置路径；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认读 /etc/auriga/site.json）"
	depsSource := "源码目录；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认用配置的 source.dir）"
	helpSpecs["deps status"] = helpSpec{
		usage:   "./auriga deps status [--json] [--source <目录>] [--config <site.json>]",
		summary: "逐生态（go、web、java、system）比较当前源码锁摘要，列出命中位置（缓存或哪个套件）或缺失及可用补法，并列出工具镜像、基础镜像。需要 sudo/root。",
		docs:    map[string]string{"config": depsConfig, "source": depsSource, "json": "只输出机器可读 JSON"},
	}
	helpSpecs["deps fetch"] = helpSpec{
		usage:   "./auriga deps fetch [--accept-new-deps] [--source <目录>] [--config <site.json>]",
		summary: "按配置的依赖源准备缺失且允许自动补的生态（Go、Web），在固定 digest 的工具镜像里运行源码中的 <生态>-inputs.sh prepare，写回可写缓存并记录来源与 SHA-256。需要 sudo/root。",
		notes: []string{
			"allowPublic=false 时 Go 不回落 proxy.golang.org，校验库只经配置的代理，取不到就失败；Web 拒绝没有 integrity 的 lock 条目。",
			"Java（Maven）、系统包、Python（operator）、基础镜像与工具镜像只能 deps import 或套件提供。",
			"acceptNewDeps=false（且未加 --accept-new-deps）时只打印新增依赖清单并以退出码 1 结束。deps.caFile 会只读挂进准备容器。",
		},
		docs: merge(buildDocs, map[string]string{
			"config":          depsConfig,
			"source":          depsSource,
			"accept-new-deps": "本次允许拉取新增依赖（等同配置 deps.acceptNewDeps=true，仅限可自动补的生态）",
		}),
	}
	helpSpecs["deps import"] = helpSpec{
		usage:   "./auriga deps import <目录或归档> [--trust-fingerprint <指纹> | --trust-state] [--source <目录>] [--config <site.json>]",
		summary: "校验目录或 tar 归档后按锁摘要导入可写缓存并记录来源。缓存布局：根 SHA256SUMS（覆盖全部文件，可带 SHA256SUMS.sig 与 release-public.pem）、inputs/<生态>/<锁摘要>/、images/<用途>/<digest>/image.json。也直接接受解包后的发行开发套件（devkit.json 或累积 devkit-catalog.json）。路径必填，放在参数之前。需要 sudo/root。",
		notes: []string{
			"导入源（目录或归档文件）必须 root 所有、组和其他用户不可写；U 盘等介质请先复制到这样的目录。",
			"缓存布局不加 --trust-fingerprint/--trust-state 时按人工导入处理：只校验完整性并打印条目供人工审核。",
			"发行开发套件必须用 --trust-fingerprint 或 --trust-state 核验发行签名（拒绝未签名与套件自带公钥自证）；按当前源码各生态锁摘要在套件各版本里取输入，写入 inputs/<生态>/<锁摘要>/，并导入最新版本的镜像（带归档与镜像 ID）。",
		},
		docs: map[string]string{
			"config":            depsConfig,
			"source":            "源码目录（只有导入发行开发套件时用来算锁摘要）；只允许 root 直接运行时使用，经 sudo 调用时拒绝（默认用配置的 source.dir）",
			"trust-fingerprint": "独立核对过的签名公钥指纹（SHA256:<64 位小写十六进制> 或裸十六进制）；给出时必须签名有效且指纹匹配",
			"trust-state":       "用 state 已钉定的发行/站点公钥核验签名（需要配置 server.data）；与 --trust-fingerprint 二选一",
		},
	}
	helpSpecs["deploy"] = helpSpec{
		usage:   "./auriga deploy [<40 位提交号>] [--full | --services a,b] [--dry-run] [--json] [参数]",
		summary: "在本机用源码快照构建并部署（设计 docs/dev/site-deploy.zh.md D10）。需要 root 直接运行；经 sudo 调用与参数白名单在下一步开放。",
		notes: []string{
			"源码：给了提交号就从 source.git 远端 fetch 到数据目录同级的 auriga-source.git，提交必须在某个 allowedRefs 上，git archive 导出（不读 root 的系统/全局 git 配置）；否则用 source.dir 或 --source（都须 root 所有、组和其他用户不可写）。",
			"配置了 source.git.requireSignedCommits 或 trustedSigners 时本版本直接拒绝（退出码 1）：提交签名校验在统一现场部署 2a（下）实现。",
			"配方：一律用源码快照里的 deploy/nas/offline-build（deploy-recipe.json 声明暂存清单、固定 digest 与 CLI 能力版本，能力版本不符时先升级 CLI）；Dockerfile 拒绝 syntax/escape 解析器指令（含 BOM、// 与 JSON 变体），FROM 只能是固定 digest、${<SVC>_BASE} 或已声明阶段，拒绝 ADD <URL> 与引用外部镜像的 --from；构建前比较源码锁清单与依赖输入。",
			"没有 state 时执行首次安装（复用 install 步骤，访问方式取 server.install）；有带 trustFingerprint 的套件就钉定发行钥，否则标记未钉定。",
			"再次部署：全量构建，装配摘要与当前一致的镜像跳过切换，然后完整启动（render → preflight → start）；新发行写入 auriga-releases/<序号>/，镜像加打 auriga/<服务>:r<序号>，保留 server.keepHistory 份（默认 3）。",
			"切换失败时：先停掉新版本会写数据的服务（platform、collab、edge 等），再读迁移与数据目录格式版本，与切换前相同且上一版是成功记录才自动回滚（退出码 2），否则退出码 3 交人工；首装失败退出码 1 并保留现场。",
			"每次写 <logs.dir>/<时间>-deploy.log 与同名 .json 收据（脱敏），--json 时标准输出只打印收据。",
		},
		docs: merge(buildDocs, map[string]string{
			"full":            "强制重建全部镜像（不按装配摘要跳过），与 --services 互斥",
			"services":        "只构建这些镜像（逗号分隔：platform,edge,agent,bifrost,operator），其余沿用当前；切换仍完整启动；首次安装不可用",
			"dry-run":         "只打印计划：会构建哪些镜像、切换方式、要补的依赖、镜像来源、迁移与格式版本；不构建、不切换",
			"json":            "标准输出只打印收据 JSON（进度写标准错误）",
			"backup-db":       "部署前 pg_dump 各库（统一现场部署 2b 实现，本版本会拒绝）",
			"config":          "站点配置路径；只允许 root 直接运行时使用（默认 /etc/auriga/site.json）",
			"source":          "人工源码目录（root 直接运行时使用，不能与提交号同用）；默认用 source.dir 或 source.git",
			"accept-new-deps": "本次允许按依赖源补新增依赖（等同 deps.acceptNewDeps=true，仅限 Go、Web）",
		}),
	}
	helpSpecs["rollback"] = helpSpec{
		usage:   "./auriga rollback [--to <序号>] [--json] [--config <site.json>]",
		summary: "把站点切回保留的历史发行（默认上一份成功的部署），完整启动并等待健康。需要 root 直接运行。",
		notes: []string{
			"只有当前迁移与数据目录格式版本与目标运行时记录相同才执行；否则需要目标声明 schemaCompatFloor 与格式上限（统一现场部署 2b 生成），本版本一律交人工，退出码 3。",
			"这是 2b 之前的临时规则，有风险：迁移版本没变不等于数据没变，新版本运行期间可能写入了旧代码不认识的行数据或取值，回滚后旧代码可能读错或报错。新版本运行越久风险越大，执行前请先备份并确认这段时间的业务改动。",
			"回滚失败不重试，退出码 3。每次写日志与收据。",
		},
		docs: map[string]string{
			"to":     "目标序号（auriga history 查看）；默认当前之前最近一份成功的部署",
			"json":   "标准输出只打印收据 JSON",
			"config": "站点配置路径；只允许 root 直接运行时使用（默认 /etc/auriga/site.json）",
		},
	}
	helpSpecs["history"] = helpSpec{
		usage:   "./auriga history [--json] [--config <site.json>]",
		summary: "列出保留的部署历史：序号、状态、源码提交与快照摘要、是否声明 floor，* 标出当前发行。需要 root。",
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
