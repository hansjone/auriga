# real-dsh 构建

完整构建继续使用 `../scripts/build-real-dsh.mjs`，现在默认消费匹配的已签 Linux 开发套件，所有 Docker RUN 和完整 build 均为 `--network=none`，不拉取基镜像。独立构建阶段使用固定 Node 镜像、原 Corepack/pnpm 11.28.6 缓存和现有 workspace lock（`--offline --frozen-lockfile --ignore-scripts`），从当前明列源码和合同重新生成托管 broker。现有 `package-broker.mjs` 在复制前后验包；不复用旧项目 broker 冒依赖。托管入口在进程内执行，不需要 Windows helper；桌面发行的产物和验包条件照旧。

连接外网只发生在维护者准备固定输入时。现场缺缓存、签名、对应源码锁或实际镜像时明确失败，不回退到在线 npm/Corepack。运行镜像不带入 broker 构建目录、编译依赖或 npm/pnpm 内容缓存。Corepack 的 pnpm 11.28.6 保留既有 workspace 构建用途，不作为 dedicated 运行工具的路径或版本权威。

固定 Node22.19.0 内的 Corepack0.34.0 使用 `install -g --cache-only` 时只导入缓存，不设置默认版本。toolchain 导入已验归档后，再用 `corepack install -g pnpm@11.28.6` 从该缓存激活精确构建版本，随后启用 shim 并核 `pnpm --version`。这一步保持 `COREPACK_ENABLE_NETWORK=0` 和 RUN 断网；缺缓存仍失败，不查询或采用 latest。仅导入缓存会让无项目目录下的裸 pnpm 命令尝试查询默认版本，不能据此判断归档未导入。

## 完整镜像的同锁开发输入

现有 `deploy/offline/build.go` 负责 platform/edge/agent/bifrost/operator，DSH 上游源码附件供只读参考；它没有另一个 DSH 冷构建实现。DSH 的权威入口仍是本目录 Dockerfile 与公开 `build-real-dsh.mjs`（compose prepare 也调用它），本批不更改服务端发布、签发或版本选择。

新增 `build.runtimeDependencies` 是固定四项 artifact，每项沿原 `{url,sha256}` 格式：`manifest`、`corepack`、`webCache`、`npmCache`，文件分别为 `runtime-dependencies.json`、`corepack.tgz`、`web-cache.tar.gz`、`npm-cache.tar.gz`。前两个归档直接复用现有 Web 输入原字节；npm cache 按现有 rc.3 和插件生产 lock 补齐，只封装 `_cacache`。`system/agent` 是 deb 输入，不能当作 SDK npm 闭包。

维护者在已核验的 Node22.19.0/npm10.9.3 Linux amd64 工具镜像中，把源码和旧 Web 输入只读绑定，输出绑定到独立空目录，执行：

```sh
export AURIGA_DSH_WEB_INPUTS_DIR=/approved/inputs/web
export AURIGA_DSH_NODE_IMAGE_CONFIG=sha256:daa74c183f7d8c1ba55ed79c76e912f50be8782ca9d2da640645f906dce474b8
# 可选续用已核 SHA256、根目录为 _cacache 的旧 npm 缓存归档：
# export AURIGA_DSH_PREPARE_NPM_CACHE_TAR=/approved/dsh-npm-cache.tar.gz
# export AURIGA_DSH_PREPARE_NPM_CACHE_SHA256=<已批准的完整原件SHA256>
sh deploy/nas/offline-build/dsh-inputs.sh prepare /absolute/empty/runtime-dependencies
```

prepare 先验 Web 的原 `SHA256SUMS` 和 pnpm lock，保留其旧 source-lock 原件，以新 envelope 登记当前九项依赖文件的原字节 SHA、旧 Web seed SHA、三个缓存归档 SHA/长度和原 Node manifest→config 对应。新增 exports 等 manifest 字节变化必须重封当前 envelope；缓存本身不变可复用。它只在连接外网的准备阶段运行现有锁的 npm ci、始终 ignore-scripts；node_modules、项目源码、npmrc、日志和预制 broker 不进入输入。最终完整断网构建仍须证明两个管理器的缓存确实齐全，不能只凭 envelope 存在验收。

把输出四个原件及脚本打印的 SHA256 作为 `build.runtimeDependencies` 的四项输入。`build.tools.runtime` 登记同一受信 Node 镜像的原 manifest 引用（允许相同 digest 的镜像仓库）或已验 config `sha256:daa74…`；原 source manifest pin `cff78…` 不变。devkit 沿原签名/锁键/逐文件 blob 流程保存四项与 `images/tool-runtime.tar`，没有新的外层归档协议。源码和开发套件都必须使用本批新源码配方，不修改旧已签包。

现场沿原 `./auriga devkit verify <套件目录>` 绑定源码；在现有 Node 命令环境运行完整入口，例如：

```sh
node deploy/compose/scripts/build-real-dsh.mjs --local-only --image-tag 127.0.0.1:5000/auriga/real-dsh:site-build
```

入口从已签 standalone/cumulative 套件自动选对应缓存，并用已批准源码包的公钥验签、比源码锁和每个原件 SHA。Git checkout 维护构建可用 `AURIGA_REAL_DSH_VERIFY_KEY` 指定原独立批准公钥。基镜像从套件固定用途取受控派生 tag；镜像未加载时只加载已核摘要的套件 image tar，再核 Docker-save 中唯一 Config/RepoTags、config JSON SHA/OS/arch 和本地 inspect 身份。classic engine 的 config Id 与 containerd 的 manifest Id 分别核对，不互相推断，也不接受任意本地 tag。

维护者尚未打出已签 devkit 时，可显式使用 `AURIGA_REAL_DSH_DEPENDENCIES_DIR=<上述目录>`、`AURIGA_REAL_DSH_DEPENDENCIES_SHA256=<独立批准的manifest SHA>`、原固定 `NODE_IMAGE=<versions.json中的原manifest引用>` 和下一节的 runtime TGZ 输入。缺独立摘要不接受目录自证。context 只复制这四个固定文件，复制前后均验证，文件摘要流式读取；临时目录需要足够空间，可用现有 `TMPDIR` 指向专用 SSD 工作目录。

已核原始 Node Docker-save 归档但本地 daemon 未保留 canonical RepoDigest 时，维护阶段可改用显式 `AURIGA_REAL_DSH_NODE_ARCHIVE=<绝对路径/node-runtime-22.19.0-cff78eb5.tar>` 与 `AURIGA_REAL_DSH_NODE_ARCHIVE_SHA256=<独立批准的原件SHA256>`，并取消 `NODE_IMAGE`；仍必须同时提供上述显式依赖目录及其独立 manifest SHA。该入口先重核同一 envelope、源码固定 manifest/config，再核原件 SHA、文件名、唯一 Docker-save manifest/Config/RepoTag、实际 config 字节和平台；重复 tar 成员、重复 JSON 键或重复 tag 均拒。FROM 只能是按固定源码版本和原 manifest 前8位派生的 `auriga-input/node-runtime:22.19.0-cff78eb5`，加载后再次检查实际 config/manifest 身份。不能传任意 tag、用两种 base 输入并用或借此覆盖默认已签套件路径；它只是有独立摘要的开发输入，不是签名套件。本批原件为78,974,976B、SHA256 `4dcd214a743ef522699ebbb0c7a41051fb08d4fd9158e86ed21b48dc48a4aee0`，对应 config `daa74…`，没有修改原 source manifest `cff78…`。

完整配方用原 corepack cache-only 恢复构建 pnpm；broker 只从原 store/policy-cache 执行 offline frozen install，两次生产 npm ci 只从独立临时 `_cacache` 离线恢复。临时 cache 不进入最终镜像；保留缓存 envelope 进入 `/opt/fixture` 与运行镜像 manifest，绑定实际构建输入。没有更改任何依赖版本，也不在 source-only 发行增加我方预制安装包。

## 固定运行 pnpm 输入

dedicated 配方消费只读 `/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`，由当前受信 Node executable 显式启动。`tooling/package.json` 与 `tooling/package-lock.json` 只封装安装器 `clients/installer/upstream.json` 已批准的 pnpm **11.28.5** 与同一 npm SHA512，不升级 Node/DSH/pnpm。正式 Linux 镜像仍固定 Node 22.19.0/npm 10.9.3，满足该 pnpm 的 `>=22.13` engine；Windows 安装器运行 Node 24.21.0 的 pin 保持原值。

维护者准备原始 `pnpm-11.28.5.tgz`：复用已批准的原始包缓存/Forgejo 原件，来源 URL 和 SHA512 均在上述 lock。不得重打包已安装的 `payload/tools/pnpm` 树冒充原始 npm TGZ。连网输入准备和现场断网构建分开；构建不会下载或运行 `pnpm install` 来补工具。纯字节验证及输出发行索引所需 SHA256：

```sh
node deploy/compose/real-dsh/tooling.mjs source-input /approved/auriga-source /approved/pnpm-11.28.5.tgz
```

Linux 发行索引 `build.runtimeTooling` 填 `{ "url": "<原始TGZ绝对路径或批准镜像URL>", "sha256": "<上述验证输出>" }`。新源码出 devkit 时该输入必需，原始包会保存为 `cache/runtime-tooling.tgz` 并进入签名清单、依赖锁键和累积套件 blob；不得修改旧已签包。现场先沿原命令 `./auriga devkit verify <套件目录>`，完整构建的 checked context 自动从 `.auriga-devkit.json` 对应版本取得该包，再核套件 SHA256 与源码固定 SHA512。源码 checkout 维护构建可显式设置 `AURIGA_REAL_DSH_TOOLING_TARBALL=/approved/pnpm-11.28.5.tgz`。缺输入或任一摘要不符在调用 Docker 前拒绝。

完整 Docker 配方在 `--network=none` 阶段将已验原始 TGZ 写入独立临时 npm cache，再按同一 lock 执行 `npm ci --offline --ignore-scripts --bin-links=false`，复制成固定 `/opt/auriga-tools/pnpm`。临时 cache 和原始 TGZ 不进入最终镜像。最终 manifest 覆盖完整工具树、锁、pin 和校验代码；构建沿原 bin/native 规则规范可执行 mode，并以运行 UID 用同一 Node + 固定 cjs 入口核精确版本。SDK/Auriga/工具层由实例只读 rootfs 提供，profile/store/cache 写本人 quota home。

固定运行工具与完整构建缓存分开核验。实际完整镜像、Linux 权限及正常市场页仍须串行验证；源码接线和纯 fixture 不代表真实冷构建已成功。

## 增量构建

仅修改未参与 broker 构建的 Auriga 插件源码时，可选择增量配方复用已签发运行时的稳定层，减少需要重新传输的镜像层。

先用原根验证上一份运行时签发包及实际镜像摘要，确认它是没有用户 home、现场配置或秘密的干净构建制品。将已验证的镜像固定为完整 digest，再从仓库根执行：

```sh
node deploy/compose/scripts/build-real-dsh-incremental.mjs \
  --base-runtime-image "registry.example.com/auriga/real-dsh@sha256:<完整摘要>" \
  --image-tag registry.example.com/auriga/real-dsh:plugin-update
```

增量入口在复制前检查工作树根、白名单输入的每级路径及源码树中的链接，然后交给 Docker 使用私有临时上下文。此前以仓库根为上下文直接运行 `docker build -f deploy/compose/real-dsh/Dockerfile.incremental .` 的调用需迁移到上面的脚本，配方的 `COPY` 路径现在以脚本生成的上下文为准。脚本构建并标记本地镜像，仍由运营者按下一步流程推送和签发。

守卫先核对基础镜像清单中的实际文件、DSH/插件/运行工具的 package/lock、Node/npm、DSH/pnpm 实际安装版本，以及 versions/build/inputs/runtime/start/tooling 和 installer pin 的字节。插件安装时使用的生产 lock 会从本次原始输入重新推导并逐字比较。增量额外核对 broker 源锁并验包；该锁覆盖构建配方、workspace lock、broker 源码、参与 bundle 的插件源码目录和所用合同。旧镜像没有固定运行工具或托管 broker、源锁变化或验包失败时，必须先全量构建。增量不安装依赖或重编 broker。

旧插件 `src` 会整体删除后复制新快照，已删除源码不会残留。构建仍调用原 `build.mjs manifest`，执行真实公开 API/native 模块检查并重新生成文件清单。全量和增量均用 UID/GID 200000 在断网下加载真实托管入口，并确认走到预期不存在的占位凭据路径；不会读取实例凭据或启动 DSH。仅增量命令预加载一个幂等权限适配：已为 `0755` 的普通文件不重复 chmod，以免 overlay 将未变的大二进制复制进新层；需要修改的权限仍交原函数处理。默认完整构建不加载该适配。增量配方只复用已验证的基础层，不修改运行中的实例；基础镜像来源验证仍由运营者负责。

构建完成后仍按原流程推送并固定新摘要，运行 `collect-runtime-proofs.mjs`、以原根签发更高序号、严格导入并公开激活。增量构建不替代这些门禁，也不代表正常用户页面已经验收。

纯 fixture 验证：`node --test deploy/compose/real-dsh/incremental.test.mjs`。实际 Docker 构建、层复用、运行时证明和 NAS 验收另行串行执行。
