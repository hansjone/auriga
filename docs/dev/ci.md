# 公共 CI 与本地复现

## Java

`java.yml` 在 Ubuntu/Windows、Temurin 25 上运行 Maven Wrapper 的 `verify`，缓存
Maven 仓库。覆盖契约生成、编译、PostgreSQL 17 测试、随机端口 health、ArchUnit、
Spotless。本地运行 `./mvnw -B verify`，Windows 为 `.\mvnw.cmd -B verify`。
详见 [Java 开发](java.md)。

## 契约

`contracts.yml` 安装固定版 Buf，运行 `buf lint`、`buf breaking`。基线是
origin/main 的 contracts；仅当 main 尚无契约基线时跳过 breaking，真实兼容性错误
不会被忽略。检出完整历史。

Node 22.19.0 与 `npm ci` 使用 contracts 模块独立的 package-lock。
Redocly 按只读的 `contracts/redocly.yaml` 检查 iam、org、workspace、远程连接器四份
OpenAPI。Ajv 2020-12 保持 `strict: true`，并显式 `allowUnionTypes: true` 以支持契约
中合法的 string/boolean 联合类型；保留其他严格检查。安装标准格式插件，编译所有
schema 和 `$defs`，用文件 URI 保持跨文件相对引用。

```sh
cd contracts
buf lint
buf breaking --against '../.git#branch=origin/main,subdir=contracts'
cd ../services/contracts
npm ci --ignore-scripts --no-audit --no-fund --cache target/npm-cache
npm run lint-openapi
npm run check-schemas
npm run bundle
```

Windows npm.cmd 无法启动时，可在 `services/contracts` 用构建安装的 Node：
`target/node/node.exe target/node/node_modules/npm/bin/npm-cli.js run check-schemas`。
源契约 lint/strict 错误需报告给维护者，不静默跳过。

## 敏感信息

`security.yml` 直接下载官方 Gitleaks **8.30.1** 并校验官方 SHA-256 清单，不使用
需要许可证的 gitleaks-action。扫描完整 git 历史和工作目录。
`.gitleaks.toml` 扩展默认规则，加 RFC 1918 IPv4 规则；loopback 与 RFC 5737 文档地址
不匹配。排除本地 target/node_modules、git 内部数据和派活文件；真实源码、文档均扫描。

下载当前系统的官方二进制，在根目录运行：

```sh
gitleaks dir --redact .
gitleaks git --redact --config .gitleaks.toml .
```

不要删默认规则或放行真实地址。源码示例使用文档域名和地址，真实配置通过环境变量
提供。历史扫描若发现早期提交问题，需仓库所有者单独处理。

## CLA

`cla.yml` 使用 `contributor-assistant/github-action@v2.6.1`，监听
pull_request_target 与签名/recheck 评论。此特权工作流不检出、不执行 PR 代码。
签名保存于本仓库 `cla-signatures` 分支的 `signatures/cla-v1.json`。
hansjone 和机器人免签；同仓库使用 GITHUB_TOKEN，无需跨仓库 PAT。

所有者需先建 `cla-signatures` 分支，允许工作流写入；**不要预建签名 JSON**，由
action 创建。为主分支启用 CLA 状态检查。CLA.md 是英文草稿，接受外部贡献前须完成
审阅并决定版本；改协议版本时使用新的签名路径并重新收集同意。
GitHub 事件、签名写入与权限配置只能在实际仓库验证。

## 依赖与工作流语法

`dependabot.yml` 每周检查 Maven 多模块和 GitHub Actions。契约 npm lock 独立维护。
可用 actionlint 检查：

```sh
actionlint .github/workflows/java.yml .github/workflows/contracts.yml .github/workflows/security.yml .github/workflows/cla.yml
```

Go、前端和插件的工作流由 K1b 维护。

参考：[CLA Assistant Lite](https://github.com/contributor-assistant/github-action)、
[Gitleaks](https://github.com/gitleaks/gitleaks)。
