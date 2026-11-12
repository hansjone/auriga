# UAC 身份源与外壳登录设计草案

## 1. 范围与依据

本稿基于 `51beea24b3e42619f8d8b5879ba158176235fa87`，供用户审阅后拆分实现。契约新增项均为草案，尚无运行时接线。审查等级 A，涉及 contracts，交主控标记【待审查】。

本轮的用户要求是 UAC 扫码与团队账号并列，登录页归云端门户或 Desktop 外壳，插件继承身份。Desktop 可跳过并稍后登录，平台认证失败不得封锁本地 DSH。云端托管入口要求登录。桌面登录仅认证并返回当前 DSH，进入本人云端实例由门户现有工作空间流程负责。

依据为任务文件、`docs/architecture/identity.zh.md`、`contracts/README.md`、`contracts/deployment/README.md`、`contracts/plugin/local-login.md`、`contracts/resource/unified-protocol.md`，以及 memwiki 的《Auriga 工作台产品设计》《DSH 宿主兼容与连接模式认证》。公开文档中的地址均使用示例域名。企业真实地址只进入部署配置。

### 1.1 原型协议核实

核实方式为只读源码、原型 Git 记录与测试夹具。本轮没有连接企业 UAC 或内网。以下是原型调用行为，线上可用性及官方支持程度仍待 UDS 团队确认。

- Hub 的 `lib/identity/uac/qr-challenge.js` 本地生成两个各 16 字节随机值，组成 `TwoDIMAuth:<key>:<value>`。另生成 32 字节 browserBinding，存 SHA-256，TTL 120 秒，最多 5000 条。扫码串仅包含公开坐标。
- `lib/identity/uac/index.js` 向 baseUrl 下 `/uacqr/auth/qrcode/verify.serv` POST JSON，字段含 qrCodeKey、qrCodeValue、loginClientIp、loginSystemCode、originSystemCode。原型固定 loginClientIp 为回环地址。verifyCode 是五字段拼接后的 MD5，属于上游格式。Auriga 的安全绑定须另外使用高熵秘密。
- 成功判断为外层 `code.code` 或 `code` 等于 `0000` 且 `bo.code=0000`，员工号来自 `other.account/empNo`，令牌来自 `other.token/authValue`。`bo.code=4002` 等待，`1002` 已扫码。Hub 浏览器通过 GET SSE 发起验证，一轮后断流，重新订阅继续。设备挑战使用 S256 verifier，拒绝经浏览器 SSE 兑换。
- `user-search.js` 向配置的 userSearchUrl POST，头为 X-Emp-No 与 X-Auth-Value，body 含 employeeShortId、enableLabel、keyword。要求 HTTP 2xx、业务成功、恰一条员工号严格相等的结果，拒绝多义和保留主体。使用独立 HTTP Agent，避免继承原型的企业代理设置。
- `dsh-team/lib/uds/client.js` 的 GET `/api/auth/verify?token=...&system_code=...` 明确写着 TODO 待 UDS 团队确认，响应映射也写着按实际结构调整。该接口及查询参数令牌传递不进入本稿上游契约。
- `validator.js` 有 header/cookie 取凭据与 HR 查询逻辑，仅有员工资料或请求自报员工号不能证明认证。原型中的 trust 分支不成为 Auriga 接入依据。
- `session-revalidation.js` 每两小时复验，首次延迟一分钟。明确拒绝只标记需重新扫码，保留本地会话。网络、5xx、畸形响应保持未知结果。该行为作为本地连续性参考。
- `lib/web/login/desk.js` 区分 src=uac/team。UAC 手机确认没有两位比对码。`lib/linked/auth.js` 曾将已验证的 uacEmpNo 与 Hub uid 关联 owner。本稿按当前 DSH owner 与平台身份分离规则处理。
- `dsh-team-hub/test/core/auth.test.mjs`、`test/devices/devices-qr.test.js` 为 verify.serv 与资料查询提供本地替身。Git 中 `818890f5`、`1dafe54a` 可追溯 Hub/设备流程。测试证明源码预期，无法证明当前企业上游成功。`uds-auth/` 当前可见内容主要为会话与工作区管理脚本，未找到可引用的 UDS 官方接口说明。

### 1.2 当前 Auriga 接缝

IAM 已有 IdentityConnector 的 descriptor/validateConfig/begin/advance、身份源管理、登录事务 cookie 围栏、会话与邀请策略。现有 `nativeConnect` 只适用于登记的第一方团队账号加 TOTP，不包含 UAC。

门户 `src/native-login/page.ts` 当前从插件导入渲染器，主要支持表单，认证后调用现有本人实例 open，进入 `/open/{id}`。管理台有独立 LoginView 与 IdentityProvidersView。两入口的 audience、cookie、权限与 step-up 分离。

插件 `src/login/` 有视图和本地控制器，`src/local/` 持有平台认证流程与凭据，`src/identity/` 同时承担本地 owner/standalone 准入，`src/managed/` 承担托管验签、门禁与会话。Desktop 的 `runtime/core.mjs`、`cli.mjs`、Windows launcher 已有启动及升级生命周期，新增外壳认证服务需接入这些正常入口。

## 2. IAM UAC 身份源

### 2.1 配置与协议边界

连接器类型取 `uac`，与团队账号并列。通用步骤调度、成员策略、session、审计仍归 IAM。企业适配器通过既有连接器机制装配，企业地址及专用代码留在企业模块。管理台为已有 provider 提供 enabled、baseUrl、systemCode 配置。提供商的名称、顺序、创建和删除继续走现有管理接口。

baseUrl 必须 HTTPS、无 userinfo/query/fragment，按部署受信允许列表校验 hostname、端口与解析地址，拒绝重定向。上游可能位于企业私网，允许列表由部署管理员明确登记范围。管理台不可任意开启内网访问。配置更新执行既有 iam.providers.write、ctx=admin、Origin/CSRF 和 ETag。enabled=false 立即阻止新挑战并撤销该源事务及关联平台会话和设备授权。

systemCode 到原型 loginSystemCode/originSystemCode 的映射尚缺依据，禁止实现时简单复制到两个字段。userSearchUrl 与认证有效性语义也待确认，由企业适配器受信配置注入，不能从浏览器传入任意 URL。配置可保存为停用，正式启用须有已核准的适配器配置。未完成确认时返回 common.unavailable。

新增 `/login/uac/challenges` API 明确区分 UAC 二维码。现有 QrStep 要求手机输入 matchCode，上游原型缺此交互，因此本稿新增 Uac schema，保留既有步骤完整定义。

### 2.2 挑战生命周期

1. 外壳发现已启用身份源，用户选择 UAC。可信入口解析 tenant、规范 Origin、portal/admin audience，创建既有 IAM 登录事务和 UAC 挑战，绑定入口 cookie、provider/configRevision 与过期时间。已有邀请代码只保存在该事务中。
2. 返回 UacChallenge 的公开扫码内容和有效期。页面不持有 UAC token、浏览器绑定秘密或 PKCE verifier。二维码过期后只能新建，刷新会取消旧事务。建议 TTL 120 秒，以经确认上游有效期为上限。
3. IAM 后台有界轮询上游，同一挑战单飞，按两秒起步和上游限流退避。GET 状态只读取本地事务，不执行登录或上游副作用。多副本用共享持久存储及 CAS，进程内 Map 不作为生产唯一存储。
4. 手机确认后，IAM 取到上游令牌，再调用已确认的校验接口核员工号一致性。仅查询到员工资料不足以完成认证，需 UDS 证实查询的鉴权与 token-subject 绑定保证。
5. 校验成功写 verified。发起端以绑定 cookie 和精确 Origin 调 complete，原子消费挑战并进入成员策略，必要时继续 MFA。只有事务为 authenticated 且服务端 session 建立成功才算登录。
6. complete 的唯一消费记录与会话创建同事务提交，响应丢失可在原绑定端读取既有完成结果。晚到上游响应必须匹配事务代次，取消、停用、到期后无权复活。失败统一白名单错误，未知挑战或绑定失败为404。

首期采用轮询。回调方式须先取得 UDS 签名、重放窗、投递语义及来源认证资料，再另增契约。扫码转发的社会工程风险依赖手机明确展示目标系统与确认信息，浏览器绑定只能阻止窃取发起端会话，不能证明扫码人理解二维码来源。若 UDS 无该能力，提交用户确认其接入范围与风险后启用。

### 2.3 首次成员关联

主键使用 `(tenantId, providerId, verifiedSubject)` 唯一约束，verifiedSubject 为上游确认的稳定员工号字符串，保留前导零。大小写和规范化仅按 UDS 文档处理。

- 已绑定身份直接解析原成员，先检查成员在职/启用与 provider 状态。
- 预置且已由管理员核实的员工号可以关联同租户唯一成员。任意团队账号的自填员工号不能用于自动接管。
- 邀请必须仍有效、属于同租户、用途匹配、未消费，且受邀员工号与已验证主体相同。邀请与身份关联在 org 策略授权及 IAM 唯一约束下原子协调，重试返回同一成员。
- 已有团队账号的用户可从已认证账号发起 purpose=link，再验证 UAC。双方认证均须新鲜，保留账号原角色，不根据上游 role/department 提权。
- 无唯一可信映射和有效邀请时拒绝注册，展示申请邀请。禁止按姓名、邮箱模糊合并。重复、跨租户或员工号回收冲突进入管理员处理，审计只记最小主体标识。

### 2.4 复验与撤销

IAM 加密保存会话复验必需的上游令牌，密钥可轮换。建议两小时复验、启动一分钟后分散调度，均为待审参数。明确401/403或经确认的撤销码使平台 session/device grant 失效，事件与权威状态传播到 edge、外壳与旧连接。业务码不能全部视为撤销，需上游确认错误分类。

网络/5xx/畸形响应记 unknown，有界重试，不续长令牌。既有平台会话最多用到自身有效期与最后成功复验后的两小时上限，先到者为准。管理操作仍执行原 step-up。到期仅停平台能力，本地 owner 会话继续。上游复验超时不得重新解释为“未曾登录”，UI 显示总部不可达或需要重新认证。

Auriga 退出撤销本入口 session 与对应本机设备授权，不调用未经确认的 UDS 全局退出。门户与管理台分别退出。停用、成员冻结、上游明确撤销时，所有关联平台会话按撤销范围失效。

### 2.5 替身与 UDS 确认清单

测试使用随机回环端口的协议替身，由测试显式注入 transport。生产默认禁止替身开关、固定成功 token 和自动降级。替身覆盖 waiting/scanned/success/denied/expired、员工号不符/多条、错误业务码、HTTP错误、超时、畸形及超大响应、并发 complete、断连重试、停用与到期晚响应。只使用合成员工号，测试结束关闭服务。

需向 UDS 团队取得版本化文档和脱敏联调样例，确认以下事项。

- 二维码生成权、格式与熵要求，是否需要先注册挑战，二维码期限与次数限制。
- verify.serv 是否是正式开放接口，MD5 字段次序/编码、客户端 IP 来源、两个 system code 的授权和取值。
- 状态码、token 字段、有效期、audience、scope、refresh/introspection/revocation 能力，以及资料接口是否验证 token 与员工号严格绑定。
- 员工号稳定性、回收、外包身份与离职事件，角色字段的可信范围。
- 资料查询正式端点、最小字段、私网出口/代理/TLS CA 策略、速率及可用性约束。
- 手机目标系统展示、防二维码转发确认能力，回调鉴权和重试，单点退出与多端会话行为。

## 3. 登录页归属与迁移

### 3.1 云端门户和 edge

门户提供统一登录入口，身份源选择包含团队账号和已启用 UAC。表单渲染器先迁到 web/shared，再解除 portal/admin 对插件源码依赖。UAC 使用新增协议，不虚构两位码。管理台可复用渲染器，但登录事务与管理权限独立。

门户成功后复用 `POST /api/workspace/v1/me/instance/open`、就绪等待和 `/open/{id}`。edge 校验本人实例、一次性票据、实例 cookie、权威 session，并注入签名身份头。托管插件保留身份头验签、准入与当前会话读取，删除第二套托管登录界面。会话失效由外壳引导回登录入口，插件显示“正在恢复平台会话”或服务异常。托管插件永远不呈现可跳过的未登录状态。

domain 与纯 IP 部署均沿现有入口契约。IP 同源实例保留已知弱隔离，端口无法隔离 cookie。edge 转实例剥离全部平台凭据，管理写请求保持独立 Origin 与 CSRF 校验。

### 3.2 Desktop 外壳

建议以原生外壳窗口承载登录页，使用窗口导航 API 和受限原生桥交付启动能力。Desktop guardian 生命周期拥有一个认证 broker，启动器先启动 broker，再打开外壳登录页或 DSH。外壳可提供 loopback 登录页供其受控窗口承载，凭据和认证逻辑由 broker 承担。Electron renderer 不开启 Node 能力，不加载远程脚本。现有 guardian 资源停用职责与认证状态协调。

本地页服务器只绑定 `127.0.0.1` 随机端口，拒绝其他 Host、非精确 Origin、跨源 CORS、非预期 Fetch Metadata。初始页面通过外壳创建的单次高熵启动能力引导，能力放 URL fragment，页加载后清除 fragment，通过同源 POST 交换短期 HttpOnly SameSite=Strict cookie 和内存 CSRF token，失败与重放均拒绝。启动能力只经受控启动链交给指定窗口，不落日志、命令行或公开文件。实际实现须选择安全窗口/导航交付 API，无法满足时改用原生窗口桥接。

写请求均验证完整 Origin、会话 cookie、同步 CSRF token 和请求体 schema。无 Origin 的原生调用另走 OS IPC，禁止凭缺少 Origin 放行。TLS loopback 不具备通用可信证书时使用回环 HTTP，明示其信任边界限当前机器。防 DNS rebinding 依赖绑定地址与精确 Host；随机端口仅降低碰撞。

登录请求由 broker 代理到受信平台，远程 cookie jar 只存在 broker，页面只见步骤和脱敏状态。团队账号沿现有 nativeConnect S256 取得 device-only 凭据。UAC 首期完成门户认证后走既有 RFC8628 人工核码和授权，broker 保存 verifier/device code。UAC 扫码完成本身不自动批准设备授权，若需无二次确认须另行审定 UAC 的事务绑定设备契约。门户 USER token 不交给本机插件。

平台 access/refresh token、设备 verifier 仅由 broker 持有，操作系统用户密钥保护持久化，Windows 用 DPAPI CurrentUser 及用户 ACL。UAC 上游 token 留 IAM。插件进程只读状态及受限能力，页面不获得任何 token。broker 代理调用使用固定操作白名单、固定平台 Origin/资源 audience，禁止输入任意 URL、header 或令牌解析表达式。

### 3.3 插件代码归属清单

- `src/login/page.js`、`styles.js`、`index.js`、`index.d.ts` 的 UI 迁入 web/shared 或 Desktop 自有资源，调用方迁完后从插件删除。`local-adapter.js`、`local-controller.js` 的平台登录事务控制迁 broker，插件旧登录路由与表单入口删除。
- `src/login/logout.js` 的入口改成外壳 logout 命令，服务端撤销和 cookie 清理迁外壳，不遗留插件第二生命周期。`errors.js` 可迁共享文案层。
- `src/local/platform.js` 的 LoginJar、登录/刷新/设备授权逻辑以及 vault 中平台材料迁 broker。`local/team.js`、`runtime.js`、`routes.js`、`native-rpc.js` 的平台分支改读取 ShellAuth 状态和请求受限代理能力。旧平台凭据文件由已认证迁移流程导入 broker 后安全清理，导入失败清除旧平台能力并提示重登，保留本地内容。
- `src/identity/owner-admission.js`、owner 准入、standalone 所需身份源/应急逻辑继续服务本地身份。其余 core/oidc/session/store 按调用点拆出平台部分，不能整目录删除导致离线 owner 失效。
- `src/managed/door.js`、`host-auth.js`、`guards.js`、keys/session 验签与 edge 身份读取继续保留，managed/panel 取消登录视图入口，logout 跳外壳。模型、心跳、设置权限沿当前认证通道。
- `web/portal/src/native-login/page.ts`、管理台登录页改用共享渲染器。插件 web/设置侧栏只展示状态及“登录/退出”动作，通过受准入的后端桥接 broker。

独立装到现有 DSH 的连接模式也要能从插件动作启动外壳 broker，可将最小 broker 随独立插件制品装配并用同一 OS 用户运行。缺 broker 时保留本地能力并提示安装外壳，不能退回旧插件登录页。该分发选择待用户审阅。

## 4. ShellAuth 本地通道契约

生产方按 schema 严格检查字段。消费方允许扩展响应字段，但命令的未知操作、任意 URL/header/凭据输入在授权前拒绝。authenticated/identity、事件 type/snapshot/code 的关联约束还须由接收端按本节语义检查。

权威消息定义在 `contracts/plugin/shell-auth.schema.json`，OpenAPI `shell-auth.yaml` 是三语言生成用的 IPC 投影。`/messages` 禁止成为公网或普通 DSH HTTP 接口。它只投影 OS 认证后的消息，发布订阅持续推送同一 ShellAuthEvent。

Windows 使用禁远程访问的命名管道，DACL 仅当前用户 SID 和必要系统主体，验证对端 token SID、受控子进程启动记录及每次启动的随机能力，避免仅凭 PID 信任。Unix 对应用户私有0700目录中的0600 socket，并核对 peer uid。能力只经继承的私有句柄或管道交给插件后端，不放 argv/env/网页。相同 OS 用户的恶意代码属于同用户信任边界，不能声称该管道隔离已被攻陷的用户账户。

### 4.1 字段与生命周期

- ShellAuthCommand 包含 protocolVersion=1、requestId、shellEpoch、expectedRevision、action。支持 snapshot、subscribe、open_login、cancel_login、logout。首次 snapshot 的 shellEpoch 使用全零 UUID、expectedRevision=0，仅此只读动作允许发现 epoch。其余动作须与当前 epoch/revision 一致，否则 common.conflict。
- ShellAuthSnapshot 包含 shellEpoch、单调 revision、state、expiresAt，可选 identity/reason。identity 绑定 tenant/member/device/provider，empNo 可选。authenticated 必须包含当前 identity。expiresAt 为快照使用截止，最长30秒且不能超过平台凭据有效期；订阅每10秒重发当前快照或客户端到期前重取，属于本地健康检查。signed_out/authenticating/revoked 不含 identity。unreachable/reauth_required 可保留上次脱敏 identity 供显示，不授予平台操作。
- ShellAuthEvent 含 requestId、type，snapshot/changed 必须包含 snapshot，error 必须有白名单 code，ack 确认动作已接受。退出完成以 signed_out/revoked 快照为准。订阅主动事件 requestId 使用订阅命令 id。消费方按 epoch/revision 丢弃旧事件，重连先 snapshot，再订阅，服务端原子注册并补发当前快照消除间隙。
- requestId 在同 epoch 内幂等，缓存有界保存至命令到期，同一 id 不同内容拒绝。logout 必须先本地停权、递增 revision，再排队远程撤销。网络失败不恢复本地身份，重新登录生成新代次，旧排队撤销只针对旧设备/会话。
- broker 重启生成新 epoch，所有订阅失效。插件在管道断开或快照 expiresAt 到期立即停平台能力，重连读取权威状态。本地 owner 保持可用。时间使用 UTC 与本地单调截止双重限制，禁止时钟回拨延长快照有效期。
- open_login 只拉起配置指定的外壳入口，不接受 URL。cancel_login 只取消本命令对应发起窗口的尝试。多窗口共享同一 DSH owner 的平台身份，关闭一个窗口不撤销其他窗口。

最小命令示例如下，所有值均为合成数据。

```json
{"protocolVersion":1,"requestId":"00000000-0000-4000-8000-000000000001","shellEpoch":"00000000-0000-0000-0000-000000000000","expectedRevision":0,"action":"snapshot"}
```

外壳响应示例如下，首次获得 epoch 后所有写命令都提交该 epoch 与最新 revision。

```json
{"protocolVersion":1,"requestId":"00000000-0000-4000-8000-000000000001","type":"snapshot","snapshot":{"protocolVersion":1,"shellEpoch":"00000000-0000-4000-8000-000000000002","revision":0,"state":"signed_out","expiresAt":"2026-11-13T00:00:30Z"}}
```

### 4.2 平台功能的凭据路径

状态消息没有凭据传递功能。现有市场、网络、AI、Team MCP 的调用需要补齐 broker 代理端口契约，按当前受限 device audience/scope 分派固定操作，身份由 broker 当前快照确定，响应只返业务数据。长连接由 broker 持有或提供受限流桥，平台字节不得经任意目标转发。实施前要盘点所有现有令牌调用点并补全操作 schema，本稿的状态通道不能被解释为已覆盖业务数据代理。

模型凭据仍通过公开 DSH credentials/操作作用域适配，页面不能 resolve 平台票。需要与现有模型身份绑定协议一起评审，避免将 shell token 放入全局环境或模型配置。代理未接好时对应平台功能按不可用呈现，本地个人模型照常工作。

## 5. 跳过、登录、退出与跨身份内容

首次 Desktop 启动提供“跳过，使用本地 DSH”。跳过不创建平台会话、云实例或设备授权。设置和侧栏平台区域显示“未登录”和“登录”，本地 owner 准入照常执行。

之后用户点击登录，插件后端向 broker 发 open_login。登录完成改变快照并推送，当前 DSH 内平台 UI、固定代理调用与新网络连接立即更新，无需为身份刷新重启 DSH。用户取消扫码仅取消尝试。平台不可达时展示故障分类，有可恢复凭据的离线状态显示自动恢复，避免反复要求用户登录。

本地私有会话、个人 Skill、个人模型、工具配置按 DSH owner 持久保留，平台账号切换不改写或重新绑定本地 owner。服务器私有资源仍按平台 owner 隔离，切换后立即失去旧平台身份可见性。

共享资源按 `unified-protocol.md` 串行处理。退出/切换先停平台请求与旧连接，阻止旧异步响应提交，通知 guardian 进入 stopping，确认 DSH 进程树退出后隔离 `skills-active/current`，持久化 disable 回执并进入 private_only，再按新身份在线重验和重新授权。清空失败为 blocked，不能把旧共享内容交给新身份。旧身份 cache/ledger 放原身份根，禁止自动恢复到 current。历史会话可查看，private_only 使用新会话。

这里存在需用户确认的体验边界。从未登录且 private_only 的运行中补登录可以不重启，也不自动激活共享资源。已有共享资源参与上下文时，统一资源包协议要求停止进程并隔离，退出或跨账号切换可能需要受控重启。当前“稍后登录无需重启”通过推送新身份实现，不能扩大为共享上下文跨身份热切换。若用户要求共享内容切换也保持进程，须先修改并审定统一资源包协议及真实宿主卸载保障。

云端无法跳过。托管退出使 edge 实例 cookie、HTTP/WS 和重连认证失效，回门户登录页，默认不停止实例任务。托管共享资源变化另走既有云端可信执行器的 guardian 等价流程。

## 6. 安全与验收要求

挑战绑定 cookie、完整 Origin、tenant、audience、provider 修订和 TTL，多副本原子消费，公开二维码内容不承担轮询授权。complete/取消采用写请求，严格 Origin 和事务秘密校验，已认证管理操作再校现有 CSRF。GET 状态无登录副作用，校验绑定 cookie、受信 Host、同源 Fetch Metadata，并在 Origin 存在时核精确值。

令牌仅在 IAM/edge/外壳等受信服务端保管，插件后端通过能力代理继承身份，网页只接收脱敏状态。禁止 token、cookie、设备码、verifier、二维码坐标、完整上游响应和 URL query 进入日志。身份类日志最多保留员工号与内部 ID，姓名、部门、邮箱、电话不记录，二维码与认证页面禁止第三方遥测和缓存。

实现验收必须覆盖跨源/跨用户管道访问、DNS rebinding、CSRF、登录 CSRF、跨 audience/tenant 事务交换、挑战重放、并发消费、员工号冒认、多义关联、邀请重复消费、source停用、token过期/吊销、晚响应与外壳重启。刷新请求结果不确定时沿现有旋转令牌围栏要求重登，只在能证明未发出请求时安全重试。

云端从正常门户完成团队/UAC登录、实例准备、进入DSH及退出后旧HTTP/WS拒绝。Desktop 从正式启动器验证跳过、本地正常使用、侧栏拉起登录、无重启状态更新、多窗口、外壳崩溃和断网恢复。共享资源已激活时验证停止/隔离/新会话和崩溃恢复。容器healthy、源码与内部API只作局部证据。

## 7. 分期与派工边界

### 阶段一，契约和 UDS 确认

用户审阅本稿，取得上游文档，确定 systemCode 映射、正式校验与复验语义。补全受限 broker 平台业务操作契约。所有新增契约经非作者 A 级独审，精确 HEAD 完整 CI 合格后交主控处理合并。以下工作线可先用同一替身与草案并行开发，真实 UAC 启用依赖确认结果。

### 工作线 A，IAM UAC 身份源

文件范围为 services/iam/api、impl 的连接器/登录/会话，企业适配器模块，以及 services/org 的成员关联策略接口。contracts 的进一步变动由主控统一协调。依赖阶段一。验收包括三语言生成、严格员工映射/邀请关联、复验/撤销、共享存储并发消费、RLS和跨租户负例。配置与邀请提交失败可安全重试，单个挑战最多创建一个 session。

### 工作线 B，门户登录和 edge

范围为 web/portal/src/native-login、原门户登录入口、web/shared 渲染器与 API，infra/edge 仅调整路由/新事务保护接线。负责迁出插件 UI 的唯一写入，管理台与 Desktop 消费它。依赖 A 的 API，可先用替身。验收双入口 audience 隔离、二维码状态/取消/过期、额外验证、正常本人实例准备与进入、退出后HTTP/WS失效，domain/IP两模式。

### 工作线 C，Desktop 外壳认证

范围为 clients/installer/runtime、windows/launcher、打包脚本与新 shell-auth 模块，guardian 认证状态接线。依赖 A 及 ShellAuth/业务代理契约，B 提供共享 UI。验收正式安装包正常启动入口、DPAPI/ACL、跨用户拒绝、cookie/CSRF、S256、人工设备确认、令牌不入页面/日志、随机端口清理、多窗口及更新后凭据恢复。与资源 guardian 共享单一状态协调，不新增并行停用写者。

### 工作线 D，插件继承身份并删除旧登录

范围为 clients/dsh-plugin/src/login、identity/local/managed 的平台分支和 web 设置/侧栏，现有资源与模型适配层只作必要调用迁移。依赖 C 的状态与业务代理、B 的 UI 迁出提交。验收旧平台登录页和路由确实删除，托管无未登录页，未配置/跳过/离线的真实本地 DSH 可用，稍后登录推送生效，所有原平台功能走受限代理，旧凭据迁移与旧异步请求围栏完整。

### 工作线 E，管理台配置

范围为 web/admin/src/views/IdentityProvidersView.vue、admin/providersApi、providersForm、providersMessages 与对应测试。依赖 A 配置 API、B 共享 UI。验收开关/baseUrl/systemCode、ETag冲突、权限和CSRF、启用前错误提示、关闭后会话失效，普通成员不能读取或修改配置。

每线经针对性测试形成可部署产物通知主控，由主控串行安排开发环境部署与正常入口验收。本稿交付物是可审阅文件，实施部署属于后续派工。

## 8. 待用户决定

1. 首次成员关联建议仅允许管理员核实员工号、双认证绑定或有效邀请，理由是避免自填员工号导致既有账号被接管。待决定是否另开自动入团队注册，若开放须指定成员角色与组织策略。
2. UAC 设备授权建议首期沿既有人工核码，理由是当前 nativeConnect 仅认证团队账号加TOTP。待决定是否追加 UAC 专用绑定设备契约，以一次扫码完成 Desktop 授权。
3. 复验建议两小时和明确拒绝立即撤销平台会话，未知结果仅在现有会话期限内有界保留，理由是兼顾撤销传播和企业网络波动。待决定合规要求是否缩短期限。
4. 共享内容已激活时建议接受统一资源包要求的受控停止、隔离及新会话，理由是内存上下文可能残留旧身份内容。普通补登录用快照更新，待决定是否将进程内共享切换另列研发课题。
5. 独立插件建议同包提供最小外壳 broker，理由是继续支持现有 DSH 的独立安装且登录页始终归外壳。待决定是否要求先安装完整 Auriga Desktop。
6. 企业适配器建议留企业模块，公共仓库保留通用 Uac/ShellAuth 契约与示例配置，理由是符合身份源架构与公开仓库规范。待决定 UAC 接入的分发范围及目标用户。
7. 若 UDS 手机缺少目标系统确认或抗转发保障，建议先限定受信团队试用并明确展示系统名，理由是本地挑战绑定无法消除诱导扫码。待决定正式启用所需的手机确认保障。
