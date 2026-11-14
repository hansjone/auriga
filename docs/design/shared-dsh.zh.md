# Auriga DSH 云端共享与独立容器提降权设计

## 1. 结论与基线

调研日期为2026-11-13，初始精确基线 `270ed0d8eca1c8c34e30d21207e6bff304a27b91`。2026-11-14返工合入 main `0180ff3675ed3bae851e781bebc7a44fe110d1ba`，依据用户已定决定及独审意见定稿。产品名统一为 Auriga DSH，上游包为 `@deepseek-ai/dsh@0.2.1-rc.3`。产品规则已定，接口契约为待实现草案，共享运行时待实现和部署。审查等级 A，涉及 contracts，交主控标记【待审查】，首轮须全新非作者完整独审，同PR返工由原审者续审。

用户要求默认共用一个容器、一个 DSH 主进程，各有会话与工作区；管理员可以给 UAC 或邀请成员配置独立容器，也可降回共享；切换默认丢弃会话。这些是目标，不是现有能力。**三个候选均不满足直接内置条件；优先借鉴 sunbigfly 的功能分解，但不把它原样装进 Auriga。公开 API 验证已完成并发现原生 owner 旁路、跨成员文件读取和全池故障风险；用户于2026-11-14决定“暂时接受存在共享的可能吧，先优先做完，等以后再解决这个问题”。按第3.1节接受范围继续自研首期最小适配，不等待上游补齐隔离能力。共享只面向同一互信团队，不跨租户；对外文档和管理台必须明示成员可能互相看到文件，并可能以 owner 能力操作。**

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

以下区分首期能力与后续隔离目标，不代表基线已有。进程自身和受信插件始终可访问全池状态；原生 owner 入口会绕过非 owner 成员策略，首期接受范围见第3.1节。

| 对象 | 同进程原生边界判定 | 需要的措施及不能承诺的部分 |
|---|---|---|
| 会话、子会话、工作区元数据 | 非 owner 载体及归属全链接线后能隔离；原生 owner 旁路首期接受 | 可信归属表和 remoteAccess 每请求/事件授权；列表、搜索、历史、附件、派生、删除、通知逐项核验，不能只改侧栏；不承诺原生浏览器具备成员保密边界 |
| 工作区目录 | 只能靠约定 | cwd 与用户 ID 分目录，不等于 OS 阻止同 UID 读取；目录映射禁止浏览器指定 |
| 文件读写、上传下载、搜索 | 隔离不了，已复现本人会话读取他人文件，首期接受 | 首期服务器固定目录映射并控制创建入口，不声称限制 cwd 即限制读取；后续受限 fs/broker 覆盖预览、下载、链接、竞态、proc 和工具后端 |
| shell、terminal、后台任务、子 Agent | 默认隔离不了；首期产品不开放，不能保证 owner 旁路均被封住 | 首期 ToolRuntime tools.guard 硬拒 shell/terminal 等执行工具，移除相应宿主能力并默认拒未知方法；guard 不覆盖原生 terminal/设置/MCP 等控制器，原生 owner 旁路按第3.1节披露 |
| MCP 工具 | 共享 stdio/全局凭据隔离不了 | 平台远程 Streamable HTTP MCP 按成员/会话签下游能力，每调用校验；不共享带权限的 MCP 子进程，不开放任意用户添加服务 |
| 环境变量 | 进程内隔离不了 | 不放个人密钥于 process.env，不按请求临时修改它；子进程只取允许表。插件仍能读取全局环境 |
| 模型 Key | 只能在请求路由层隔离 | 上游真 Key 留 AI 网关；主进程只获短时成员能力；并发、流式和后台任务均绑定不可变身份，不能共用 ownerKey |
| 插件配置、原生 user skill | 默认全局，隔离不了 | 固定可信插件集合；共享模式禁止任意插件、个人Key和私有skill；受限个人展示配置用独立数据服务，不改全局 settings |
| 日志 | 能隔离读取权限，进程内不隔离 | 固定归属标签、脱敏、按人导出，管理员诊断走审计；stdout、崩溃栈可能混入多人内容 |
| 内存、GC、插件故障 | 隔离不了 | 无进程级保密或故障隔离；内存耗尽、事件循环阻塞及崩溃影响全池 |

直接问题的回答：**在没有额外强制机制时，用户可以通过对话诱导文件工具或命令读取另一个目录、凭据文件和共享进程可见资源。** 只隐藏工作区按钮或设 cwd 无法阻止。提示词和 AGENTS 规则只能辅助，不算安全门禁。

按用户切系统账号仅适用于独立执行子进程，不能在一个 Node 进程中并发 setuid/seteuid 来切请求身份；父进程仍持全体数据。后续加强是每会话/成员的执行 broker，以独立 UID、挂载/PID/网络命名空间或沙箱 worker 限本人文件与资源，并在 broker 外保存凭据。这些加强不纳入首期依赖。首期不开放通用命令执行，先做公开机制可实现的硬拒绝和归属检查；原生身份与文件隔离缺口按第3.1节接受并披露，不再作为延期首期的条件。需要主动使用任意执行能力的成员使用独立容器。

公开 API 可行性证据：rc.3 上游 [remote-access/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/remote-access/src/index.ts) 导出 registerPolicy、invalidate，非 owner 无策略默认拒绝；[session-controller/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/api/session-controller/src/index.ts) 已用 remoteAccess.allows 过滤列表、搜索和投影。可用此机制自研 principal/策略，避免原型修改。但这不证明新建、文件与所有 UI 路径已经可用于多用户。

上游 [sandbox/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox/src/index.ts) 明确网络及进程可见性不属于 SandboxMode，定义的 full 是该文件效果策略的执行完整度，不能解释成“租户全隔离”。[sandbox-policy](https://github.com/deepseek-ai/deepseek-harness/blob/1f81c047fc48fa190a683e469daa699857e9afe8/packages/sandbox/sandbox-policy/src/index.ts) 的会话 cwd 决定 workspaceRoot，也是执行策略而非身份。

不适合共享：互不信任的外部客户、跨组织高敏资料、个人/生产密钥、需要任意 shell/MCP/插件/提权的开发、浏览器登录高权限后台、特权网络运维、合规要求进程级保密及故障域隔离的用途。总部系统 DSH 保持独立；它具有跨成员授权，不能加入普通共享池。

### 3.1 已知风险（首期接受）

用户已明确接受以下共享风险，优先完成首期。对外使用文档、管理台成员模式页及降权确认面板必须展示以下文案。

**“共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。”**

首期接受以下四项主要风险。

1. **原生身份回落为 owner**：SPIKE 已复现原生 Cookie/browserPeer 将不同成员映射到同一 owner；`/connect` operator 构造 owner 另有源码证据。edge 签名成员头或插件调用 createPeer 不会自动替换原生 Cookie、index snapshot 和 WS 身份。成员策略因此可能被旁路，含禁 shell 等限制；首期不能宣称所有入口均为非 owner。后续采用受控非 owner HTTP/WS/index 载体，或等待上游公开准入、撤销接口，不覆写私有方法。
2. **原生文件可跨成员读取**：已授权本人 session 后，`workspaceFiles/read`、`readBytes`、`stat` 仍接受其他成员文件路径。绝对路径、父目录、Windows junction、硬链接反例已复现；stat 也会泄露元数据。首期接受互看文件的可能性，保留服务器目录映射与可实现的入口限制；后续以受限 fs/broker 覆盖全部文件入口及工具后端，不能用单次 realpath 检查宣称解决硬链接或竞态。
3. **全池故障与取消范围**：同步阻塞拖住全部会话和采样，进程崩溃影响所有成员，进程内无法选择性硬杀本人执行。首期只承诺合作取消；30秒等待超时由外部 supervisor 保守停止整个池及其执行树，明确中断其他人任务，未确认停止则保持 blocked。后续外部成员执行域才承担选择性硬取消；不承诺首期在30秒内完成停池。
4. **他人会话可见、企业身份冒用与审计错归**。成员 A 可经原生 owner 入口打开并驱动 B 的会话，读取 B 的会话正文和历史。企业 skill 按会话归属绑定 B 的凭据时，A 发出的指令可能以 B 的企业身份操作内部应用，平台和业务侧审计会记在 B 名下。会话能力校验仍可通过，保留会话绑定也会保留这一冒用路径。此项由独审指出，用户明确决定“也接受，写进已知风险”，纳入首期接受范围，并在管理台及对外文档披露。P/V线保留 A 驱动 B 会话、企业应用替身按 B 执行且审计归 B 的反例。后续受控非 owner 载体须同时绑定真实发起成员与会话所属人，并拒绝不匹配的企业调用。

SPIKE“原生入口隔离缺口清单”逐条处理如下，均为首期接受的剩余限制；已能做到的拒绝仍纳入首期，不以接受风险为由删除校验：

1. **已复现／源码证据，首期接受**：Cookie/browserPeer 的 owner 旁路已复现，`/connect` owner 构造经源码确认；处理及后续载体方向见主要风险①。
2. **已复现，首期接受**：`workspaceFiles/read/readBytes/stat` 可跨路径，字节下载同受影响；处理及后续 fs/broker 方向见主要风险②。
3. **源码风险，首期接受**：`workspaceFiles/changes` 文件观测没有成员根限制；目录 list/changes 有目录范围检查，不能混为同一结论。后续观测与下载一起进入受限文件服务。
4. **源码风险，首期接受**：`session/create` 的 cwd、`workspace/create` 的 path、directoryPicker/list/createDirectory 和 working-directory 工具需服务器控制。首期拒绝成员任意指定目录和原生创建/改 cwd，改用服务器固定映射并核对 workspaceId；这不形成 OS 文件隔离。
5. **源码风险，首期接受**：read/read_image 依赖 ctx.fs，glob/grep 存在搜索子进程路径，文件引用候选入口待复核。首期审核工具允许表，未审核执行工具默认拒绝；后续统一限制 fs 与搜索后端，不能以封 shell 推导这些入口均安全。
6. **能力缺口，首期接受，未证实越权**：`/api/session/uploadFileBinary` 是 owner-only Fetch，非 owner 默认403；给成员发 owner Cookie 又可能允许请求指定 sessionId。首期保留403，不为共享删掉门禁；Remote `fileUploads/upload` 单独核验。后续补成员上传载体及归属授权，不把未验证的成员上传标为可用。
7. **已有授权接线但未完整实测，首期接受**：session page/attachment/follow、内容搜索、workspace baseline、session通知已有授权检查，没有证据认定正确非 owner 载体下必然泄漏；owner 旁路、错误归属或漏 action 会破坏前提。首期建立可信归属、父子继承与发布前登记，覆盖持久恢复及撤销测试；未知 action 拒绝，不把局部通过写成全链隔离。
8. **源码风险，首期接受**：terminal attach/retain/close、settings/plugin、MCP stdio、jobs/subagent 位于 ToolRuntime guard 之外。首期宿主 profile 移除对应能力和动态安装，明确方法允许表、未知方法一律拒绝；`tools.guard` 对 shell/terminal 等执行工具单调硬拒绝，pre-execute 放行也不能覆盖。原生 owner 旁路仍按风险①披露，UI隐藏不算硬拒绝；后续受控载体覆盖完整端点清单。

证据来自 `auriga-SHARED-DSH-SPIKE` 提交 `7e7ea403` 的 `spike/shared-dsh/` 与主控留存 `.agents/runs/auriga-SHARED-DSH-SPIKE/`：identity.json、public-api.json、endpoints.json、upstream-manifest.json、load-normal/throw/block/crash.json 及对应源码摘录。该提交收证时尚未推送。验证为 Windows Node 22.19.0、锁定 rc.3 的最小公开服务宿主；8项 runner 结果符合预期（崩溃子项预期退出23），不是完整 Web/WS、Linux容器、真实 IAM/SSO/helper 或真实模型验收。4会话短流约56→60MiB仅为小样本，不能外推20/4容量；Linux proc、挂载、链接竞态、完整上传附件、提示注入、guardian与长上下文尚未完成实测。合作取消及外部终止测试树不证明选择性硬取消。

上游原生准入/撤销、可替换受限文件后端与外部成员执行域列为后续改进，不推迟第7节首期派工。首期仍验证已有控制面的租户准入、签名、防伪、能力归属和撤销，以及可实现的硬拒绝与风险提示；本节接受不自动扩展到新发现的其他越权或凭据泄露。

## 4. 自研架构与容量

### 4.1 物理池与逻辑分配

拆为 physical pool（一个容器＋一个 DSH 主进程）与 placement（一个租户成员的云端工作环境）。初期一个池，容量满后增加池，每池一个主进程。每人有独立 placementId、epoch、资源选择和会话归属，不以物理实例 owner 冒充所有用户。每池只服务同一互信团队且不跨租户；第8节记录已定规则。

workspace 是唯一分配写者；用户认证后 open 创建/恢复 placement，调度到已有池；池不存在时由 workspace 发规格，agent 拉精确受管制品、配置 guardian、资源视图和网络，再启动。浏览器和插件均不能创建容器。池管理身份是系统主体，平台不主动授予普通成员 owner；原生 DSH Cookie 回落 owner 的已知旁路按第3.1节首期接受，不能声称因此不存在 owner 能力。独立容器仍通过同一编排链创建。旧 `/me/instance/open` 不在本草案偷偷改语义，共享接入前需新增 placement open/票据协议并整体接线。

生命周期为 pending → provisioning → ready → draining → stopped/failed。成员退出仅撤销该浏览器连接，不按人数误停全池；凭据失效的该 placement 必须整体不可用。没有用户且没有任务时才可闲置回收；资源失效按第5节保守停全池。容器崩溃影响池内所有用户，取消在途任务、记录 uncertain 状态，不自动重放产生副作用的命令；恢复持久化历史并验证信任，受污染会话不恢复执行。独立容器只影响单人。

压测起点为每池20名登记用户、4个同时活跃主会话、每人1个活跃主会话；这是**已定的初始调度预算，不是已测性能结论**，不额外裁模型上下文或默认中转 token 限流。子会话和后台执行计入其主会话资源预算。内存预算按 `基线RSS + 活跃数×P95会话增量 + 缓存 + 30%余量` 估算，用真实长上下文、获准的平台工具、WS 混合压测修订。持续超80%内存、队列等待或事件循环延迟超 SLO 时停止接纳并扩新池；已绑定用户保持粘性，不静默迁移在途会话。扩池完成前对新准入排队或拒绝，并返回可重试状态；不自动搬迁在途会话。

### 4.2 身份输入与防串号

edge 先完成门户/管理台 audience 认证、实例 cookie/票据授权，清除外部所有身份头，再签短寿命 `Auriga-Identity`。agent ingress 继续验签转发。shared 模式的 aud 不能只等于物理池：需新增版本的签名声明，绑定 tenant、member、placement、epoch、physicalInstance、method、canonical path、iat/exp/jti；旧单 owner 协议不直接复用。纯IP/HTTP沿既有 ADR0020能力和风险，不放开 Origin/CSRF，也不退回信任任意 header。

首期可信适配层验证签名/kid/iss/aud/有效期、单值 header、方法路径和重放，向 workspace 核对 placement 当前 epoch 与成员有效性。在自有受控调用路径通过公开 createPeer 创建**非 owner principal**，从可信映射取会话/工作区归属，所有自称 userId/role/agentPreset 的参数无效。不能按 displayName、UAC 工号文本或路径前缀充当授权真源。平台 HTTP/WS 每连接绑定成员，epoch 改变主动断开、撤销重连票据，禁止绕过 edge/ingress 直达原始端口；这些检查不改变原生 DSH browserPeer、Cookie 和 /connect 内部 owner 身份。完整非 owner 页面、index与WS载体属于后续，不以插件验签冒充已实现。

SPIKE 证明自有 HTTP 载体的 createPeer 和 remoteAccess 拒绝有效，但未提供可部署的完整非 owner Web/WS 载体。首期沿用原生页面时必须披露 owner index/策略旁路，不能只过滤侧栏便承诺隔离。自研创建路径在 session/workspace 归属持久化前不可对外可见，失败销毁；子会话继承且不能改 tenant/member/epoch；远程 delegated 会话沿单独授权。异步任务保存绑定快照并逐次复核，不依赖全局“登录用户”；缺失 sessionId 的模型辅助调用须绑定或拒绝，不能回落 owner。

### 4.3 目录、配额和备份

池数据固定分三类：`/var/lib/auriga/pools/<pool>/host/` 为受信 DSH_HOME/会话索引，`.../members/<tenant>/<member>/<epoch>/workspace/` 为本人文件，`.../control/` 为归属、资源账本和审计。平台界面默认展示本人 workspace，但原生文件入口可跨成员读取，首期不承诺只见本人文件；host/control 不作为工具 cwd、skill 扫描根或上传目录。ID 均由服务器生成并校验，无用户名路径。管理存储唯一落盘方仍是 agent。

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

共享环境由平台注册的有限业务工具调用企业登录服务。每次调用从可信宿主会话取得tenant/member/placement/epoch/sessionId及任务绑定，拒绝模型参数自报身份，子会话继承归属并另发能力。handler按会话所属成员的平台会话读取IAM权威最新企业材料。原生 owner 入口允许 A 驱动 B 的会话，此时可信会话上下文指向 B，工具会取 B 的材料并可能以 B 身份操作内部应用、将审计记到 B 名下。这一完整性及冒名风险按第3.1节④首期接受。SSO Cookie可脚本读取沿登录设计的单IP/单域名取舍，共享进程和同源脚本也存在凭据保密风险。

保留登录设计的兼容端点 POST /dsh-team/outbound 与 GET/POST /dsh-team/agent-credentials；loopback对端校验只是第一层，不能区分共享池内的用户。handler还必须验证任务能力的audience、600秒有效期、credentials/outbound scope、撤销状态、成员身份、认证代次、DSH会话owner和placement epoch；会话ID和能力必须相互绑定，跨成员、跨会话、过期、旧代次及伪造请求拒绝。shared补充围栏先由K线冻结为只增契约，不静默改变独立/桌面现有helper协议。

企业凭据以请求级不可变上下文传给handler；await、流式回调、并发与后台任务都保持同一绑定并逐次校验有效性。禁止写process.env、全局shellEnv、全局provider配置或“当前用户”单例。DSH_SESSION_ID、DSH_TEAM_TASK_CAPABILITY、DSH_TEAM_BASE等兼容变量仅存在于受控任务的独立env对象中，不修改主进程环境，也不向成员开放通用shell/终端。任务结束、退出、切换或撤销立即释放能力及缓存；缓存键至少含tenant/member/session/epoch/credentialVersion，不能跨成员共享token缓存。复验轮换后下一次调用读取最新版本，晚响应匹配旧代次时丢弃。

默认优先agent-outbound代发，由handler重建本人认证头，目标限定部署outboundAllowedHosts，空表拒绝；响应过滤token/Cookie。沿登录设计保留allowRawAgentToken默认true及可即时关闭的开关，关闭时下一次原始读取拒绝且只发outbound scope；raw开启时仅具credentials scope的本人受控helper可在内存读取empNo/token/updatedAt，禁止把凭据作为模型工具结果。原始材料不落工作区、skill正文、会话、静态配置、日志或stdout/stderr，工具只返回业务结果或安全状态。原始读取的进程内可见风险继续明确披露，不将任务授权表述为内存保密隔离。

首期验收使用平台下发的同一示例skill并发操作两个成员各自的内部应用替身，验证正常回合分别得到本人业务结果、自研调用链拒绝 A 能力配 B 会话、A/B轮换及迟到回调保持绑定，并覆盖raw开关、出站域名允许表、注销/复验失败/提降权后的旧能力拒绝。自研调用链避免向模型、日志、会话或工作区写入凭据。另须保留原生 owner 反例，验证 A 可读取并驱动 B 的会话、以 B 的企业身份执行业务且审计归 B，将实际结果与第3.1节④披露一并验收。SPIKE 已验证公开工具硬拒绝及请求级模型适配的可行性，真实 IAM/SSO/helper 接线由首期 P/N 线完成。原生 owner、文件、同源脚本及企业身份冒用按第3.1节接受，继续首期企业skill实现，后续加强真实发起成员与会话双重绑定。

## 6. 管理台提权与降权

### 6.1 管理操作和权限

成员详情展示当前模式、状态、独立配额、切换结果。提权按钮为“分配独立容器”，降权为“改为共享”。`workspace.placements.read/write` 登记在 `contracts/catalog/m3.json`，内置owner/admin授予，均仅限ctx=admin及本租户授权管理员。写操作要求600秒内step-up，读结果沿普通管理读取授权。UAC管理员需通过可执行的管理员step-up方案，扫码登录后仍须满足近期验证。IAM原工作线按第7.2节指定边界接入角色授予、会话权限和step-up。

确认面板列出会话永久丢弃、运行任务合作取消、取消超时停全池及文件策略，并展示第3.1节完整风险文案，明确“成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下”。操作者须填写原因，请求使用 If-Match 和 Idempotency-Key。审计记录操作者、租户、目标成员、原目标模式、operation、修订、epoch、step-up引用、结果与分类错误，并按脱敏规则记录。跨租户对象返回404，管理接口执行管理员授权。独立容器 entitlement 与实际 mode 分别存储，切换成功才生效，UAC及团队邀请使用同一授权流程。

### 6.2 双向状态机和故障处理

两个方向使用同一操作状态机：accepted → fencing → draining → preparing → committing → succeeded；失败进入 failed，停止不确认或恢复歧义进入 blocked。from/target 必须不同；同一成员仅一个在途操作，操作状态与期望模式事务持久化，节点租约和 CAS 防重复执行。

1. accepted 检查授权、成员有效性、配额与容量，预留目的端资源；失败尚未进入围栏时保留原服务和会话，审计失败。
2. fencing 标 switching，递增并持久化 epoch，撤销旧 open 票据、浏览器/网络/模型/MCP能力，阻止新 prompt/附件/后台任务和旧连接继续调用。用户看到“正在切换，将关闭当前会话”。
3. draining 请求本人主/子会话、后台任务与外发调用合作取消，合作取消等待预算为30秒，作为压测起点。首期不承诺选择性硬取消；共享池超时由进程外 supervisor 保守停止整个池及其执行树，会中断其他成员任务。事件循环阻塞时进程内计时器和取消请求也可能延迟，因此30秒不是停止完成保证。未取得外部停止收据则转 blocked，不冒成功；确认全池静止后，其他成员只可按当前有效 epoch 与资源重新连接，不自动重放旧任务。独立容器超时停止本人容器执行树。
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

新增 `contracts/proto/auriga/node/v1/shared_dsh.proto` 定义 mode/binding，Java/Go 同源生成。`contracts/events/placement.schema.json` 定义 `placement.switched` 和 `placement.switch_failed` 结果负载，两事件在 `contracts/catalog/m3.json` 与 `contracts/events/README.md` 登记，由workspace生产，source为auriga/workspace、subject为placementId、消费方为audit。沿CloudEvents信封取tenant，operationId幂等，revision/epoch围栏。成功要求sessionsDiscarded=true且无failureCode，失败带分类。fromMode!=targetMode、当前绑定一致及成员归属由生产方事务验证。消费者与NodeControl实现按工作线接入后启用事件。

管理员接口用x-admin-only和x-permission关联目录权限，写操作标记x-step-up-max-age-seconds=600。409、412、428均返回公共Problem错误体，428缺少If-Match使用common.validation_failed。首次登录尚无分配时，GET /me/placement返回404和common.not_found，由placement open创建分配。切换结果schema用if/then要求failed/blocked携带failureCode，succeeded要求丢弃会话并禁止失败码。常驻契约测试检查三个路径的shared-dsh.yaml引用，使用当前契约执行状态正负例。

本轮契约是可生成的控制面草案，不冒充完整共享数据面协议。实现前 K 线仍须冻结 placement open/票据、shared身份签名版本、资源视图选择及回报、模型/消息能力细分、guardian影响范围和成员撤销；缺一不启用共享。旧实例接口不提供部分兼容假象。三套生成器、OpenAPI lint、strict schema、契约正负例及 Java 契约校验结果详见工位报告；通过生成不等于隔离验证。

### 7.2 可直接派工的工作线

SPIKE证据已吸收，可先派首期K。K冻结后，W/E/R/P/N/U及配套IAM原工作线按独占边界并行开发，各线使用K定义的端口替身独立验收。联合接线固定为W权威分配与编排端口 → IAM权限及step-up → E入口 → R池运行 → N模型/消息能力 → P宿主与企业skill → U正常页面 → V全链验收。W先提供分配及编排端口，E/R收据以K替身验证，接线后补真实收据验收。P按已就绪的N能力接入。首期继续实现已接受风险下的功能，公共契约变化交K处理。每条任务附精确base、依赖契约版本、只改路径和验证命令，工人提交产物，由主控串行部署与验收。

#### 0. SPIKE：公开 API 可行性验证（已完成收证）

- 范围：auriga-SHARED-DSH-SPIKE 已完成最小公开服务宿主验证，本设计线不重复试装。非owner Peer/remoteAccess、ToolRuntime guard、模型请求绑定及固定skill根存在公开可用机制；原生owner、跨成员文件与单进程故障反例已吸收至第3.1节，不将 runner 符合预期解释为隔离通过。
- 文件边界：SPIKE自己的隔离工位、独立rc.3 DSH_HOME、随机端口与证据报告，不修改本设计线文档或共享工位运行状态；不得改DSH、prototype或解析内部文件。
- 依赖：本稿已定功能边界、已合并auth-shell第3.1.2节及主控交回的提交7e7ea403报告；按用户首期接受决定继续，不等待更强隔离。
- 验收：已收到完整命令、版本、API导出、8项runner及缺口清单；适用边界见第3.1节。完整Web/WS、Linux、真实企业登录和容量测试转首期V/P/N/R各线，不标成已通过；上游能力需求列后续，不再派重复SPIKE阻塞首期。缺口不能用全局身份/环境变量或通用shell绕过。

#### K：共享控制面与数据面契约

- 范围（首期可派）：冻结placement open/票据、共享身份头版本、成员撤销与epoch、平台skill选择/资源回报、企业任务能力围栏、模型/消息能力、guardian及取消超时全池影响；补权限、事件生产映射、错误码与契约向量，不承诺选择性硬取消。
- 文件边界：独占 contracts/**、对应web/shared/src/generated/**和infra/gen/**生成产物；只改必要生成脚本。Java生成物仍进target。其他线不手写或修改生成类型。
- 依赖：本稿已吸收SPIKE及首期风险决定，可立即派工；复用auth-shell既有IAM企业凭据接口，不另建登录生命周期。当前三条placement接口仅控制面起点。
- 验收：只增检查、三套生成器、lint、strict schema与契约正负例全通过，包含跨成员/session/epoch、600秒期限和撤销、30天封存/空目录/会话丢弃、未知模式拒绝。形成可被后续任务直接引用的精确契约HEAD。预计3–5日。

配套IAM改动由主控交IAM原工作线串行接收。该线独占 `services/iam/api/**` 与 `services/iam/impl/**` 中角色权限、管理会话、step-up及对应测试，依赖K冻结的目录和600秒要求，先用W端口替身验证。实现内置owner/admin的workspace.placements.read/write授予、portal/device/instance的管理权限过滤、UAC管理员可执行的step-up，以及失效凭据撤销。验收覆盖普通成员、跨租户、缺失及过期step-up拒绝，UAC和邀请管理员正常流程通过。其他工作线通过IAM公共API接入。主控与现有IAM派工合并范围后排期。

#### W：分配、容量与提降权编排

- 范围（首期可派）：pool/placement持久化、租户准入、固定初始预算、模式切换、预留、围栏、合作取消/超时停全池/封存/重建、幂等/CAS/outbox与崩溃恢复；维护窗口旧用户名单及批量进度。
- 文件边界：独占 services/workspace/api/** 和 services/workspace/impl/**，通过IAM/resource公共端口取权威输入；不改其他服务表或infra。数据库只新增迁移。
- 依赖：K。先发布权威分配及编排端口，按K用E入口凭据与R运行收据替身独立验收，联合接线时补接真实端口，再验证完整切换和ready状态。
- 验收：真实临时PG验证同租户池、跨租户拒绝、20登记/4活跃/每人1活跃准入、满载扩池、同成员并发切换拒绝、合作取消及30秒等待超时外部停全池、无停止收据保持blocked、封存30天新目录、旧会话不恢复、故障重启与晚ACK无双活。预计6–9日。

#### E：edge与节点入口身份

- 范围（首期可派）：平台placement票据/实例cookie与签名身份声明，绑定成员和epoch，平台HTTP/WS验证、主动撤销连接；域名和纯IP沿当前已合并单源部署规则执行。原生DSH身份缺口须披露，不负责改写上游browserPeer。
- 文件边界：独占 infra/edge/** 和 infra/agent/internal/ingress/**；不改renderer/runtime/storage或clients/dsh-plugin。需要新增共享类型只向K申请。
- 依赖：K先用W/P替身独立验收。联合接线在W端口及IAM授权就绪后接入权威分配，P就绪后补真实宿主入口验收，记录原生页面owner风险。
- 验收：平台入口伪造、多值header、错aud/方法/路径、重放、过期、跨成员、旧epoch、WS续用均拒绝；门户正常/open与共享票据只到本人placement，原始端口不可绕过edge直达；复测并披露原生Cookie/browserPeer及/connect仍可能以owner操作。预计5–8日。

#### R：池运行、目录与统一资源guardian

- 范围（首期可派）：每池一个主进程、成员目录与硬配额、封存和到期删除、平台统一skill固定扫描根、可信插件集合、移除shell/terminal/jobs/subagent/MCP stdio及动态安装能力、进程外supervisor超时停全池、资源失效停全池、受污染会话停止恢复。
- 文件边界：独占 infra/agent/internal/renderer/**、runtime/**、storage/**、observer/**及该线必要节点执行配置，不改ingress或插件；与W以契约交接目录和收据，不共同落盘。
- 依赖：K先用W/E/P替身独立验收。联合接线在W与E就绪后接入调度、封存意图及入口，P就绪后补profile和工具注册的真实验收。
- 验收：一个池一主进程、不跨租户；服务器目录映射与创建入口校验，记录原生文件跨根为首期接受风险，不能报读隔离通过；每人5GiB/10万inode起点；禁止个人目录扫描与任意安装；外部supervisor在阻塞/崩溃/取消超时下停全池并回报收据；任一资源到期实际停全池并显示影响、清理不确认拒绝启动、封存快照不向普通新会话自动挂载。预计6–10日。

#### P：共享宿主、企业skill与凭据适配

- 范围（首期可派）：自有适配路径构造非owner会话上下文、可信归属表/父子继承/发布前登记/恢复撤销；拒绝任意cwd和原生创建/目录修改，服务器映射创建；remoteAccess明确允许表、未知方法/action一律拒绝；ToolRuntime tools.guard硬拒shell/terminal等执行工具，独立关闭guard外控制器。平台统一skill有限工具入口，按auth-shell迁入既有两handler/helper契约，逐调用成员绑定、企业材料轮换和raw开关；资源视图、受限设置和原生DSH风险提示。首期不补完整非owner浏览器载体或受限fs。
- 文件边界：独占 clients/dsh-plugin/src/shared/**（新增适配目录）、src/managed/**及必要宿主注册/打包文件；与auth-shell插件工作线串行接收精确基线，不并行编辑其handler和shellEnv。不改IAM或AI/network服务实现。
- 依赖：K、已收SPIKE证据、已合并登录设计及其可用handler实现；E提供可信身份，R提供平台资源视图，N提供模型与消息下游能力，接口未就绪先按K替身验证。
- 验收：双成员正常页面、双企业应用替身，自研调用链跨成员/会话任务能力拒绝，轮换后取新材料、迟到回调丢弃、退出使旧能力失效，覆盖raw默认开和关闭后的下一次拒绝。请求级上下文避免写入process.env或全局当前用户，工具按凭据脱敏规则返回。未知方法拒绝，guard拒绝后工具体不执行，pre-execute不能放行，profile关闭对应能力。复测原生owner和文件缺口，保留A读取并驱动B会话、以B企业身份执行业务且审计记在B名下的反例，并验证第3.1节④披露。预计8–12日。

#### N：平台模型与消息网络归属

- 范围（首期可派）：每成员/placement/epoch/session的模型短时能力、计量与流式合作取消；自有provider/outbound broker从session查权威归属，缺失sessionId拒绝或显式绑定；共享物理连接上的逻辑邮箱、授权、背压和去重；云端/桌面互通及切换旧积压不可执行。
- 文件边界：独占 infra/ai-gateway/**（模型与internal/network）及services/collab/**内必要授权适配；需要IAM现有能力接口改动交K并由IAM原工作线串行接线，不自建并行token存储。不改插件或workspace实现。
- 依赖：K先用W/P替身独立验收。联合接线在W分配、E入口与R池就绪后提供能力给P，消息协议沿main已合并版本执行。
- 验收：自有provider/broker两个成员并发模型请求分别计量，伪造归属、缺失身份和旧epoch拒绝，不回落共享owner/account适配；平台消息路由复用连接但不广播私有流，原生owner观察风险按第3.1节披露；桌面与云端明确授权收发、切换后旧队列不进入新会话，失败不自动重放有副作用的命令。预计5–8日。

#### U：管理台与普通入口可见流程

- 范围（首期可派）：成员模式与独立授权、step-up、影响确认、进度/失败/重试、批量重建名单、容量与第3.1节风险提示，品牌Auriga DSH及中英文；企业skill只显示平台统一资源来源。
- 文件边界：独占web/admin/src/views/**内对应成员/实例页面及其i18n/测试，web/portal对应placement入口页面，以及对外使用文档 `docs/user/shared-dsh.zh.md` 和 `docs/user/shared-dsh.en.md`。文档应含第3.1节完整风险、管理切换操作、取消超时停全池和独立容器入口，随正常页面同批发布。web/shared生成文件归K，共用UI文件由主控串行分配。
- 依赖：K冻结后先以端口替身验收组件。联合接线在W、IAM、E、R、N、P按上述顺序就绪后接入正常页面，显示R的全池影响及W的审计/进度。
- 验收：UAC/邀请成员同规则，缺step-up拒绝，202显示登记状态，切换面板确认文件封存30天、新空目录、会话丢弃。对外中英文文档、管理台模式页和降权面板均展示第3.1节完整风险，明确“成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下”，同时披露互看文件、owner能力及停全池影响。管理接口权限有效，页面可观察真实新会话。预计3–5日。

#### V：集成、压测与独审

- 范围（首期集成可派）：上述产物合成发布候选，验证完整正常入口、两成员/两租户负例、企业应用skill、池故障、提降权中断、维护重建和容量起点，审查授权与凭据影响面；逐项标明第3.1节已知接受、已实现拒绝及未实测，不以风险接受抹去失败。
- 文件边界：独占 e2e/**对应测试、公开验收说明与本任务审查报告；发现实现问题回原线修复，不由测试线并行改业务文件。首轮完整独审必须全新非作者，会话与返工续审沿主控规则。
- 依赖：K至U可部署产物；真实PG/DSH全链由主控串行占唯一槽，工人不碰NAS。开发部署按可独立验收改动经必要测试后小批进行，不等全阶段收官或先用完整CI阻挡每次试用部署；合并仍守独审与精确HEAD完整CI。
- 验收：真实Web/WS正常门户进入所分配Auriga DSH、企业skill操作本人应用、两向切换、新空目录、旧会话丢弃。完整复核历史/附件/上传/事件授权与Linux路径和进程，记录原生owner、文件跨读及A驱动B会话导致企业身份冒用与审计错归的首期接受反例，并核对三处对外文案。验收资源到期及合作取消超时停全池、阻塞/崩溃影响范围、80%内存停止准入及扩池，披露压测实值。预计5–8日。

K至V上述各线区间合计41–65工程日，可按文件边界并行。配套IAM改动由主控并入IAM现有工作线后核定排期。受控非owner完整Web/WS/index载体、受限fs/broker及外部成员执行域列为后续工作线，另定契约、范围和验收。首期按互信团队及已定功能范围实施。

首期发布验收覆盖平台租户准入、管理权限、签名及能力围栏负例，自研非owner调用路径的归属与撤销，ToolRuntime硬拒绝及guard外宿主能力关闭，未知协议默认拒绝，企业应用skill并发与撤销负例，外部确认资源撤销/到期和合作取消超时停全池，以及切换、重启及晚ACK无双活。真实浏览器正常入口及风险提示须可见。原生owner旁路、跨成员文件读取、全池故障、他人会话可见及企业身份冒用/审计错归按第3.1节首期接受，保留反例和影响记录。其他新增越权或凭据泄露交主控处理。完整非owner隔离和Linux文件逃逸覆盖列入后续改进验收，真实宿主证据由各实现工作线提供。

## 8. 已定决定

用户于2026-11-14对七项建议明确“全部按推荐”，以下为实施规则，不再作为待选方案。

1. **共享信任范围**。共享仅限同一互信团队，文档与管理台明示成员可能互看文件及会话内容、以owner能力操作，并可能以其他成员身份操作内部应用、将审计记在被冒用者名下。第3.1节四项主要风险纳入首期接受，服务端归属检查和可实现的硬拒绝须完成。
2. **容量与租户**：初期一个池，容量满后加池，每池一个DSH主进程，不跨租户；扩池不静默搬迁在途会话，避免扩大数据与故障影响面。
3. **共享功能边界**：产品不开放任意插件、个人Key、私有skill、成员自行共享skill/MCP、shell和终端；首期保留公开机制硬拒绝并披露owner旁路，不能承诺全部原生入口均可靠封禁。有主动使用这些功能需要的成员提权到独立容器。平台统一下发的企业应用skill按第5.1节受控调用，保持既有业务用法与成员归属。
4. **guardian**：一份资源到期保守停全池，并明确会中断其他成员任务；通过统一故障域确保失效内容不继续执行。
5. **切换处置**：提权与降权均封存本人文件30天，新模式空目录，会话始终丢弃；避免自动携带旧执行配置，同时提供受审计的数据取回窗口。
6. **旧用户**：在维护窗口内批量重建，登记“继续独立”名单，先给导出/封存窗口，不做历史会话迁移；不在首次登录时静默销毁数据。
7. **压测起点**：每池20登记/4活跃/每人1活跃、每人5GiB/10万inode、30秒合作取消等待、超时保守停全池、30天封存；不保证30秒内硬取消本人或完成停池。以上是已定初始预算，性能上限须由压测证明，数值不代表模型调用默认限流或已验证容量。

设计规则已经按用户“全部按推荐”及其后“暂时接受，先优先做完”定稿；SPIKE证据已经吸收，不再等待已知隔离缺口解决才启动首期。运行时上线仍须完成第7节首期验收，开发部署由主控按必要测试小批串行推进；PR合并须完整独审及精确HEAD完整CI。当前仅设计与契约草案，未实现、未部署。
