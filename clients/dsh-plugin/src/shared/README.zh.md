# 共享宿主适配 P

契约基线为 `f6001a02`，已合入 #337 后的 main `edcdb32b`。协议以 `contracts/shared-dsh/**` 为准。自有调用路径按请求校验归属，任务变量通过不可变对象传递。

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

工具行使用公开配置 `mode: native`，对应 SHARED_PROFILE.toolPresentation。该配置让有限工具可被模型直接调用。R 的 profile 核验同时检查这一配置，公开 ToolRuntime 测试也按此配置装配。

- `disconnect(revocation)`：主动断开匹配 placement 的自有连接；W/节点撤销事件调用 `ctx.aurigaShared.revoke(event)`，清本地归属投影并使 remoteAccess 缓存失效。进程重启后的围栏仍由 W/IAM 持久化保证。
- `issueTask(binding,execution,['outbound'])`：沿 IAM 既有授权接缝签发 K 能力；从真实 tools execution 获取任务，不接受模型指定成员、会话、taskId。有限企业工具不领取 credentials scope。
- `finishTask(binding,execution,token)` 在任务结束时撤销该任务能力。有限企业工具在 finally 释放能力。受信 helper 使用 `ctx.aurigaShared.openTask(execution)` 返回的上下文，结束时调用 close()。该上下文提供 DSH_SESSION_ID、DSH_TEAM_TASK_CAPABILITY、DSH_TEAM_BASE、DSH_WEB_URL 和 DSH_TEAM_HELPERS，helperBase 由宿主设置为回环 `/dsh-team` 地址。变量只交给对应任务的受控 helper。
- `config()`：实时读取私有部署配置 `{allowRawAgentToken,outboundAllowedHosts}`；raw 缺省为 true，明确 false 后下一次原始读取拒绝。空主机清单拒绝全部。

`enterprise` 子对象提供 `taskId(token)`、`credentials(binding,claims)` 与可选的 `outbound(request,material,binding)`。taskId 从任务能力解析，P 随后严格验签并与任务权威核对。credentials 调用既有 IAM 企业材料读取，须按本人绑定返回最新 `{empNo,token,updatedAt,credentialVersion,cookies?}`。默认 `enterprise-transport.js` 从私有配置 empNoHeader/authValueHeader 重建认证头，以禁重定向和实际超时取消执行 HTTPS 请求，返回 `{statusCode,headers,body,json}`。部署可提供经过同等校验的 transport。P 只保留有限请求头、响应头，已知 token/Cookie 值与结构化认证字段脱敏，并在异步返回前再核能力和版本。

启用共享前需要装配这些权威端口。W/IAM/R/N 在本目录以契约替身验收，rc.3 公开宿主组件另行实测。

## 自有 HTTP 与工具入口

`POST /auriga/shared/{session,invoke,resources,status}` 每请求消费 E 签名 Auriga-Identity，精确绑定方法与去部署前缀的规范路径，校验 60 秒、单值、重放及实时分配。未知路径或方法拒绝。session 只收 parentSessionId；invoke 只收 action、sessionId、args，action 明确允许表见 runtime.js。resources 只收本人 sessionId；status 返回完整风险及只读平台设置来源。这里是供联合接线的 JSON 载体，完整非 owner 浏览器页面与 WS 不在本线实现。

自有载体的 workspaceFiles/read、readBytes 和 stat 先检查服务器映射的成员工作区前缀，再解析根与目标的 realpath 并复核范围，向宿主传入已解析路径。缺失文件、目录穿越、其他成员的绝对路径和指向根外的符号链接会被拒绝。这一入口检查仍有同 UID 硬链接和检查后竞态风险，原生 owner 的文件访问风险继续按设计3.1披露。

两条兼容 helper 路由为 `GET/POST /dsh-team/agent-credentials` 与 `POST /dsh-team/outbound`。按真实 socket 限回环；不信转发地址。支持 `X-DSH-Session-Id`、`X-Dsh-Team-Task-Capability`，以及正文 sessionId/dshSessionId、taskCapability/capability；并存不一致拒绝。成功原始材料仍为 `{empNo,token,updatedAt}`，no-store；出站保留上游 statusCode 和 X-Dsh-Team-Outbound-Status。企业能力每次严格核 600 秒、scope、taskId、完整 SessionBinding、材料版本和撤销。raw 只从兼容 handler 返回给受控 helper，不注册为 AI 工具。

出站 method 统一大写，正整数 timeoutMs 超过60000时截到60000。业务请求头保留允许表内的字符串，丢弃非字符串值，并拒绝 CR/LF。身份和任务能力头仍按 K 的单值字符串规则严格检查。

注册有限工具 `auriga_enterprise_request`、`auriga_shared_resources`。执行时核真实 agent/session 对象，从登记表取归属，再取任务能力，不接收模型提交的企业 token。ToolRuntime guard 拒绝其余所有工具；pre-execute 的允许不能覆盖 guard 的单调拒绝。guard 外控制器须由 R 的 profile 实际关闭。

Python helper 从 oclaw `0321a05b0db3797fdb5320aacabe5244436972f6` 的两处 `uds_skill_auth.py` 原文迁入，分别位于 `skills/uds-skill-auth/scripts` 和 `skill-helpers/python`，随包保留上游 MIT 许可。两 handler 已对照同版本 `dsh-team/lib/agent-auth.js` 的接口行为与 auth-shell 第3.1.2节，按 K 的共享签名能力及逐调用权威校验适配。测试用原 helper 的 `request()` 和 `resolve(apply_env_aliases=False)` 调用真实临时回环 handler，覆盖 raw 默认开启和关闭后的下一次拒绝。平台 skill 发布时由 R 放入签名活跃根。

## N 模型与消息接线

共享入口要求 `llm` 服务和 `ports.downstream`，向公开 LlmRuntime 注册 `auriga-shared` adapter。`ports.downstream.taskForSession(binding,signal)` 从真实在途任务返回 taskId 与稳定 callId，`issue(binding,taskId,audience,scopes,signal)` 接 N 的内部签发端口。`model(request)` 接 N SharedProvider.Invoke 并把流转为公开 LLM chunk。request 的 tenantId、sessionId 来自登记归属，taskId、callId 来自宿主，capability 经独立用途公钥校验。body 是模型参数，N 的内部传输适配负责序列化为 SharedRequest.Body。

`send(request)` 和 `receive(request)` 对接 N SharedBroker 的发送/接收端口，保留 messageId、target、body，按登记会话取得 message.send 或 message.receive 能力。目标授权、64KiB 上限、背压、去重、收据和单次投递继续由 N/msgnet-v2 承担。P 在调用前后与每个模型分片发布前重核能力，关闭时合作取消在途模型流。生产传输桥须按 `infra/ai-gateway/SHARED-DSH.zh.md` 映射到 N 的 Go 内部端口，当前以契约替身验收。

## 验证与界限

根目录执行 `node --test clients/dsh-plugin/test/shared/shared.test.mjs`，已纳入插件 test 脚本。测试密钥均运行时生成。schema 快照与 K 原文件逐对象比对。签名及 schema 覆盖使用的七种冻结消息。

公开组件的 rc.3 package.json/package-lock.json 固定在 `clients/dsh-plugin/test/public-host`，来源为 SPIKE 提交7e7ea403。根目录执行 `npm ci --ignore-scripts --prefix clients/dsh-plugin/test/public-host`，将 AURIGA_DSH_PUBLIC_PACKAGE_ROOT 指向该绝对目录，再执行 `node --test clients/dsh-plugin/test/shared/public-host.test.mjs`。Plugin CI 的 Windows/Linux 两个任务均执行安装并设置该路径，CI 缺少路径时测试直接失败。测试组合公开 API，验证 pre-execute 放行后的 guard 拒绝及原生 owner/文件访问反例。本地未设置路径时可单独运行其余单元测试。

双成员归属与企业应用通过替身验收，临时 HTTP 服务使用随机回环端口并在结束时关闭。锁定 rc.3 公开组件实测了原生 Cookie 将 A/B 映射到同一个 owner，以及该 Peer 通过 Gateway 读取 B 会话目录和本人会话跨目录文件。企业调用按 B 身份执行、审计归 B 的反例继续由企业应用替身记录。正常门户页面、完整 Web/WS、Linux 及 NAS 部署转主控串行联合验收，并保留第3.1节全部共享风险披露。
