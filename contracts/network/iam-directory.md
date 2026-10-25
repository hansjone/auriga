# IAM N1 系统目录与服务凭证

`openapi/iam-network.yaml` 是 IAM 对外 API 的补充文档，入口为
`GET /api/iam/v1/network/systems?limit=50&cursor=...`。页面遵循
`network/system-directory.schema.json`；Java 内部投影与 collab RPC 类型由
`proto/auriga/iam/v1/network_directory.proto` 生成。

- `limit` 默认 50，允许 1–200；非法值返回 `common.validation_failed`。
- `nextCursor` 仅是绑定认证 tenant 的节点位置，不代表授权；每次翻页重查策略。
- 只返回已注册、节点及设备未吊销、归属账号有效的 DSH。离线仍可发现；本接口不提供在线状态。
- `displayName` 来自设备展示名，`ownerUserId` / `ownerDisplayName` 来自 IAM 账号。
  不输出设备 id、原始设备/账号 JSON 或任何会话 local 信息；名称不授予权限。
- 接受 portal/admin 用户和持有 `auriga.connect.collab.use` 的 device；均需有效的底层会话。
  device 必须已注册，来源取认证 did；用户按本人全部未吊销源节点的禁用策略保守过滤。
  组织网络关闭或对应来源→目标策略禁用时隐藏目标；不接收调用方自报来源或 tenant。

`NetworkDirectoryService.ResolveNetworkNode` 仅供内部 `svc:collab` 服务 JWT，
需既有内部可信传输及 `auriga-tenant-id` 委托业务租户，且安装端在 common gRPC
委托白名单中允许此主体。请求仅含十进制字符串 `realm` / `node`；响应 `exists`
和 `NETWORK_DIRECTORY_NODE_TYPE_DSH` 说明已注册 DSH。未知、外域、已吊销及组织关闭
统一返回不存在；本查询不授发送权，不查询非零 local。COLLABIAM 应在 `local=0` 时
先核节点，再返回 `system`；非零 local 的类型从 collab 自己的 opaque local 登记核实，
不得回退为猜测 `session`。

## 网关服务 JWT 文件出口

与 edge 相同，仅支持同机 POSIX 合并安装，默认关闭；Windows 文件系统拒绝启动。
新增 IAM 配置前缀 `auriga.iam.ai-gateway-token-export`，支持：

- `enabled=true`，并要求 `auriga.platform.combined=true` 和 IAM 启用。
- `installation-secret-file`：绝对、规范路径，文件为 43 字符安装秘密，无换行；
  上级目录仅安装属主可访问，文件仅属主可读写。安装秘密不得挂载给网关。
- `token-file`：绝对、规范路径，与 secret 不同；输出目录 0750，文件 0640，
  必须为 IAM 进程属主并由配置的 reader group 读取。与 edge 使用独立输出目录/组。
- `reader-group` 默认 65532；`installation-owner` 默认 -1（IAM 进程属主），
  可显式指定安装秘密属主 UID，语义与 edge 相同。

安装时在既有 `auriga.iam.internal-secret-hashes` 配置中提供 `ai-gateway` 的
SHA-256 摘要，摘要必须对应本次独立生成的安装秘密。固定签发
`sub=svc:ai-gateway`、`aud=auriga.internal`、`ptyp=internal_service`、`ctx=service`，
有效期 300 秒；启动后立即尝试，随后每 60 秒重新认证、原子替换输出文件。签发或文件校验失败
保留旧文件且不延长有效期；停止时删除文件。网关必须每次读取文件并按有效期拒绝旧 JWT。
输出 JWT 不能反过来用于签发其他服务身份；安装秘密按服务分别配置、不得共用。

部署接线由部署任务完成：IAM 写入路径、网关只读挂载、reader group、安装秘密与摘要、
网关/ collab 委托租户白名单及 mTLS。此任务不修改部署文件、不操作现场部署。
