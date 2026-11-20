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
  bundle          bundle --build --offline 在现场断网构建离线包（必须 --sign-key）
  keygen site     生成站点构建密钥对（私钥只留在受控开发机）
  trust add-site-key|remove-site-key   在服务器登记或移除站点构建公钥（需要 root）

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
		{"status", "查看服务状态。需要 sudo/root。"},
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
		usage:   "./auriga bundle --build --offline --sign-key <站点构建私钥> [参数]",
		summary: "在源码目录用已解包的开发套件断网构建现场离线包。离线构建必须 --sign-key（先 keygen site，并在服务器 trust add-site-key 登记）。",
		docs: merge(buildDocs, map[string]string{
			"release":  "发行索引 JSON 路径",
			"out":      "输出离线包 tar 路径；留空时为 auriga-offline-<版本>.tar",
			"build":    "从源码构建（--offline 必须与它同用）",
			"offline":  "断网构建，须与 --build 同用",
			"version":  "离线包版本号，例如 0.2.0-site.3",
			"sign-key": "已登记的站点构建私钥（离线构建必填）；私钥须妥善保管，丢失后需重新生成并登记",
			"install":  "构建后先备份再 upgrade 到本机（仅 Linux，需 --build --offline，且需 root）",
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
