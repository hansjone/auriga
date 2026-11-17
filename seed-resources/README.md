# 开发种子资源

本目录的归档和运行回执默认忽略，原旧插件种子保持原用法。本期新增统一目录发布路径，供主控重置开发数据后使用；本任务不实际发布。

从 dsh-memwiki 项目按其锁文件构建后运行 `npm pack --ignore-scripts` 或 `pnpm pack --ignore-scripts`，使用已构建的 tgz，不对上游 DSH 做改动。归档内 `package/package.json` 的 name 必须是 `dsh-memwiki`，version 必须与传入版本一致；服务端重新核对，不执行脚本。若发布包保留 prepare/prepack 等生命周期声明，须在种子构建阶段产出没有这些声明的发行 package.json 后再 pack，不能仅依赖打包时的 ignore-scripts。

默认只输出原始清单预览，不访问网络：

```powershell
node seed-resources/publish-unified-plugin.mjs ./seed-resources/dsh-memwiki-0.4.0.tgz 0.4.0
```

实际发布由主控在已授权环境显式添加 `--publish`。配置 `AURIGA_SEED_ORIGIN`（如 `http://192.0.2.10:8080`）、`AURIGA_SEED_DSH_RANGE`，以及 `AURIGA_SEED_AUTHOR_TOKEN`、`AURIGA_SEED_GENERAL_TOKEN`、`AURIGA_SEED_SECURITY_TOKEN`、`AURIGA_SEED_PUBLISHER_TOKEN`。令牌只从环境读取，不放参数或回执；作者、普通审核者、安全审核者须不同，安全审核者须有当前安全审核资格，发布者不能是作者，并满足接口近期验证要求。

脚本按统一接口创建统一目录 plugin、上传、送审、两人审核、v2 签名发布，输出 packageId/versionId。用于空目录种子；已有同名资源会失败，不覆盖或降级已有版本。失败保留服务端草稿供管理中心处理，不能重复执行后冒称幂等恢复。第二期客户端仍需从正常入口安装验收。
