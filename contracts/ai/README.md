# G1 首批契约

`openapi/ai.yaml`、`proto/auriga/ai/v1`、`auth/access-token.schema.json` 与
`catalog/m3.json` 是正式接口。这里规定跨字段和跨进程语义；契约通过不表示业务实现已交付。
所有部署都适用，包括域名 TLS、显式内网 IP:port HTTP 前台；内部身份通道仍须节点/edge mTLS，
同机可用经过操作系统身份验证的专有 socket。前台 HTTP 不代表允许未认证的跨主机内部调用。

## 身份和受信实例通道（D1）

- 管理面只接受 `aud=auriga/ctx=admin`，每操作显式权限；敏感操作要求 `stepup_time` 在 600 秒内。
  门户令牌必须剔除 `catalog/m3.json` 的全部 `adminOnly` 权限，不能仅按 key 后缀猜测。
- 数据面只接受单一 AI audience 的 `at+jwt/ES256`，最长 300 秒。
  `instance/ptyp=user` 的 `sub` 是主人，`instanceId/credentialVersion` 必填，
  `perms` 是主人有效权限与 `{ai.models.invoke,ai.usage.read}` 的交集，不能从 roles 再扩权。
  `device/ptyp=device` 必须有 `did`，仍仅用 `auriga.connect.*`；
  `auriga.connect.models.invoke` 映射为本人模型调用和本人用量读取，仍在线检查模型 ACL。
  `ait_`、身份头、浏览器、外部服务账号、内部服务 JWT 不得调用数据面。
- 每个托管实例使用节点在本地创建的**专有实例 socket**访问内部 AI 代理。
  socket 以实例独立的运行身份/挂载权限隔离，不能让实例选择任意实例 id 或访问别人的 socket。
  节点从自己持久化的实例/socket 绑定取得 `instanceId`，从节点证书得到 `nodeId`，
  原 JWT 仅转发、不改写。不存在安全本地实例身份机制的驱动不得启用托管 AI。
- 节点用自己的 mTLS 证书访问 edge **独立内部 listener** 的 `/internal/ai/v1/**`。
  edge 校验节点 SAN、吊销和 workspace 当前 `(tid,owner,instance,node)` 调度绑定，
  剥除全部客户端 `Auriga-Forwarded-*`，根据认证结果写入
  `Auriga-Forwarded-Tenant`、`Auriga-Forwarded-Instance`、`Auriga-Forwarded-Node`（均小写 UUID）。
  请求再转为 gateway 的 `/ai/v1/**`；这些标记仅对 gateway 验证过的 edge mTLS 身份有效。
  gateway 必须逐项比较 JWT、受信标记和在线 `GetInstanceAiBinding`；缺少/重复/不匹配均拒绝。
  认证绑定和标记不能来自 IP、Host、普通请求头或实例提供的 owner 参数。
- edge 公网 listener 永不路由 `/internal/**` 且拒收 `ctx=instance`；
  gateway 托管入口只信显式配置的 edge SAN 白名单。device 走独立公网数据策略，不接受转发标记。
  允许内网 IP:port 作为地址不改变证书身份或来源校验；不能开放实例源 CORS 来补接线。
- 每次准入在线核有效成员、模型 ACL、实例/设备状态和配额。控制面不可用失败关闭，
  不影响 DSH 本身运行。绑定/撤销事件仅加速失效，不是用旧缓存继续付费的凭证。
  到期/成员失效/设备吊销/实例删除/紧急凭据撤销取消在途流。
  正常轮换最多 60 秒重叠，且不超过前代 token 到期；紧急吊销无重叠。

## JSON/SSE、错误和幂等（D2）

仅 models 与 chat-completions。请求字段严格白名单、最大 1 MiB；必须给明确 `max_tokens`，
单个 choice；messages 内容仅文本或工具消息，不接图片/音频/Responses/embedding。
未知字段、超上下文/最大输出/不支持的工具与采样能力在上游前返回 `common.validation_failed`。
schema 表示结构，role/content/tool_call_id/tool_calls 一致性由 G1 服务端验证：
tool 必有已有调用 id；assistant 工具调用才可 content=null；普通 user/system 必须是字符串。

`/ai/v1/**` 的局部错误是 `{error:{code,message,type},requestId}`，管理面仍 RFC 9457 Problem。
错误 code 的 HTTP/gRPC/中英文单一定义在 `catalog/m3.json`，`common.*` 复用既有 common 映射。
供应商拒绝、超时和不可达分别映射网关错误，不能透传供应商 key、body、完整查询 URL 或伪装用户401。

SSE 每事件最大 256 KiB，总字节及队列均有界；`data` 为 `ChatCompletionChunk`，
可分割 UTF-8/事件边界。正常最后可含 usage，之后 `data: [DONE]`。
响应前按错误 HTTP 状态返回；响应开始后仅尽力发送同型 `AiError` 并关闭，不能改 HTTP 状态，
失败不发成功 `[DONE]`。`ai.stream_interrupted` 的 502 仅适用于尚未开始响应，
已开始时只是流内 code。断连、到期、撤销、首字节/空闲/总超时均关闭上游 body/context。
不能把取消推断为未计费。

创建管理资源遵循 24 小时幂等；作用域 `(tid,sub,HTTP method,规范路径,key)`，
保存规范化请求指纹，同键异体 `common.conflict`。
**模型 POST 的例外**：同键仅一次预占、一个执行者、一次上游提交；
并发相同请求 `ai.request_in_progress`；终态重试 `ai.replay_unavailable`，
附 originalRequestId/outcome，不保存 prompt/正文供重放，也不再次执行。
无 key 的每次请求都是新请求；首批不做自动上游重试、故障切换、断点续流或工具写操作重放。

## 原子预算、账本和机密（D3/D5）

部署 `AdmissionPolicy` 默认每 `(tid,sub)` 2 个在途请求、对应日 token/月金额限额的 25%。
实例与设备跨所有 gateway 共享。周期“不限”时对应的正数绝对在途上限必填；
同时设置比例和绝对值时取较小值。0 配额禁止，非零有限额按已结算+全部未释放预占核验。
未配置可计算的安全预占上界则拒绝准入，不能用字符数充当精确 token。
金额用单一部署币种 micros，固定价格版本，向上取整并检查整数溢出。

`BudgetAdmission` 固定主体、模型/供应商/key 版本、限额快照、UTC 日/月周期及最大输出。
在 PostgreSQL **同一事务**领取主体槽、日/月预算预占和 requestId 唯一执行权。
配额降低只影响新准入，不能删除既有预占；并发/在途上限用 `common.rate_limited`，
实际周期额度不足用 `ai.budget_exceeded`，不可确认用 `ai.budget_unavailable`。
只有已结算耗尽且确定下一周期可恢复时给 Retry-After；unknown 占用不能虚构恢复时间。

状态 `reserved -> dispatching -> completed/failed/cancelled/unknown`，带 revision 条件更新。
上游提交前先持久化 dispatching；仅能证明尚未提交的 reserved 可释放。
dispatching 崩溃或断流无可信 usage 时为 unknown，三个用量数值必须 null/未设置，不能写0；
最坏金额/token预占继续占用，不因租约到期退款。并发槽可在执行已停止后回收，
但不能释放 unknown 金额/token预占。可信最终 usage 一次结算、释放差额；
结算、requestId 幂等状态和 `usage.recorded` outbox 同事务；NATS 断连不丢账。
跨周期终态仍写准入固定周期；负数、超预占、币种/价格冲突进入异常对账。

供应商 secret 只在 gateway 出站时解密，AES-256-GCM 的 AAD 绑定 tenant/provider/credential/密文版本，
主密钥来自受保护部署文件，不随同一数据库行存储。正常 key staged->active->retiring->revoked，
新请求固定一个 credentialId；紧急撤销取消其流。无读取 secret 的 API。
默认禁止内网供应商、重定向及实例自选 baseUrl；未来 gateway 受信内网端点例外须独立审批，
不能放宽实例 egress。首批只假 Transport，不接付费模型或新增云服务（D7）。
