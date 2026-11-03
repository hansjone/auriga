# real-dsh 构建

完整构建继续使用 `../scripts/build-real-dsh.mjs`。仅修改 Auriga 插件源码时，可选择增量配方复用已签发运行时的稳定层，减少需要重新传输的镜像层。

先用原根验证上一份运行时签发包及实际镜像摘要，确认它是没有用户 home、现场配置或秘密的干净构建制品。将已验证的镜像固定为完整 digest，再从仓库根执行：

```sh
docker build --platform linux/amd64 --provenance=false --sbom=false \
  --build-arg BASE_RUNTIME_IMAGE=registry.example.com/auriga/real-dsh@sha256:<完整摘要> \
  -f deploy/compose/real-dsh/Dockerfile.incremental \
  -t registry.example.com/auriga/real-dsh:plugin-update .
```

守卫先核对基础镜像清单中的实际文件、两组 package/lock、Node/npm、DSH 实际安装版本，以及 versions/build/inputs/runtime/start 的字节。插件安装时使用的生产 lock 会从本次原始输入重新推导并逐字比较。任一输入改变会拒绝增量构建，应使用完整构建。

旧插件 `src` 会整体删除后复制新快照，已删除源码不会残留。构建仍调用原 `build.mjs manifest`，执行真实公开 API/native 模块检查并重新生成文件清单。仅增量命令预加载一个幂等权限适配：已为 `0755` 的普通文件不重复 chmod，以免 overlay 将未变的大二进制复制进新层；需要修改的权限仍交原函数处理。默认完整构建不加载该适配。增量配方只复用已验证的基础层，不修改运行中的实例；基础镜像来源验证仍由运营者负责。

构建完成后仍按原流程推送并固定新摘要，运行 `collect-runtime-proofs.mjs`、以原根签发更高序号、严格导入并公开激活。增量构建不替代这些门禁，也不代表正常用户页面已经验收。

纯 fixture 验证：`node --test deploy/compose/real-dsh/incremental.test.mjs`。实际 Docker 构建、层复用、运行时证明和 NAS 验收另行串行执行。
