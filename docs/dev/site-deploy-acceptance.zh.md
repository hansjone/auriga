# 统一现场部署验收矩阵（第 11 步）

对应设计 `docs/dev/site-deploy.zh.md` §5 第 11 步与 D5–D8、D11–D14。  
环境：本机 WSL Docker（context=`default`，Server 29.1.3，Ubuntu 26.04.1，**非 NAS**）；客户端在本机 Windows。  
所有输出与收据均已脱敏；验收临时目录跑完清理。

## 环境与版本

| 项 | 值 |
|---|---|
| 日期 | 2026-11-22～23 |
| Docker | 29.1.3 / compose 2.40.3（WSL）；**注意** `/tmp` 为 3.9G tmpfs，大产物须放 `/var/tmp` |
| Go（Windows 测） | 本机工具链；已知失败项见下 |
| Go（Linux 测） | go1.27.0（WSL 临时） |
| Node | 本机 Windows Node 22.19.0 |
| forgejo-runner 源码 | v13.2.1（`code.forgejo.org/forgejo/runner`） |
| 钉定 JDK | `eclipse-temurin:25-jdk@sha256:b8705e936964c228dfce359e63dbf08f6c69cb380ed1d576b1f6b1064f15567d` |
| 钉定 Python 基镜 | `python:3.13.8-slim-bookworm@sha256:61169c2bdb8e6bb44a8dfad33f569d324d52f079fded9a204b322a6fb9c9f799` |

## A. 前序遗留

| 项 | 结果 | 说明 |
|---|---|---|
| A1 构建 Node zip 摘要 | 通过 | `winX64ZipSha256=ea3fad0e…`；nodejs.org + npmmirror 两源 `SHASUMS256.txt`；`prepare.mjs` 以仓库为准 |
| A2 Maven `distributionSha256Sum` | 通过 | `verify_maven_distribution` + `stage_verified_maven_distribution`（只拷已复核 zip 现解开，不信任预解压目录）；`TestVerifyMavenDistribution`；钉定 JDK 容器 `--network=none` 实测 `mvnw -B -o --version` 通过 |
| A3 钉定 JDK digest 复核锁 | **通过** | POM：`POM_RANGE=NONE`；钉定 digest 上 `prepare`+`hash-lock`；`LOCK_COMPARE=IDENTICAL`（1288 条）；**未改**仓库 `services/maven-deps.sha256` |
| A4 重建 java/python 输入 | **通过（本机，未发布）** | python 锁摘要 `83dffbda…`；java 锁摘要 `e38fd12a…`；布局见报告 |
| A5-1 `history --json` sudoers | 已实现 | 精确行 + 静态单测 |
| A5-2 rollback 信号 | 已实现 | 取消后仍写收据 |
| A5-3 deploy 收尾 ctx | 已实现 | `deployCleanupContext`（2m） |
| A5-4 运行器取消信号 | 已测 | host → 进程组 **SIGKILL** |
| A6 2d Docker / P5 / 降级 | 部分 | 单测覆盖；真 compose / P5 组合未做 → **保持** broker 变化走完整启动 |

## B. 服务端矩阵（WSL 隔离站点）

| 格 | 结果 | 命令/退出码/耗时/关键字段 |
|---|---|---|
| B1 源码+本地缓存 | 未做完 | java/python 输入已备；全量 go/web/基镜与真部署未串完 |
| B1 发行开发套件 | 未做 | 需主控提供带 `trustFingerprint` 的现有套件 |
| B1 现场套件 `deps fetch` | 未做 | 同上 |
| B2 全量首装 / 增量路径 | 单测覆盖路径判定 | 真 Docker 未跑完 |
| B3 退出码 2 / 3 | 单测通过 | |
| B4 离线缺依赖→import | 未做完 | |
| B5 工作流 | 取消见 A5-4；成功/回滚/并发未在一次性用户下跑完 | |
| B6 缓存两次部署 | 未做完 | |

## C. 客户端（Windows）

| 格 | 结果 | 说明 |
|---|---|---|
| C1 现场套件 prepare | **通过** | `prepare.mjs`（npmmirror/长超时）；`lockSha256=eac47448…`；`manifestSha256=43fe70c9…`；85920 文件 / ~1.12GiB；耗时 ~1029s |
| C1 发行 Windows 套件构建 | 未做 | 需带 `trustFingerprint` 的已签发行套件 |
| C1 `client build` 断网构建 | 未做完 | 缺独立批准的 `--verify-key` / `--release-public-key` / `--resource-trust-json`（及 `AURIGA_CLIENT_*`）；prepare 产物已保留 |
| C2 管理员 ProgramData 端到端 | 需用户 | `client-e2e-admin.ps1` |

## 单元/静态验证

| 命令 | 结果 |
|---|---|
| `gofmt -l .`（deploy/offline） | 干净 |
| `go vet` Windows / GOOS=linux / GOOS=windows | 通过 |
| `go test ./...` Windows | 仅 `TestReleaseCacheSignedPartsAndTagGate/source-only` 失败（main 既有） |
| Linux 全量（交叉编译 `offline.test`，WSL `root` + `nobody`） | 全部通过 |
| `node --test scripts/client-release/site-deps.test.mjs` | 12/12 |
| `node --test clients/installer/test/*.test.mjs` | 380 pass / 6 skip / 0 fail |
| 2c `TestCoverage*` | 通过 |

## 与设计不同之处

1. 自动回滚仍一律完整启动（P5 真组合未测通）。
2. 运行器 host 取消为进程组 **SIGKILL**。
3. 构建 Node zip 摘要仓库钉死。
4. 本机 `/tmp` tmpfs 过小：大 prepare 须用 `/var/tmp`（验收环境注意）。

## 清理

验收产生的本机临时路径：`/var/tmp/auriga-s11`（A3/A4 Java 输入与日志）、`/tmp/auriga-s11-a4-python-*`（A4 Python 输入）、`%TEMP%\auriga-s11-c1*`（C1 客户端套件与下载缓存），以及拉取的钉定 JDK/Python 镜像。确认留档后统一清理；C2 脚本在管理员终端跑完后会自动恢复或删除 `%ProgramData%\Auriga\site.json` 与临时目录。
