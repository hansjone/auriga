# 只从 Forgejo 获取构建输入

本入口通过 Forgejo Generic Packages 分发冻结的镜像、Go 文件代理、Maven 仓库、npm/pnpm 缓存和系统包。现场 HTTPS 地址、所有者和凭据环境变量名放在被忽略的 `deploy/forgejo/config.local.json`。公开 Dockerfile 继续使用原默认配置。

已验收源码为 `84dca4a66e6a09f6c6e00628d833fce41920d855`，平台为 Linux amd64。五应用镜像、十个 Go 程序、三个 Web 产物和 Java reactor 已在空构建缓存、无外网路由的独立 Docker 环境重建。Go 输出包含 `auriga-nas-bootstrap` 和 `runtime-release`。Java 执行 `-DskipTests package`，此记录覆盖编译与装配。

镜像身份、产物摘要、健康检查和余项记录在[构建验收记录](../../deploy/forgejo/build-acceptance.json)，新增包的完整回读统计见[发布核验记录](../../deploy/forgejo/publication-verification.json)。

公开 `init.sh`、`start.sh --network-offline` 起栈后，平台正式 TLS 探针及 edge、bifrost、PostgreSQL、NATS 健康检查通过。门户和管理台 HTML 返回 200，实际 JavaScript 与冷建产物逐字节一致。首次管理员初始化被 `unknown NAS source leaf: uac` 阻断，Agent 等待首次加入令牌并周期重启。正常登录、Agent 加入和真实 DSH 签名运行时导入仍需主控完成验收。

## 清单与配置

`forgejo-deps-inventory.json` 保留原始 6980 条发现及输入摘要，`acceptanceSourceCommit` 记录本轮构建源码。每项通过 `forgejo.evidence` 或 `inputGroup` 指向 `closure-index.json` 中的成员、精确版本、SHA-256 和包路径。Go 的 `h1`、npm/pnpm 的完整性与锁文件匹配，Maven 继承版本来自有效 POM。

- `present` 表示已有可定位的 Forgejo 输入，包含本轮补齐的输入。
- `source` 表示固定源码提供的本地模块、辅助脚本或应用构建输出。
- `platform-excluded` 保留锁文件明确排除 Linux x64 glibc 的包及其约束。
- `non-input-reference` 保留资金链接、测试样例、健康探针和源代码属性等扫描结果。
- `missing` 保留尚无精确证据的输入。

本次共有 5908 条 Forgejo 输入记录、114 条源码记录、506 条平台排除和 452 条非输入引用，缺失为 0。`supplyCounts` 中 5384 条只引用既有归档，524 条包含本轮补包证据。同一依赖可以由多个归档提供。本轮新增七个包、242 个文件，完整回读共 907937376 字节。

`artifacts.json` 记录分片路径、长度和 SHA-256。`api-listed` 表示既有 API 元数据，`uploaded-readback` 表示本轮完整回读通过。完整归档摘要及分片顺序以 `closure-index.json` 为准。`java-release.json` 复用 Maven 工位的完整发布索引，其余 `*-release.json` 记录增量包。CI action 源码已按完整提交冻结，运行 GitHub 工作流所需的 runner 和服务集成由 CI 环境提供。

重新发现和映射时输出临时文件，保留发布清单便于比较。

```sh
node deploy/forgejo/inventory.mjs /data/auriga/discovery.json
node deploy/forgejo/map-inventory.mjs /data/auriga/discovery.json \
  deploy/forgejo/closure-index.json /data/auriga/mapped.json
```

从 Forgejo 准备两个源码检出目录。`recipes` 使用本分支的构建入口，`source` 固定到验收提交。将 `recipes/deploy/nas/offline-build/` 复制到 `source/deploy/nas/offline-build/`，其余源码保持验收提交内容。后续源码或锁文件变化时重新采集并验证对应闭包。

在 `recipes` 下复制 `deploy/forgejo/config.example.json` 为 `deploy/forgejo/config.local.json`，设置现场 HTTPS 根地址、包所有者和只读 token 环境变量名。凭据通过环境注入。使用 Node 24，并把下载、临时目录及 Docker 数据目录放到数据盘。以下命令在 `recipes` 下执行。

```sh
export work=/data/auriga/forgejo-rebuild
mkdir -p "$work"
node --input-type=module - "$work" <<'JS'
import { readFileSync, writeFileSync } from 'node:fs';
const work = process.argv[2];
const closure = JSON.parse(readFileSync('deploy/forgejo/closure-index.json'));
const overlay = JSON.parse(readFileSync('deploy/forgejo/overlay-release.json'));
const artifacts = [...new Map([
  ...closure.archives.flatMap(archive => archive.parts), ...overlay.artifacts,
].map(part => [part.path, part])).values()];
writeFileSync(work + '/download-manifest.json', JSON.stringify({ artifacts }, null, 2));
JS
node deploy/forgejo/download.mjs deploy/forgejo/config.local.json \
  "$work/download-manifest.json" "$work/parts"
```

下载器核长度和 SHA-256，拒绝跳转、跨源路径、路径穿越、重复目标及覆盖。已有输出需保留供核查，并选择新目录继续下载。按索引顺序拼接后再次核完整归档摘要。

```sh
node --input-type=module - "$work" <<'JS'
import { readFileSync, mkdirSync, openSync, writeFileSync, closeSync } from 'node:fs';
import { createHash } from 'node:crypto';
const work = process.argv[2];
const closure = JSON.parse(readFileSync('deploy/forgejo/closure-index.json'));
mkdirSync(work + '/archives');
for (const archive of closure.archives) {
  const output = openSync(work + '/archives/' + archive.name, 'wx');
  const digest = createHash('sha256');
  try {
    for (const part of archive.parts) {
      const bytes = readFileSync(work + '/parts/' + part.file);
      if (bytes.length !== part.bytes || createHash('sha256').update(bytes).digest('hex') !== part.sha256)
        throw Error('Part changed: ' + part.file);
      writeFileSync(output, bytes);
      digest.update(bytes);
    }
  } finally { closeSync(output); }
  if (digest.digest('hex') !== archive.sha256) throw Error('Archive mismatch: ' + archive.name);
}
JS
```

## 从依赖构建

从已核验归档恢复目录，并在各目录执行 `sha256sum -c SHA256SUMS`。

- Go 输入归档 `auriga-go-inputs-linux-amd64-963bfe65.tar.gz` 解到 `inputs/go`。
- Web 输入归档 `auriga-build-web-inputs-linux-amd64-963bfe65-r2.tar.gz` 解到 `inputs/web`。
- Java 输入归档 `auriga-build-java-inputs-linux-amd64-784643c8.tar.gz` 解到 `inputs/java`。
- agent DEB、bifrost APK、platform DEB、operator wheel 归档分别解到 `output/system/agent`、`output/system/bifrost`、`output/system/platform`、`output/system/operator`。

Java 先按原始清单全量复核，再应用 `java-overlay-84dca4a6.json`。核对 `baseArchiveSha256` 及两个覆盖文件的 SHA-256，把 `java-source-locks-84dca4a6.sha256` 复制为 `inputs/java/source-locks.sha256`，把 `java-files-84dca4a6.sha256` 复制为 `inputs/java/SHA256SUMS`，再次全量校验。此覆盖绑定 collab POM 新增的 BouncyCastle 1.83 声明，对应 JAR 已在原始输入中。覆盖适用于固定的验收源码。

加载索引中的八个构建/运行基镜像 Docker 归档，以及 PostgreSQL、NATS、registry 归档，核镜像身份和 Linux amd64 架构。DSH Node 基底采用 OCI layout，归档内根目录为 `node-base`，通过支持 OCI layout 的工具导入。它和 DSH npm 缓存供原有签名构建流程使用。

Go 配方使用 `GOPROXY=file:///inputs/download`，Maven 使用本地仓库及 `-o`，Web 恢复 pnpm、供应链元数据和 store 后离线安装，Operator 从本地 wheel 执行 `--no-index` 安装。

在只允许 Forgejo 的独立 VM/网络环境串行构建，环境同时限制 Docker daemon 出站。编译容器使用空临时目录、空输出目录及 `--network none --pull never`，源码和输入只读挂载，按配方设置资源上限。C 盘低于 15 GiB 时暂停。五镜像通过 `assemble-image.sh` 串行装配，脚本固定使用 `--network=none --pull=false --no-cache`。完整挂载、镜像参数和打包命令见[离线重建说明](../../deploy/nas/offline-build/README.zh.md)。

## 正式验收

将应用镜像、两个 Go 辅助程序和公开 NAS 目录组成发行材料，按正常安装流程填写访问地址及镜像摘要，执行 `scripts/init.sh`、`scripts/start.sh --network-offline`。用生成的正式 TLS 探针检查平台，从门户、管理台页面继续验收首管创建、登录、Agent 加入及签名运行时导入。

本轮五镜像 Docker 归档及 Go/JAR/Web 产物留在工位供主控取用，NAS 部署由主控串行安排。`uac` 安装白名单阻塞需在对应任务修复后复测。签名、源码范围及运行时校验继续按[正常安装流程](../../deploy/nas/README.md)执行。临时验证栈在结束后清理。
