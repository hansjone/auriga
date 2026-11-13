# 云端共享 DSH 与独立容器提降权设计草案

## 1. 结论与基线

调研日期为2026-11-13，Auriga 精确基线 `270ed0d8eca1c8c34e30d21207e6bff304a27b91`，DSH 为 `0.2.1-rc.3`。本稿及契约均未实现、未部署。审查等级 A，涉及 contracts，交主控标记【待审查】，须全新非作者完整独审；不据本文开放共享环境。

用户要求默认共用一个容器、一个 DSH 主进程，各有会话与工作区；管理员可以给 UAC 或邀请成员配置独立容器，也可降回共享；切换默认丢弃会话。这些是目标，不是现有能力。**三个候选均不满足直接内置条件；优先借鉴 sunbigfly 的功能分解，但不把它原样装进 Auriga。推荐先做公开 API 可行性验证，再自研最小适配。若不能证明身份、读文件与执行边界，默认共享只能开放给明确接受弱隔离的互信成员，不能作为不可信多租户产品上线。**

一个 Node 进程的插件共享地址空间和进程权限。会话 ID、工作区 cwd、Cordis scope、0700 目录都不是同 UID 下的安全隔离边界。即使子命令有沙箱，进程内的插件、文件读取服务和凭据缓存仍是共同信任域。禁止用“workspace-write”直接推导“不能读其他工作区”。

依据包括 [统一资源包](resource-package.zh.md)、`contracts/plugin/identity-token.schema.json`、`clients/dsh-plugin/src/managed/{door,session,host-auth,directories,models}.js`、`services/workspace/impl/.../WorkspaceService.java`、`infra/agent/internal/{renderer,runtime,storage,ingress}`、`infra/edge`。其中 workspace 当前按 owner 查询/创建一个物理实例，managed 门禁校验 `sub == ownerId`；它们不能仅把所有 owner 指到同一实例就完成共享。

记忆库《Auriga 工作台产品设计》《DSH宿主兼容与连接模式认证》中云端身份与工作区约束继续成立：先认证后准备/进入；托管插件不显示未登录可用；目录限 workspace；身份凭据失效拒绝执行。连接模式的“可跳过平台登录”不适用于云端托管。登录设计取 [PR #320 精确版本](https://github.com/hansjone/auriga/blob/79718017a2cb23996d55ffc3bc92aa5f91a5d8e2/docs/design/auth-shell.zh.md)，仍在审查，不合并到本分支。UAC 工号映射到 IAM 主体和租户成员，部门资料不授予模式权限，扫码 token 不进入 DSH；认证来源不决定是否有独立容器。

## 2. 公开插件调研与源码证据

搜索覆盖 GitHub、npm、DSH 社区讨论，关键词包括 `@deepseek-ai/dsh multi user plugin workspace`、`dsh Cordis 多用户`、`dsh multi-tenant`。搜索站和插件市场只用来发现候选，下列结论来自克隆后的精确源码。npm 的包元数据不等于 rc.3 实测；未运行任何第三方安装脚本，未安装候选，未启动真实 DSH，也未验证运行行为。各仓库为浅克隆，最近提交时间可核对，但不能据此推断长期维护频率。工位 `.research/` 保存三个源码仓库、rc.3 上游和查询/校验日志，不进入产品提交。

| 候选及快照 | 许可证、活跃度 | 依赖与 DSH 声明 | 身份、会话和目录实现 | 选择结论 |
|---|---|---|---|---|
| [sunbigfly/dsh-multi-user-service](https://github.com/sunbigfly/dsh-multi-user-service/tree/6d5547bb4cc956ecd04f9115a5a2cc233f29eb8a)，0.1.1 | LICENSE 为 MIT；最近提交2026-10-28，距调研16天，有大量测试，未验证维护响应 | Cordis 4.0.5、dsh-api-gateway rc.2、schemastery 3.18.5、smol-toml 1.9.1、ws ^8.21.3、zod 4.6.7；Node >=22；engines/兼容矩阵明确 rc.2，rc.3 unknown | 自有服务账号、成员入口、会话归属及成员 cwd；Linux 原生命令边界探测，限制工具来源，清理环境；同一个原生 Agent 注册表 | 功能最接近，借鉴分解与负向用例；内部包解析、方法替换及 rc.3 未确认，不直接内置 |
| [dusbin/dsh-multi-tenant](https://github.com/dusbin/dsh-multi-tenant/tree/b2f5866ed5cde467d7ce7eca45848faeed8a1407)，0.1.0 | package.json 声明 MIT，但快照无独立 LICENSE，复制前补核；最近提交2026-08-30，距调研75天，有单测 | ldapts ^9.0.0、openid-client ^5.7.1、node:sqlite；未声明 engines.dsh 或 DSH peer 版本；rc.3 未验证 | 认证反代、SQLite 用户/RBAC；网关改写 sessionId 前缀，列表和 WS 下行过滤；没有等价的系统文件/执行隔离证据 | 不选：自有登录和 Auriga 重复，协议形状依赖较重；前缀不能约束模型的文件访问 |
| [gepeiyu/dsh-workos-tenant](https://github.com/gepeiyu/dsh-workos-tenant/tree/5c64ad90605d676059cf948e9a1aa1f96e81c56f)，0.3.0 | LICENSE 为 MIT；最近提交2026-10-12，距调研32天，有测试；市场的旧0.2.7不作为当前版本 | Cordis ~4.0.4、schemastery ~3.18.4、WorkOS node ^10.13.0；dsh-llm peer/compatibility `>=0.2.0-rc.1 <0.3.0`，不能单凭范围认定接受后续 prerelease rc.3 | WorkOS AuthKit，organization/user 归属表；用户目录规范化；模型 provider 前缀和凭据引用命名空间；可选 D1 | 不选：认证不适配 UAC，运行时大量改 prototype；应用目录校验不覆盖任意命令 |

### 2.1 sunbigfly：最接近功能目标，但不是合格成品

实际阅读 [carrier/member.mjs](https://github.com/sunbigfly/dsh-multi-user-service/blob/6d5547bb4cc956ecd04f9115a5a2cc233f29eb8a/carrier/member.mjs) 的成员路由和自有传输装配、[carrier/native-sessions.mjs](https://github.com/sunbigfly/dsh-multi-user-service/blob/6d5547bb4cc956ecd04f9115a5a2cc233f29eb8a/carrier/native-sessions.mjs) 的 create/resume、父子归属及取消/释放，以及 [carrier/member-boundary.mjs](https://github.com/sunbigfly/dsh-multi-user-service/blob/6d5547bb4cc956ecd04f9115a5a2cc233f29eb8a/carrier/member-boundary.mjs)。后者导入宿主 `@deepseek-ai/node-addon-system/landlock-run` 子路径，Linux probe 必须 full；成员目录要 canonical、0700 且同进程 UID，保护根不能重叠，shell 环境按允许表重建。MCP 来源被排除，任意插件也不能仅凭授权进入执行允许表。其同 UID 的0700不是隔离证明，实际约束依赖原生 launcher 和全面接线。

[carrier/host-package.mjs](https://github.com/sunbigfly/dsh-multi-user-service/blob/6d5547bb4cc956ecd04f9115a5a2cc233f29eb8a/carrier/host-package.mjs) 从宿主入口解析包位置；native-sessions 通过 `Symbol.for('cordis.original')` 取得实例后替换注册表方法，member-boundary 替换 sandbox.confine、spawn、spawnTerminal。虽然不修改磁盘上的 DSH 文件，这些仍不符合 Auriga“只用公开 API，不依赖内部结构”的要求。不能改个版本声明就说兼容 rc.3。只参考已核对的行为与测试思路，不直接复制这些接入手法。

### 2.2 dusbin：网关隔离不等于执行隔离

阅读 [ownership.js](https://github.com/dusbin/dsh-multi-tenant/blob/b2f5866ed5cde467d7ce7eca45848faeed8a1407/lib/host/ownership.js)、[gateway.js](https://github.com/dusbin/dsh-multi-tenant/blob/b2f5866ed5cde467d7ce7eca45848faeed8a1407/lib/host/gateway.js)、[visibility.js](https://github.com/dusbin/dsh-multi-tenant/blob/b2f5866ed5cde467d7ce7eca45848faeed8a1407/lib/host/visibility.js) 和 lib/index.js。会话归属编码为 `u-uid-t-tid-s-uuid`，创建时改写，其他顶层 sessionId 检查归属。列表限本人或租户角色，管理员能看全部。visibility.js 注释仍称 events.mux 不在本层过滤，但当前 gateway 已有 pumpDownlink，因此不引用旧注释断言“完全未过滤 WS”。

仍存在审查重点：handleApiPost 注释明确解析失败或超大体透传；未知信封/新端点不能默认透传。就算修复，前缀、过滤列表和 WS 帧也没有给 bash、文件系统或 MCP 加用户安全域，不能用于公开多租户执行。内置另一个密码/LDAP/OIDC 登录入口还会破坏 Auriga 统一身份职责。

### 2.3 gepeiyu：覆盖面广，接线方式不符约束

阅读 [src/service.js](https://github.com/gepeiyu/dsh-workos-tenant/blob/5c64ad90605d676059cf948e9a1aa1f96e81c56f/src/service.js)、[workspace-scope.js](https://github.com/gepeiyu/dsh-workos-tenant/blob/5c64ad90605d676059cf948e9a1aa1f96e81c56f/src/workspace-scope.js)、[model-scope.js](https://github.com/gepeiyu/dsh-workos-tenant/blob/5c64ad90605d676059cf948e9a1aa1f96e81c56f/src/model-scope.js)、guards.js 和 auth.js。用户目录按 organization/user 拼接、拒绝部分符号链接并核对 realpath；会话和工作区 guard 过滤列表及派生对象。模型引用带身份 hash 前缀，credential resolve 先读个人引用，再回退共享引用；不能把该回退直接用于 Auriga 的每用户中转授权。

service.js 的 patchPrototype、patchGateway、patchCredentialProvider 修改服务原型及私有标记；不满足公开扩展点约束。工作区根未配置时 assertWorkspacePath 直接 resolve，属于必须禁止的生产配置。应用层路径检查也不能限制任意 shell 或恶意进程内插件。WorkOS 和 D1 都不是本任务需要的新认证/持久化依赖。

### 2.4 内置和上游更新策略

本轮不硬选插件，不把候选放入生产依赖。若后续某候选去掉不合规接线、通过 rc.3 及隔离验收，优先采用**精确 commit 的源码快照＋锁文件＋隔离构建制品**，作为统一资源包 plugin 审核/签名；不运行浮动 Git main，不在启动时拉 npm latest。保留 MIT LICENSE、版权及 NOTICE；dusbin 先取得明确许可证文本并核依赖许可证，未补核不分发。安全高风险资源按统一包要求至少两名不同非作者审核，其中一名具备安全资格，作者不得自审/自发。

需要长期修补时才在 Auriga 独立目录维护小型 fork，记录上游 commit、补丁清单、制品摘要和依赖 SBOM；不 fork DSH。平台编排和 IAM 适配由 Auriga 实现，不能跟第三方包绑定到同一个新登录系统。每次上游升级冻结新快照、比对权限/依赖/接口、重新审核签名和真实双用户验证；旧版本撤销、租约与高水位沿统一资源包。当前通用资源 P1 尚不接 plugin v2，因此未完成插件协议迁移前走既有受管插件发行链，不能用本文提前启用 v2。

## 3. 隔离边界与攻击面

以下“能隔离”仅指补齐设计后的服务端授权，不代表基线已有。进程自身和受信插件始终可访问全池状态。

| 对象 | 同进程原生边界判定 | 需要的措施及不能承诺的部分 |
|---|---|---|
| 会话、子会话、工作区元数据 | 能隔离（需完整接线） | 可信归属表和 remoteAccess 每请求/事件授权；列表、搜索、历史、附件、派生、删除、通知均过滤，不能只改侧栏 |
| 工作区目录 | 只能靠约定 | cwd 与用户 ID 分目录，不等于 OS 阻止同 UID 读取；目录映射禁止浏览器指定 |
| 文件读写、上传下载、搜索 | 默认隔离不了 | 原生 workspace-write 主要约束写；读路径必须正向允许本人根和只读运行库，拒绝链接逃逸、proc、宿主路径；所有入口都要覆盖 |
| shell、terminal、后台任务、子 Agent | 默认隔离不了 | 所有命令必须走会话执行沙箱；禁止 danger-full-access 和成员自行批准扩大范围，杀完整进程树；无执行门禁时禁用这些工具 |
| MCP 工具 | 共享 stdio/全局凭据隔离不了 | 平台远程 Streamable HTTP MCP 按成员/会话签下游能力，每调用校验；不共享带权限的 MCP 子进程，不开放任意用户添加服务 |
| 环境变量 | 进程内隔离不了 | 不放个人密钥于 process.env，不按请求临时修改它；子进程只取允许表。插件仍能读取全局环境 |
| 模型 Key | 只能在请求路由层隔离 | 上游真 Key 留 AI 网关；主进程只获短时成员能力；并发、流式和后台任务均绑定不可变身份，不能共用 ownerKey |
| 插件配置、原生 user skill | 默认全局，隔离不了 | 固定可信插件集合；不能让一个成员安装会执行代码的私有插件。个人配置用独立数据服务，不改全局 settings |
| 日志 | 能隔离读取权限，进程内不隔离 | 固定归属标签、脱敏、按人导出，管理员诊断走审计；stdout、崩溃栈可能混入多人内容 |
| 内存、GC、插件故障 | 隔离不了 | 无进程级保密或故障隔离；内存耗尽、事件循环阻塞及崩溃影响全池 |

直接问题的回答：**在没有额外强制机制时，用户可以通过对话诱导文件工具或命令读取另一个目录、凭据文件和共享进程可见资源。** 只隐藏工作区按钮或设 cwd 无法阻止。提示词和 AGENTS 规则只能辅助，不算安全门禁。

按用户切系统账号仅适用于独立执行子进程，不能在一个 Node 进程中并发 setuid/seteuid 来切请求身份；父进程仍持全体数据。可行的加强是每会话/成员的执行 broker，以独立 UID、挂载/PID/网络命名空间或沙箱 worker 限本人文件与资源，并在 broker 外保存凭据。这样仍是一个 DSH 主进程，可有临时执行进程，但需要用户接受更复杂的部署。**仅给 shell 加沙箱不够**：进程内 fs、原生文件预览/附件、浏览器、插件、MCP 都要禁止或交给同一 broker；不能证明全面覆盖则共享仅提供无文件/无执行的受限聊天能力，或继续使用独立容器。

公开 API 可行性证据：rc.3 上游 [remote-access/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/remote-access/src/index.ts) 导出 registerPolicy、invalidate，非 owner 无策略默认拒绝；[session-controller/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/session-controller/src/index.ts) 已用 remoteAccess.allows 过滤列表、搜索和投影。可用此机制自研 principal/策略，避免原型修改。但这不证明新建、文件与所有 UI 路径已经可用于多用户。

上游 [sandbox/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox/src/index.ts) 明确网络及进程可见性不属于 SandboxMode，定义的 full 是该文件效果策略的执行完整度，不能解释成“租户全隔离”。[sandbox-policy](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox-policy/src/index.ts) 的会话 cwd 决定 workspaceRoot，也是执行策略而非身份。

不适合共享：互不信任的外部客户、跨组织高敏资料、个人/生产密钥、需要任意 shell/MCP/插件/提权的开发、浏览器登录高权限后台、特权网络运维、合规要求进程级保密及故障域隔离的用途。总部系统 DSH 保持独立；它具有跨成员授权，不能加入普通共享池。

## 4. 自研架构与容量

### 4.1 物理池与逻辑分配

拆为 physical pool（一个容器＋一个 DSH 主进程）与 placement（一个租户成员的云端工作环境）。平台默认可先只有一个池；后续多池是容量扩展，每个池仍一个主进程。每人有独立 placementId、epoch、资源选择和会话归属，不以物理实例 owner 冒充所有用户。跨租户共池风险更大，推荐先按同一互信租户分池，是否接受偏离“所有用户一个容器”由第8节决定。

workspace 是唯一分配写者；用户认证后 open 创建/恢复 placement，调度到已有池；池不存在时由 workspace 发规格，agent 拉精确受管制品、配置 guardian、资源视图和网络，再启动。浏览器和插件均不能创建容器。池管理身份是系统主体，不授予普通用户 owner；独立容器仍通过同一编排链创建。旧 `/me/instance/open` 不在本草案偷偷改语义，共享接入前需新增 placement open/票据协议并整体接线。

生命周期为 pending → provisioning → ready → draining → stopped/failed。成员退出仅撤销该浏览器连接，不按人数误停全池；凭据失效的该 placement 必须整体不可用。没有用户且没有任务时才可闲置回收；资源失效按第5节保守停全池。容器崩溃影响池内所有用户，取消在途任务、记录 uncertain 状态，不自动重放产生副作用的命令；恢复持久化历史并验证信任，受污染会话不恢复执行。独立容器只影响单人。

建议试点上限每池20名登记用户、4个同时活跃主会话、每人1个活跃主会话；这是**待压测的调度预算，不是性能结论**，不额外裁模型上下文或默认中转 token 限流。子会话和后台执行计入其主会话资源预算。内存预算按 `基线RSS + 活跃数×P95会话增量 + 缓存 + 30%余量` 估算，用真实长上下文、工具、WS 混合压测修订。持续超80%内存、队列等待或事件循环延迟超 SLO 时停止接纳并扩新池；已绑定用户保持粘性，不静默迁移在途会话。若坚持全平台永远只有一个容器，满载只能排队/拒绝并扩容，不能承诺无中断横向扩展。

### 4.2 身份输入与防串号

edge 先完成门户/管理台 audience 认证、实例 cookie/票据授权，清除外部所有身份头，再签短寿命 `Auriga-Identity`。agent ingress 继续验签转发。shared 模式的 aud 不能只等于物理池：需新增版本的签名声明，绑定 tenant、member、placement、epoch、physicalInstance、method、canonical path、iat/exp/jti；旧单 owner 协议不直接复用。纯IP/HTTP沿既有 ADR0020能力和风险，不放开 Origin/CSRF，也不退回信任任意 header。

插件可信适配层验证签名/kid/iss/aud/有效期、单值 header、方法路径和重放，向 workspace 核对 placement 当前 epoch 与成员有效性，再创建**非 owner principal**。从可信映射取会话/工作区归属，所有自称 userId/role/agentPreset 的参数无效。不能按 displayName、UAC 工号文本或路径前缀充当授权真源。每个 HTTP 请求独立绑定；WS 握手固定身份，逐流/逐方法授权，epoch 改变主动断开、取消重连票据；撤销不能只等 WS 重连。池内禁止普通浏览器直达 DSH owner 端口。

公开 API 验证线必须确认可通过已导出 Peer/Remote 扩展点注入该 principal，正确生成非 owner 页面与 access snapshot；不能把默认 owner index 返回所有用户，再指望过滤侧栏。新建 session/workspace 在归属持久化前不可对外可见，失败销毁；子会话继承且不能改 tenant/member/epoch；远程 delegated 会话沿单独授权而非变更 owner。异步任务保存绑定快照并逐次复核，不依赖当前浏览器的全局“登录用户”。

### 4.3 目录、配额和备份

建议池数据分三类：`/var/lib/auriga/pools/<pool>/host/` 为受信 DSH_HOME/会话索引，`.../members/<tenant>/<member>/<epoch>/workspace/` 为本人文件，`.../control/` 为归属、资源账本和审计。成员只见自己的 workspace；host/control 不作为工具 cwd、skill 扫描根或上传目录。ID 均由服务器生成并校验，无用户名路径。管理存储唯一落盘方仍是 agent。

workspace 内每人默认建议5 GiB、10万 inode及有界上传/解压预算，实际值由管理台配额决定。现有 layout 的 DiskUsageMB/SoftQuotaExceeded 是实例软配额，需新增成员计量及硬限额，否则一人可塞满全池。临时目录、附件、会话历史、下载缓存分别计费/限额，不通过移到 tmp 绕过。备份按成员逻辑归属和一致快照导出，加密、租户授权、可恢复性验证，不能把全池 DSH_HOME 原包交给任一成员。物理全池灾备仅受审计运维可访问。

### 4.4 模型、消息网络与桌面

AI 网关持上游 Key；成员调用获得限 audience、member、placement、epoch、session 的短时下游能力，网关从验证结果归属计费，拒绝模型参数里的自报身份。禁止把现有单实例 model credential 当全池公共 Key，禁止按请求改 process.env 或全局 provider。原生模型目录可共享公开元信息，私有 provider 设置和个人 Key 引用必须按成员隔离；若公开 LLM provider API 无法做到请求绑定，试点禁个人 Key，不伪造已经兼容。流式取消、失败重试、计量去重以调用ID关联；auto 池内路由不因此增加模型参数限制。

消息网络的物理连接仍以 DSH/插件为联网主体，不能每个历史会话心跳；共享主进程复用稳定物理传输，但逻辑 capability、邮箱、授权、背压与去重按 tenant/member/placement/epoch/session 分区。不能沿旧的一个 owner/device credential 广播给全池。桌面 DSH 和云端互通仍走平台远程 Team MCP 与既有消息网络授权，每个消息校验发送者、目标、父子关系及代次；提降权撤销旧地址，旧积压进入过期/不可执行状态，不重放到新会话。Desktop 本地身份与数据不因云端切换而迁移，给出云端历史丢弃提示即可。

## 5. 统一资源包在共享进程下的冲突

统一资源包要求共享 skill 独立固定 customSkillDirs 活跃根、私有 skill 原生 user 根、身份切换清空活跃视图、guardian 在撤销或租约到期停止受影响 DSH 并新建会话。共享进程中**不能轮流把全局固定根切到当前点击用户**，也不能把所有用户私有技能放进同一个原生 user 根；否则并发扫描、缓存和模型上下文会串人。签名证明字节可信，不授予另一成员读取私有内容的权限。

建议共享池首期只装平台统一且全池有权的只读基础技能；公共源码/制品缓存按摘要共享，选择/租约/安装回报按 placement 记录，受管根由 agent 唯一写入，保留上游预置目录。用户私有 skill 和用户各自选择的共享 skill，只有在公开 API 证明存在会话级目录/来源过滤及缓存隔离后开放；必须从扫描到实际注入全链证明，不用 UI 隐藏代替。未通过时共享模式明确禁用这些能力，独立容器和 Desktop 继续完整能力；这是产品差异，需用户决定。

保守 guardian 规则：一个主进程里任何已加载共享正文失效，都停整个池，终止其执行树，清空所有受影响活跃视图并重验租约/撤销清单后重启；所有受污染会话不可继续执行，可按授权只读查看。单成员凭据失效若能证明其全部 Agent/后台/远程能力已撤销且资源未进入全局缓存，则只隔离该 placement；无法证明时同样停全池。不能继续套“只停这个人的容器”这一旧假设。

离线私有安全启动也不能直接复用：全局共享根不清空、缓存/进程残留不确认就拒绝启动；清空后共享池首期仍不提供未经验证的私有 skill。若需要“每人独立 guardian 故障域和私有技能”，应使用独立容器或升级为独立 DSH 主进程，后者偏离本任务共用主进程目标，须另决策。成员身份切换不原地重用已载私有上下文，销毁其旧范围并分配新 epoch。

## 6. 管理台提权与降权

### 6.1 管理操作和权限

成员详情展示当前模式、状态、独立配额、切换结果。提权按钮是“分配独立容器”，不授予 Docker/root 权限；降权是“改为共享”。权限建议新增 `workspace.placements.read/write`，仅 ctx=admin、该租户授权管理员，step-up 采用既有 IAM 近期凭据策略；UAC 管理员无可用 MFA 时需已有可执行的管理员 step-up 方案，否则拒绝，不把扫码自动等同 step-up。

确认面板列出会话永久丢弃、运行任务将取消、文件策略、预计影响，要求填写原因；请求 If-Match 防并发、Idempotency-Key 防重复。审计记录操作者/租户/目标成员/原目标模式/operation/修订/epoch/step-up引用/结果与分类错误，不记录会话正文或 token。跨租户对象404，普通门户及插件不能调用管理接口。独立容器 entitlement 与实际 mode 分开：只有切换成功才实际生效；从不以 UAC/团队身份来源隐式获得特权。

### 6.2 双向状态机和故障处理

两个方向使用同一操作状态机：accepted → fencing → draining → preparing → committing → succeeded；失败进入 failed，停止不确认或恢复歧义进入 blocked。from/target 必须不同；同一成员仅一个在途操作，操作状态与期望模式事务持久化，节点租约和 CAS 防重复执行。

1. accepted 检查授权、成员有效性、配额与容量，预留目的端资源；失败尚未进入围栏时保留原服务和会话，审计失败。
2. fencing 标 switching，递增并持久化 epoch，撤销旧 open 票据、浏览器/网络/模型/MCP能力，阻止新 prompt/附件/后台任务和旧连接继续调用。用户看到“正在切换，将关闭当前会话”。
3. draining 取消本人主/子会话、终端、后台任务与外发调用，限时30秒建议值；超时由受信执行器杀进程树。共享 → 独立不得为正常单人切换先杀其他人的任务；若无法确认本人执行隔离，转 blocked 并按安全策略停池，不冒成功。
4. 证明静止后销毁本人旧会话数据、索引、附件会话引用和消息路由；不迁移历史/上下文，设置 sessionsDiscarded=true。安全审计按保留政策独立保存，不可从门户恢复会话。会话删除失败不能推进提交。
5. preparing：共享 → 独立创建新容器/新 DSH_HOME/新工作区、同一 Auriga 插件并验证门禁；独立 → 共享预留 placement、清理旧容器运行能力并准备空工作区。工作区策略见下一节。旧独立容器不再可启动，但在回滚窗内保留受控快照。
6. committing 用旧 revision、operation、当前 epoch CAS 更新唯一绑定；旧执行必须已不可用，再发新能力。通过正常门户/DSH 页面与会话门禁探测后 succeeded，发布 outbox 事件，释放源配额和清理源制品。202 仅表示登记，不表示可验收。

回滚不恢复已丢弃会话。目的端失败时关闭新能力/临时容器，保持更高 epoch，在原模式重新创建**空会话**并重新签票；若文件快照有效可恢复本人工作区文件。若无法证实源已静止、绑定提交结果或权限被撤销，保持 unavailable/blocked，重试持久化 reconcile，不能让源和目标同时可执行。重启按 operation 与代次恢复，旧 ACK/晚事件不能覆盖新状态。用户看到具体分类和重试状态，不能在失败时无提示跳回仍活着的旧会话。

### 6.3 文件、旧用户与保留策略

**会话默认丢弃已经确定；工作区文件并未确定。** 推荐“封存本人文件快照30天＋新模式空工作区”，管理员可按审计流程导出；恢复文件须用户显式申请、内容扫描并限本人 workspace，不恢复 .dsh/.env/模型 Key/插件运行态。理由是文件价值高于临时会话，同时不把旧模式的执行配置自动带进共享信任域。备份空间、到期删除和最终删除失败告警要验收；快照不允许通过普通新会话自动读取。

现有独立容器用户不做双运行兼容或后台会话迁移。推荐先通知维护窗口与文件导出期；管理员逐批确认保留独立的名单，其余执行同一降权流程重新建共享 placement，会话丢弃，未确认文件策略前不删除数据。不把所有现有用户无条件永久授予独立 entitlement，也不在首次登录时暗中销毁工作区。

## 7. 契约草案、工作线与验收

### 7.1 本轮只增契约

新增 `contracts/openapi/shared-dsh.yaml`，通过 workspace.yaml 新增三个 path 引用接入生成链，原路径、schema、required 和语义不改。包括 `GET /me/placement`、`POST /admin/members/{memberId}/placement-switches`、`GET /admin/placement-switches/{operationId}`，DshInstanceMode 为 shared/dedicated，DshPlacement 含 placement/member/epoch/revision/state，切换仅接受 discard 与推荐的 archive-and-reset。未决定项落入草案不表示已经批准。

新增 `contracts/proto/auriga/node/v1/shared_dsh.proto` 只定义 mode/binding，不擅自占用既有 NodeControl 的 oneof 或给现有 InstanceSpec 换含义；Java/Go 同源生成。新增 `contracts/events/placement.schema.json` 定义 workspace.placement_switched 和 workspace.placement_switch_failed 的结果负载。沿 CloudEvents 信封取 tenant，workspace 生产，subject=placementId，operationId 幂等，revision/epoch 围栏；成功必须 sessionsDiscarded=true 且无 failureCode，失败带分类。fromMode!=targetMode、当前绑定一致、成员归属由生产方事务验证。消费者登记、权限目录、NodeControl 接线属于后续实现，未接线不得发送新事件。

本轮契约是可生成的控制面草案，不冒充完整共享数据面协议。实现前 K 线仍须冻结 placement open/票据、shared身份签名版本、资源视图选择及回报、模型/消息能力细分、guardian影响范围和成员撤销；缺一不启用共享。旧实例接口不提供部分兼容假象。三套生成器、OpenAPI lint、strict schema、契约正负例及 Java 契约校验结果详见工位报告；通过生成不等于隔离验证。

### 7.2 分期、依赖、文件边界

0. **API/隔离可行性线（先行阻断点）**：只用独立 rc.3 DSH_HOME 和临时随机端口，验证公开 PrincipalMap/Peer、remoteAccess.registerPolicy、会话创建归属、非 owner 页面、文件/工具替换点、会话 skill 来源与模型凭据作用域。测试 A/B 并发和延迟回调、未知端点拒绝。不得改 DSH、原型或解析内部包；有缺口提交上游公开能力需求，本项目保留独立模式。约3–5工程日，结果决定后续范围。
1. **K 契约线**：独占 contracts、生成代码和上述待冻结协议；补权限与事件映射、错误码、共享/独立 open 契约，约3–5日。其他线只消费生成类型，不能各造字段。A等级审查。
2. **W 编排线**：独占 services/workspace impl 的 placement/pool/switch及迁移，IAM和resource只经公共 API；幂等/CAS/outbox、容量/配额、崩溃恢复，约6–9日。依赖 K，可与后两线并行。
3. **E 身份入口线**：独占 infra/edge、agent ingress 的签名验证接线及托管身份适配层，替代单 owner 状态；逐HTTP/WS身份围栏、纯IP/域名、伪造/重放负例，约5–8日。涉及受保护路径，不自行合并。
4. **R 运行与资源线**：独占 infra/agent renderer/runtime/storage、云端guardian/资源唯一落盘；公开执行 broker、挂载和资源视图隔离，约8–15日。与E须先冻结 ingress 文件归属；不能并行改相同文件。私有技能公开 API 若不存在，保留关闭而非绕过。
5. **P 插件与模型网络线**：独占 clients/dsh-plugin 的共享适配新目录及必要 ai/network 公共端口；拆分成员上下文、来源过滤、短时能力、后台取消和重连。约8–12日。UI沿原生DSH，不新造Vue插件或第二登录；适配代码集中，约定 API 不符合即失败封闭。
6. **U 管理台线**：独占 web/admin 的成员模式/确认/进度和中英文；依赖 K，可先组件验收再接 W，约3–5日。验收包括 UAC/邀请成员同规则、未step-up拒绝、202与成功区分、文件影响提示。
7. **V 集成与独审线**：作者之外的审查者做全新完整审查；独立测试负责人覆盖正常入口、双用户/双租户攻击、共享池故障及提降权中断，约5–8日。真实PG/DSH全链只由主控串行占槽，工人不碰NAS。实现产物交主控，经针对性测试串行小批开发部署，用户从正常页面验收。

粗估为41–67工程日，取决于公开 API 缺口；排期可并行缩短但不能跳0线，不是交付承诺。无文件/无工具的受限试点约12–20工程日；全功能不可信多租户若需要新增上游能力，工期另估，不能承诺现有插件即装即用。

发布门禁至少包含：A不能列出/读/改/订阅B的会话、附件、搜索、skill及模型引用；绝对路径/链接/硬链接竞态/proc/env读取拒绝；命令/终端/MCP/后台/子Agent全部受限；未知协议默认拒绝；UAC失效本人不可执行；资源撤销及断网租约到期确实停对应故障域；取消、切换、节点重启和晚ACK无双活；旧票据、重放、跨租户和旧epoch拒绝；真实浏览器门户进入、提降权、新建会话和受限提示均可见。没有真实宿主结果只能报代码/设计待验证。

## 8. 待用户决定

1. **共享信任等级**：推荐先限同一互信团队，明确进程内无保密隔离。若默认必须承载互不信任用户，则在完整文件/执行/凭据边界被证明前仍保持独立容器；接受风险不能替代实现门禁。
2. **一个池还是永远一个容器**：推荐初期一个池、容量满后多个池且每池一个主进程，先不跨租户；若要求全平台永久一个容器，应接受排队、全员维护窗口和共同崩溃影响。
3. **共享功能差异**：推荐公开 API 验证前不开放任意插件、个人 Key、私有 skill、成员任意共享 skill/MCP、shell和终端；需要这些能力的人提权到独立容器。若必须功能相同，应先完成 broker 和每会话公开扩展点，不以全局目录轮换代替。
4. **guardian影响面**：推荐先保守停全池并明确中断其他人任务；若不能接受全池因一份资源到期停机，选独立容器或经证明的分域执行架构。原统一资源包规则不能原样套用。
5. **文件处置**：推荐本人文件封存30天、新模式空目录；会话始终丢弃。直接删除省空间但损失不可逆，自动复制旧 .dsh 和凭据有串号及安全风险。
6. **旧用户重建**：推荐维护窗口内批量重建，明确保留独立名单，无历史会话迁移；先给导出/封存窗口，避免登录即破坏。未决定文件策略前不执行删除。
7. **容量/配额与停任务时限**：推荐以20登记/4活跃/每人1活跃、5GiB/10万inode、30秒取消、30天封存作为压测与产品草案起点，根据试点证据调整。它们是待定运维预算，不是已测性能或模型调用默认限流。

上述决定应与API可行性证据一起审定。未经决定及隔离门禁验证，本设计不把“默认所有人共享”标记为已可上线。
