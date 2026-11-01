# 公共 CI 与本地复现

## Java

`java.yml` 在 Ubuntu/Windows、Temurin 25 上运行 Maven Wrapper 的 `verify`，缓存
Maven 仓库及 wrapper 发行包。覆盖契约生成、编译、PostgreSQL 17 测试、随机端口 health、ArchUnit、
Spotless。本地运行 `./mvnw -B verify`，Windows 为 `.\mvnw.cmd -B verify`。
详见 [Java 开发](java.md)。

`java.yml`、`e2e.yml`、`nas-bundle.yml` 共用 `.github/actions/maven`：
通过 `actions/cache` 缓存 `~/.m2/repository` 与 `~/.m2/wrapper/dists/**/*.zip`，key 包含 OS、
POM 与 wrapper 配置摘要。只缓存发行归档，避免直接执行缓存中的解压目录；每次恢复后由 wrapper
校验发行包并解压。`.mvn/wrapper/bootstrap.sh` 只对 `mvnw -B --version`
引导阶段最多尝试三次（间隔 2、4 秒），实际构建与测试失败立即返回。
两个 platform Dockerfile 同样先引导发行包，再执行原构建命令。

`.mvn/wrapper/maven-wrapper.jar` 为官方 3.3.4 二进制，不再在每次 CI/Docker 构建中下载。
入库时核对 Maven Central 官方 `.jar.sha1`，并使用
[Apache Maven KEYS](https://downloads.apache.org/maven/KEYS) 验证同址 `.jar.asc`：
签名主钥指纹 `88BE34F94BDB2B5357044E2E3A387D43964143E3`。
Maven 3.9.16 zip 另核对官方 `.zip.sha512`。
两个制品的 SHA-256 固定在 wrapper properties，Linux/Windows wrapper 每次运行都校验
jar，新发行包在执行前校验。升级时必须重新验证官方签名/摘要并同步 pin；缓存不能替代校验。

## Go 测试 PostgreSQL 制品

`go.yml` 在测试前运行 `.github/scripts/prepare-postgres.ps1`，按仓库中的
`postgres-artifacts.json` 固定摘要预取 Zonky PostgreSQL 17.5.0 Windows/Linux amd64 jar。
摘要源为对应 Maven Central 制品的官方 `.jar.sha256`。网络错误最多尝试三次，每次超时
120 秒，间隔 2、4 秒；校验失败直接拒绝，下载未完成的文件不进入正式缓存。
命中 `actions/cache` 后仍校验 jar，并从校验成功的 jar 重新提取 txz，避免信任独立的旧 txz。
缓存 key 包含 OS、架构和摘要清单，不缓存数据库数据或解压后的运行目录。
ctl 通过 `AURIGA_PG_CACHE_DIR` 复用归档；显式 CacheDir/`--cache-dir` 优先，
未配置仍使用各数据目录中的 `cache`。更新 ctl 内嵌版本时需同步清单。

本地验证下载逻辑与引导重试（不需要 Pester）：

```sh
node --test .github/scripts/bootstrap.test.mjs
pwsh -NoProfile -File .github/scripts/artifacts.test.ps1
```

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
