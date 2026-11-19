# real-dsh 构建

完整构建继续使用 `../scripts/build-real-dsh.mjs`。独立构建阶段使用固定的 Node 镜像、Corepack 中的 pnpm 11.28.6 和现有 workspace lock（`--frozen-lockfile --ignore-scripts`），从明列的公共源码和合同文件生成托管 broker。现有 `package-broker.mjs` 在复制前后验包；镜像只接收 `shell-auth` 成品和构建输入的 SHA-256。成品含原执行器、网络 bundle、Ajv、合同和许可证。托管入口在进程内执行，不需要 Windows helper；桌面发行的产物和验包条件照旧。

首次构建需要获取已有锁定的 npm/pnpm 包，包括构建时用的 Vite/esbuild、Ajv 和网络依赖；冷缓存下，原有 DSH/broker 全量步骤仍需 npm registry 或既有完整固定输入。运行镜像不带入 broker 构建目录、编译依赖或 npm/pnpm 包缓存。Corepack 的 pnpm 11.28.6 仅保留既有 workspace 构建用途，不作为 dedicated 运行工具的路径或版本权威。

## 固定运行 pnpm 输入

dedicated 配方消费只读 `/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs`，由当前受信 Node executable 显式启动。`tooling/package.json` 与 `tooling/package-lock.json` 只封装安装器 `clients/installer/upstream.json` 已批准的 pnpm **11.28.5** 与同一 npm SHA512，不升级 Node/DSH/pnpm。正式 Linux 镜像仍固定 Node 22.19.0/npm 10.9.3，满足该 pnpm 的 `>=22.13` engine；Windows 安装器运行 Node 24.21.0 的 pin 保持原值。

维护者准备原始 `pnpm-11.28.5.tgz`：复用已批准的原始包缓存/Forgejo 原件，来源 URL 和 SHA512 均在上述 lock。不得重打包已安装的 `payload/tools/pnpm` 树冒充原始 npm TGZ。连网输入准备和现场断网构建分开；构建不会下载或运行 `pnpm install` 来补工具。纯字节验证及输出发行索引所需 SHA256：

```sh
node deploy/compose/real-dsh/tooling.mjs source-input /approved/auriga-source /approved/pnpm-11.28.5.tgz
```

Linux 发行索引 `build.runtimeTooling` 填 `{ "url": "<原始TGZ绝对路径或批准镜像URL>", "sha256": "<上述验证输出>" }`。新源码出 devkit 时该输入必需，原始包会保存为 `cache/runtime-tooling.tgz` 并进入签名清单、依赖锁键和累积套件 blob；不得修改旧已签包。现场先沿原命令 `./auriga devkit verify <套件目录>`，完整构建的 checked context 自动从 `.auriga-devkit.json` 对应版本取得该包，再核套件 SHA256 与源码固定 SHA512。源码 checkout 维护构建可显式设置 `AURIGA_REAL_DSH_TOOLING_TARBALL=/approved/pnpm-11.28.5.tgz`。缺输入或任一摘要不符在调用 Docker 前拒绝。

完整 Docker 配方在 `--network=none` 阶段将已验原始 TGZ 写入独立临时 npm cache，再按同一 lock 执行 `npm ci --offline --ignore-scripts --bin-links=false`，复制成固定 `/opt/auriga-tools/pnpm`。临时 cache 和原始 TGZ 不进入最终镜像。最终 manifest 覆盖完整工具树、锁、pin 和校验代码；构建沿原 bin/native 规则规范可执行 mode，并以运行 UID 用同一 Node + 固定 cjs 入口核精确版本。SDK/Auriga/工具层由实例只读 rootfs 提供，profile/store/cache 写本人 quota home。

本批只对固定运行工具形成离线闭包，不改变原有 DSH/broker 依赖安装步骤或代表整套 Linux 冷构建已断网验收。实际完整镜像、Linux 权限及正常市场页仍须串行验证。

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
