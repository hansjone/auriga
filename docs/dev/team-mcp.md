# Team MCP 内部持久通信核心

本批只交付 `collab` 数据与内部 RPC，外部 `/ai/mcp`、生产凭据准入和 DSH 插件接线尚未实现。
总部是独立 DSH 系统；其系统身份、管理员入口另行确定，不能把管理员当成所有成员。
不创建业务 Task、任务模板、调度器或 AgentBus 工作流。

## 契约与准入

接口为 `contracts/proto/auriga/collab/v1/collab.proto` 的九个 RPC，工具参数形状见
`contracts/plugin/team-mcp.schema.json`。该 schema 不启用任何外部工具入口。
注册、绑定、拉取及 ACK 为受信 Node 私有操作，不能暴露给模型或浏览器。

只接受内部 `svc:ai-gateway`，经 common 内部 JWT 校验、受认证传输和授权代租户，
RPC 的 `actor.tenant_id` 必须等于实际委托租户。跨主机需要现有 common mTLS；
无 TLS 时 common 服务只允许回环监听，测试使用随机回环端口。
内部服务身份不自动授予其声明的 Actor 权限。

`CollabAdmission.verify(actor,operation)` 必须在线校验凭据状态、凭据主人、有效成员及动作授权。
发送时另对收件端点执行 `RECEIVE` 准入。生产尚无该组合链，缺省实现始终
`common.unavailable`；数据库端点存在或 JWT 尚未过期均不能替代。
测试只替换外部控制面，不替数据库、RLS 或 JWT/gRPC 认证。
不使用 `admin` 角色替其他人建立端点、会话或 ACK。

会话来源遵循 ADR 0016：受信插件读取 `exec.agent.session.id`，每次请求构造三个既有
`auriga/sessionId`、`auriga/toolCallId`、`auriga/rootCallId` 元数据。
meta 不是身份凭据；平台校验端点、会话绑定与线程参与者。
模型参数 schema 拒绝传入 Actor、tenant、源 session 及 `_meta`。
Node 进程和其凭据是信任边界，不增加会话 capability/nonce 协议。

## 数据、次序与重试

迁移位于 `services/collab/impl/src/main/resources/db/collab`，用 collab schema，先跑
common 迁移，再跑服务 Flyway。表为 endpoint、session_binding、thread、thread_participant、
message、delivery、request_dedup；均带 tenant_id、RLS 与包含 tenant 的外键。
迁移角色拥有表；运行角色只有 DML、无 owner/BYPASSRLS 权限。

来源端点绑定到一个设备或实例凭据主体，主人不可原地更换。会话 ID 为不透明字符串。
源端点创建线程时固定源会话参与者；目标端点在真实创建/恢复 DSH 会话后绑定该线程，
一旦绑定不能静默换会话。同一账号的其他端点或同一端点的其他会话不自动取得线程权限。

Open/Reply 先以不加写锁的查询核来源端点主人/凭据、会话绑定及必要参与关系，
拒绝请求不得先锁其他参与者端点；预检也不能改成源端点优先加锁。
随后把来源与参与端点全部按 UUID 排序加锁，再核端点状态/参与绑定与在线准入，
串行化发送、领取、ACK 与撤销。线程行锁分配单调 sequence；
消息和对方的 delivery 同事务写入。按 sequence 读取，不按本机时间推断顺序。
幂等作用域 `(tenant,source endpoint,source binding,operation,key)`；同键同体返回原消息，
异体 `common.conflict`，未成功事务不留下幂等占位。
本批不清理幂等记录，覆盖全部已保留消息的重试；后续保留策略不能先删除去重记录再接受旧键。

每次 pull 1—100 项，只领取每线程最早尚未确认的消息，活跃租约阻止越过该消息。
租约固定 30 秒；过期重领更换 token，旧 token 不能确认新租约。
ACK 只允许精确接收端点及已绑定会话，要求当前 token、未过期租约。
宿主 prompt 的 `requestId` 必须使用服务端 `delivery.id` 的规范小写 UUID 字符串，
ACK 的 `host_request_id` 必须逐字相等；不匹配时服务端返回 `common.conflict`，不推进投递。
不同 delivery 因而不能复用同一个宿主请求身份，包括同会话跨线程的投递。
同一 delivery 的所有宿主重试、重领租约仍用同一 requestId；相同合法 token/requestId
的成功 ACK 可幂等重试，旧租约仍被拒绝。不得每次随机生成或沿用上一条消息的 requestId。
状态 pending/leased/host_accepted 只表示平台待投、租约或宿主接受，不表示模型已读或完成。
端点离线不伪造 delivered；本批没有心跳在线状态声明，也不启动后台定时器。

本地端点撤销提交后的新 send/pull/ack 被拒。IAM 设备、成员及实例的实时撤销仍需生产
CollabAdmission 在线源，当前缺省拒绝，不承诺仅靠本地标志实现外部吊销。
已被宿主接受的副作用不能撤回。积压保留/配额运营策略未交付，外部服务未开放。

## 装配与当前限制

`CollabRuntimeConfiguration` 只在 `auriga.service.schema=collab` 且模块启用时装配，
使用现有 DataSource/TenantTransactionManager，注册公共内部 gRPC 服务 bean。
配置 Flyway schema/location 为 collab 与 `classpath:db/collab`。
现 combined 配置中 collab=false，本批不修改公共平台的多数据源或安装配置。
缺省 admission 不可替换为 dev-active/skip-auth；未来生产实现需独立审查后接入。

现有 `SessionService.IntrospectSession` 输入浏览器 cookie，只允许 edge，不是设备 JWT introspect。
`InstanceAiBindingService.GetInstanceAiBinding` 可供未来 gateway 在线核托管实例，但现 instance
权限限模型调用，内部精确路由表也没有 MCP，不得借此直接开放 Team。
`ait_` 心跳令牌不能调用 Team。设备刷新令牌始终仅 Node 持有，不进入模型、浏览器或日志。

## 已核宿主接缝与后续验收

固定 DSH rc.2 源码 HEAD `639ed015397290b3745d163aafe02ffee4aa3f84`：
`packages/api/session-controller/src/index.ts` 的 create(request)、prompt(request,signal)；
types.ts 的请求为 `{requestId,sessionId,mode:'queue',content:[{type:'text',text}]}`，
返回 `{accepted:true}`。commands.ts 用 source.rpcId 在 inbox 与历史事件去重，queue 走 followup。
插件应持久保存 deliveryId→sessionId，并始终令 requestId=deliveryId，串行化同一 delivery
的宿主准入；固定该身份重试，accepted 后再 ACK。不能把自然语言输出自动当线程 reply。

后续真实闭环必须有独立总部与成员的真实 DSH：源工具开聊、指定端点真实入队、成员工具
回复、总部原线程收到，覆盖断网、重连、ACK 丢失和进程恢复；本批内部核心测试不冒充该验收。

## 检查

在 Windows 每次 shell 先点源工作区 `.agents/auriga-env.ps1`。

```powershell
.\mvnw.cmd -pl services/collab/impl -am verify '-Dtest=CollabPostgresTest,CollabGrpcTest' '-Dsurefire.failIfNoSpecifiedTests=false'
node --test contracts/test/team-mcp.test.mjs
```

契约检查还包含 Buf lint/兼容性和 Go 生成一致性；新增 schema 测试已接入现 check-schemas 显式列表。
涉及 contracts 的 PR 标【待审查】，所有 PR 必须由非作者独立审查，作者不自行合并。
