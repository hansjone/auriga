# 共享宿主适配 P

基线：`f6001a02`；协议以 `contracts/shared-dsh/**` 为准。这里实现自有调用路径，不替换原生浏览器的 Cookie、index 或 WS 身份。不修改 DSH 原型或私有方法，不写 `process.env`，不使用全局当前用户，不回退独立实例 owner。

> 共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。

## 宿主装配

R 的共享 profile 选择包入口 `auriga-dsh/shared`，不得同时加载默认的独立 owner 入口。该入口依赖公开 Cordis 服务 webServer、connection、remoteAccess、tools、sessions、agents，以及由受信平台装配的 `aurigaSharedPorts`。缺依赖保持不可用；不得为试运行补 owner 凭据或允许所有方法。

`aurigaSharedPorts` 是本适配器的进程内装配对象，不是新增公网协议。联合接线将其方法映射到 K 的既有权威端口；所有输入和结果均须从受信服务取得，不能把请求正文直接当作该对象。

- `options`：physicalInstanceId、identityKeys（只信 E 身份用途的 Ed25519 公钥 Map）、capabilityKeys（企业 IAM 公钥 Map；需要 N 时以 audience 为键分别提供公钥 Map）、可选 now（Unix 秒）。用途之间不能复用信任集合。
- `resolve(binding, platformSessionId)`：每次核 W 分配及 IAM 成员/平台会话，返回 `{active,binding,platformSessionId,authGeneration,authorizationSetDigest}`；超时及不可用拒绝。不能仅用事件缓存当实时权威。
- `register(sessionBinding,parentSessionId)`：映射 W Register，事务登记且返回完整相同 SessionBinding。异归属冲突，父子继承全部归属。会话登记 ACK 及再次 resolve 成功后才发布宿主对象。
- `lookup(sessionId)`：映射权威归属读取，返回完整 SessionBinding。恢复不信宿主自报 owner；不得恢复未登记、旧 epoch 或旧 authGeneration。
- `authorizeTask(claims)`：调用 IAM/N 对应能力权威，每次核任务、jti、成员、会话、版本与退出状态，返回 `{active,credentialVersion,allowRawAgentToken}`。任务结束、退出、切换后 active=false。
- `resourceView(binding)`：映射 W/R 的平台视图，只允许匹配授权集合、有效租约。P 拒绝失效调用；外部停池、ResourceReport 和 guardian 的落盘及收据由 R/W 完成。
- `createPeer(options)`：用公开 `new Peer(ctx, options)` 创建非 owner Peer；该工厂由宿主装配提供。不能假定 connection 存在未验证的 createPeer 方法。每次 HTTP 调用结束释放 Peer。
- `sessions.prepare(id,{meta:{cwd}})`、`publish(prepared)`、`discard(prepared)`：对接公开 SessionStore prepare/enter 与撤销生命周期。prepare 必须未发布；失败必须 discard。cwd 由 P 按 K 固定池路径生成，客户端只可传 parentSessionId。
- `invoke(action,binding,args)`：有限 action 的公开宿主调用。P 调用前后都重核本人归属；不得将未经检查的 sessionId/cwd 从 args 再次传入宿主，以覆盖已核 binding。
- `assertProfile(expected)`：同步核对实际加载配置，只有全部已卸载的能力与固定平台 skill 根符合预期才返回 true。执行工具、terminal、jobs、subagent、stdio MCP、动态安装、个人 Key、私有 skill 均关闭；includeDefaultRoots=false，customSkillDirs 只能是 R 的平台活跃根。返回 false、Promise 或缺失均拒绝启动。该接口不是 P 已实际卸载控制器的证明，真实 profile 验收留联合接线。
- `disconnect(revocation)`：主动断开匹配 placement 的自有连接；W/节点撤销事件调用 `ctx.aurigaShared.revoke(event)`，清本地归属投影并使 remoteAccess 缓存失效。进程重启后的围栏仍由 W/IAM 持久化保证。
- `issueTask(binding,execution,['outbound'])`：沿 IAM 既有授权接缝签发 K 能力；从真实 tools execution 获取任务，不接受模型指定成员、会话、taskId。有限企业工具不领取 credentials scope。
- `config()`：实时读取私有部署配置 `{allowRawAgentToken,outboundAllowedHosts}`；raw 缺省为 true，明确 false 后下一次原始读取拒绝。空主机清单拒绝全部。

`enterprise` 子对象提供 `taskId(token)`、`credentials(binding,claims)` 与 `outbound(request,material,binding)`。taskId 从任务能力解析，P 随后严格验签并与任务权威核对。credentials 调用既有 IAM 企业材料读取，须按本人绑定返回最新 `{empNo,token,updatedAt,credentialVersion,cookies?}`。outbound 由企业私有适配器重建认证头、禁重定向并执行 HTTPS 请求，按 timeoutMs 实际取消网络请求，返回 `{statusCode,headers,body,json}`；不得记录材料或在异常中夹带材料。P 只保留有限请求头、响应头，已知 token/Cookie 值与结构化认证字段脱敏，并在异步返回前再核能力和版本。

以上端口必须真实实现后才能启用共享；本目录测试全部以替身装配，不冒充 IAM/R/N 已接线。

## 自有 HTTP 与工具入口

`POST /auriga/shared/{session,invoke,resources,status}` 每请求消费 E 签名 Auriga-Identity，精确绑定方法与去部署前缀的规范路径，校验 60 秒、单值、重放及实时分配。未知路径或方法拒绝。session 只收 parentSessionId；invoke 只收 action、sessionId、args，action 明确允许表见 runtime.js。resources 只收本人 sessionId；status 返回完整风险及只读平台设置来源。这里是供联合接线的 JSON 载体，完整非 owner 浏览器页面与 WS 不在本线实现。

两条兼容 helper 路由为 `GET/POST /dsh-team/agent-credentials` 与 `POST /dsh-team/outbound`。按真实 socket 限回环；不信转发地址。支持 `X-DSH-Session-Id`、`X-Dsh-Team-Task-Capability`，以及正文 sessionId/dshSessionId、taskCapability/capability；并存不一致拒绝。成功原始材料仍为 `{empNo,token,updatedAt}`，no-store；出站保留上游 statusCode 和 X-Dsh-Team-Outbound-Status。企业能力每次严格核 600 秒、scope、taskId、完整 SessionBinding、材料版本和撤销。raw 只从兼容 handler 返回给受控 helper，不注册为 AI 工具。

注册有限工具 `auriga_enterprise_request`、`auriga_shared_resources`。执行时核真实 agent/session 对象，从登记表取归属，再取任务能力，不接收模型提交的企业 token。ToolRuntime guard 拒绝其余所有工具；pre-execute 的允许不能覆盖 guard 的单调拒绝。guard 外控制器须由 R 的 profile 实际关闭。

本基线未包含设计引用的原型 handleAgentCredentials、handleOutbound、shellEnv 注册及 uds_skill_auth.py。此实现按冻结 wire 格式新增共享 handler，不假称已迁入原型 helper。共享模式禁 shell，不注册全局 shellEnv；既有 request()/resolve() 的 Python 原型原文、桌面兼容和真实 IAM broker 需由 auth-shell 原工作线串行提供后补核。

## 验证与界限

根目录执行 `node --test clients/dsh-plugin/src/shared/shared.test.mjs`；已纳入插件 test 脚本。测试密钥均运行时生成。schema 快照与 K 原文件逐对象比对，不改 contracts。签名及 schema 只覆盖使用的七种冻结消息，不把轻量校验器当通用 JSON Schema 引擎。

双成员、双企业应用均为替身；临时 HTTP 服务只监听随机回环端口，结束即关闭。原生 owner 驱动 B 会话、按 B 企业身份执行业务且审计归 B 的测试是显式模拟反例，不是真实原生页面复测。真实 DSH、Web/WS、原生文件跨读、Linux、普通用户页面和 NAS 部署仍由主控串行联合验收。任何这些项目未测，都不能写“已隔离”“已交付”或“可验收”。
