# 只从 Forgejo 获取构建输入

本入口通过 Forgejo Generic Packages 分发冻结的镜像、Go 文件代理、Maven 仓库、npm/pnpm 缓存和系统包。现场 HTTPS 地址、所有者和凭据环境变量名放在被忽略的 `deploy/forgejo/config.local.json`。公开 Dockerfile 继续使用原默认配置。

已验收源码为 `b7359410db3dcf1107af240a2a6d2f85bae6a5e6`，平台为 Linux amd64。五应用镜像、十个 Go 程序、三个 Web 产物和 Java reactor 已在 NAS 的独立空 Docker/语言工作环境中冷建；输入只从 Forgejo 获取，编译容器无网络。Go 输出包含 `auriga-nas-bootstrap` 和 `runtime-release`。Java 执行 `-o -DskipTests package`，本轮覆盖编译和装配。恢复过程复用已通过的编译结果；NAS ACL 影响的首批镜像被保留为失败证据，规范公开输入权限后重新装配。

镜像身份、产物摘要、健康检查、网络证据、资源曲线和清理结果记录在[构建验收记录](../../deploy/forgejo/build-acceptance.json)。基线发布统计见[发布核验记录](../../deploy/forgejo/publication-verification.json)，NAS 工具及源码覆盖增量见[NAS 发布核验记录](../../deploy/forgejo/nas-publication-verification.json)。

公开 `init.sh`、`start.sh --network-offline` 的核心健康检查、平台正式 TLS 探针、门户与管理台 HTML 200、实际 JavaScript 与冷建产物逐字节比对、首次管理员初始化及正常登录均通过。Agent 以严格 CA pin TLS Join 加入，正常管理员节点 API 确认 Docker 驱动在线且心跳小于45秒。该最小验证使用独立临时发行公钥；默认 NAS 配方在未配置正式运行时 bundle 时返回空发行根，Agent 会按安全规则拒绝，不能将本记录解读为默认空根安装已可直接入网或真实 DSH 发行验收。

历史 `84dca4a6` 证据保留在 Git；当时的 `uac` 首管阻断已由主线修复。此前本机资源保护暂停的续验已迁到 NAS。真实签名 DSH 运行时、目标 Engine 兼容及现有开发环境部署由主控串行安排。

## 清单与配置

`forgejo-deps-inventory.json` 保留原始 6980 条发现及输入摘要，`acceptanceSourceCommit` 保留 `84dca4a6` 基线；`source-compatibility-b7359410.json` 记录新源码复核，实际冷建提交以 `build-acceptance.json` 为准。每项通过 `forgejo.evidence` 或 `inputGroup` 指向 `closure-index.json` 中的成员、精确版本、SHA-256 和包路径。Go 的 `h1`、npm/pnpm 的完整性与锁文件匹配，Maven 继承版本来自有效 POM。

- `present` 表示已有可定位的 Forgejo 输入，包含本轮补齐的输入。
- `source` 表示固定源码提供的本地模块、辅助脚本或应用构建输出。
- `platform-excluded` 保留锁文件明确排除 Linux x64 glibc 的包及其约束。
- `non-input-reference` 保留资金链接、测试样例、健康探针和源代码属性等扫描结果。
- `missing` 保留尚无精确证据的输入。

本次共有 5908 条 Forgejo 输入记录、114 条源码记录、506 条平台排除和 452 条非输入引用，缺失为 0。`supplyCounts` 中 5384 条只引用既有归档，524 条包含本轮补包证据。同一依赖可以由多个归档提供。基线补齐七个包、242 个文件，完整回读共 907937376 字节。NAS 续验另上架 DinD 工具和 Web 源码覆盖：实际37文件、129845217字节全部回读，有效34文件、129843027字节；被 LF 元数据覆盖替代的旧3文件保留不删。当前依赖索引共824文件，应用冷建输出另列专门清单。

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
const webOverlay = JSON.parse(readFileSync('deploy/forgejo/web-overlay-release.json'));
const builder = JSON.parse(readFileSync('deploy/forgejo/nas-builder-release.json'));
const artifacts = [...new Map([
  ...closure.archives.flatMap(archive => archive.parts), ...overlay.artifacts,
  ...webOverlay.artifacts, ...builder.archive.parts,
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
const builder = JSON.parse(readFileSync('deploy/forgejo/nas-builder-release.json'));
mkdirSync(work + '/archives');
for (const archive of [...closure.archives, builder.archive]) {
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

源码 `b7359410db3dcf1107af240a2a6d2f85bae6a5e6` 另需应用 `web-overlay-b7359410.json`：先核原 Web 归档及文件摘要，再按覆盖清单核两个文件，把它们分别复制到 `inputs/web/source-locks.sha256` 和 `inputs/web/SHA256SUMS`，再次校验。唯一源码清单变化是 panel 的检查与测试脚本；pnpm 锁、依赖声明、corepack 和缓存字节均未改变。[兼容核验记录](../../deploy/forgejo/source-compatibility-b7359410.json)保存重扫统计及14处移动引用的逐项核对。

加载索引中的八个构建/运行基镜像 Docker 归档，以及 PostgreSQL、NATS、registry 归档，核镜像身份和 Linux amd64 架构。DSH Node 基底采用 OCI layout，归档内根目录为 `node-base`，通过支持 OCI layout 的工具导入。它和 DSH npm 缓存供原有签名构建流程使用。

Go 配方使用 `GOPROXY=file:///inputs/download`，Maven 使用本地仓库及 `-o`，Web 恢复 pnpm、供应链元数据和 store 后离线安装，Operator 从本地 wheel 执行 `--no-index` 安装。

在只允许 Forgejo 的独立 VM/网络环境串行构建，环境同时限制 Docker daemon 出站。编译容器使用空临时目录、空输出目录及 `--network none --pull never`，源码和输入只读挂载，按配方设置资源上限。C 盘低于 15 GiB 时暂停。五镜像通过 `assemble-image.sh` 串行装配，脚本固定使用 `--network=none --pull=false --no-cache`。完整挂载、镜像参数和打包命令见[离线重建说明](../../deploy/nas/offline-build/README.zh.md)。

## 正式验收

### NAS 隔离环境

在 NAS 数据盘新建本次专用目录，从 Forgejo 下载并校验 `nas-builder-release.json` 中的 Docker 27.1.2 DinD 归档。独立 daemon 的镜像、BuildKit、语言临时目录和输出初始为空；不挂载 NAS 主 Docker socket，不挂载已有测试栈的目录或卷，也不发布端口到 NAS。

外层使用新建的 `--internal` 网络。唯一连接外网的临时代理只接受固定 Forgejo Git upload-pack 和 Generic Packages 路径，拒绝任意 URL、CONNECT 和跳转。源码及所有输入经此代理从 Forgejo 获取，构建容器继续使用 `--network none`。保存实际 Git 克隆、包下载、外部域名与直接 IP 连接失败，以及代理拒绝非 Forgejo 路径的证据。

本次外层 daemon 容器限制为4864MiB、2 CPU，代理128MiB、0.25 CPU；总内存上限4992MiB。Go、Web、Java 和镜像装配串行执行，内层编译分别限制为2048、1536、2560MiB。每轮采集 NAS `MemAvailable` 和 daemon 内存，低于1.5GiB时只停止本次 daemon，并保存暂停证据。

本轮最初 Go/Web 阶段未启用层级计费，父容器上限没有强制约束内层总量；Java 阶段先临时收紧 daemon 到2048MiB，使其与2560MiB编译容器、128MiB代理的配置上限之和低于5GiB，随后保留产物重建外层容器并修正层级计费。资源曲线逐条标出该检查点，不能据此声称全程统一聚合上限均已生效。

cgroup v1 环境还需显式设置外层容器的 `--cgroupns private`，并在启动任何内层容器前，将该私有命名空间自身的 `memory.use_hierarchy` 设为1，确认后续子组继承。NAS 默认值为0时，仅设置外层内存上限不能累计子容器。此操作只针对新建容器自己的 cgroup；NAS 全局 cgroup 保持原配置。

本次 NAS 5.10 上，JDK 基底 Ubuntu 26.04 的 GNU tar 1.35 在最小目录提取时返回 `Bad address`，默认及无 seccomp 的诊断容器均复现。实际编译保留默认安全配置，使用同一冻结 Node 基底中 Debian 的 GNU tar 1.34、cp 和动态库；JDK 和 JRE 镜像摘要保持固定。可在独立 daemon 内导出这些既有输入：

```sh
: "${verifiedNodeImage:?set the independently verified Node base image ID}"
compat=/data/auriga/forgejo-rebuild/compat
mkdir -p "$compat/bin" "$compat/lib"
cid=$(docker create --network none "$verifiedNodeImage")
docker cp -L "$cid:/lib64/ld-linux-x86-64.so.2" "$compat/ld-linux"
docker cp "$cid:/lib/x86_64-linux-gnu/." "$compat/lib/"
for tool in tar cp; do
  docker cp "$cid:/usr/bin/$tool" "$compat/$tool.bin"
  printf '#!/bin/sh\nexec /compat/ld-linux --library-path /compat/lib /compat/%s.bin "$@"\n' \
    "$tool" > "$compat/bin/$tool"
  chmod 0755 "$compat/bin/$tool" "$compat/$tool.bin"
done
chmod 0755 "$compat/ld-linux"
docker rm "$cid"
printf '%s\n' "$verifiedNodeImage" > "$compat/SOURCE_IMAGE"
(cd "$compat" && find . -type f ! -name SHA256SUMS | LC_ALL=C sort | xargs sha256sum) \
  > "$compat/SHA256SUMS"
```

将该目录只读挂载到 Java 容器的 `/compat`，启动 shell 时设置 `PATH=/compat/bin:$PATH`，执行原 Java 配方。先验证目录 tar 提取和 `cp -a`，并将工具字节摘要及来源镜像身份一并保存到验收证据。首次失败尚未进入 Maven，Go/Web 已成功的产物可保留，只续做 Java。

QNAP 共享目录的默认 POSIX ACL 可能使 `mkdir(0755)` 得到0750，导致正式安装器报 `invalid NAS source root mode`。先检查专用目录的默认 ACL；在导入镜像前，仅移除本次自用目录及独立 Docker 数据目录上的继承 ACL，保留原 POSIX 模式，再创建新的空验收数据目录。镜像层若继承 NAS 的 guest 拒绝项，固定非 root 用户会在加载 libc 时得到 `Permission denied`；应使用无此继承项的专用数据目录，并先执行非 root 工具启动探针。构建上下文若已带有这些扩展项，BuildKit 可能把它们带入 COPY 层，导致镜像内程序虽声明 `--chmod=0755`，实际仍无法由固定 UID 执行。仅清理 daemon 目录不足；需在装配前清除公开源码、输入和编译输出的继承项，规范公开目录0755、普通文件0644、程序0755，并重新核内容 SHA-256。受影响镜像重新装配，保留原失败镜像身份，不能手工改镜像层冒充原摘要。公开 NAS 发行目录还应保证目录0755、普通脚本0644及辅助程序0755，使固定非 root 探针可读；权限调整只针对公开源材料，生成的私有配置与密钥保留原有模式。保留失败现场日志，使用原安装器重新初始化；不要修补已有正式材料目录或放宽安装器检查。

NAS 的 DinD 客户端基底使用 BusyBox，缺少装配脚本要求的 GNU `sha256sum --quiet`。装配与正式 NAS 脚本在已核验的 Debian Node 工具镜像中执行，挂载独立 daemon 的 socket、Docker CLI 和 CLI 插件，保持输入及工作目录的绝对路径一致；此处的 socket 和 host 网络均属于独立 DinD 命名空间。管理容器缺少 `netstat` 时，从同一冻结 DinD 基底复制 BusyBox 与 musl loader，用显式 loader 的小型包装入口提供 `netstat` 和 `ip`，保存其字节摘要；不要伪造端口预检结果。管理容器限制256MiB、0.5 CPU，纳入已启用层级计费的4864MiB父上限。构建本身继续使用无网络模式和固定本地基镜像，不修改摘要校验。

验收结束后先导出不含凭据的日志、镜像身份、产物摘要和资源曲线，再删除本次容器、专用网络、新导入工具镜像及专用目录；核对原测试栈容器仍在。不要对 NAS 主 daemon 执行全局 prune。

首次 Agent TLS Join 必须收到至少一个合法发行根。正式部署应先按[正常安装流程](../../deploy/nas/README.md)消费独立批准的签名运行时 bundle/root；不要写入已封存材料、删除身份历史或改用明文绕过。本轮最小验证在独立临时配置中挂载专用公开 `ArtifactSigningKey`，通过现有 `artifact-keys-directory` 属性提供给平台，私钥仅在内存生成后丢弃；它不属于正式发行授权。空根拒绝所产生的旧节点通过正常管理员 API 撤销，新令牌用于全新身份目录，验收记录保留这段过程。

将应用镜像、两个 Go 辅助程序和公开 NAS 目录组成发行材料，按正常安装流程填写访问地址及镜像摘要，执行 `scripts/init.sh`、`scripts/start.sh --network-offline`。用生成的正式 TLS 探针检查平台，从门户、管理台页面继续验收首管创建、登录、Agent 加入及签名运行时导入。

本轮五镜像 Docker 归档、公开 NAS 配方及两个辅助程序通过 Forgejo 分片发布，并完整回读到工位；路径、长度和摘要见[冷建输出清单](../../deploy/forgejo/cold-output-release-b7359410.json)。归档不携带现场秘密、临时验收根或正式签名 DSH bundle。主控可据此准备小批开发部署；本任务仅使用独立临时栈，现有开发入口的部署与正常页面验收仍由主控安排。签名、源码范围及运行时校验继续按[正常安装流程](../../deploy/nas/README.md)执行，临时环境清理结果见构建验收记录。
