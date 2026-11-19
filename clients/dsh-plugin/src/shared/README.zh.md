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

内部安装工厂可为独立实例复用底层授权：`createSharedRuntime({expectedMode})` 缺省 `shared`，只接受精确 `shared` 或 `dedicated`，每次绑定均必须匹配安装侧指定模式。`createProductionSharedPorts(ctx,{profileValidator})` 缺省保留共享 profile 校验；只有内部代码可提供同步 `(ctx,expectedProfile,skillRoots)=>true` 验证函数，Promise 或其他值都不能授予 profile。这两个参数不从浏览器或 YAML 读取，不放宽共享 guard，也不将共享消息原生来源扩到 dedicated。

容器内 DSH SDK 与 Auriga 插件分别安装在只读 `/opt/dsh`、`/opt/auriga-dsh`。`host-sdk.js` 的生产 `loadHostSdk()` 只沿固定 `/opt/dsh/package.json` 解析公开导出，返回同一 SDK 的 `{Peer,LlmAdapter}`；使用 `profile-resolution.js` 的真实路径、包归属和公开 export 校验，不从配置、环境变量或浏览器选择模块，也不回退旧开发依赖。ports 插件装配 `createProductionSharedPorts(ctx,{Peer:sdk.Peer,sdk,...})`，Host 使用 `createSharedDownstream({runtime,ports:ports.downstream,sdk:ports.sdk})`。两个工厂的 SDK 参数仅内部代码传递，Peer 与 sdk.Peer 不同即拒；缺 SDK 明确失败。独立实例模块复用相同内部 shape。普通纯单测可显式注入合成类；锁定 rc.3 组合测试验证真实 Peer、LlmAdapter 及公开 LlmRuntime 的类身份和登记。

工具行使用公开配置 `mode: native`，对应 SHARED_PROFILE.toolPresentation。该配置让有限工具可被模型直接调用。R 的 profile 核验同时检查这一配置，公开 ToolRuntime 测试也按此配置装配。

共享技能根是容器内唯一固定只读挂载 `/run/auriga/skills-active/current`，与 Agent 正式 writer、renderer 和 Docker mount 一致；Host 不接受宿主机 PoolLayout 路径、子路径或额外根。`skill-filesystem` 必须关闭默认根、不设置 bundled 根，provider 为 `auriga-shared`。原生 `skill` 工具沿公开 `tools/execute` 继续调用原 Registry/Filesystem；Host 前后核真实会话代次，并要求实际定义的 provider、source、完整路径和资源目录属于平台视图，返回正文与已核定义一致。私有目录、运行时同名覆盖及链接逃逸不能借这个工具加载；Shell、插件安装及其余非白名单工具仍拒绝。

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

共享消息的生产 Host 从真实当前 agent/session、工作目录及公开持久化 provider 生成进程内见证，经固定 Node socket 的 `messageContext` 在当前 K 能力下取得实际 typed-local 注册。首调用只物化关联，不发送、不拉取、不默认开启接收。回包后 Host 使用原生 resolver 核活体与 local，再生成新的见证供业务调用；不从模型参数或进程 owner 推导成员身份。操作 ID 对同会话、原生绑定与 Host boot 稳定，不随短任务或令牌 JTI 改变。

`shared-host.json.messageTrust` 配置为 `{realm,roots:[{kid,public_key_spki}]}`，根来自获准的正式网络安装材料。Host 延迟打开固定的 `host/message-trust` 原子缓存，按现 `openV2TrustStore` 验证并持久保存原 control、publication、announcement 与首发/版本高水位。`messageContext` 随带的三个公开签名对象只能用于沿已安装根刷新，不能自行成为信任根。缺配置、过期、撤销或换根只拒消息能力，不阻模型服务装配。Node 必须保证该 Host UID 可以写固定缓存且保留跨重启。

SourceGet、FETCH 页及各项、attempt/admit/receipt 等结果按原网络信封验证实际签名、受众、连接 epoch、channel nonce 和原请求 ID。SUBMIT 保留既有无签名 HTTP SendResult，使用从原契约生成的校验器及本次实际 from/to 校验，不伪造服务器签名。拉取不等于原生持久接收，原生接收前不得回报收据。后台 receive 仅允许 fetch、attemptBegin、executionAdmit、receipt、receiveSet 五个固定操作，不向模型注册 receive 工具。

有限 `auriga_send_message` 工具只接收 aur1 目标地址和既有 body 结构，按实际宿主会话与公开 `execution.callId` 生成稳定幂等键；不接受模型自报发送者、owner 或任务。服务端仍核目标的真实 typed-local 类别与当前权限。模型结果沿原工具的统一提交投影，不返回私有投递和线程标识。独立/桌面网络路径未改变。

接收通过本人会话的显式开关管理，默认关闭。`ctx.aurigaShared.messagingSettings(peer,{sessionId,enabled?})` 省略 `enabled` 时读本人真实地址与接收状态，传布尔值时调用原 `receiveSet`；前后均重核本人会话权限。`messagingSessions(peer)` 仅列公开宿主中本人可访问的会话。正常原生浏览器模块消费这两个受信接口；受限 JSON 载体也支持无 query 的 `POST /auriga/shared/messages`，body 仅允许 `sessionId` 与可选布尔 `enabled`，沿既有新鲜 E 身份。状态区分实际开关、持久意图、待确认及不可用，不把点击或 RPC 受理当作收件成功。

rc.3 原生 `session/list`、`session/control` 先按 `{kind:'global'}` 进入公共读控制器，再逐个会话和增量按本人 session role 过滤。共享策略只对这两个 action 同时给最小 global 入口与本人 session 权限；不包含原生 create、全宿主 modelCatalog、credential、MCP 或全局事件。会话创建始终经可信 Host 的登记后发布流程。正常浏览器模块仍须逐调用和流输出前后核归属与代次，不能把同步 policy 投影代替在线权威。

显式意图原子保存在 `/var/lib/auriga/pools/<pool>/host/.host/message-preferences`，收件账本保存在同目录的 `message-inboxes/<contextId>`，复用 Node 已施加的 `.host` 硬配额，不新增预算。正式信任缓存仍在已有单独硬配额的 `host/message-trust`。后台仅处理已明确开启、且原生 agent 仍在内存中的会话，不自动恢复冷会话。新账本只在平台证明邮箱从未开启且无历史（`false/1/0/0`）时初始化；已有邮箱丢失本地账本明确拒绝。平台实际 typed-local 注册、native resolver 和来源权威保持不变，这份账本只为原 `DurableInbox` 提供收件投影，不是 owner 或地址分配账本。

串行升级时，Host 自动锁住旧 `host/message-preferences` 或 `host/message-inboxes/<contextId>` 账本，将完整内容以新文件原子写入 `.host` 配额目录，保留旧原件和摘要，不移动可能带旧 project-id 的 inode。新账本后续写入保留该摘要；发现旧写者仍活跃、旧本后来分叉、缓存损坏或配额写入失败均明确拒绝消息，不清除收据、游标或高水位。升级应先停止旧 Host；新版本运行期间保留旧锁，避免旧版本并发回写。

真实后台顺序为签名页与逐项验证、持久接入、原尝试/执行准入、公开 `NativeHostPort.prompt`、会话和 provider 的持久 flush、持久 `accepted`，最后提交原收据。丢失收据回包后从原账本重试相同操作，不再次 prompt。禁用先同步隔离正在拉取的接收者，再持久意图和远端确认；未确认的禁用不能继续执行消息。写入结果不确定时停止该写者，不能用旧内存覆盖磁盘或假报成功。

普通受控任务只申请独立 `task.execute`，不以公司 UAC 材料为前置，也不增加 Shell、终端或任意插件权限。IAM 明确返回 `SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT` 时，团队登录用户仍能执行已有普通任务，任务环境中的 `DSH_AURIGA_ID`、`DSH_AURIGA_TOKEN` 为空。`AVAILABLE` 才为同一真实 execution 另开独立企业任务，按现有策略传递凭据；两任务分别关闭。未知状态、失效平台登录、撤销或他人材料仍拒绝，不能将认证异常降为“无材料”。环境只属于当前任务，刷新后下次任务重取材料，不修改共享进程全局环境。

## 完整原生 turn 的普通任务准入

`createTurnAdmission` 在真实公开 `agent/request` 上按 agent 对象、turn 和原生 signal 登记唯一普通 `task.execute` 根。后续模型步骤、重试、受控工具与消息子任务共享该根；`AVAILABLE` 的企业 token 不作为父。工具必须是公开 ToolRuntime 的实际 execution；模型还须匹配原 request signal，消息工具传递其真实 execution，不能按会话或模型参数猜一个现有父。

真实完成点是公开 `session/event` 的 `turn/end`，对应 rc.3 AgentLoop 的 `finally`。该通知不等待异步清理，Host 先同步围栏，再等待仍在运行的调用和实际 Finish 确认；取消时也等已开始的工具体真正结束。下一轮不能越过旧根的关闭屏障。所有 UDS `finishTask` 回包必须明确 `fenced: true`，并精确回显完整 `binding`、`taskId`；空回包、错归属、未知 Begin 或 Finish 都不能当作已释放。Node 仍负责真实 W 准入、内存门禁与 IAM/W 结束围栏，本 Host 不替代这些权威服务。

内部 `openSharedExecution` 的 `ordinaryToken` 与企业 `token` 分开，生产子 Begin 仅从私有 ordinary lease 刷新后取 `parentCapability`。独立消息与设置在各自有限的 `scope.run` 中使用普通根，覆盖原持久收件、原生 prompt/flush 和签名收据；持久意图或 inbox 不永久占任务槽。

独立实例的内部工具持有者可调用 `const settle = task.retain()`，但必须由其真实 JobRegistry 注册事件与同 execution 的会话日志证明作业因果；不得由工具结果 JSON 声明后台留存。`settle()` 幂等且仅归还该留存，之后还须 `await task.close()`，由子 Finish 到父 Finish 全部确认才释放。正常 `turn/end` 只允许已留存的旧执行继续刷新并保有预算，新一轮等待它结束；撤销立即失权，仍等真实作业终态才 settle。共享 profile 禁用 jobs，公开借用的 `openTask` 也不暴露 `retain`；真实独立实例 JobRegistry 的消费由独立模块负责。

共享入口实际安装公开 `tools/execute` 钩子和被动 `shellEnv` 注册表贡献者：按真正原生 execution 准备任务，再由同步 `shellEnv.collect(execution)` 取本次环境，结束前在同任务刷新并核当前归属，最后清除材料及结束两类 handle。保留的 `DSH_SESSION_ID` 由宿主注册表自身提供，贡献者只声明自己的材料和 helper 变量。原企业工具复用钩子内的企业子任务，`aurigaShared.openTask` 只借当前原生调用，不另分配或提前 Finish。退出、取消、agent dispose 和晚回包不能恢复旧材料或已结束任务。长运行 helper 沿原回环能力刷新当前材料；已启动进程的环境快照不会被全局改写。安装侧须提供 `@deepseek-ai/dsh-shell-env` 被动服务，并且同一模式只装一个环境发布者；这不启用共享 Shell 或新增通用脚本执行端。

根目录执行 `node --test clients/dsh-plugin/test/shared/shared.test.mjs`，已纳入插件 test 脚本。测试密钥均运行时生成。schema 快照与 K 原文件逐对象比对。签名及 schema 覆盖使用的七种冻结消息。

公开组件的 rc.3 package.json/package-lock.json 固定在 `clients/dsh-plugin/test/public-host`，来源为 SPIKE 提交7e7ea403。根目录执行 `npm ci --ignore-scripts --prefix clients/dsh-plugin/test/public-host`，将 AURIGA_DSH_PUBLIC_PACKAGE_ROOT 指向该绝对目录，再执行 `node --test clients/dsh-plugin/test/shared/public-host.test.mjs`。Plugin CI 的 Windows/Linux 两个任务均安装并设置该路径，同时设置 AURIGA_REQUIRE_PUBLIC_HOST=1，缺少路径时测试直接失败。其他工作流未提供宿主路径时，两项测试跳过并打印由 Plugin 工作流承担验证的原因。测试组合公开 API，验证 pre-execute 放行后的 guard 拒绝及原生 owner/文件访问反例。

双成员归属与企业应用通过替身验收，临时 HTTP 服务使用随机回环端口并在结束时关闭。锁定 rc.3 公开组件实测了原生 Cookie 将 A/B 映射到同一个 owner，以及该 Peer 通过 Gateway 读取 B 会话目录和本人会话跨目录文件。企业调用按 B 身份执行、审计归 B 的反例继续由企业应用替身记录。正常门户页面、完整 Web/WS、Linux 及 NAS 部署转主控串行联合验收，并保留第3.1节全部共享风险披露。
