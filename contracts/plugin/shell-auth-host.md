# 插件后端专用宿主 IPC（AUTH-K2 第二轮）

本文件增补 `shell-auth-control.md`，与页面 `/loopback/messages`、skill 的 `/dsh-team/*` 分开。`/host/contexts`、`/host/credentials` 只是 OpenAPI 生成器的 IPC 消息投影，不得注册为 HTTP、MCP 工具或模型可调用操作。消息结构在 `shell-auth-control.schema.json`，不扩充 `ShellAuthCommand` 或固定业务代理白名单。

## 来源、owner 和期限

C 的 OS helper 必须先核实本机 SID/uid、受控 DSH 子进程及创建时间/继承能力、DACL/peer credentials 和连接随机能力；同 OS 用户本身不足以准入。只有授予 `plugin-backend` 角色的真实插件后端连接可调用。身份来自已核验连接，消息里的 PID、owner、角色、HTTP 头、模型参数都不能证明来源。宿主能力通过启动器私有继承通道交接，不进入 URL、环境全局变量或页面。没有真实 peer verifier 时失败关闭。

broker 为该连接生成随机 UUID `ownerId`，连接内稳定。owner 绑定 OS 身份、受控进程创建时间和连接，不按可复用 PID 归属。D 的可信后端只能从宿主会话查询和工具执行回调取得 `hostSessionId/toolCallId/rootCallId`，与当前 DSH owner 核对；不得从模型工具参数、页面 JSON 或 skill 提交的同名字段登记。C 保存此来源及认证角色。

请求信封固定为 protocolVersion=1、requestId、shellEpoch、expectedRevision、expiresAt。expiresAt 必须晚于当前 UTC 且最多 30 秒，以接收时单调剩余期限为上限；epoch 和 revision 必须精确匹配 authenticated 快照。开始、外部副作用前、提交结果前均复核。同 owner/epoch/requestId 的相同内容只返回原结果，不延长有效期；不同内容 conflict。凭据读取即使相同 requestId 重试也必须重新核身份、当前配置和凭据版本，不缓存秘密响应。

## 登记、句柄交接和撤销

1. D 在可信宿主会话建立时向 `/host/contexts` 发 `ShellAuthHostContextCommand`，action=register、context.kind=session、hostSessionId 和期望 expiresAt。不得提供 executionContextId 或 ownerId。
2. C 核来源后生成不可预测 UUID executionContextId。`ShellAuthHostContextResult.context` 即句柄交接消息：返回 id、broker 派生 ownerId、宿主会话、kind 和实际期限，epoch/revision 在结果信封。D 只在该后端连接内存保存；此 ID 是检索句柄，不能代替连接认证，不得交模型或浏览器，也没有跨 owner 转移能力。
3. 实际上下文期限取请求期限、now+600 秒、当前认证/来源期限的最小值；会话进行中若需继续使用，D 从仍有效的宿主来源重新登记并撤旧句柄，不能凭旧句柄自行续期。每 owner 最多 128 个有效上下文，超限 rate_limited/capacity_exceeded。登记本身不注册远端节点，也不获得平台权限。
4. 先有 session 句柄，才能发既有 `network_open` 和 `team_node_register`。后者创建远端 endpoint，C 将成功返回的 endpoint 记录到该 owner/会话；随后 `team_node_bind` 建立本会话 binding。endpoint/binding 均来自已验证远端结果，不能在登记请求预填；业务输入中的 endpointId/hostSessionId 只作断言，与当前记录逐项比较，不能借其他 endpoint 接管来源。失败或丢响应沿既有幂等规则处理，不捏造成功。
5. Team MCP 工具执行时再登记 kind=tool，必填同 owner/hostSessionId 的有效 parentExecutionContextId（session 句柄），及可信 toolCallId/rootCallId。工具操作使用 tool 句柄，C 从注册记录构造既有 SessionMeta；session 句柄不能冒充工具调用。network_submit 同样按其宿主来源匹配；拥有句柄不放宽业务权限、subject 或目标白名单。
6. action=revoke 只携带 executionContextId；同 owner 撤销返回 revokedExecutionContextId。D 在会话删除、工具完成/取消或失去 owner 时主动撤销。C 撤销 session 时同时撤其 tool 子上下文、关闭关联流并拒绝晚结果。已撤记录在该连接存有 tombstone 时重复撤销幂等；未知/其他 owner 均安全拒绝，不透露是否存在。

断线、进程退出、epoch/revision 改变、退出、身份切换、来源失效及期限到达立即清上下文及关联流。重连必须重新登记，不回收旧 ownerId/句柄。C 不把登记结果持久化为可跨重启能力。真实 OS peer 验证和宿主来源核验须由 C/D 的运行时测试证明；schema 只能拒绝字段越界。

## 企业登录信息读取

D 的企业 handler 使用 `/host/credentials` 的 `ShellAuthHostCredentialsRequest`，携带上述信封、当前有效的 executionContextId 和 purpose=raw/outbound，无账号、员工号、tenant、device 或 session 选择器。raw 只供已验证的本人任务 credentials scope；outbound 只供已验证的本人任务 outbound scope。D 必须先核宿主 owner、任务能力的 audience/scope/期限/撤销，并对响应提交再次核验；C 也核同一连接 owner、上下文及当前身份。

C 用自己持有的当前平台凭据调用既有 `POST /api/iam/v1/me/uac/credentials {}`；平台 access/refresh token 从不交 D。响应 `ShellAuthHostCredentialsResult.credentials` 包含 IAM 权威 binding、credentialVersion、empNo、当前 token、updatedAt 和必要的可选 cookies。binding 的 tenant/member/device/provider/sourceSessionId/authGeneration 必须与 broker 当前已验证身份及 IAM 权威状态一致；不接受调用者输入作为绑定。IAM 需在现有凭据结果增加可选 binding/credentialVersion，给新 broker 的读取必须提供两者；可选仅为旧消费者兼容。

读取的是权威当前值，不使用登录时的 token 副本。复验换新提交后下一次读取返回新 token/credentialVersion；该版本只用于读取/CAS元数据，不是准入代次，正常换 token 不让有效来源身份掉线。binding.credentialVersion 若提供须与顶层 credentialVersion 相同。并发刷新时取完整一致版本，若结果提交前版本改变则有界重新读取；退出、来源代次/身份改变后丢弃晚到结果，只返回安全错误。

`allowRawAgentToken` 默认 true。D 和 C 都实时读取受信配置，缺省 true，读取配置失败则 raw 失败关闭。关闭后下一次 purpose=raw 返回 forbidden/raw_token_disabled，提交前再次检查以截住正在进行的 raw 读取；D 映射为既有 HTTP 403 raw_token_disabled。purpose=outbound 继续允许受信 handler 取得内存凭据并代发，但不能把它作为原始凭据接口回给 skill；部署关闭 raw 后任务只注入 outbound scope。

成功与 error 互斥。ShellAuthHostError 只返回固定 code/reason/retryable：未认证 unauthenticated，非插件角色/跨 owner forbidden，旧 epoch/revision/身份 conflict，过期/已撤上下文 forbidden，材料未就绪或权威读取故障 unavailable。只有 unavailable/capacity_exceeded 可有界重试，不能在失败时退回旧缓存。所有错误无上游原文、token、Cookie 或身份选择器。

秘密仅经已认证 IPC 进入本人后端 handler 内存；不得落盘、进入访问/调试日志、诊断、stdout/stderr、环境全局变量、页面、模型参数或工具结果。响应不订阅、不广播、不缓存。raw 经既有受控 handler 才交本人 skill 进程即时使用；outbound 只输出业务响应。退出时立即清材料及本地能力，网络撤销排队不能延迟本地停权。

## K3 profile_read 的账号与成员边界

选择以已验证 `/me/device-session` 的 memberId 为成员权威，不假定它与 accountId 相等。
`profile_read` 的固定上游改为 `GET /api/iam/v1/me/device-profile`，仍只接收 `{}`；C 持有的当前平台设备 Bearer 不交给 D。返回 StandardDeviceProfile：`{account:{id,tenantId,displayName}}`，账号 id 只与已验证设备 JWT sub 比较，tenantId 与当前租户比较；C 输出 `{memberId,displayName}`，其中 memberId 始终来自设备会话。D 不得用 account.id 推导成员 ID，不得选择设备或来源会话。

`StandardDeviceController` 的新接口与现有 device-session 共用 `StandardDevices.access` 的在线核验：签名/audience、已登记 client、tenant/account/device、Org 有效权限和当前来源状态均校验；在最终事务再次核对设备账号/来源，读取当前 active 账号资料。无查询参数，重复 Authorization 拒绝，响应 no-store，不返回或伪造浏览器 Me.session、Cookie 或令牌，不延长来源 idle。未知、失效来源或控制面失败均失败关闭。

选择独立设备资料接口，是因为现有 `/me` 依赖浏览器 sid，不能用合法的无 sid 设备令牌读取。浏览器 `/me` 的 Me schema 和处理逻辑保持不变；原来 C 调 `/me` 的接线在本批迁移为 device-profile，不允许回退浏览器 cookie。IAM 本分支已实现所需接口，AUTH-D 无须再等一个资料接口补丁；仍须在主控安排的真实 Desktop/DSH 入口联调时验证。
