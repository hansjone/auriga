# real-dsh 构建

完整构建继续使用 `../scripts/build-real-dsh.mjs`。独立构建阶段使用固定的 Node 镜像、Corepack 中的 pnpm 11.28.6 和现有 workspace lock（`--frozen-lockfile --ignore-scripts`），从明列的公共源码和合同文件生成托管 broker。现有 `package-broker.mjs` 在复制前后验包；镜像只接收 `shell-auth` 成品和构建输入的 SHA-256。成品含原执行器、网络 bundle、Ajv、合同和许可证。托管入口在进程内执行，不需要 Windows helper；桌面发行的产物和验包条件照旧。

首次构建需要获取已有锁定的 npm/pnpm 包，包括构建时用的 Vite/esbuild、Ajv 和网络依赖。没有修改锁或增加运行时依赖；冷缓存下，全量构建需要访问 npm registry 或可用镜像。运行镜像不带入 broker 构建目录、编译依赖或 npm/pnpm 包缓存。原离线 Corepack/pnpm 仍随运行镜像提供。

仅修改未参与 broker 构建的 Auriga 插件源码时，可选择增量配方复用已签发运行时的稳定层，减少需要重新传输的镜像层。

先用原根验证上一份运行时签发包及实际镜像摘要，确认它是没有用户 home、现场配置或秘密的干净构建制品。将已验证的镜像固定为完整 digest，再从仓库根执行：

```sh
node deploy/compose/scripts/build-real-dsh-incremental.mjs \
  --base-runtime-image "registry.example.com/auriga/real-dsh@sha256:<完整摘要>" \
  --image-tag registry.example.com/auriga/real-dsh:plugin-update
```

增量入口在复制前检查工作树根、白名单输入的每级路径及源码树中的链接，然后交给 Docker 使用私有临时上下文。此前以仓库根为上下文直接运行 `docker build -f deploy/compose/real-dsh/Dockerfile.incremental .` 的调用需迁移到上面的脚本，配方的 `COPY` 路径现在以脚本生成的上下文为准。脚本构建并标记本地镜像，仍由运营者按下一步流程推送和签发。

守卫先核对基础镜像清单中的实际文件、两组 package/lock、Node/npm、DSH 实际安装版本，以及 versions/build/inputs/runtime/start 的字节。插件安装时使用的生产 lock 会从本次原始输入重新推导并逐字比较。增量额外核对 broker 源锁并验包；该锁覆盖构建配方、workspace lock、broker 源码、参与 bundle 的插件源码目录和所用合同。旧镜像没有托管 broker、源锁变化或验包失败时，必须先全量构建。增量不安装依赖或重编 broker。

旧插件 `src` 会整体删除后复制新快照，已删除源码不会残留。构建仍调用原 `build.mjs manifest`，执行真实公开 API/native 模块检查并重新生成文件清单。全量和增量均用 UID/GID 200000 在断网下加载真实托管入口，并确认走到预期不存在的占位凭据路径；不会读取实例凭据或启动 DSH。仅增量命令预加载一个幂等权限适配：已为 `0755` 的普通文件不重复 chmod，以免 overlay 将未变的大二进制复制进新层；需要修改的权限仍交原函数处理。默认完整构建不加载该适配。增量配方只复用已验证的基础层，不修改运行中的实例；基础镜像来源验证仍由运营者负责。

构建完成后仍按原流程推送并固定新摘要，运行 `collect-runtime-proofs.mjs`、以原根签发更高序号、严格导入并公开激活。增量构建不替代这些门禁，也不代表正常用户页面已经验收。

纯 fixture 验证：`node --test deploy/compose/real-dsh/incremental.test.mjs`。实际 Docker 构建、层复用、运行时证明和 NAS 验收另行串行执行。
