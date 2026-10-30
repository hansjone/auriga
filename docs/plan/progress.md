# 进度

> 只记任务编号、状态和结论。计划见 [roadmap.zh.md](roadmap.zh.md)。

交付目标是让公众按公开流程自行部署；NAS 用于真实环境验收。通用安装、制品、签名和配置能力应在公开仓库可复现，现场参数与凭据单独配置。域名访问和内网纯 IP 访问都须完成登录、管理台、实例 HTTP/WebSocket 及会话边界验收；现有局部回退代码不等同完整支持。

改造基线：Auriga 基于现有 Hub 和 Team 延续并完善大部分已有功能。逐项核对原功能在新项目中的归属与完整闭环；源码复用、迁移或重构均是实现手段，不能因新接口暂缺或原型未画出而缩减产品。用户已明确取消或调整的功能按决定处理。

界面分工：管理台为独立 Vue 应用；DSH 插件按原生登录、功能、设置三部分开发，复用宿主控件、字体和主题色彩，支持独立轻量安装。功能优先级与布局依据[管理台需求复核](../design/auriga-admin/requirements-review.md)和[插件设计](../design/auriga-plugin/README.md)，插件外观以 DSH 系统为准。本地登录和退出延续现有 Team 的完整流程及能力，在 Auriga 中补齐接线和改进；所需 Auriga API 对接在内部承接，不新增“登录总部”或第二套可见授权向导；云端由平台管控认证、实例准备与退出，插件承接展示和操作。客户端适配、实例内退出接线及完整资源安装仍待后续交付，不能用页面或原型代替业务闭环。

登录后的处理按运行环境区分：客户端完成身份认证后返回当前 DSH；云端还须调用工作空间后端准备本人实例，等待就绪后通过正式换票入口进入 DSH，优先复用既有实例打开 API。离线 owner 准入仅用于可选 standalone 模式。Auriga 创建的 DSH 默认预装并启用插件，现有镜像、profile 和节点配置已有自动安装启用链路；新页面与发行制品仍需逐项验收，现有 DSH 也可独立安装轻量插件。

总部 Agent 按明确的新方式改造：它是独立的系统 DSH，管理员进入 DSH 后通过对话完成总部工作，经 Team MCP 与各 Agent 沟通协作。工具、MCP 传输和授权接线是平台重点，任务、对话、执行与历史由 DSH 承载；管理台保留访问入口、授权及实例运行管理，不恢复旧 Hub 的任务/AgentBus 工作流，也不另造业务任务编排系统。

成员协作参照 Codex 原生 agent、子 agent 与持久会话机制，区分创建与派生、仅发消息与触发执行、运行中引导、等待通知、中断恢复及上下文范围。当前通信工具是先行基础，不代替完整能力目标；先以真实本地登录、平台连接和两个完整 DSH 的收发闭环验收。

通信网络以插件／DSH 实例为连接主体，多个获权主会话和子会话共用稳定连接；总部负责可靠交换，总部系统 DSH 是执行节点。NETWORK1 设计 [#124](https://github.com/hansjone/auriga/pull/124) 已作为待实施方案合入。N0 接缝门禁已于 2026-10-25 合入 main：契约 [#125](https://github.com/hansjone/auriga/pull/125)、ai-gateway NATS callout/ACL [#126](https://github.com/hansjone/auriga/pull/126)、插件宿主 adapter/ledger [#127](https://github.com/hansjone/auriga/pull/127)、插件 NATS 硬缓冲传输 [#128](https://github.com/hansjone/auriga/pull/128)；均精确 HEAD CI 全绿且非作者独审通过。N1：collab 邮箱/游标/首联 [#129](https://github.com/hansjone/auriga/pull/129)、ai-gateway HEAD/FETCH/RECEIPT 骨架 [#130](https://github.com/hansjone/auriga/pull/130)、edge NATS-over-WSS 升级路由 [#131](https://github.com/hansjone/auriga/pull/131) 已合（均独审通过、精确 HEAD CI 全绿）；下一步插件接线（宿主 adapter↔传输/系统收件箱），再 IAM 网络上限、两真实 DSH。完整网络 IAM、生产硬背压、HA、容量与双 DSH 往返尚未验收；旧定时 pull 仍为迁移期路径。

2026-10-27 Beta 阶段进度：

- 插件来源控制 [#213](https://github.com/hansjone/auriga/pull/213)、IAM 纯回执读取 [#216](https://github.com/hansjone/auriga/pull/216)、网关来源三帧搬运 [#217](https://github.com/hansjone/auriga/pull/217) 已合入。精确候选 CI 分别 12/12、16/16、22/22 全绿，本批按用户明确指示合并；独立外部审查尚未完成，不把作者或主控补充检查记作独审通过，也不扩展为以后免审。
- 基础设备连接 [#206](https://github.com/hansjone/auriga/pull/206)、普通会话登记 [#210](https://github.com/hansjone/auriga/pull/210)、第五投递批准 [#208](https://github.com/hansjone/auriga/pull/208)、来源搬运契约 [#211](https://github.com/hansjone/auriga/pull/211)、整链无版本撤销 [#212](https://github.com/hansjone/auriga/pull/212) 均已合入。只读回执接口只恢复原操作结果；历史 ACTIVE 不授当前使用权，仍须新鲜见证、Get、最终本地 CAS 和签前第五 RPC。
- collab 来源处理及纯回执恢复 [#219](https://github.com/hansjone/auriga/pull/219) 已按精确 CI 6/6、全新非作者第3轮 A 审 0 高/0 功能中合入。本地确定失败墓碑固化原连接出处和完整关联摘要，跨连接恢复核原归属、当前连接及签前原行；旧或损坏历史不补造。Get 有效期向下规范到整秒，不延长授权上界。完整真实 PostgreSQL collab reactor 558 项、真 IAM＋collab＋PG 联合 37 项均零失败/错误/跳过；两同源坏实现分别触发 5/1 个真实旧结果泄露断言，审者另独立通过 34 项聚焦、3 项传输及 43 个探针断言。前置 IAM 权威连接出处 [#220](https://github.com/hansjone/auriga/pull/220) 已按精确 CI 24/24、非作者 A 审及完整实库 804 项合入。IAM 原操作耐久未提交围栏 [#221](https://github.com/hansjone/auriga/pull/221) 已按精确 CI 16/16、全新非作者 A 审 0 高/0 功能中合入：保留完整旧回执，仅在原连接不可逆失效且原键仍未提交时无发行地落耐久失败；真实 PostgreSQL IAM reactor 828 项全过，两同源坏实现分别命中 2/1 个真实行为断言失败、零错误/跳过。collab 显式围栏接线 [#222](https://github.com/hansjone/auriga/pull/222) 已按精确 CI 6/6、全新非作者 A 审 0 高/0 功能中合入：只在已有耐久 pending 的原始 gRPC UNAVAILABLE 后显式 no-create Fence，原回执先耐久保存，RPC 后与签前重核；同 Java 源码的真实联合正控 114 项全过、两坏实现分别命中 1/12 个持久事实断言，来源继承明确、原 37 项完整保留。下一步仍为真实 org/issuer/HTTPS 桥、IAM/collab/gateway/插件/NATS WSS 来源整链和真实 DSH native 见证；durable 单播 SEND、两真实 DSH 对话及 Beta 整体验收仍未完成。
- 中转站有界 runtime [#207](https://github.com/hansjone/auriga/pull/207) 与实际上游 Transport [#209](https://github.com/hansjone/auriga/pull/209) 已合。取消原因/测试阶段修复 [#215](https://github.com/hansjone/auriga/pull/215) 经精确 CI 22/22 成功及全新非作者独审 0 高/0 功能中合入，真实 PostgreSQL 全模块 2643 通过、4 个具名跳过。MiniMax 流式兼容 [#214](https://github.com/hansjone/auriga/pull/214) 经精确 CI 22/22、全新非作者 R2 审查 0 高/0 功能中合入；新头真实 PostgreSQL 全模块 2907 通过、4 个具名跳过。正常系统 DNS 下真实上游 JSON/SSE 两路 Transport 均通过唯一有效计量、无推理字段外泄及停止证明；完整真实模型结算和 Beta 整体目标仍待验收。

2026-10-26 Beta 推进（此前组件检查点）：

- Beta 先完成两个真实 DSH 对话、插件登录和前端、模型中转站；Auto、调用方模型 Key、总部系统 DSH 及完整资源治理留到后续。同机隔离整体推迟。下面的组件通过不表示真实双 DSH 或收费模型已完成验收。
- N1 插件总装 [#151](https://github.com/hansjone/auriga/pull/151)、公开 HostPort [#153](https://github.com/hansjone/auriga/pull/153)、IAM 客户端 [#152](https://github.com/hansjone/auriga/pull/152)、COLLABIAM [#147](https://github.com/hansjone/auriga/pull/147)、wire [#154](https://github.com/hansjone/auriga/pull/154)、opaque local 登记 [#155](https://github.com/hansjone/auriga/pull/155) 均已精确 CI 与非作者独审后合入。密钥运营 [#156](https://github.com/hansjone/auriga/pull/156) 关闭加载失败污染状态问题、整合两种装配模式后经新 20 CI 与第二轮独审合入，随后群组 [#173](https://github.com/hansjone/auriga/pull/173) 关闭三项功能问题，经 19 CI 与第二轮独审合入，保持 V11→V12 顺序。合后网络/local 真实 PG 定向 204/204、零跳过通过。IAM 服务端 [#178](https://github.com/hansjone/auriga/pull/178) 四身份/限额 RPC 已合，主控 54/54、独审 55/55 零跳过；第五投递批准仍失败关闭。
- N1 非零会话类型 [#184](https://github.com/hansjone/auriga/pull/184) 已合，V13/不可变类型见证及 lookup/lock 已实证。客户端 [#189](https://github.com/hansjone/auriga/pull/189) 修复消费方前向兼容后，经精确 20 CI 与第二轮 A 审合入；root 完整前端 1568 通过、6 旧条件跳过，实际安装 rc.2 SDK 9/9、真实公开 Controller/JSONL 冷热 HostPort 49/49，独立新旧消费者反证有效。READY 登记恢复 [#190](https://github.com/hansjone/auriga/pull/190) 经 15 CI 与 A 审合入；root478/478，独审90及额外8真PG零跳过。来源契约 [#182](https://github.com/hansjone/auriga/pull/182)、来源事务内核 [#187](https://github.com/hansjone/auriga/pull/187) 和登记搬运契约 [#191](https://github.com/hansjone/auriga/pull/191) 均经精确 CI/非作者 A 审合入；187锁序问题有真实旧序死锁反证。合后固定 eadb8ca 完整 IAM+collab 同 reactor 813/813 零失败/错误/跳过通过，临时 PG 均关闭。来源三 RPC 后续由 #205 接入真实 IAM 权威（见下）；完整 SEND/第五授权与两真实 DSH 往返仍未完成。
- 生产登记和公钥通道：edge 两个精确 GET [#193](https://github.com/hansjone/auriga/pull/193)、网关原字节搬运 [#194](https://github.com/hansjone/auriga/pull/194)、本库政策版本 [#195](https://github.com/hansjone/auriga/pull/195)、注册首次创建事实小契约 [#196](https://github.com/hansjone/auriga/pull/196)、collab 登记 handler [#197](https://github.com/hansjone/auriga/pull/197) 均经精确全 CI 与首次非作者 A 审合入。root194真PG2223 pass事件/2142叶、4旧skip；195完整593/593、197完整499/499均零skip。独审覆盖真实socket/TLS身份、strict raw/绑定换代、版本回滚与旧锁序反证、真实RPC提交/签名/丢回包恢复；均明确控制面夹具边界。194时间格式低项登记L21，不阻塞；193本机既有control ingress前缀guard红测保留，不改网卡策略换绿。首次创建响应 [#199](https://github.com/hansjone/auriga/pull/199) 已经精确6CI和新A审合入，root完整IAM608/608零跳过、独审90+7真实PG覆盖最终COMMIT失败/原子可见性/身份变更，稳定Node及票据不变；安装根pin [#200](https://github.com/hansjone/auriga/pull/200) 经25CI与新A审合入，root全前端1576通过6旧条件skip/实际rc.2 SDK9，独审47及正常生成/两公开构建stage复制输入验证过。静态配置不启网络。首次签名 READY 内部原语 [#203](https://github.com/hansjone/auriga/pull/203) 已经精确12CI及新非作者A审合入，root完整前端1655通过/6旧条件skip、实际rc.2 SDK9/9，独立79定向+33回归+6探针过，跨node坏签名绑定攻击真实成立并被负测击红；它尚未默认接线，生产provider与完整同锁账本首启仍待完成，两DSH往返尚未验收。合后固定1c19930完整IAM+collab同reactor886/886零失败/错误/跳过通过，本轮四组PG均关闭；早前中断轮次原样保留且未计通过，不冒真实跨进程闭环。
- 中转站契约读取 [#159](https://github.com/hansjone/auriga/pull/159)、页面 [#157](https://github.com/hansjone/auriga/pull/157)、管理存储/API [#158](https://github.com/hansjone/auriga/pull/158)、预算账本 [#171](https://github.com/hansjone/auriga/pull/171)、JSON/SSE [#172](https://github.com/hansjone/auriga/pull/172)、固定快照 [#176](https://github.com/hansjone/auriga/pull/176)、device 核心 [#181](https://github.com/hansjone/auriga/pull/181) 已合。三个正式 device HTTP 端点 [#185](https://github.com/hansjone/auriga/pull/185) 修复合法多字节幂等键容量后，经精确 25 CI 和第二轮独审合入；独立真实 PG/HTTP 新包 87 叶及额外 13 叶通过，退回旧容量准确触发 8 叶失败，实际 rc.2 Adapter JSON/SSE 均通过。租户内只读恢复候选 [#188](https://github.com/hansjone/auriga/pull/188) 经 25 CI 和 A 审合入，不自动取消或退款。仍缺 runtime/Supervisor 的完整接线与跨重启验收；仅 D7 假 Transport，缺可信计量的实际模型保持禁用，不代表公网或付费模型已开通。
- 管理台节点 [#160](https://github.com/hansjone/auriga/pull/160)、中转站总览/用量 [#161](https://github.com/hansjone/auriga/pull/161)、管理 mTLS [#179](https://github.com/hansjone/auriga/pull/179)、edge 专用路由 [#180](https://github.com/hansjone/auriga/pull/180) 与正式租户用量 [#183](https://github.com/hansjone/auriga/pull/183) 均经精确 CI 和独立 A 审合入。独立合后域名 HTTPS/真实 IP HTTP→edge→内部 mTLS→管理服务→低权限 PG 组合通过；最新一轮 98 请求、54 预期拒绝，覆盖两 owner、外租户隔离、稳定分页、unknown 原账及全账本/outbox/revision 不变，原 CRUD/密文回归通过。控制面仍为明确夹具，不冒真实 IAM 登录或 GUI；管理首页等未开放工作流后续处理。
- 插件登录 [#163](https://github.com/hansjone/auriga/pull/163)、设置 [#164](https://github.com/hansjone/auriga/pull/164)、AI 短票 [#165](https://github.com/hansjone/auriga/pull/165)、公开 Adapter [#166](https://github.com/hansjone/auriga/pull/166)、统一刷新/注册 [#170](https://github.com/hansjone/auriga/pull/170)、专用在线核验 [#169](https://github.com/hansjone/auriga/pull/169)、过滤终态计量 [#174](https://github.com/hansjone/auriga/pull/174) 均已合。独立合入快照 2bf104f 锁定安装后 lint/types/test/build/bundle 全通过，1457 项通过、6 真实宿主条件跳过；成品仅生产依赖经实际 rc.2 公开 loader/Runtime 注册/注销及缺计量拒绝通过。后续协议/核心均未据此冒真实模型可调用；插件仅用公开 API，以实际安装 rc.2 导出为运行基线。
- 资源模块 [#162](https://github.com/hansjone/auriga/pull/162) 复审通过但继续草稿，等待正式权限与路由/托管身份。Docker 取消/健康测试 [#177](https://github.com/hansjone/auriga/pull/177) 和设置壳等待修复 [#186](https://github.com/hansjone/auriga/pull/186) 已经精确 CI 与非作者审查合入，坏实现负向验证保留；另两个成功健康探针的阶段/真实拒连/回收修复 [#192](https://github.com/hansjone/auriga/pull/192) 经精确 22 CI 与独审合入，正常/慢准备/拒连延迟通过，跳 health、提前 endpoint、never-ready 坏变体准确失败。非法网络恢复拒绝测试 [#198](https://github.com/hansjone/auriga/pull/198) 与续期退避回归 [#201](https://github.com/hansjone/auriga/pull/201) 均已精确22CI/新非作者审后合入，保原生产超时/退避，正常/受控慢/真实坏行为反证有效；201低级回调登记屏障建议记L22，不影响当前落盘写者join。无关首失与单失败作业重跑均留证，不以重跑成功代替因果修复或宣称旧CI唯一成因。

- 来源权威三 RPC [#205](https://github.com/hansjone/auriga/pull/205) 经精确 17 CI 和全新非作者 A 审合入；Register/Get 实际兑换、当前设备及会话/组织权限、事务末版本与锁序重核，所有终态 Get 也须活体身份，仅原操作回执与 Revoke 保留受限恢复例外。root 完整 IAM reactor 734/734 零失败/错误/跳过，独审自写 9 + 定向 14 全过；旧终态旁路反证 88 绿→80 权限断言红→恢复 88 绿，原产物逐 SHA 恢复。过时注释低项记 L23；org 无 revision，不承诺跨服务撤权零窗口，第五/SEND 尚未接通。
- 未决准入引用 [#202](https://github.com/hansjone/auriga/pull/202) 经精确 25 CI 和全新 A 审合入；实际 INSERT 后提交结果不明仅将私有引用交恢复，不授重派/退款，公开错误不泄引用。root 最终原始 UTF8 完整 Go/真 PG 2279 通过事件、2191 叶子，4 既有条件跳过，零失败/损坏 JSON；独审真提交失败及两坏变体反证有效。同库测试 ACL 竞争由 [#204](https://github.com/hansjone/auriga/pull/204) 沿既有事务 advisory lock 最小修复，经精确 22 CI/独审合入，新 8 叶及三包同库并行多轮全过，旧裸 SQL 和错误锁号准确击红。现场有界 Supervisor 与可启动 D7 合成 runtime 正在实施，尚未作为交付验收；仍无真实收费模型调用。
2026-10-24 本地接线批次（已合入，除 #123）：

- EDGECONNECT2 [#120](https://github.com/hansjone/auriga/pull/120)、NATIVELOGIN2 [#121](https://github.com/hansjone/auriga/pull/121)、TEAMREMOTE2 [#122](https://github.com/hansjone/auriga/pull/122) 已合入 main（各有非作者独审通过）。TEAMREMOTE2 遗留低项：合法拆分 Accept 列表误拒 406。
- LOCALCONNECT2 [#123](https://github.com/hansjone/auriga/pull/123)：独审阶段暂不通过（中 3）；投递随新网络重做，登录相关中项待单独修；PR 仍开着。

## M0 立项

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| P1–P6 开工准备 | 完成 | 工具链 JDK 25.0.4、Maven 3.9.16、Go 1.27；公开仓库 + 私有仓库；脱敏文档和 15 篇 ADR 入库；开发环境探测：依赖可在线下载，构建缓存放共享目录 |
| C1 契约第一版 | 完成 | OpenAPI（iam、org、workspace、远程连接器）、protobuf（入网、NodeControl、iam / org 内部接口）、事件、身份源步骤协议、插件协议、令牌和 KV；buf lint、redocly lint、Ajv 编译通过；Go / Java / TS 类型生成并编译通过；Opus 安全评审 25 条已全部处理（实例改用独立子域，ADR 0020） |
| K1 仓库骨架 | 完成（Codex，拆成 K1a、K1b） | K1a：Maven 22 个模块、契约代码生成、ArchUnit + Enforcer（违规依赖构建失败已验证）、嵌入式 PostgreSQL 测试基座、Spotless、gitleaks、CLA 草稿和检查；K1b：go.work 6 个模块、ctl dev（嵌入式 PostgreSQL 15433 + NATS 14222）、4 个 Vue 包、插件骨架、CI |
| K2 common 公共库 | 完成（#11） | 测试 7→128；独立审查 2 高 3 中 7 低已修；契约补内部服务令牌（6d1cfe3） |
| S1 DSH mcp-client | 完成（Codex） | 原生 mcp-client 支持 Streamable HTTP 和自定义请求头，但不透传 DSH 会话；会话相关的团队工具由插件注册后转发（ADR 0016）。原型在本地分支 spike/s1-mcp-client |
| S2 面板 Web Component | 完成（Sonnet） | 托管和连接两种模式都在真实 DSH 0.2.0-rc.2 里挂载成功，样式隔离自检 6/6；连接模式由插件验签后经本机路由提供，设备令牌不进页面（ADR 0017）。原型在本地分支 spike/s2-panel |
| S3 Windows 原生隔离 | 暂停（M4 再继续） | 第二轮修好 WFP 清理、登录权限、配额；第三轮（审查意见）shim 改 SYSTEM 并通过存活实验，但负载启动回归、清理时进程未结束；第四轮修进程数 / AppContainer / 内存，第五轮修回归（任务说明已写好） |
| S4 DSH 运行时制品 | 完成（Codex） | Node + DSH + 锁定依赖打成 tar.gz（约 180 MB 压缩）；Windows / Linux 断网、只读目录启动通过；可在 Windows 交叉打 Linux 包且可复现（ADR 0019）。原型在 spike/s4-dsh-runtime 分支 |

## M1 平台底座

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| B2 iam 核心 | 完成（#13） | Codex 实现；CI 全绿；Cursor 主控只读安全审查通过（`.agents/runs/auriga-B2-review.md`），无阻断项；接线留给 B6/B7/安装器 |
| B8 门户/管理台壳 | 完成（#12） | Cursor；shared API+步骤渲染+mock；portal/admin 登录与框架；第二轮去掉本地 alias、错误码对齐契约 |
| B1 ctl 本机内置 | 完成（#14） | Cursor；`dev/start/stop/status`；每服务 schema/角色；文档 `docs/dev/ctl.md`；lint errcheck 返工后 CI 全绿 |
| B7 edge 接入网关 | 完成（#15） | Cursor；会话/CSRF/Host 隔离/身份头/`/open` 骨架；安全审查通过（`auriga-B7-review.md`）；gitleaks+lint 返工后 CI 全绿；E2 接真节点 |
| B6 org | 完成（#16） | Cursor；FORCE RLS、超管上限、邀请幂等、PermissionResolver/hooks、user.*；OrgCoreTest 8/0；CI 全绿；审查 `auriga-B6-review.md`；接线：iam consumeInvitation 传 accountId、合并部署多 DataSource |
| B3 团队账号连接器 | 完成（#17） | Cursor；team-account：邀请/口令+TOTP/Passkey/扫码、`/me/**`；测试 34；审查 `auriga-B3-review.md`；接线：建 Provider、Passkey 联调、composite hooks |
| B4 通用 OIDC 连接器 | 完成（#18） | Cursor；Discovery+码+PKCE、claimMapping、Provider GET/PATCH；43 测；审查 `auriga-B4-review.md`；**生产前补 id_token JWKS 验签** |
| B5 企业连接器 | 等待 | 私有仓，需用户提供公司登录信息 |
| B9 M1 集成 | 完成（#22） | Cursor；platform 合并装配（iam+org 双 schema）、种子应急账号、默认安全配置、NATS 会话吊销接线；两轮独立审查；文档 `docs/dev/m1.md` |
| 补审修复 | 完成（#20、#21） | Cursor 主控期间自审合并的 B3/B4/B7 经独立补审共 19 条问题（含高危），edge 由 Cursor、iam 由 Codex 修复，各两轮复审 |
| 包名整改 | 完成（#19） | Java 根包和 groupId 改为 `auriga`（ADR 0011 修订） |

**M1 完成（2026-10-20）。**

## M2 工作空间（提前开工）

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| NASREADY1 / NASINSTALL1 普通共享目录安装 | 已合并 #84，测试栈已启动 | 容器模式支持普通共享目录下的新建 bind 数据根，保留固定 UID、receipt、材料和镜像检查，不改共享上级权限；精确 CI 与独审通过。真实 NAS 新测试栈已启动，纯 IP 双入口和 DSH HTTP/WebSocket 会话边界有独立证据，用户已进入完整 DSH；测试发行根仅限本次测试栈，不能视作正式发行。继承 ACL 有效访问的通用预检补强另见 NASPRE1 |
| A1 节点代理骨架 | 完成（#23） | Cursor 实现，Codex 修复续期竞争等；三轮独立审查；线上 Linux Go 测试打开 `-race` 后又抓到一处真实数据竞争并修复 |
| P0 插件离线版 | 完成（#24） | Cursor 实现，Codex 修复真实网络路径和 IP 字面量绕过；三轮审查，复审用真实网络实测 |
| W1 workspace 服务 | 完成（#26） | Codex 实现（中途额度用完，恢复后续做）；实例、规格代数、调度、生命周期、入网 CA、NodeControl、强制吊销（契约加 `force`）；线上 11 个数据库集成测试全绿；三轮审查 |
| A2 Docker 驱动 | 完成（#25） | Cursor；容器安全参数写死、节点本地策略（白名单、资源上限）、制品校验与 GC、防 YAML/环境变量注入；三轮审查 |
| A3 实例入口与观测 | 完成（#27） | Cursor 实现、Codex 修复；mTLS 只认 edge、身份头失败关闭、文件操作防逃逸（含 Windows 联接）、cron 解析、CPU 增量；两轮审查 |
| P1 插件托管模式 | 完成（#28） | Cursor；三轮审查；真实宿主的交互事件与完整端到端仍待 W3 验证 |
| E2 edge 反代实例 | 完成（#29） | HTTP / WebSocket 经节点 ingress；节点身份取自证书 URI SAN，节点证书含 clientAuth + serverAuth |
| W2 M2 集成基线 | 完成（#30） | Compose + 假 DSH；main c02245e 七组 CI 全绿，E2E M2 于 2026-10-20 17:39（北京时间）通过。真实 DSH 和严格会话吊销验证仍待补齐 |
| W2V / W2Vb 验收补强 | 已合并 #31、#42 | 补齐实际停止的 phase/容器/确切 edge 503 三证、预热与一次性计划消费、吊销前原连接探活、A/B 会话对照和深层日志脱敏；冻结提交 CI 全绿、非作者独审通过，真实 DSH 整轮证据由 W3R 核证 |
| W3 真实 DSH 冒烟 | 已合并 #37 | 配置表达式绕过与活动未知误判经三轮独审修复；固定版本真实页面、健康和门锁已实测。完整生命周期由后续 W3V/HB1/W3R 补齐 |
| W2H 底座加固 | 已合并 #36 | Host、socket、种子与闹钟预算加固；临近编辑保护受固定硬上限约束；真实 PG 回归、双平台 CI 及三轮独审通过后，按用户授权合并 |
| W3V 实例网络与就绪 | 已合并 #40 | 受信 edge 内部入口、实际健康就绪及停止计划观察已实现。补修代理重启端点、待重启旧代和实际监听端口恢复，以及快速重启 CPU 窗口；四包 Windows/原生 Linux 回归通过，本机未改 ingress 用例 TLS 失败留证，最终双平台 CI 和第 2 轮新会话复审通过 |
| HB1 / TERM1 真实心跳 | 已合并 #41 | 计数允许显式未知，缺失/非法值拒绝；未知或不健康保守阻止回收且不伪造用户活动。补采真实浏览器与模型终端来源；第 2 轮独审及最终 CI 通过，本机真实原始心跳的四项零计数、双静默窗口和 HTTP 交互推进已通过，未实际创建 PTY |
| W3R 真实生命周期 | 已合并 #43 | 精确源码 f6b5d90 重建全部镜像；25 项 CI、第 2 轮源码独审及真实终验核证通过。原始心跳双静默/交互、闲置回收、预热/到期消费、双会话 HTTP/原 WS 吊销均实测通过。额外实际代理重启保留原 DSH 容器/启动时间，入口约 1.3 秒恢复、持续稳定并恢复计划观察；实际镜像与构建导出一致。按用户本轮明确授权合并，主线 c13b642 的运行代码与已验收 f6b5d90 完全一致 |
| NASHB NAS 内部入口 | 已合并 #44 | 最终 CI 与非作者独审通过；补齐 edge 稳定名字/角色与 agent 内部心跳 URL、实例父域；默认及定制域名配置、原生 Linux 脚本及篡改负向验证通过，仅本机源码准备，未部署 NAS |
| D0 容器部署包 | 已合并 #35、#39，整合工件已重建 | 四轮审查后合并准备包；Linux 执行位补丁经独审及 CI 通过。整合提交 cf8814a 的专用构建成功，实际工件校验和、save/load、标记、镜像 ID 及原生 0644 脚本/CI 二进制验证通过，39 PASS。该三镜像准备包仍缺下列通用真实安装闭环，NAS 作为后续真实验收环境 |
| RT3D1 NAS 准备包发行工具 | 已合并 #81 | 同一精确提交预构建 Linux/amd64 runtime-release，与 bootstrap 一起交付，manifest 和根校验和覆盖两工具。23 项检查、8 个工作流及全新非作者完整三路径独审通过，高/中/低为零；实际下载 ZIP、两静态 ELF、4 条根摘要、3 份镜像 tar、44 个安装源码与镜像 index→M→C 链均核同。VCS modified=true 与原核验器首失如实保留，不冒纯净元数据或跨引擎实际载入。包仍缺官方根、真实 DSH 材料/全部层、ORAS 和第三方基础镜像，不等同完整离线安装或现场可登录 |
| RT3D2B 目标 Engine 镜像身份 | 第 2 轮验收中 | 保留制品原 Id，从三 tar 核 config/唯一平台/有序层链；加载后按目标实际不可变 Id 导出并关联原字节，三项成功才输出建议。首轮精确 23 项检查、8 工作流通过；实际 CI 包完整公开 loader 在 Docker27 legacy 与 Docker29 containerd 各三次真实 load/inspect/save 通过，实际 Id 分别为 config 与 index，归属首轮原包。独审发现 metadata 原地复制可破坏旧 marker 或硬链接 env，现改同目录临时文件准备后原子替换、marker 最后发布，保 versions 同源 inode；32 项脚本回归通过，首次失败保留。新精确 CI、实际包校验与新非作者限定复审待完成；不重跑未改的 Engine 解析链冒新证据，不称 QNAP 现场已部署 |
| DEP1 通用部署策略接线 | 已合并 #45 | 修复 renderer 的镜像白名单键，并用真实 init/render→生产策略加载验证镜像准入与资源上限；冻结提交 33cec32 的 22 项 CI 及新会话独审通过，无高、中、低问题。修复已进入主线，正式工件待通用信任链和入口补齐后重建 |
| 通用容器交付与纯 IP | 尚未闭环 | 整合提交 cf8814a 的三镜像工件已构建、校验和及本机实际载入通过，原生脚本套件 39 PASS；仍缺真实制品/签名消费链/模板闭环、资源预算一致性及纯 IP 登录与实例 WebSocket 验收，不以工件成功代替产品可部署 |
| IP0 纯 IP 访问边界 | 已合并 #47 | 按 ADR 0020 的可信团队路径模式定义完整 Origin/端口、管理 CSRF、双登录事务与每实例 cookie；公开浏览器入口和内部心跳 DNS 别名分离。冻结 3a1c9a7 的 15 项 CI 及两名非作者独审通过，无高、中、低问题；18 份 Schema 和 66 项契约测试零跳过。之后仍须 IP1/IP2 实现及 IP3 域名/纯 IP 真实浏览器验收 |
| IP1a 受信配置加载 | 已合并 #50 | 同一 Schema 生成 Go 类型，生产加载器实际接受原 4 正例、拒绝 32 结构及 12 启动语义负例；冻结 5c3b98d 的 22 项 CI 与独审通过；正式模块测试、静态检查、构建与生成漂移门禁已通过。服务与浏览器尚未接线，不计作纯 IP 已可用 |
| JC1 Java 配置加载 | 已合并 #52 | 同一原 Schema 生成 Java 模型并严格加载，精确数字、format、跨字段和外部引用禁用均经实际回归；返回深副本，JAR 保留单份原 Schema。冻结 dea94f3 的 6 项 CI 与全新独审通过；原 93 项、独立 5 项方法及 42 条新增 Go/Java IDNA 对照通过，生成无漂移；合并后主线四工作流全部成功 |
| IP1b 真实入口与 IAM 通道 | 已合并 #54 | 三实际 listener、完整 authority/Origin、专用 mTLS 与在线管理 CSRF RPC、唯一 SPA mode 已实现；首轮独审的 3 项中风险已修，冻结 50b98c0 的 14 项 CI 与第 2 轮全新独审均通过，独立代理、78 项 CSRF 边界、原样票据及真实 TCP mTLS 探针通过。凭据文件支持热更新，安装侧持续发行仍待 IP1f |
| IP1d 管理台内存 CSRF | 已合并 #53 | 每 document 安全随机内存 token、单 flight、Retry-After、会话变化隔离及固定同源请求已接入实际管理台；模式类型从原 Schema 生成。首轮独审的满槽退出和响应延迟两项中风险已修；冻结 34748b9 的 7 项 CI 与第 2 轮全新会话独审均通过，独立共享 45 项、原探针 12 项及新增 8 项通过。真实浏览器仍待 IP3 |
| IP1c / IP1e IAM 与公开装配 | 已合并 #55 | ce8a830 的 6 项 CI、102 项选择集、Linux 完整 IAM 两轮各 143 项及真实 DSH 完整生命周期通过；23 条原始心跳、实际停机/预热/消费和完整 A/B HTTP/WS 吊销通过，991 blob 前后一致。第 2 轮独审发现凭据变更重试过滤已 revoked 行，窄修 3a9dbbe 的 6 项 CI、Windows 105/Linux 49 项回归已通过，第 3 轮新独审高/中/低均零、独立 42 项通过后合并，53 个合入 blob 全部一致；装配原 20 blob 未变，独立 13 项探针通过，历史失败保留 |
| IP1f 安装服务令牌刷新 | 已合并 #56 | combined Linux 固定 svc:edge 可信持续发行、原子目录消费及代际停机保护已实现，默认关闭；原真实 IAM/common/PG mTLS 七阶段通过。消费前 owner/祖先/单链接/完整内容和身份校验已补，冻结 7b0b1a3 的 6 项 CI 与第 2 轮限定只读独审高/中/低均零后合并，实际 19 个合入文件与已审源一致。作者 Windows 5 顶层/11 子、Linux 9 顶层/54 子回归通过；本轮独审未重做动态验证，split、Windows 发行及正式部署未完成 |
| IP2b 纯 IP 实例路径 | 已合并 #57 | 6e98e9b 的 22 项 CI 与非作者独审通过（高 0、中 0、低 2），六个合入 blob 与冻结一致；74 项 edge 测试、真实 HTTP/HTTPS 双实例 cookiejar、双向 WS 撤销及 Linux race 通过。空 path 绝对 URL 的尾斜杠/SW scope 规范偏差，以及未证越界的非法 UTF-8 拒绝策略列为低风险加固，已随 IP2c #58 关闭；未声称跨实例利用。完整 Origin 接线见已合并 IP2a，公开输入与 IP3 真浏览器另验 |
| IP2c 实例路径规范加固 | 已合并 #58 | 空 path 的同源绝对响应映射到带尾斜杠实例根，Location/SW 出口核精确 mount，四入口统一拒绝非法 UTF-8 并保合法 Unicode；仅两生产文件和一回归。冻结 8e5b134 的作者 Windows/Linux 79 顶层、累计 167 pass、race/vet/build 与双目标 lint 通过，精确 22 项 CI 与全新独审高/中/低均零后合并；独立完整 82 顶层/222 累计、vet/build 通过，3 个合入 blob 与已审源一致 |
| IP2a 完整浏览器 Origin | 已合并 #59 | 受保护 browserOrigin 精确消费同一配置的 scheme/host/有效端口，与内部心跳别名分离；冻结 d78e15d 的 28 项 CI、两份全新非作者独审均通过，高/中/低均零，31 个合入 blob 同源。独立插件140项、契约89项与19 schema、HTTP/WS边界264项、Go三模块及生产链8组矩阵通过；作者新真实 DSH 最小集成通过7条accepted心跳、双65秒静默与真实GET交互，另次live Origin/WS通过。完整idle证据仍按旧提交归属，未计作IP3真浏览器 |
| IP2d 公开安装输入与材料复用 | 已合并 #60 | 受信 access 文件经原生产 Load 贯通，direct HTTP domain/IPv4 与 NAT 端口准确派生，完整匹配才复用安装材料。主控真实 CLI 发现仅旧模板残留仍覆盖并建 CA，窄修 1915006 已补 PKI/模板旧件和目录链接写前拒绝；Windows8顶层/50子、Linux非root race12顶层/95子及静态检查通过。原执行源的两个新安装认证、真实DSH与accepted心跳通过，domain双65秒/GET投影及最终脚本27份材料幂等通过；未把旧整轮冒充修后重演。1009源码与154证据哈希一致；精确6项CI与全新非作者独审均通过（高/中/低零），独立Linux107、Windows58、e2e54与额外51项探针通过，7个合入blob全同 |
| IP2e 公开 NAS 配方同步 | 已合并 #64 | 同源 access、可信材料、固定身份降权安装、mTLS 与持续服务 JWT 已实现。R6 6fde7eeb 的 22 项检查、7 个工作流成功，全新非作者独审高/中/低均零；真实可路由 namespace 独立 race 复测关闭旧 LAN 源绕过，规则依赖文档限定的受信直连桥拓扑，不提供 L2 身份认证。固定新 edge 与真实 DSH 正式 AIT 互通，10 条健康心跳覆盖 268.238 秒；本轮未保存运行期完整桥成员快照，不借旧轮次材料补证。材料/receipt/九字段解析器未变范围已逐 blob 核对并引用旧审查。正式模板、发行信任生产消费与资源预算仍待补齐，公开 NAS smoke 为 PARTIAL；未实际验 Caddy，NAS 尚未部署 |
| RT0 运行时发行信任契约 | 已合并 #66 | 专用 RuntimeReleaseEnvelope 绑定真实单平台 OCI 摘要与归档摘要，冻结分区发行序号、CA-pin 初始根和有界背书链。首轮独审的并发启用规范与纳秒精度问题已修复；R2 bc29ecdc 的 14 项检查、6 个工作流最终全部成功，全新第 2 轮独审高/中/低均零。原输入先红后绿，21 schema、159 JS、聚焦 Java 及当次 Java→Go 验签通过，独立复测时间全范围与边界。契约与向量不代替生产 keyring、真实 OCI 检验或持久启用门禁 |
| RT1 发行信任 Join/Renew 消费 | 已合并 #69 | 按已合并契约接入 Join/Renew 固定根、持久 keyring 与续证结果分离；最终 CI 与非作者独审已通过。实际 OCI 检验与持久启用门禁仍待 RT2，不称完整 NAS 安装完成 |
| RT2 制品与启用门禁 | 真实整合已验，交付待补 | 正式 OCI 核验、持久高水位、公开模板及生产 Docker 启用/恢复均已合并。固定生产源码的正式格式测试根、实际 CA-pin TLS Join 与真实 DSH 整轮已实测并独立核证；通用安装与完整部署交付继续验收，不以底层或本机测试代替公众可部署结论 |
| RT2V 正式声明与原始 OCI 核验 | 已合并 #71 | 固定根验证完整正式签名、分区与平台，按原始 manifest/config 字节核摘要、大小、字段和配置 ID；标准库字段别名差异经独审修复。精确 CI 和全新复审通过；底层核验结果不等于生产执行授权 |
| RT2F 公开 proof 与正式模板 | 已合并 #73 | Compose 收集 registry 原始单平台 manifest/config，bootstrap 使用独立 runtime 根签名并由 receipt 覆盖材料；Workspace 消费正式平台和发行时间。attestation 输出 index、重入密钥相容与结构别名问题已修复；精确 ad48aec 的 14 项检查、6 个工作流及全新第 2 轮独审通过，高/中/低均零。独立字段别名与实际 VerifyOCI 消费通过，合入 22 个文件与受审源一致；完整生产启用另由 RT2R 验收 |
| RT2R 生产 Docker 启用与恢复 | 已合并 #74 | 正式证据、持久未决 intent、独立实际执行记录与固定 Engine ID 已贯通，恢复不重选执行对象；坏证据撤入口保进程，确认删除后才清实际记录并保高水位。模拟 Engine 竞争、Windows 夹具初始化超时及闹钟监听重入丢缓存已按首失修复；精确 5b37671 的 22 项检查、7 个工作流及全新全范围非作者独审通过，高/中/低均零。合后主线 13 项检查、5 个工作流通过；完整 fake DSH 测试根生命周期与真实 Docker 窄恢复已验，真实 DSH 新整合见 RT2I，真实 Engine 崩溃 fencing 仍另行验收 |
| RT3A 通用 Docker 发行材料 | 已合并 #75 | 显式发布者签发与独立外部根、精确镜像策略导入复用生产 Trust、正式声明及原始 OCI 验证；公开材料不含发布私钥。合法 Windows 短名兼容与包内根别名拒绝已验。精确 HEAD 22 项检查、7 个工作流成功，全新非作者全范围独审零问题；两项未变测试/依赖失败各单次重试，首失保留。本批交付声明、公钥和原始 manifest/config，NAS 材料装配已由 #77 交付，registry 取件与镜像层离线交付另批 |
| RT2E 真实镜像身份验收 | 已合并 #76 | 原始 M/C 严格校验及精确仓库/平台归属核对后，一次固定实际 Engine ID，容器两个镜像字段必须同一个 ID；保留原隔离与生命周期断言。精确 HEAD 6 项检查、5 个工作流及全新非作者源码独审、独立正反探针通过 |
| RT2I 正式根真实 DSH 整轮 | 本机实测及独立核证通过 | 固定生产源码 786ed2a、测试源码 3ca9848，全新测试根/身份/receipt 经实际 CA-pin TLS Join、原始 proof 和生产启用；22 条受理心跳、两次 66.392 秒静默与真实交互推进、闲置停止三证、预热/到期消费、A 原 WS 吊销关闭及 B 持续成功。标准 e2e 退出 0；额外收集器字段错检保留并另以原始证据核证。Docker 参数核同 1024MiB/500m/128，仅为固定预算；不扩为官方发行、纯 IP 浏览器矩阵或 NAS 部署结论 |
| RT3B 通用 NAS 运行时材料装配 | 已合并 #77 | 显式已签 bundle、独立外部根与批准镜像，组装本安装公钥绑定的模板、runtime 根和原始 proof，统一 receipt 与生产只读挂载；节点策略精确引用与原仓库前缀同时满足。精确 HEAD 22 项检查、7 个工作流成功，无重跑，全新非作者全范围独审零问题；独立固定身份/receipt/策略 74 项通过。材料实际 Java/Go 消费通过，镜像层运输及现场验收另批 |
| RT3C1 本地发布准备参数化 | 已合并 #78 | 构建入口显式选择规范本地仓库/tag，原始 proof 收集独立批准精确仓库；保留默认流程、版本/锁/基础镜像 pin 与原严格校验，拒绝歧义 RepoDigest。精确 HEAD 6 项检查、5 个工作流和全新非作者完整 7 路径独审均通过，高/中/低为零；合后主线 5 项检查、4 个工作流通过。完整 Node 118 项及真实本地构建/推送、原 proof、显式临时根签发/导入、固定身份材料装配通过；离线镜像层运输及现场另验 |
| RT3C2 完整离线 OCI 载体 | 已合并 #79 | 先复用生产签名、独立外部根、精确镜像与原 M/C 验证，再流式核全部层及显式总预算，复制时重验至全新最小 OCI layout；保留正常 Windows 短路径并拒物理目录交叠。精确 HEAD 的 22 项检查、7 个工作流与全新非作者完整 12 路径独审均通过，高/中/低为零，合入 mode/blob 同源。作者 Windows/Linux/race、双平台 vet/lint通过，具名跳过与首次原日志被覆盖的证据缺口如实记录。真实约 216 MB/8 层快照、4 类拒绝、新隔离仓库导入和全量读回通过；公开导入接线见 RT3C3，未部署现场 |
| RT3C3 公开本地 registry 离线导入 | 已合并 #80 | Linux import-layout 先完整验签快照，经独立审核 ORAS 和受限运输屏障只写批准的数字回环 origin/repo，再按原 M/C/全部层读回。精确 23 项检查、8 个工作流全成功、0 重跑；新非作者第 2 轮高/中/低为零，关闭首轮三项问题，真实 ORAS 八类攻击未批准请求全零、两种父退出收尾通过。实际 CI 工具在无默认路由的新空仓库完整导入并逐 HTTP 核 10 对象/约 216 MB/8 层；用户本轮明确授权后普通合入，12 个 mode/blob 与受审 tree 相同。Stage 仍仅字节预算/前后取消，不冒硬时钟抢占、完整离线包或现场部署 |
| RT3E1 恢复取消与 Ready 门禁 | 已合并 #82 | Inspect/List 继承调用方取消并同步撤销入口；最终执行保存跨过 Start deadline 后拒绝端点和 Ready，保实际 started 事实、高水位、intent 与同 worker 重试。成功 fixture 按真实健康和终态信号同步，保独立 deadline/取消负测。最终 22 项检查、7 个工作流全成功、0 重跑，新非作者完整 5 路径独审高/中/低为零，独立 99 项通过；用户本轮明确授权后普通合入，5 个 mode/blob 与受审 tree 完全一致。自然 Windows 首失的具体延迟来源仍未唯一定位，不承诺同步 bbolt 可中断 |
| RT2S 持久高水位与连续启用临界区 | 已合并 #72 | 真实 bbolt 高水位按 purpose/name/OS/arch 四维持久保存；同步授权、提交与整个启用回调持同一分区资格，Close 拒绝新资格、唤醒排队者并等待已持资格调用返回。底层 API 与存储/回调边界已独立验收；生产 verifier/driver 消费已由 RT2R 验证，不以底层完成代替完整部署验收 |
| RB1 NAS 每实例内存预算输入 | 已合并 #70 | 新增 operator 明确选择的内存上限，默认保持512MiB；同一规范值贯通写前校验、固定身份降权、receipt 与生产 policy。agent 的 min(规格,策略) 语义未变，节点总容量及 agent 自身 cgroup 独立配置；现场预算仍未选择，NAS 尚未部署 |
| IP3a 公开 Compose 真实前端 | 已合并 #61 | 精确 0c5a159 的 6 项 CI 与全新非作者独审通过，高/中/低均零；公开 edge 镜像嵌真实门户和管理台，M2 严格检查 HTML/JS/CSS。候选真实浏览器已完成双入口密码与 TOTP 登录、真实 DSH 工作区打开和刷新；属于本机组件验证 |
| IP3b 首次团队注册死锁 | 已合并 #62 | common outbox 的 subject 锁按物理 schema 隔离，保留同表串行；真实双数据源 PostgreSQL 回归先红后绿，精确 ca16924 的 6 项 CI 与新独审通过。候选后端正式首次 TOTP、账号/成员投影及新真实 DSH 心跳通过。同表发布者更新须整体停止，不能混跑旧新版本 |
| IP3c 浏览器会话续换 | 已合并 #63 | 既有受信 IAM RPC 续换与活动节流已接线，保留 KV/CSRF、绑定与撤销边界。首轮两项中危已修：缺投影的 /open 不在 ownership 前 Touch，无关 sid 撤销不污染五分钟缓存；精确 1dd1051 的 22 项 CI 与全新第 2 轮独审通过，高/中/低均零，独立 82 项通过。候选真实浏览器跨 5 分钟、正常退出与旧实例 HTTP 拒绝已观察；完整 IP/WS 矩阵另验 |
| IP3d edge 更新后网桥恢复 | 已合并 #65 | 精确 4241e73 的 22 项 CI 与新非作者独审通过，高/中/低均零；Start 复用既有网络与成员信任校验，失败关闭，不重建 DSH。域名组件与主线 596c56e 的纯 IP 新安装均实际替 edge、重启 agent，保两台 DSH 容器、进程和挂载并恢复持续心跳；edge 重建丢失内存实例绑定，须从门户重开，不计原实例 cookie 无缝恢复 |
| IP3 域名/纯 IP 真实浏览器 | 部分通过，协议矩阵待补 | 域名组件及主线 596c56e 的全新公开 Compose 纯 IP 安装，已实际验证双入口独立登录、跨 5 分钟、管理多 tab 退出/门户存续、两 owner 自然实例页面、门户退出后旧实例 HTTP401、跨 owner 拒绝；纯 IP 实际执行管理内存 CSRF 退出写请求。另验仅 agent 重启后原实例入口可用，两台原 DSH 进程/挂载不变，操作后的真实心跳各跨 120 秒。两测试栈均正常停止并保留数据；尚缺实际 WebSocket 关闭与完整浏览器协议矩阵，不用页面可见代替协议证据，NAS 未部署 |
| A2F 制品与 GC | 已合并 #34 | 独立审查通过后合并；遗留以审查报告和主控手册为准 |
| CF1 / RL1 | 已合并 #32 / #38 | common 偶发测试与 Windows renderer lint 修复，经独立审查 |

## 2026-10-24 首批功能迭代

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| PORTAL1 门户实例入口 | 已合并 #85 | 正式启动/进入、等待就绪、取消和错误重试，固定同源 open 入口；精确 2e7fcd8 的 8 项 CI 与独立审查通过，21 项组件/API测试通过。尚未部署本批前端 |
| ADMIN1 节点/实例/成员操作 | 已合并 #88 | 节点配置/排空、实例启停/重启/常驻、成员启停；保留现有 CSRF 与有效权限。精确 45913c1 的 8 项 CI、独立审查和35项测试通过；真实浏览器新版本联调待完成 |
| PLUGIN1 资源面板 | 已合并 #87 | 资源市场/我的资源双页签、只读列表与版本详情。合法空游标问题修复后，精确 dd35423 的8项CI与新会话第二轮独审通过，35项测试通过；真实资源认证通道、安装状态/配置及执行闭环仍待接线 |
| RECOVERY1 节点离线恢复 | 已合并 #86 | 旧同代状态覆盖新Inventory的问题经8645fae状态水位/V4迁移修复；精确6项CI与新会话第2轮独审通过，高/中/低0。独审真实旧/等时帧、带旧数据V3→V4升级/RLS/ACL、异常时间屏障活性、并发和旧代语义通过；尚未部署复验 |
| NASPRE1 服务挂载预检 | 已合并 #89 | 精确7122164的22项CI与新会话第2轮独审通过。合法umask077空PGDATA留给官方entrypoint准备，已有数据仍检查运行UID；作者19项真实权限/默认164项脚本回归、独立8项真实探针及完整init通过。真实权限入口仍为显式本机测试，未纳入CI，未现场更新 |
| R1A 真实资源目录 | 已合并 #90 | 四个GET、独立schema/RLS、实时Org授权与签名游标。测试配置冲突修复后，精确7c651ed的6项CI及完整15文件非作者独审通过，高/中/低0；34项相关测试通过。资源默认关闭，插件真实访问通道仍待接线 |
| ADMIN2 邀请与分组 | 已合并 #91 | Vue3邀请凭据一次性显示、分组及成员调整；精确2d8f5ab的8项CI与非作者独审通过，无高中，72项测试通过。撤读权后旧列表残留由ADMIN3关闭，可信邀请URL由INVITELINK1/ADMIN5补齐；parentId null语义仍待处理 |
| ORGSAFE1 分组父链安全 | 已合并 #92 | 完整同租户父链与坏链有界校验，结构写串行化，阻止RC/RR并发成环。精确82581c9的6项CI与非作者独审通过，高/中/低0；作者最终8项真实PG、独立4项定向复验通过。不扩parentId null语义，未现场更新 |
| ADMIN3 管理台配额 | 已合并 #93 | 默认/组/成员整层配额及成员有效值读取，区分继承/null/0并保未知字段，超安全整数阻止写回。精确1ff4795的8项CI及完整非作者独审通过，高/中/低0，ADMIN2 P3关闭；作者108项、独立73项与8项补充竞态探针通过。后端并发目标校验见QUOTASAFE1；不防多管理员覆盖 |
| R1META1 资源元数据写入 | 已合并 #94 | 现有POST创建与强ETag PATCH，服务端owner/等级、实时ACL/近期验证、可选键24h幂等及原子回滚。精确da5dc50的6项CI与完整非作者独审通过，高/中/低0；作者37项真实HTTP/PG、相关合计56项及独立7项定向通过。仅元数据，不含版本发布或插件安装，未现场更新 |
| R1OBJ1 本地对象存储 | 已合并 #95 | 原字节暂存、内容寻址、不覆盖提交及读回；Linux lease进程锁缺陷返工后，精确65e3c32全部6项CI与新会话第2轮独立复审通过，高/中/低0。双平台各25发现、24通过/1具名跳过；独立Linux重编及同/跨实例、别名、复制中stage与退出孤儿探针通过。无解包/上传API/安装或已提交GC |
| QUOTASAFE1 配额服务完整性 | 已合并 #96 | 同租户目标校验、已知字段验证、与组删除同序写锁、精确大整数合并及gRPC越界拒绝；保留整层PUT/未知字段及ADMIN3空覆盖错误码。精确679b5ce的6项CI与非作者完整独审通过，高/中/低0；作者及独立14项真实PG通过。未现场更新 |
| INVITELINK1 可信邀请链接 | 已合并 #97 | 唯一受信DeploymentAccess且租户匹配才生成真实门户链接；保持201必填code/url，缺少可信来源在写前503，无占位链接或不可用邀请行。精确188d09b的6项CI与非作者完整独审通过，高/中/低0；作者及独立26项PG/兑换/装配测试通过。管理台接线见ADMIN5 |
| ADMIN4 角色与授权 | 已合并 #98 | 正式角色目录、详情、自定义CRUD、权限目录及成员/分组绑定，编辑与授予权限分开，内置定义/服务账号绑定只读。精确909e9c0全部8项CI与完整非作者独审通过，高/中/低0；作者147项及独立112项相关测试通过。后端事务目标与并发保护另见ROLESAFE1 |
| ROLESAFE1 授权并发完整性 | 已合并 #99 | 已用真实PG复现24项缺陷，复用同序写锁并同事务校验绑定目标，18种RC/RR所有者竞争保至少一名活跃直接owner，失败事务和事件整体回滚。精确379fc9d全部6项CI与非作者完整独审通过，高/中/低0；作者77项及独立40项真实PG通过，不扩组继承owner或服务账号政策 |
| ADMIN5 可信邀请链接展示 | 已合并 #100 | 正式返回的可信链接与code一次性展示/复制、代次清理和401/403封闭，写后读失败保结果并只重读；不从管理台地址拼门户。精确b8ff6e2全部8CI与非作者完整独审通过，高/中/低0；作者164项及独立56项相关测试通过 |
| INVITESAFE1 邀请引用完整性 | 已合并 #101 | 先红确认缺失/跨租户role/group创建、目标删除后兑换及删除/兑换竞争实际留下悬空关系。邀请方法补同序锁和写前逐项复核，保持原兑换幂等/可信URL/授权/回滚；精确4ce83e0全部6CI与非作者完整独审通过，高/中/低0。作者110项及独立27项真实PG通过，无契约或锁实现变更 |
| ADMINR1 原型资源治理 | 已合并 #102 | 独立Vue管理台按HTML组织八组导航、紧凑列表、详情抽屉、固定资源版本及元数据创建/修改；真实强ETag、412保草稿、创建意图幂等与只读重试。资源版本误标DSH版本已修。精确7a50e96全部8CI与非作者完整13文件独审通过，高/中/低0，独立148项定向回归通过；主控实际组件fixture视觉另验，不冒真实服务。上传、审核、签名发布及分发尚未开放，未部署 |
| R1ARCH1 内部归档校验 | 已合并 #103 | 单gzip member/POSIX ustar普通文件和目录严格子集，完整流预算、CRC/EOF与按目标OS的路径校验；最小Compress1.28生产依赖准入。精确57e7fa4全部6CI与非作者完整7文件独审通过，高/中/低0，独立Linux重编17归档测试及20边界探针通过。仅内部组件，不是公开包格式定稿、上传API、安全解包或安装闭环 |
| PLUGSURF2 原生插件页面 | 已合并 #104 | 功能页原生CustomElement/DOM与独立DSH设置项；字体、颜色、焦点和错误沿宿主系统主题变量，删除Vue、自有调色板及独立OS主题监听。真实slot目标名和SRI模块/构造器对应缺陷已先红修复。精确3874097全部12CI、216项相关用例及新非作者第2轮复审通过，高/中/低0；独立70项、真实rc.2 SlotCore、实际Chrome模块/SRI与宿主主题、当前包烟测通过。UI19682B、包58205B，无新运行依赖；默认独立安装如实未配置。原生登录、真实总部连接/资源传输、安装与工具部署未接，未部署现场 |
| DESIGNBASE1 HTML设计基线 | 已合并 #105 | 18个现有管理台/插件HTML、需求复核、源码与截图原样入库，后续工位共享视觉和功能优先基线；工作原件字节保持，Git按既有规则规范换行。精确3bbe3cc全部6CI与非作者完整18文件独审通过，高/中/低0，独立原件摘要、生成一致性、37相对链接及两目录扫描通过。原型数据与动作仍为演示，不表示各业务能力已交付 |
| PLUGDOC2 原生规范同步 | 待外部审定 #106 | 同步原生 DOM/Custom Element、独立管理台 Vue 和宿主主题说明，保留 Desktop 转发 cookie 限制；功能/设置载体与未接登录/连接/资源安装明确区分。精确 eb6674e 全6项CI及非作者完整两文件独审通过，高/中/低0；含受保护的 contracts 路径，保留待审查，由外部审定后处理 |
| PLUGLOGIN1 原生登录视图 | 已合并 #107 | 原生步骤表单、受控失败和完成回调，沿宿主主题。首轮两项中问题（布尔隐藏值、语言切换漏完成通知）已先红修复；精确4cdeef8全12项CI与全新非作者第2轮复审通过，高/中/低0，独立66项UI及27条探针通过，原断言和首轮证据保留。视图不自行发请求、建会话或导航，真实客户端认证与宿主会话仍待接线 |
| PLUGSTORE1 离线持久准入 | 已合并 #108 | 固定owner与OTP counter原子持久化，提交成功后才签发会话，修复不同事务同OTP并发完成窗口。精确c670955全12项CI与非作者完整8文件安全独审通过，高/中/低0；独立实际rc.2全177项（0skip）及8组真实provider探针通过，含六进程同counter竞争、重启重放与真实落盘后异常封闭。仅可选standalone内部前置，未挂真实入口，未部署 |
| CLOUDENTRY1 云端原生进入 | 已合并 #109 | 新/instance-login.html原生静态入口复用IAM表单/TOTP、会话确认、本人实例打开/就绪观察及正式换票进入，含取消、晚到、失败重试及实际发行输入。精确9de4dcc全8项CI与非作者完整9文件独审通过，高/中/低0；独立60项、额外16负向及2条实际ESM产物流通过。新入口依赖图无Vue，实际浏览器HTTP夹具烟测通过；未切默认、未做真实后端栈/DSH部署验收。门户退出见#110，实例内退出与客户端会话适配另行交付 |
| CLOUDLOGOUT1 云端原生退出 | 已合并 #110 | 同源原生退出页、显式确认调用既有IAM注销，严格未重定向HTTP204成功，GET无注销副作用，不创建/启停实例。首轮两项中问题（真实HTTP状态误分类、历史返回空白）已先红修复，补22项回归；精确1e3dfea全8项CI与全新非作者第2轮复审通过，高/中/低0。独立产品116项、原探针9项、新探针8项及类型/格式/三入口图通过；实际Chrome协议夹具验证两轮后退恢复、状态冲突与旧响应不导航。合入14个mode/blob与受审树一致。实例内可信门户退出配置、本地流程装配与真实IAM/edge/DSH/WS联调未交付，未部署 |
| PORTALCFG1 可信门户配置 | #111 待外部审定 | 既有 access.portalOrigin 经 renderer-owned Loader row、严格校验及冻结副本进入受认证 bootstrap，缺值无推导回退；profile 不能覆盖字段或子树，保域名/纯 IP 完整 Origin。精确0404293全28项检查成功后，非作者完整10文件独审通过，高/中/低0；独立155项Node、Go renderer/vet、完整插件类型与21Schema通过。命中 contracts/infra-agent，保持【待审查】，不自行合并。退出按钮、主体/会话绑定与真实IAM/WS联调未交付 |
| POLICYSAFE1 策略完整性 | 已合并 #113 | 三种既有策略的版本读写复用tenant锁，RC同版本仅一方成功，RR旧快照整体回滚；workspace按既有六字段类型/下界验证，保未知字段、省略及精确大整数，不加上界或事件。精确5592da2全6CI后由非作者完整3文件独审通过，高/中/低0，独立真实PG61项全过；作者集中161项及最终61项按分轮归属。3个合入blob与受审树一致。团队资料PATCH无ETag，未部署 |
| ADMIN6 系统设置 | 已合并 #112 | 团队资料与workspace策略接真实接口；首轮纯IP等CSRF登记时卸载仍发写的问题已修。精确dd8ddec全8CI后，全新非作者第2轮复审M1关闭、高/中/低0；独立设置42项与共享登记/重挂真网络2探针通过，取消后服务器零业务交付，作者247项全量过。原忽略signal的假fetch探针原样保留并说明调用不等于交付，未冒全绿；首轮6077字节前缀完全保留。8个合入blob及配套#113三文件均与受审树一致；团队PATCH无ETag，未部署 |
| LOCALPORT1 新插件本地迁移 | 准备中，未提交 | 源码层迁入Desk及真实本机会话基础，随新Auriga包独立发行；旧插件运行时消费草稿已撤回。49项相关检查、类型及实际打包加载探针通过；生产仍available=false、登录503，无会话签发。新IAM本机绑定、持久退出失败处理、专属Origin/CSRF、真实Host/WS与宿主主题尚未完成，不称登录交付；不以原设备人工核码或门户USER令牌暗中代替 |
| FEATUREBASE1 功能继承核对 | 本轮只读核对完成 | 按旧Hub/Team真实UI、路由和SDK核对，默认保留完善原功能；模型中转/渠道/Auto、资源实际安装/分发、账户安全、任务凭据、SDK/文件协作及运维等缺项纳入改造，不能按新API便利或占位页面默删。总部明确改为DSH对话与Team MCP工具。此项仅确认继承基线，未把缺项标成交付 |
| PLUGRESOURCE1 本机插件管理 | #115 待外部审定 | 真实库存、详情、原生启停及非秘密标量配置，未核实安装状态与managed/边界模块保持只读。精确61b0f21全12CI后非作者完整23文件独审高/中/低0；独立使用当前实际rc.3宿主重验配置持久、秘密保留、原生鉴权、HMR停启及恶Host/Origin/managed/revision负向，均无跳过，另生成类型一致。作者186项插件/1原有可选跳过、70面板/33壳/161契约通过。命中contracts保持【待审查】，正式安装依赖已合#117，未在用户实例上线。市场分发、安装更新卸载、上传与Skill/复杂配置仍保留需求 |
| IDPUI1 身份源管理 | 已合并 #114 | 真实IAM列表/详情/schema配置新建编辑和启停，准确停用语义、内置只读、秘密/未知配置保留；整合设置后287项、类型/lint/build通过。精确8aaf287全8CI后非作者完整8文件独审高/中/低0，独立40项及共享CSRF卸载/重挂/撤权恢复真HTTP6探针通过。合后8blob同受审树，未部署真实身份源；目录/连接测试/完整准入与恢复仍需后续 |
| TEAMMCP1 内部通信核心 | #116 待外部审定 | 9内部RPC、7表RLS、持久线程/消息/投递/幂等；生产在线准入仍默认拒绝，外部远程MCP/Node/managed/系统总部及combined尚未接通。首轮独审中2（非参与者导致合法回复死锁、跨delivery复用宿主requestId假确认）先红后修；精确f3c76eb自身25CI全成功后，全新非作者第2轮复审均关闭、新增高/中/低0。独立真实PG/gRPC19项与五组PG探针全通过，生成物一致；首轮12703字节与旧probe保全。contracts保护保持【待审查】，不自合，不冒真实双DSH闭环或业务Task引擎 |
| HQENTRY1 总部DSH入口 | 只读核对完成 | 现open/edge/插件门锁都要求portal主体等于实例owner，不能将管理员个人open冒系统总部；已列系统实例归属、运行管理与保留实际管理员身份的授权短票最小边界。尚未实施，不在进入时自动发任务 |
| PACKLINK1 本地安装/link | 已合并 #117 | 修复缺dsh.bundle、根Cordis与库导出混淆、子路径漏browser；补公开patch/files和目录准备说明，成品tar携带完整资产。精确34d67d3全12CI后新非作者完整5文件独审高/中/低0；真实当前rc.3目录与tar各2通过0跳过、独立重打包SHA一致。主仓快进后5blob同受审树并准备UI，使用用户原目录再跑实际installer2通过0跳过；完整默认app原生安装后已启用/运行中、真实浏览器双页签及原生设置可开、资源200/无auth401/恶Origin403，Console error0。尚未配置壳不冒本地认证或平台连接已通，未动用户profile |
| RENDERENTRY1 托管浏览器入口 | #118 待外部审定 | 将renderer行改为可被真实collector发现的包根，保持id/inject/managed/Origin/MCP/token_ref保护。已整合#117依赖，精确6ff32df全17CI后新非作者完整4文件独审高/中/低0；独立renderer10顶层及14子测试0跳过、vet通过，私有overlay恢复旧入口后两分支按预期失败。infra/agent保护保持【待审查】，未启动真实托管栈、未现场部署 |
| NATIVELABEL1 原生侧栏标签/图标 | 已合并 #119 | 真实页面发现原始panel标签及空图标，改原生getter与宿主Users Regular/Medium；遵守size/active/currentColor，无新SVG/图片/样式/依赖。精确64cb87d全12CI后新非作者完整3文件独审高/中/低0，独立32项全过且0跳过；核当前rc.3及固定rc.2模块兼容。root完整当前rc.3页面实测Auriga标签、展开16px/收起18px、选中描边1.3px、颜色与宿主文本一致且Console error0；主仓已快进c677b8f，3blob与受审树相同。用户实际profile未修改，未冒登录或平台连接已通 |

## M3 AI 与资源

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| C2 M3 契约草案 | 已合并 #33 | 三轮独立审查；D1–D7 用户决定已定，正式契约按草案落地 |
| C3 正式契约 | 已合并 #46 | 按 D1–D7 定义 34 REST、7 内部 RPC，Java/Go/TS 同步生成；冻结 1e23a2b 的 16 项 CI 和新会话复审通过，无高、中、低问题。已修敏感操作 step-up 与实例本人用量内部映射，92 条新增负向断言经独立变体核证；契约通过不等同业务或 agent 制品信任链已实现 |
| GOP1 Go 安全补丁工具链 | 已合并 #48 | CI 与四份 Go 镜像构建固定 1.27.3；冻结 695afed 的 22 项 CI 及非作者独审通过，无高、中、低问题。语言最低版本保留 1.27.0，不以工具链补丁代替第三方依赖准入 |
| D0b 前置依赖接线 | 已合并 #49 | 统一 Go/Java 依赖、资源测试依赖和单一权限目录 JAR 打包，修补准入发现的生产依赖风险；两种模块解析与 Java 架构边界通过，Windows 调用扫描及 Linux 包级扫描未命中受影响生产导入。固定扫描工具的 Linux 符号分析因 SSA 错误未完成，完整图剩余未导入风险仍留证。冻结 abe22ef 的 22 项 CI 与独审通过，已形成 G1/R1 的统一基线 |
| I3a 实例 AI 签发与权限登记 | 已合并 #51（签发与权限子范围） | 已实现仅受认证 workspace 可调用的专用签发、有效成员/权限交集、全目录门户过滤与租户权限登记；冻结 d8bd6cb 的 6 项 CI 与独审通过。作者 232 条测试、3 条架构检查及审者 30 条聚焦测试、5 条独立探针通过。WR1/G1 权威绑定、轮换吊销联调及设备 resource-audience 交换仍待后续，不计作完整身份链已交付 |
| G1/G2/R1/N1/X1/F1/P2 | 待分批实现 | G1/R1 正式契约和统一依赖基线均已合并，可以按小任务推进；开工顺序与后续模块见 [m3-start.zh.md](m3-start.zh.md) |
