# Auriga DSH 云端共享与独立容器提降权设计

## 1. 结论与基线

调研日期为2026-11-13，初始精确基线 `270ed0d8eca1c8c34e30d21207e6bff304a27b91`；2026-11-14合入 main `8e56e7bd2163ec3ab8ba740faf611167f8a01454`，按用户“全部按推荐”定稿。产品名统一为 Auriga DSH，上游包仍为 `@deepseek-ai/dsh@0.2.1-rc.3`。产品规则已经确定，接口契约仍为待实现草案，共享运行时未实现、未部署。审查等级 A，涉及 contracts，交主控标记【待审查】，须全新非作者完整独审；不据本文开放共享环境。

用户要求默认共用一个容器、一个 DSH 主进程，各有会话与工作区；管理员可以给 UAC 或邀请成员配置独立容器，也可降回共享；切换默认丢弃会话。这些是目标，不是现有能力。**三个候选均不满足直接内置条件；优先借鉴 sunbigfly 的功能分解，但不把它原样装进 Auriga。先完成公开 API 可行性验证，再自研最小适配；共享只面向同一互信团队，不跨租户，文档与管理台必须明示进程内无保密隔离。身份与资源归属验证仍是上线门禁，不以互信代替服务端授权。**

一个 Node 进程的插件共享地址空间和进程权限。会话 ID、工作区 cwd、Cordis scope、0700 目录都不是同 UID 下的安全隔离边界。即使子命令有沙箱，进程内的插件、文件读取服务和凭据缓存仍是共同信任域。禁止用“workspace-write”直接推导“不能读其他工作区”。

依据包括 [统一资源包](resource-package.zh.md)、`contracts/plugin/identity-token.schema.json`、`clients/dsh-plugin/src/managed/{door,session,host-auth,directories,models}.js`、`services/workspace/impl/.../WorkspaceService.java`、`infra/agent/internal/{renderer,runtime,storage,ingress}`、`infra/edge`。其中 workspace 当前按 owner 查询/创建一个物理实例，managed 门禁校验 `sub == ownerId`；它们不能仅把所有 owner 指到同一实例就完成共享。

记忆库《Auriga 工作台产品设计》《DSH宿主兼容与连接模式认证》中云端身份与工作区约束继续成立：先认证后准备/进入；托管插件不显示未登录可用；目录限 workspace；身份凭据失效拒绝执行。连接模式的“可跳过平台登录”不适用于云端托管。登录以已合并的[UAC身份源与外壳登录设计](auth-shell.zh.md)为准，包括第3.1.2节企业应用skill按本人会话读取最新登录材料或代发请求的既定用法。UAC 工号映射到 IAM 主体和租户成员，部门资料不授予模式权限；平台登录token和企业token不交给通用模型上下文，企业材料仅经本稿第5.1节的受控服务按调用读取。认证来源不决定是否有独立容器。

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
| shell、terminal、后台任务、子 Agent | 默认隔离不了 | 共享模式固定禁用shell和terminal；平台有限业务工具单独校验，后台与子Agent继承身份且不能扩大工具集；独立容器命令沿其沙箱规则 |
| MCP 工具 | 共享 stdio/全局凭据隔离不了 | 平台远程 Streamable HTTP MCP 按成员/会话签下游能力，每调用校验；不共享带权限的 MCP 子进程，不开放任意用户添加服务 |
| 环境变量 | 进程内隔离不了 | 不放个人密钥于 process.env，不按请求临时修改它；子进程只取允许表。插件仍能读取全局环境 |
| 模型 Key | 只能在请求路由层隔离 | 上游真 Key 留 AI 网关；主进程只获短时成员能力；并发、流式和后台任务均绑定不可变身份，不能共用 ownerKey |
| 插件配置、原生 user skill | 默认全局，隔离不了 | 固定可信插件集合；共享模式禁止任意插件、个人Key和私有skill；受限个人展示配置用独立数据服务，不改全局 settings |
| 日志 | 能隔离读取权限，进程内不隔离 | 固定归属标签、脱敏、按人导出，管理员诊断走审计；stdout、崩溃栈可能混入多人内容 |
| 内存、GC、插件故障 | 隔离不了 | 无进程级保密或故障隔离；内存耗尽、事件循环阻塞及崩溃影响全池 |

直接问题的回答：**在没有额外强制机制时，用户可以通过对话诱导文件工具或命令读取另一个目录、凭据文件和共享进程可见资源。** 只隐藏工作区按钮或设 cwd 无法阻止。提示词和 AGENTS 规则只能辅助，不算安全门禁。

按用户切系统账号仅适用于独立执行子进程，不能在一个 Node 进程中并发 setuid/seteuid 来切请求身份；父进程仍持全体数据。可行的加强是每会话/成员的执行 broker，以独立 UID、挂载/PID/网络命名空间或沙箱 worker 限本人文件与资源，并在 broker 外保存凭据。这些加强方案只作为后续安全研究，不纳入本次已定共享范围。共享模式固定禁用shell和终端，不向成员开放通用命令执行；文件预览、附件、受管工具与平台企业应用skill仍须验证本人归属。未通过身份和文件门禁的共享构建不得上线；需要任意执行能力的成员使用独立容器。

公开 API 可行性证据：rc.3 上游 [remote-access/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/remote-access/src/index.ts) 导出 registerPolicy、invalidate，非 owner 无策略默认拒绝；[session-controller/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/session-controller/src/index.ts) 已用 remoteAccess.allows 过滤列表、搜索和投影。可用此机制自研 principal/策略，避免原型修改。但这不证明新建、文件与所有 UI 路径已经可用于多用户。

上游 [sandbox/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox/src/index.ts) 明确网络及进程可见性不属于 SandboxMode，定义的 full 是该文件效果策略的执行完整度，不能解释成“租户全隔离”。[sandbox-policy](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox-policy/src/index.ts) 的会话 cwd 决定 workspaceRoot，也是执行策略而非身份。

不适合共享：互不信任的外部客户、跨组织高敏资料、个人/生产密钥、需要任意 shell/MCP/插件/提权的开发、浏览器登录高权限后台、特权网络运维、合规要求进程级保密及故障域隔离的用途。总部系统 DSH 保持独立；它具有跨成员授权，不能加入普通共享池。

## 4. 自研架构与容量

### 4.1 物理池与逻辑分配

拆为 physical pool（一个容器＋一个 DSH 主进程）与 placement（一个租户成员的云端工作环境）。初期一个池，容量满后增加池，每池一个主进程。每人有独立 placementId、epoch、资源选择和会话归属，不以物理实例 owner 冒充所有用户。每池只服务同一互信团队且不跨租户；第8节记录已定规则。

workspace 是唯一分配写者；用户认证后 open 创建/恢复 placement，调度到已有池；池不存在时由 workspace 发规格，agent 拉精确受管制品、配置 guardian、资源视图和网络，再启动。浏览器和插件均不能创建容器。池管理身份是系统主体，不授予普通用户 owner；独立容器仍通过同一编排链创建。旧 `/me/instance/open` 不在本草案偷偷改语义，共享接入前需新增 placement open/票据协议并整体接线。

生命周期为 pending → provisioning → ready → draining → stopped/failed。成员退出仅撤销该浏览器连接，不按人数误停全池；凭据失效的该 placement 必须整体不可用。没有用户且没有任务时才可闲置回收；资源失效按第5节保守停全池。容器崩溃影响池内所有用户，取消在途任务、记录 uncertain 状态，不自动重放产生副作用的命令；恢复持久化历史并验证信任，受污染会话不恢复执行。独立容器只影响单人。

压测起点为每池20名登记用户、4个同时活跃主会话、每人1个活跃主会话；这是**已定的初始调度预算，不是已测性能结论**，不额外裁模型上下文或默认中转 token 限流。子会话和后台执行计入其主会话资源预算。内存预算按 `基线RSS + 活跃数×P95会话增量 + 缓存 + 30%余量` 估算，用真实长上下文、获准的平台工具、WS 混合压测修订。持续超80%内存、队列等待或事件循环延迟超 SLO 时停止接纳并扩新池；已绑定用户保持粘性，不静默迁移在途会话。扩池完成前对新准入排队或拒绝，并返回可重试状态；不自动搬迁在途会话。

### 4.2 身份输入与防串号

edge 先完成门户/管理台 audience 认证、实例 cookie/票据授权，清除外部所有身份头，再签短寿命 `Auriga-Identity`。agent ingress 继续验签转发。shared 模式的 aud 不能只等于物理池：需新增版本的签名声明，绑定 tenant、member、placement、epoch、physicalInstance、method、canonical path、iat/exp/jti；旧单 owner 协议不直接复用。纯IP/HTTP沿既有 ADR0020能力和风险，不放开 Origin/CSRF，也不退回信任任意 header。

插件可信适配层验证签名/kid/iss/aud/有效期、单值 header、方法路径和重放，向 workspace 核对 placement 当前 epoch 与成员有效性，再创建**非 owner principal**。从可信映射取会话/工作区归属，所有自称 userId/role/agentPreset 的参数无效。不能按 displayName、UAC 工号文本或路径前缀充当授权真源。每个 HTTP 请求独立绑定；WS 握手固定身份，逐流/逐方法授权，epoch 改变主动断开、取消重连票据；撤销不能只等 WS 重连。池内禁止普通浏览器直达 DSH owner 端口。

公开 API 验证线必须确认可通过已导出 Peer/Remote 扩展点注入该 principal，正确生成非 owner 页面与 access snapshot；不能把默认 owner index 返回所有用户，再指望过滤侧栏。新建 session/workspace 在归属持久化前不可对外可见，失败销毁；子会话继承且不能改 tenant/member/epoch；远程 delegated 会话沿单独授权而非变更 owner。异步任务保存绑定快照并逐次复核，不依赖当前浏览器的全局“登录用户”。

### 4.3 目录、配额和备份

池数据固定分三类：`/var/lib/auriga/pools/<pool>/host/` 为受信 DSH_HOME/会话索引，`.../members/<tenant>/<member>/<epoch>/workspace/` 为本人文件，`.../control/` 为归属、资源账本和审计。成员只见自己的 workspace；host/control 不作为工具 cwd、skill 扫描根或上传目录。ID 均由服务器生成并校验，无用户名路径。管理存储唯一落盘方仍是 agent。

workspace 内每人初始配额为5 GiB、10万 inode，并执行有界上传/解压预算；这些数值作为压测起点，管理台可在获权范围调整。现有 layout 的 DiskUsageMB/SoftQuotaExceeded 是实例软配额，需新增成员计量及硬限额，否则一人可塞满全池。临时目录、附件、会话历史、下载缓存分别计费/限额，不通过移到 tmp 绕过。备份按成员逻辑归属和一致快照导出，加密、租户授权、可恢复性验证，不能把全池 DSH_HOME 原包交给任一成员。物理全池灾备仅受审计运维可访问。

### 4.4 模型、消息网络与桌面

AI 网关持上游 Key；成员调用获得限 audience、member、placement、epoch、session 的短时下游能力，网关从验证结果归属计费，拒绝模型参数里的自报身份。禁止把现有单实例 model credential 当全池公共 Key，禁止按请求改 process.env 或全局 provider。原生模型目录只共享获准的公开元信息；共享模式固定禁止个人 Key 和个人 provider 凭据，成员通过平台中转能力按本人归属调用。独立容器按原有个人配置能力运行，不将其配置自动复制到共享池。流式取消、失败重试、计量去重以调用ID关联；auto 池内路由不因此增加模型参数限制。

消息网络的物理连接仍以 DSH/插件为联网主体，不能每个历史会话心跳；共享主进程复用稳定物理传输，但逻辑 capability、邮箱、授权、背压与去重按 tenant/member/placement/epoch/session 分区。不能沿旧的一个 owner/device credential 广播给全池。桌面 DSH 和云端互通仍走平台远程 Team MCP 与既有消息网络授权，每个消息校验发送者、目标、父子关系及代次；提降权撤销旧地址，旧积压进入过期/不可执行状态，不重放到新会话。Desktop 本地身份与数据不因云端切换而迁移，给出云端历史丢弃提示即可。

## 5. 统一资源包与共享企业应用 skill

共享skill沿[统一资源包规范](resource-package.zh.md)的审核、签名、租约、撤销及唯一落盘规则。共享模式只装平台统一下发且全池成员有权使用的只读基础skill与企业应用skill。禁止成员安装任意插件、个人Key、私有skill、成员自行选择或共享skill/MCP、shell和终端；需要这些能力的成员提权到独立容器。此范围是已定产品规则，不因未来发现扩展点而自动放开。

共享skill使用独立固定customSkillDirs活跃根，由agent唯一写入并保留上游预置目录；公共制品缓存可按摘要复用，选择/租约/安装回报仍绑定placement。平台只把全池共同获权的内容加入池的活跃根；不把某成员的企业资料或凭据写入正文。平台资源授权不一致时按授权集合拆池或拒绝对不匹配成员准入，不能给全池取权限并集。身份切换销毁该成员旧会话范围、增加epoch、重新授权，不切换全局扫描目录。

统一包的私有skill原生user根在共享模式关闭；不能把多人私有内容放进同一个user根，也不能轮流把全局固定根切到当前点击用户。独立容器及Desktop继续沿既定私有skill规则。签名只证明制品可信，不能隔离同进程插件或内存。

guardian按物理池执行：一份已加载资源到期或恶意撤销，保守停全池及其执行树；管理台和文档明确提示会中断其他成员任务。清空失效活跃视图、重验租约与撤销清单后才重启，受污染会话不可继续执行，历史只按归属只读查看。单成员凭据失效撤销本人全部能力、会话及连接；不能证明范围已停止时按安全策略停池。恢复不得复活旧epoch。

共享池不提供离线私有skill启动分支；全局共享根清理或残留执行无法确认时拒绝启动。独立容器与Desktop的离线私有模式仍以其guardian规则执行。共享模式的资源停机故障域就是全池，不另造每用户DSH主进程。

### 5.1 现场企业应用 skill 与凭据作用域

现场已有“用skill操作内部应用”的用法，按[登录设计第3.1.2节](auth-shell.zh.md#312-向dsh提供企业登录信息)承接。共享模式中的这些skill只能由平台统一审核、签名和下发，走统一资源包；成员不能把自写skill、MCP端点或脚本装进共享池。text-only skill只承载调用说明，不夹带可执行Python文件；既有helper和handler属于受信插件/工具制品，单独审核和固定版本。登录设计中的request()、resolve(apply_env_aliases=False)兼容调用由受控helper适配，不开放用户传入任意命令、代码或模块。

共享环境由平台注册的有限业务工具调用企业登录服务，不通过开放shell运行skill。每次调用从可信宿主会话取得tenant/member/placement/epoch/sessionId及任务绑定，不接受模型参数自报身份；子会话继承归属并另发能力。handler按已绑定成员的平台会话读取IAM权威最新企业材料，不复用全池owner/device凭据、不拿当前点击窗口作为全局用户。SSO Cookie可脚本读取是登录设计已接受的单IP/单域名取舍，本文不声称共享进程或同源脚本能对互信成员提供凭据保密。

保留登录设计的兼容端点 POST /dsh-team/outbound 与 GET/POST /dsh-team/agent-credentials；loopback对端校验只是第一层，不能区分共享池内的用户。handler还必须验证任务能力的audience、600秒有效期、credentials/outbound scope、撤销状态、成员身份、认证代次、DSH会话owner和placement epoch；会话ID和能力必须相互绑定，跨成员、跨会话、过期、旧代次及伪造请求拒绝。shared补充围栏先由K线冻结为只增契约，不静默改变独立/桌面现有helper协议。

企业凭据以请求级不可变上下文传给handler；await、流式回调、并发与后台任务都保持同一绑定并逐次校验有效性。禁止写process.env、全局shellEnv、全局provider配置或“当前用户”单例。DSH_SESSION_ID、DSH_TEAM_TASK_CAPABILITY、DSH_TEAM_BASE等兼容变量仅存在于受控任务的独立env对象中，不修改主进程环境，也不向成员开放通用shell/终端。任务结束、退出、切换或撤销立即释放能力及缓存；缓存键至少含tenant/member/session/epoch/credentialVersion，不能跨成员共享token缓存。复验轮换后下一次调用读取最新版本，晚响应匹配旧代次时丢弃。

默认优先agent-outbound代发，由handler重建本人认证头，目标限定部署outboundAllowedHosts，空表拒绝；响应过滤token/Cookie。沿登录设计保留allowRawAgentToken默认true及可即时关闭的开关，关闭时下一次原始读取拒绝且只发outbound scope；raw开启时仅具credentials scope的本人受控helper可在内存读取empNo/token/updatedAt，禁止把凭据作为模型工具结果。原始材料不落工作区、skill正文、会话、静态配置、日志或stdout/stderr，工具只返回业务结果或安全状态。原始读取的进程内可见风险继续明确披露，不将任务授权表述为内存保密隔离。

验收使用平台下发的同一示例skill并发操作两个成员各自的内部应用替身：分别看到本人业务结果，A能力带B会话拒绝，A/B轮换与迟到回调不串材料；raw开关、出站域名允许表、注销/复验失败/提降权后的旧能力拒绝；模型输出、日志、会话和工作区均无凭据。公开API无法提供受控业务工具或完整身份绑定时该构建不开放企业应用skill，交主控按spike证据处理接线缺口，不能改用全局环境或开放shell绕过。

## 6. 管理台提权与降权

### 6.1 管理操作和权限

成员详情展示当前模式、状态、独立配额、切换结果。提权按钮是“分配独立容器”，不授予 Docker/root 权限；降权是“改为共享”。权限新增 `workspace.placements.read/write`，仅 ctx=admin、该租户授权管理员，step-up 采用既有 IAM 近期凭据策略；UAC 管理员无可用 MFA 时需已有可执行的管理员 step-up 方案，否则拒绝，不把扫码自动等同 step-up。

确认面板列出会话永久丢弃、运行任务将取消、文件策略、预计影响，要求填写原因；请求 If-Match 防并发、Idempotency-Key 防重复。审计记录操作者/租户/目标成员/原目标模式/operation/修订/epoch/step-up引用/结果与分类错误，不记录会话正文或 token。跨租户对象404，普通门户及插件不能调用管理接口。独立容器 entitlement 与实际 mode 分开：只有切换成功才实际生效；从不以 UAC/团队身份来源隐式获得特权。

### 6.2 双向状态机和故障处理

两个方向使用同一操作状态机：accepted → fencing → draining → preparing → committing → succeeded；失败进入 failed，停止不确认或恢复歧义进入 blocked。from/target 必须不同；同一成员仅一个在途操作，操作状态与期望模式事务持久化，节点租约和 CAS 防重复执行。

1. accepted 检查授权、成员有效性、配额与容量，预留目的端资源；失败尚未进入围栏时保留原服务和会话，审计失败。
2. fencing 标 switching，递增并持久化 epoch，撤销旧 open 票据、浏览器/网络/模型/MCP能力，阻止新 prompt/附件/后台任务和旧连接继续调用。用户看到“正在切换，将关闭当前会话”。
3. draining 取消本人主/子会话、终端、后台任务与外发调用，取消等待上限为30秒，作为压测起点；超时由受信执行器杀进程树。共享 → 独立不得为正常单人切换先杀其他人的任务；若无法确认本人执行隔离，转 blocked 并按安全策略停池，不冒成功。
4. 证明静止后销毁本人旧会话数据、索引、附件会话引用和消息路由；不迁移历史/上下文，设置 sessionsDiscarded=true。安全审计按保留政策独立保存，不可从门户恢复会话。会话删除失败不能推进提交。
5. preparing：共享 → 独立创建新容器/新 DSH_HOME/新工作区、同一 Auriga 插件并验证门禁；独立 → 共享预留 placement、清理旧容器运行能力并准备空工作区。工作区策略见下一节。旧独立容器不再可启动，但在回滚窗内保留受控快照。
6. committing 用旧 revision、operation、当前 epoch CAS 更新唯一绑定；旧执行必须已不可用，再发新能力。通过正常门户/DSH 页面与会话门禁探测后 succeeded，发布 outbox 事件，释放源配额和清理源制品。202 仅表示登记，不表示可验收。

回滚不恢复已丢弃会话。目的端失败时关闭新能力/临时容器，保持更高 epoch，在原模式重新创建**空会话**并重新签票；回滚同样使用空工作区，封存快照保留30天；文件恢复只走本人显式申请的审计流程，不自动回灌旧执行配置。若无法证实源已静止、绑定提交结果或权限被撤销，保持 unavailable/blocked，重试持久化 reconcile，不能让源和目标同时可执行。重启按 operation 与代次恢复，旧 ACK/晚事件不能覆盖新状态。用户看到具体分类和重试状态，不能在失败时无提示跳回仍活着的旧会话。

### 6.3 文件、旧用户与保留策略

**文件和会话处置已确定：本人文件封存30天，新模式使用空工作区，会话始终丢弃。** 封存快照由管理员按审计流程导出；恢复文件须用户显式申请、内容扫描并限本人 workspace，不恢复 .dsh/.env/模型 Key/插件运行态。理由是文件价值高于临时会话，同时不把旧模式的执行配置自动带进共享信任域。备份空间、到期删除和最终删除失败告警要验收；快照不允许通过普通新会话自动读取。

现有独立容器用户不做双运行兼容或后台会话迁移。先通知维护窗口及导出/封存窗口，管理员登记“继续独立”名单；维护窗口内批量重建，其余执行同一降权流程创建共享 placement，会话丢弃、本人文件封存30天，新模式空目录。不把所有现有用户无条件永久授予独立 entitlement，也不在首次登录时暗中销毁工作区。

## 7. 契约草案、工作线与验收

### 7.1 本轮只增契约

新增 `contracts/openapi/shared-dsh.yaml`，通过 workspace.yaml 新增三个 path 引用接入生成链，原路径、schema、required 和语义不改。包括 `GET /me/placement`、`POST /admin/members/{memberId}/placement-switches`、`GET /admin/placement-switches/{operationId}`，DshInstanceMode 为 shared/dedicated，DshPlacement 含 placement/member/epoch/revision/state，切换固定接受 discard 与 archive-and-reset：会话丢弃，文件封存30天，新模式空目录；接口仍待实现，不改变已定产品规则。

新增 `contracts/proto/auriga/node/v1/shared_dsh.proto` 只定义 mode/binding，不擅自占用既有 NodeControl 的 oneof 或给现有 InstanceSpec 换含义；Java/Go 同源生成。新增 `contracts/events/placement.schema.json` 定义 workspace.placement_switched 和 workspace.placement_switch_failed 的结果负载。沿 CloudEvents 信封取 tenant，workspace 生产，subject=placementId，operationId 幂等，revision/epoch 围栏；成功必须 sessionsDiscarded=true 且无 failureCode，失败带分类。fromMode!=targetMode、当前绑定一致、成员归属由生产方事务验证。消费者登记、权限目录、NodeControl 接线属于后续实现，未接线不得发送新事件。

本轮契约是可生成的控制面草案，不冒充完整共享数据面协议。实现前 K 线仍须冻结 placement open/票据、shared身份签名版本、资源视图选择及回报、模型/消息能力细分、guardian影响范围和成员撤销；缺一不启用共享。旧实例接口不提供部分兼容假象。三套生成器、OpenAPI lint、strict schema、契约正负例及 Java 契约校验结果详见工位报告；通过生成不等于隔离验证。

### 7.2 可直接派工的工作线

依赖顺序为 SPIKE → K → W/E/R/P/N/U → V；K冻结后W、E、R、P、N、U按下述独占文件边界并行，正常入口集成按E→R→P→W/U串行验证。任何公共契约变化回K处理。每条任务附精确base、依赖契约版本、只改路径和验证命令；工人提交产物，由主控串行部署与验收，禁止并行改NAS。

#### 0. SPIKE：公开 API 可行性验证（先行门禁）

- 范围：已由主控派发的 auriga-SHARED-DSH-SPIKE 并行执行，本设计线不重复试装。验证非owner PrincipalMap/Peer、remoteAccess策略、创建归属、列表/事件/文件过滤、禁用shell/terminal/任意插件，以及平台企业应用skill的有限业务工具调用。
- 文件边界：SPIKE自己的隔离工位、独立rc.3 DSH_HOME、随机端口与证据报告，不修改本设计线文档或共享工位运行状态；不得改DSH、prototype或解析内部文件。
- 依赖：本稿已定功能边界和已合并auth-shell第3.1.2节。结果由主控交回本设计线后吸收；当前不宣称其已通过，也不因工位存在推断结论。
- 验收：完整命令、宿主精确版本、正常双成员操作与串会话负例、API导出证据、首失和功能缺口清单；明确平台统一skill的执行入口及凭据作用域是否可实现。缺口不能用全局身份/环境变量或通用shell绕过。预计3–5工程日，实际由证据决定。

#### K：共享控制面与数据面契约

- 范围：冻结placement open/票据、共享身份头版本、成员撤销与epoch、平台skill选择/资源回报、企业任务能力围栏、模型/消息能力、guardian全池影响；补权限、事件生产映射、错误码与契约向量。
- 文件边界：独占 contracts/**、对应web/shared/src/generated/**和infra/gen/**生成产物；只改必要生成脚本。Java生成物仍进target。其他线不手写或修改生成类型。
- 依赖：SPIKE结论经主控吸收；复用auth-shell既有IAM企业凭据接口，不另建登录生命周期。当前三条placement接口仅控制面起点。
- 验收：只增检查、三套生成器、lint、strict schema与契约正负例全通过，包含跨成员/session/epoch、600秒期限和撤销、30天封存/空目录/会话丢弃、未知模式拒绝。形成可被后续任务直接引用的精确契约HEAD。预计3–5日。

#### W：分配、容量与提降权编排

- 范围：pool/placement持久化、租户准入、固定初始预算、模式切换、预留、围栏、取消/封存/重建、幂等/CAS/outbox与崩溃恢复；维护窗口旧用户名单及批量进度。
- 文件边界：独占 services/workspace/api/** 和 services/workspace/impl/**，通过IAM/resource公共端口取权威输入；不改其他服务表或infra。数据库只新增迁移。
- 依赖：K，运行收据接R、入口凭据接E；R未就绪时只做持久化/替身验证，不冒真实ready。
- 验收：真实临时PG验证同租户池、跨租户拒绝、20登记/4活跃/每人1活跃准入、满载扩池、同成员并发切换拒绝、任务30秒取消、封存30天新目录、旧会话不恢复、故障重启与晚ACK无双活。预计6–9日。

#### E：edge与节点入口身份

- 范围：placement票据/实例cookie与签名身份声明，绑定成员和epoch，逐HTTP/WS验证、主动撤销连接；域名和纯IP沿当前已合并单源部署规则执行。
- 文件边界：独占 infra/edge/** 和 infra/agent/internal/ingress/**；不改renderer/runtime/storage或clients/dsh-plugin。需要新增共享类型只向K申请。
- 依赖：K；从W公共端口读取权威分配，用P真实插件验证非owner入口。
- 验收：伪造、多值header、错aud/方法/路径、重放、过期、跨成员、旧epoch、WS续用全部拒绝；门户正常/open与共享票据只到本人placement，原始owner端口不可直达。预计5–8日。

#### R：池运行、目录与统一资源guardian

- 范围：每池一个主进程、成员目录与硬配额、封存和到期删除、平台统一skill固定扫描根、可信插件集合、shell/terminal禁用、资源失效停全池、受污染会话停止恢复。
- 文件边界：独占 infra/agent/internal/renderer/**、runtime/**、storage/**、observer/**及该线必要节点执行配置，不改ingress或插件；与W以契约交接目录和收据，不共同落盘。
- 依赖：K；身份入口接E、调度与封存意图接W、宿主功能关闭和工具注册接P。
- 验收：一个池一主进程、不跨租户；成员文件/附件不能越根；每人5GiB/10万inode起点；禁止个人目录扫描与任意安装；任一资源到期实际停全池并显示影响、清理不确认拒绝启动、封存快照不向普通新会话自动挂载。预计6–10日。

#### P：共享宿主、企业skill与凭据适配

- 范围：托管身份由单owner改为非owner会话上下文；平台统一skill有限工具入口，按auth-shell迁入既有两handler/helper契约，逐调用成员绑定、企业材料轮换和raw开关；资源视图、受限设置和原生DSH提示。
- 文件边界：独占 clients/dsh-plugin/src/shared/**（新增适配目录）、src/managed/**及必要宿主注册/打包文件；与auth-shell插件工作线串行接收精确基线，不并行编辑其handler和shellEnv。不改IAM或AI/network服务实现。
- 依赖：SPIKE、K、已合并登录设计及其可用handler实现；E提供可信身份，R提供平台资源视图，N提供模型与消息下游能力。
- 验收：双成员正常页面、双企业应用替身；跨成员/会话任务能力拒绝，轮换后新材料、迟到回调丢弃、退出旧能力失效；raw默认开与关闭后的下一次拒绝；所有路径不写process.env或全局当前用户，模型/日志/文件无凭据；无shell/terminal/个人Key/私有skill/成员自建MCP入口。预计8–12日。

#### N：平台模型与消息网络归属

- 范围：每成员/placement/epoch/session的模型短时能力、计量与流式取消；共享物理连接上的逻辑邮箱、授权、背压和去重；云端/桌面互通及切换旧积压不可执行。
- 文件边界：独占 infra/ai-gateway/**（模型与internal/network）及services/collab/**内必要授权适配；需要IAM现有能力接口改动交K并由IAM原工作线串行接线，不自建并行token存储。不改插件或workspace实现。
- 依赖：K、W权威分配；P消费能力，消息协议沿main已合并版本，不能降回旧owner广播。
- 验收：两个成员并发模型请求分别计量，伪造归属和旧epoch拒绝；同池复用连接但不互收私有流；桌面与云端明确授权收发、切换后旧队列不进入新会话，失败不自动重放有副作用的命令。预计5–8日。

#### U：管理台与普通入口可见流程

- 范围：成员模式与独立授权、step-up、影响确认、进度/失败/重试、批量重建名单、容量与共享限制提示，品牌Auriga DSH及中英文；企业skill只显示平台统一资源来源。
- 文件边界：独占 web/admin/src/views/**内对应成员/实例页面及其i18n/测试；门户placement正常入口接线归web/portal对应页面，由本线独占。web/shared生成文件只归K，共用UI文件先报主控串行分配。
- 依赖：K可先做组件，W/E就绪后做正常入口；显示R的全池影响及W的审计/进度。
- 验收：UAC/邀请成员同规则、无step-up拒绝、202不冒成功、文件封存30天/新空目录/会话丢弃确认、进程内无保密隔离与资源到期中断其他成员提示；普通用户不能调用管理接口，页面可观察真实新会话。预计3–5日。

#### V：集成、压测与独审

- 范围：上述产物合成发布候选，验证完整正常入口、两成员/两租户负例、企业应用skill、池故障、提降权中断、维护重建和容量起点，审查授权与凭据影响面。
- 文件边界：独占 e2e/**对应测试、公开验收说明与本任务审查报告；发现实现问题回原线修复，不由测试线并行改业务文件。首轮完整独审必须全新非作者，会话与返工续审沿主控规则。
- 依赖：K至U可部署产物；真实PG/DSH全链由主控串行占唯一槽，工人不碰NAS。开发部署按可独立验收改动经必要测试后小批进行，不等全阶段收官或先用完整CI阻挡每次试用部署；合并仍守独审与精确HEAD完整CI。
- 验收：正常门户进入本人Auriga DSH、企业skill操作本人应用、两向切换、新空目录、会话不可复活；资源到期停全池、进程崩溃影响范围、80%内存停止准入及扩池；披露压测实值，禁止把20/4预算当实测承载量。预计5–8日。

已定范围粗估44–80工程日，允许按文件边界并行；该估算不含上游新增API等待，实际受SPIKE证据约束。全功能不可信多租户、通用执行沙箱和私有skill不在本轮工作线，不能在任务中顺手放开。

发布门禁：A不能列出/读/改/订阅B的会话、附件、搜索、skill调用记录及模型能力；绝对路径/链接/硬链接竞态/proc/env读取拒绝；shell、terminal、任意插件/个人Key/私有skill/自建MCP按固定规则禁用；平台企业应用skill的凭据按第5.1节并发与撤销负例验证；未知协议默认拒绝；资源撤销/到期确实停全池；取消、切换、重启及晚ACK无双活；真实浏览器正常入口和影响提示均可见。没有真实宿主证据只能报代码/设计待验证。

## 8. 已定决定

用户于2026-11-14对七项建议明确“全部按推荐”，以下为实施规则，不再作为待选方案。

1. **共享信任范围**：只面向同一互信团队，文档与管理台明示进程内无保密隔离；不承载不可信多租户，服务端会话和文件归属仍须完整验证。
2. **容量与租户**：初期一个池，容量满后加池，每池一个DSH主进程，不跨租户；扩池不静默搬迁在途会话，避免扩大数据与故障影响面。
3. **共享功能边界**：不开放任意插件、个人Key、私有skill、成员自行共享skill/MCP、shell和终端；有这些需要的成员提权到独立容器。平台统一下发的企业应用skill按第5.1节受控调用，保持既有业务用法与成员归属。
4. **guardian**：一份资源到期保守停全池，并明确会中断其他成员任务；通过统一故障域确保失效内容不继续执行。
5. **切换处置**：提权与降权均封存本人文件30天，新模式空目录，会话始终丢弃；避免自动携带旧执行配置，同时提供受审计的数据取回窗口。
6. **旧用户**：在维护窗口内批量重建，登记“继续独立”名单，先给导出/封存窗口，不做历史会话迁移；不在首次登录时静默销毁数据。
7. **压测起点**：每池20登记/4活跃/每人1活跃、每人5GiB/10万inode、30秒取消、30天封存。以上是已定初始预算，性能上限须由压测证明，数值不代表模型调用默认限流或已验证容量。

设计规则已经定稿；运行时上线仍须通过第7节公开API验证、完整独审与真实正常入口验收，不因“全部按推荐”跳过工程门禁。
