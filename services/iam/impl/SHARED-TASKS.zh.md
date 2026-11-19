# 共享 DSH 任务权威

`SharedTaskService` 是可信 Agent 宿主与模型、消息服务使用的内部 gRPC 接口，复用 IAM 现有 mTLS、服务令牌验签和租户委托链。普通平台 JWT 不能作为共享能力，也不能直接调用此接口。内部令牌的 `auriga.internal` 是 audience，签发器不为其添加同名权限。

## 安装

IAM 与 Workspace 安装在同一 platform 组合内，必须已有生产 `SharedDshSessions` Bean。该接口按物理实例和共享会话查询 Workspace 的持久登记，并在线核对 ready placement、完整 binding、成员登录和 authGeneration。缺少它时，共享任务配置不能启动。

应用 IAM Flyway 迁移 `V18__shared_task_authority.sql` 后，设置：

```properties
auriga.iam.shared-tasks.enabled=true
auriga.iam.shared-tasks.allow-raw-agent-token=true
auriga.iam.shared-tasks.signing-keys.enterprise.kid=<enterprise-key-id>
auriga.iam.shared-tasks.signing-keys.enterprise.private-key-file=<absolute-enterprise-pkcs8-pem-path>
auriga.iam.shared-tasks.signing-keys.model.kid=<model-key-id>
auriga.iam.shared-tasks.signing-keys.model.private-key-file=<absolute-model-pkcs8-pem-path>
auriga.iam.shared-tasks.signing-keys.message.kid=<message-key-id>
auriga.iam.shared-tasks.signing-keys.message.private-key-file=<absolute-message-pkcs8-pem-path>
```

三份文件必须是不同的 Ed25519 PKCS8 PEM 私钥，路径必须为绝对路径，不能是符号链接，每份最多8192字节。私钥只安装在 IAM；N/P 分别配置其所需 audience 的验签公钥。此批没有新增私钥分发或 JWKS 端点，部署方负责匹配既有用途公钥。配置错误不输出私钥内容。

服务令牌 principal 必须为平台内部身份，委托租户必须精确等于 `CredentialInstallationScope` 的部署租户，不能是全零平台租户。`svc:agent` 可登记、签发、结束和读取本人材料；`svc:agent`、`svc:ai-gateway`、`svc:collab` 可在线复验。模型、消息服务不能读取企业原始 token。

## 宿主调用

协议为 `contracts/proto/auriga/iam/v1/shared_tasks.proto`。宿主先把真实 session/execution 对象核到其可信注册表，固定完整 K binding 与稳定 UUID taskId，再调用：

1. `BeginSharedTask(binding, taskId, scopes, audience)`：持久登记任务并返回签名 token 和 typed capability。空 audience 默认 `auriga.shared.enterprise`。团队用户没有 UAC 材料时，仍可显式登记具有当前平台权限的 model/message 任务。
2. `IssueSharedCapability(binding, taskId, audience, scopes)`：原活任务签发当前版本能力。企业 scope 为 `credentials`、`outbound`；模型为 `model.invoke`；消息为 `message.send`、`message.receive`。
3. `AuthorizeSharedCapability(capability)`：核对当前任务、完整已签发 claims/JTI、Workspace 登记、IAM 登录及当前权限，返回 active 和当前版本。
4. `ReadSharedCredentials(capability)`：仅企业 `credentials` 能力可读，返回字符串 `empNo`、`token`、`updatedAt`、整数 `credentialVersion`。工号保留前导零。
5. `FinishSharedTask(binding, taskId)`：幂等结束任务并留下墓碑，原 taskId 不能重新打开。结束接口不依赖已失效的 Workspace 会话，便于清理。
6. `ResolveSharedTask(tenantId, sessionId, taskId, audience, jti)`：模型、消息服务的实时权威快照。请求 tenant 必须与认证委托租户一致，audience 仅允许 model/message；有 JTI 时必须是本任务曾签发且仍保留的记录。
7. `ExecuteSharedOutbound(capability, url, method, headers, body, timeoutMs)`：仅 `svc:agent` 可调用，核企业 `outbound` scope 后在 IAM 内注入本人材料，返回脱敏业务结果；不要求 `credentials`，关闭 raw 后仍可使用。

Node relay 可使用驼峰 JSON 字段，但 K 签名内容不能用 protobuf JSON 序列化：它使用 K 规范 JSON，整数是安全数值，mode 是 `shared` / `dedicated`。`token` 是原样签名字符串，`capability` 是 typed RPC 参数；后者必须与本地验签出的内容一致。浏览器、模型参数及 helper 自报的 member/taskId 不能代替宿主真实对象映射。

企业续期后，旧 capability 直接 Authorize/Read 会因 credentialVersion 不符被拒。已运行 helper 只能以旧 capability 索引宿主本机仍活跃的原 execution/task handle，由受信 relay 使用原 binding/taskId 调用 Issue，再取最新材料。账号退出、重登造成的 session 或 authGeneration 变化不能通过这条续签恢复。已启动进程的环境不会被操作系统自动改写；后续新进程和主动读取 helper 才能取得更新，不能把个人材料写到共享进程全局环境。

raw 默认允许。将 `allow-raw-agent-token` 关闭仅禁止 credentials 签发和读取，原本获权的 outbound 代发仍可复验。配置通过当前 Environment 每次读取；配置源自身的动态刷新由部署宿主提供。

## 服务端企业代发

共享 Host 的 outbound 必须经可信 Node 调用 `ExecuteSharedOutbound`，不能再先读 `ReadSharedCredentials` 后本机注入。请求字段为 typed K capability、url、method、字符串 headers map、原始 bytes body 和 int32 timeout_ms；响应为 status_code、字符串 headers map、UTF-8字符串 body。Node socket 映射为 `enterpriseOutbound`，JSON使用capability/url/method/headers/bodyBase64/timeoutMs，显式解码base64到proto bytes。Host从返回的脱敏body解析json，取消操作需沿Node取消对应gRPC调用。

配置使用已有 `auriga.iam.uac.adapter-config-file`，在私有 JSON 文件中增加独立的 `sharedOutbound` 对象：

```json
{
  "sharedOutbound": {
    "empNoHeader": "X-Employee-Id",
    "authValueHeader": "X-Enterprise-Authorization",
    "allowedDestinations": [
      {"origin": "https://business.example.com", "addresses": ["192.0.2.10"]}
    ]
  }
}
```

现有UAC登录/验证配置仍使用原字段；sharedOutbound只允许这三个字段，必须填写实际获准的企业业务 origin、固定解析地址及企业请求头。Node/Host不能传配置或白名单。缺少对象时仅代发返回common.unavailable；提供错误对象时启动失败。生产只允许HTTPS完整origin（含端口），不接受用户名、密码、query、fragment或路径。DNS解析的每个地址和直接IP目标都须匹配该origin的地址列表；私有地址也只有明确列出才可用，不跟随redirect、不自动retry、不启用cookie管理。

请求只准 GET/POST/PUT/PATCH/DELETE/HEAD，timeoutMs 为1..60000，body和响应上限各1MiB，GET/HEAD不发送body。调用者可提供的请求头仅content-type/accept/accept-language/x-lang-id/x-requested-with；本人empNo/token头由IAM覆盖注入，保留工号前导零。所有网络I/O在IAM和Workspace事务之外，派发前、结果返回前均复验同一完整任务capability与当前credentialVersion。任务结束、退出、撤销或凭据变更导致晚响应丢弃，取消和超时中止HTTP请求。

只返回content-type/content-language和按脱敏后正文重算的content-length。JSON认证字段被删除，并收集其中的凭据值以遮盖其他字段的别名回显；本人token/工号、cookie与认证响应头值也不会返回。普通文本遮盖已知材料及认证字段值。JSON解析失败、超过边界和传输异常返回common.unavailable，不携带原错误详情、URL或正文。该代理不提供业务幂等或结果重放，Node/Host不能因超时/取消自动重试有副作用操作。

## 持久性与下游接线

任务和 JTI 保存在 IAM 强制租户 RLS 表中；任务与原平台 session、JTI 与任务均有复合租户外键。任务结束墓碑保留到原 session 绝对到期；能力最多600秒，JTI 额外保留150秒，使模型流可在自然到期后继续其已准入运行窗口，在线撤销仍生效。既有 UAC worker 每租户分批清理过期记录。

Workspace 查询在 IAM 事务外执行前后两次复验，避免 Workspace 在其事务中回查 IAM 时形成反向锁序。调用方不得携带已有 IAM 事务进入这些方法。

`infra/ai-gateway/internal/sharedauth.NewRPCGate` 把 typed `SharedTaskServiceClient` 接到既有 N Gate。客户端必须复用现有 mTLS 与可轮换服务令牌；adapter 精确覆盖出站委托租户，单次查询最长5秒，权威不可用时拒绝。Gate 继续负责用途公钥验签、初始期限、完整 binding、scope、版本和流内在线检查。

N 的共享模型 Invoke 已在 ai-gateway `--bifrost-config.sharedModel` 主程序监听器组装，复用真实 RPC Gate、Bifrost/SharedProvider 和持久账本；配置见该组件的 SHARED-DSH.zh.md。宿主→Node Unix relay、可信主任务/callId 映射和企业代发新RPC仍须联合接线及部署验证。消息还缺 msgnet-v2 的 typed-local/source_binding/producer_epoch 目录及持久执行准入/收据。必须完成正常门户实测后才能宣称共享对话可用。
