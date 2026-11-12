# UAC 身份源与外壳登录设计草案

## 1. 范围与依据

本稿基于 `51beea24b3e42619f8d8b5879ba158176235fa87`，第4轮按用户2026-11-13更正分离扫码登录与到期复验，沿用已定现场实现和七项产品决定，供后续拆分实现。契约新增项均为草案，尚无运行时接线。审查等级 A，涉及 contracts，交主控标记【待审查】。

本轮的用户要求是 UAC 扫码与团队账号并列，登录页归云端门户或 Desktop 外壳，插件继承身份。Desktop 可跳过并稍后登录，平台认证失败不得封锁本地 DSH。云端托管入口要求登录。桌面登录仅认证并返回当前 DSH，进入本人云端实例由门户现有工作空间流程负责。

依据为任务文件、`docs/architecture/identity.zh.md`、`contracts/README.md`、`contracts/deployment/README.md`、`contracts/plugin/local-login.md`、`contracts/resource/unified-protocol.md`，以及 memwiki 的《Auriga 工作台产品设计》《DSH 宿主兼容与连接模式认证》。公开文档中的地址均使用示例域名。企业真实地址只进入部署配置。

### 1.1 按现场可用实现迁移

用户明确原插件扫码认证在现场持续可用，并在最新更正中确定扫码取得token和工号即完成UAC登录。部门查询仅用于会话到期后的失效复验。本轮以用户更正及以下源码调用链为迁移依据，开发验收使用本地替身和日志断言。现场出现差异时通过脱敏诊断包回传定位。

- `oclaw/dsh-team/lib/index.js` 的挑战创建及扫码代理，调用 `lib/uds/user-search.js` 和 `lib/middleware/auth-middleware.js`；对应 Hub 实现为 `lib/identity/uac/index.js`、`qr-challenge.js`、`user-search.js`。配置来源为两项目的 config 文件，复验来源为 `dsh-team/lib/uds/session-revalidation.js`。
- `dsh-team/lib/uds/client.js` 的 `/api/auth/verify` TODO 属于另一条候选校验路径。现有源码提供verify.serv和携带token的部门查询，本稿按最新更正分别归入登录链与复验链。
- `validator.js` 的 header/cookie 提取、单独 HR 查询和 trust 分支仅作旧代码归属参考。迁移时身份来自已绑定挑战的扫码结果，部门查询在登录周期到期后执行。
- 原型 user-search 映射可能返回空 department，旧复验对网络错误保留 unknown。本轮遵照用户明确要求，补上非空部门判定，复验时用已保存的token和工号查询部门，单次查不到部门即判平台认证失效，失败分类继续保留供诊断。
- `lib/web/login/desk.js` 区分 src=uac/team，UAC 手机确认没有两位比对码。`lib/linked/auth.js` 的 uacEmpNo 关联作为来源参考，当前设计将 DSH owner 与平台身份分开。
- 原型 `test/core/auth.test.mjs` 与 `test/devices/devices-qr.test.js` 提供替身样例。用户的现场说明确立迁移基线，本轮只读核对源码，本机未执行真实 UDS 联调。

### 1.2 当前 Auriga 接缝

IAM 已有 IdentityConnector 的 descriptor/validateConfig/begin/advance、身份源管理、登录事务 cookie 围栏、会话与邀请策略。现有 `nativeConnect` 只适用于登记的第一方团队账号加 TOTP，不包含 UAC。

门户 `src/native-login/page.ts` 当前从插件导入渲染器，主要支持表单，认证后调用现有本人实例 open，进入 `/open/{id}`。管理台有独立 LoginView 与 IdentityProvidersView。两入口的 audience、cookie、权限与 step-up 分离。

插件 `src/login/` 有视图和本地控制器，`src/local/` 持有平台认证流程与凭据，`src/identity/` 同时承担本地 owner/standalone 准入，`src/managed/` 承担托管验签、门禁与会话。Desktop 的 `runtime/core.mjs`、`cli.mjs`、Windows launcher 已有启动及升级生命周期，新增外壳认证服务需接入这些正常入口。

## 2. IAM UAC 身份源

### 2.1 配置与协议边界

连接器类型取 `uac`，与团队账号并列。通用步骤调度、成员策略、session、审计仍归 IAM。企业适配器通过既有连接器机制装配，企业地址及专用代码留在企业模块。管理台为已有 provider 提供 enabled、baseUrl、systemCode 配置。提供商的名称、顺序、创建和删除继续走现有管理接口。

baseUrl 必须 HTTPS、无 userinfo/query/fragment，按部署受信允许列表校验 hostname、端口与解析地址，拒绝重定向。上游可能位于企业私网，允许列表由部署管理员明确登记范围。管理台不可任意开启内网访问。配置更新执行既有 iam.providers.write、ctx=admin、Origin/CSRF 和 ETag。enabled=false 立即阻止新挑战并撤销该源事务及关联平台会话和设备授权。

systemCode 映射到原型 loginSystemCode。originSystemCode 保留现场部署值，原型默认空字符串，两者独立。userSearchUrl 由企业适配器受信配置注入，部门查询路径固定为 `/zte-km-icenter-addresearch/user/plain/docs/search`，默认部署值从原插件配置迁入。baseUrl 与资料接口允许使用各自受信源，公开仓库只给示例域名和占位 systemCode。配置校验检查必填项、HTTPS及允许列表，保存和启用按现场既有实现推进。

新增 `/login/uac/challenges` API 明确区分 UAC 二维码。现有 QrStep 要求手机输入 matchCode，上游原型缺此交互，因此本稿新增 Uac schema，保留既有步骤完整定义。

### 2.2 挑战生命周期

1. 外壳发现已启用身份源，用户选择 UAC。可信入口解析 tenant、规范 Origin、portal/admin audience，创建既有 IAM 登录事务和 UAC 挑战，绑定入口 cookie、provider/configRevision 与过期时间。已有邀请代码只保存在该事务中。
2. 返回 UacChallenge 的公开扫码内容和有效期。页面不持有 UAC token、浏览器绑定秘密或 PKCE verifier。二维码过期后只能新建，刷新会取消旧事务。TTL 固定120秒，现场差异由关联日志定位后调整。
3. IAM 后台有界轮询上游，同一挑战单飞，按两秒起步和上游限流退避。GET 状态只读取本地事务，不执行登录或上游副作用。多副本用共享持久存储及 CAS，进程内 Map 不作为生产唯一存储。
4. 手机确认后，IAM按第2.2.1节取得非空token和工号，HTTP/业务成功且挑战绑定有效时写入已验证身份。登录期间只调用扫码验证上游。
5. 扫码校验成功写verified。发起端以绑定cookie和精确Origin调complete，原子消费挑战并执行现有成员策略，必要时继续MFA。会话建立时同事务持久化authenticatedAt和nextRevalidateAt，按第2.4节从登录时间起安排到期复验。
6. complete 的唯一消费记录与会话创建同事务提交，响应丢失可在原绑定端读取既有完成结果。晚到上游响应必须匹配事务代次，取消、停用、到期后无权复活。失败统一白名单错误，未知挑战或绑定失败为404。

首期固定采用轮询。手机目标系统展示与扫码状态作为现场日志观察项。接入范围限定受信团队，外壳明确展示目标系统名，保留挑战发起端绑定和人工设备授权核码。二维码转发的诱导扫码风险纳入试用提示与诊断，不增加 UDS 团队确认门禁。

### 2.2.1 扫码登录迁移协议

发起挑战为我方本地调用，扫码状态与凭据取得为服务端上游HTTP调用。参数沿现场源码映射，浏览器绑定秘密始终留在我方服务端。

1. 发起挑战复用原型 `QrChallengeStore.create` 语义，各生成16字节随机 qrCodeKey、qrCodeValue，另生成32字节发起端绑定秘密，存其 SHA-256。扫码内容为 `TwoDIMAuth:<qrCodeKey>:<qrCodeValue>`，有效120秒。原型的本地 start/events 路由迁入本稿 UAC challenge API，持久化及 CAS 按 IAM 多副本要求实现。
2. 扫码状态与 token 取得使用 `POST <baseUrl>/uacqr/auth/qrcode/verify.serv`，超时15秒、响应最多2MiB，拒绝重定向。JSON 请求字段为 qrCodeKey、qrCodeValue、loginClientIp、loginSystemCode、originSystemCode、verifyCode。loginClientIp 沿原型固定 `127.0.0.1`，loginSystemCode 取 systemCode，originSystemCode 取受信现场配置或空字符串。verifyCode 为 `MD5(qrCodeKey + qrCodeValue + loginClientIp + loginSystemCode + originSystemCode)`，按 UTF-8 编码后输出小写十六进制。头为 Content-Type application/json、Origin=baseUrl、Referer=baseUrl加尾部斜线、X-Requested-With=XMLHttpRequest。
3. 先要求 HTTP 2xx，外层 `String(code.code ?? code)=0000`。bo.code=4002 为 waiting，1002 为 scanned，继续轮询且不消费挑战。bo.code=0000 时提取 `String(other.account || other.empNo || '').trim()` 为工号，`String(other.token || other.authValue || '').trim()` 为 token。工号须非空且不超过128字符，token须非空且不超过16384字符。成功码缺任一凭据即失败，记录 missing_token/missing_emp_no 分类并终结本次尝试。其他状态码为上游拒绝，HTTP错误、解析错误和超时分别记分类。waiting/scanned 时暂未返回 token 属于等待状态，到期仍无 token 则过期失败。

扫码取得token和工号即完成UAC身份认证。IAM原子消费绑定挑战后按既有成员策略建立会话，显示名称读取已关联的平台成员资料，缺失时显示成员ID。初次登录阶段的部门服务调用次数必须为0。登录成功后的一个完整复验周期内，该会话的UAC认证直接视为有效，平台会话自身期限、用户退出及成员/身份源停用仍独立执行。

### 2.3 首次成员关联

主键使用 `(tenantId, providerId, verifiedSubject)` 唯一约束，verifiedSubject 为绑定挑战的成功扫码响应中取得的员工号字符串，保留前导零与大小写，仅去首尾空白。

- 已绑定身份直接解析原成员，先检查成员在职/启用与 provider 状态。
- 预置且已由管理员核实的员工号可以关联同租户唯一成员。任意团队账号的自填员工号不能用于自动接管。
- 邀请必须仍有效、属于同租户、用途匹配、未消费，且受邀员工号与已验证主体相同。邀请与身份关联在 org 策略授权及 IAM 唯一约束下原子协调，重试返回同一成员。
- 已有团队账号的用户可从已认证账号发起 purpose=link，再验证 UAC。双方认证均须新鲜，保留账号原角色，不根据上游 role/department 提权。
- 无唯一可信映射和有效邀请时拒绝注册，展示申请邀请。禁止按姓名、邮箱模糊合并。重复、跨租户或员工号回收冲突进入管理员处理，审计只记最小主体标识。

### 2.4 复验与撤销

IAM加密保存登录时取得的token和工号。在建立会话的同一数据库事务中持久化authenticatedAt、intervalSeconds和nextRevalidateAt，计算式为 `nextRevalidateAt = authenticatedAt + intervalSeconds`，时间为服务端可信UTC。该记录绑定sessionId、tenant、provider及当前认证代次。初次周期从实际建立会话的登录时间开始，期间该会话的部门查询次数为0。

复验调度只保留一个部署配置，由安装管理员在IAM的application.yaml中设置，作用于该部署全部UAC身份源。`auriga.iam.uac.revalidation.interval-seconds` 默认7200秒，来源为 `oclaw/dsh-team/lib/uds/session-revalidation.js:15` 的 `UDS_TOKEN_CHECK_INTERVAL_MS = 2 * 60 * 60 * 1000`，换算为7200秒。合法值为整数60至86400秒，含边界。配置省略时使用默认值，非整数、负数和超界均拒绝启动并记录配置键。

选择部署配置是因为周期控制IAM后台调度，由运维统一调整便于多副本协调。修改通过部署配置更新及正常IAM滚动重启生效，多副本使用同一配置修订。既有会话已持久化的nextRevalidateAt保持原值，新会话及一次复验成功后的后续周期使用当时生效的配置，并持久化所用intervalSeconds及配置修订。重启时直接读取存储的到期时间，禁止用进程启动时间重算期限。

调度器启动后和运行中只选择 `nextRevalidateAt <= now` 的有效会话。未来到期会话保持原时间，不能提前查询。到期会话按nextRevalidateAt升序，每批最多50条，使用数据库租约领取，全部署同一provider最多4个并发查询。同一tenant/provider、token哈希和工号的并行到期会话可以合并本轮查询，结果仅应用于已到期且认证代次未变的会话。

到期后的发起时间加入0至30秒稳定散列抖动，取sessionId与原nextRevalidateAt的哈希确定偏移。批量领取时同时检查原到期时间与该发起时间均已到达，领取成功后才占查询槽。积压按最早到期优先继续分批，观测dueLagMs和积压量。排队、领取和重启均保留原到期标记，成功查询前不得预写新的nextRevalidateAt。租约期间崩溃由其他工作者到期接管，结果提交需CAS检查session与认证代次，退出或重登后的旧结果丢弃。

复验使用第2.4.1节查询，取得唯一工号严格匹配且非空的部门才成功。成功时持久化lastRevalidatedAt，并原子更新 `nextRevalidateAt = lastRevalidatedAt + intervalSeconds`。该截止时间跨服务重启保留，复验成功不会延长平台会话或访问令牌自身期限。

单次查不到部门立即判定token失效并撤销会话，包括HTTP/业务拒绝、无结果、员工号不符、重复匹配、空部门、超时、网络失败和畸形响应。日志分别记录原因，界面区分上游不可达与凭据拒绝。按用户要求，单次查询拿不到部门，包括超时、网络失败，就判定失效。这会导致UDS短暂不可达时全员被退出登录，这是已知取舍，现场日志会重点观察，以后有更好的方案再改。

失效立即停止关联平台能力并撤销对应session/device grant，原子提交权威状态及outbox，传播到edge、外壳和旧HTTP/WS连接。撤销失败须重试和告警，本地先停平台能力，旧结果不能复活新身份。用户重新扫码取得token和工号后建立新会话及其新的nextRevalidateAt。

本地owner会话、私人内容、个人模型及本地工具继续可用。共享资源按第5节guardian协议停用和隔离。Auriga退出撤销本入口session及对应本机设备授权，门户与管理台分别退出。来源停用、成员冻结同样触发对应平台会话撤销。

### 2.4.1 到期部门查询协议

仅在会话nextRevalidateAt已到期并获调度租约后调用 `POST <受信资料源>/zte-km-icenter-addresearch/user/plain/docs/search`，超时3秒、响应最多2MiB。头为Content-Type application/json;charset=UTF-8、X-Emp-No=工号、X-Auth-Value=token，Origin/Referer同UAC源。JSON body为 `{employeeShortId: empNo, enableLabel: true, keyword: empNo}`。直连使用独立Agent，沿原型绕开环境HTTP_PROXY/HTTPS_PROXY并遵循受信出口配置。

HTTP须为2xx，业务码 `code.code ?? code` 为字符串0000、数字0或字符串0。列表按优先级读取bo数组、bo.rows数组、bo.list数组，其他结构视为无结果。行员工号按 `employeeShortId || employeeNO || empUIID || empNo` 取首个非空值并trim，与该会话扫码工号严格相等，恰好一条匹配才继续。保留主体administrator和以双下划线开头的主体拒绝。

department按 `deptFullName || deptName || deptShortName || orgNamePath || orgName || department` 取值并trim，须为非空字符串。姓名、邮箱和电话不参与复验。员工号不符、重复匹配、空列表、空部门、非法结构及业务失败均触发会话撤销。查询返回的部门内容只用于本次失效判定，日志仅记录departmentPresent、匹配条数及字段分支。

### 2.5 替身验收与现场日志重点观察项

测试使用随机回环端口的协议替身，由测试显式注入transport，使用合成token和工号，结束关闭服务。登录替身覆盖waiting/scanned/success/denied/expired、成功码缺token或工号、错误业务码、HTTP错误、畸形及超大响应、并发complete、响应丢失、停用与到期晚响应。成功扫码后立即建立会话，即使部门服务被配置为不可达也能完成登录，并断言登录期间部门查询次数为0。

采用可控时钟验证首次周期内零部门查询，nextRevalidateAt持久化，重启仅领取到期会话，到期边界、批次抖动、多副本租约、查询成功后推进期限和失败后撤销。复验替身覆盖唯一匹配部门、工号不符/多条、空部门、HTTP/业务错误、超时、网络失败及畸形响应。每种失败立即撤销，重试只用于撤销传播。日志断言分别核对登录链与复验链，真实UDS联调不作为设计及后续迁移实现的验收门禁。

现场部署后重点观察以下项目，用户回传脱敏诊断包后据事实调整。

- 挑战创建时间、TTL、轮询次数，waiting/scanned/成功码序列，超时阶段和响应长度。
- verify.serv路径、HTTP状态和业务码，systemCode配置修订、originSystemCode是否为空、MD5输入字段存在性。日志省略实际签名输入和二维码坐标。
- 登录时token/工号字段的实际分支、token哈希前缀及扫码耗时。
- 资料源标识及路径、HTTP/业务结果、列表分支、匹配数量、dept字段分支与departmentPresent。日志省略姓名、部门全称和完整响应中的个人值。
- 实际复验周期、nextRevalidateAt、重启领取的到期会话、批量抖动与dueLagMs、失效原因、撤销传播时间，多副本重复或晚响应情况。
- 手机目标系统展示差异、员工号变更/回收、网络出口/代理/TLS故障、响应结构变化与速率限制。观测资料作为现场修正依据。

## 3. 登录页归属与迁移

### 3.1 云端门户和 edge

门户提供统一登录入口，身份源选择包含团队账号和已启用 UAC。表单渲染器先迁到 web/shared，再解除 portal/admin 对插件源码依赖。UAC 使用新增协议，不虚构两位码。管理台可复用渲染器，但登录事务与管理权限独立。

门户成功后复用 `POST /api/workspace/v1/me/instance/open`、就绪等待和 `/open/{id}`。edge 校验本人实例、一次性票据、实例 cookie、权威 session，并注入签名身份头。托管插件保留身份头验签、准入与当前会话读取，删除第二套托管登录界面。会话失效由外壳引导回登录入口，插件显示“正在恢复平台会话”或服务异常。托管插件永远不呈现可跳过的未登录状态。

domain 与纯 IP 部署均沿现有入口契约。IP 同源实例保留已知弱隔离，端口无法隔离 cookie。edge 转实例剥离全部平台凭据，管理写请求保持独立 Origin 与 CSRF 校验。

### 3.2 Desktop 外壳

采用原生外壳窗口承载登录页，使用窗口导航 API 和受限原生桥交付启动能力。Desktop guardian 生命周期拥有一个认证 broker，启动器先启动 broker，再打开外壳登录页或 DSH。外壳可提供 loopback 登录页供其受控窗口承载，凭据和认证逻辑由 broker 承担。Electron renderer 不开启 Node 能力，不加载远程脚本。现有 guardian 资源停用职责与认证状态协调。

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

独立装到现有 DSH 的连接模式也要能从插件动作启动外壳 broker，可将最小 broker 随独立插件制品装配并用同一 OS 用户运行。缺 broker 时保留本地能力并提示安装外壳，不能退回旧插件登录页。该分发方式已按第8节决定采用。

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

已决定采用以下体验边界。从未登录且 private_only 的运行中补登录通过推送新身份生效，DSH进程继续运行，共享资源由用户后续明确激活。已有共享资源参与上下文时，退出或跨账号切换执行统一资源包协议的停止、隔离及新会话流程。

云端无法跳过。托管退出使 edge 实例 cookie、HTTP/WS 和重连认证失效，回门户登录页，默认不停止实例任务。托管共享资源变化另走既有云端可信执行器的 guardian 等价流程。

### 5.1 管理台配置与会话恢复

复验周期采用第2.4节部署配置，默认7200秒，合法整数范围60至86400秒，管理台配置页提供配置位置、范围及重启生效说明。工作线E维护该说明，工作线A负责IAM配置绑定、会话nextRevalidateAt持久化及到期调度。UDS临时故障使已到期会话的单次部门查询失败时立即撤销平台会话，用户在外壳重新扫码登录，按新登录时间重新安排nextRevalidateAt，本地DSH权限沿本节处理。

## 6. 安全与验收要求

挑战绑定 cookie、完整 Origin、tenant、audience、provider 修订和 TTL，多副本原子消费，公开二维码内容不承担轮询授权。complete/取消采用写请求，严格 Origin 和事务秘密校验，已认证管理操作再校现有 CSRF。GET 状态无登录副作用，校验绑定 cookie、受信 Host、同源 Fetch Metadata，并在 Origin 存在时核精确值。

令牌仅在 IAM/edge/外壳等受信服务端保管，插件后端通过能力代理继承身份，网页只接收脱敏状态。日志中的token仅记录SHA-256哈希前缀，cookie、设备码、verifier、二维码坐标和URL query均不记录。UDS响应仅按第9节在详细开关开启时记录脱敏结构，原始字节不落盘。身份类日志保留员工号与内部ID，姓名、部门全称、邮箱、电话省略，二维码与认证页面禁止第三方遥测和缓存。

实现验收必须覆盖跨源/跨用户管道访问、DNS rebinding、CSRF、登录 CSRF、跨 audience/tenant 事务交换、挑战重放、并发消费、员工号冒认、多义关联、邀请重复消费、source停用、token过期/吊销、晚响应与外壳重启。刷新请求结果不确定时沿现有旋转令牌围栏要求重登，只在能证明未发出请求时安全重试。

云端从正常门户完成团队/UAC登录、实例准备、进入DSH及退出后旧HTTP/WS拒绝。Desktop 从正式启动器验证跳过、本地正常使用、侧栏拉起登录、无重启状态更新、多窗口、外壳崩溃和断网恢复。共享资源已激活时验证停止/隔离/新会话和崩溃恢复。容器healthy、源码与内部API只作局部证据。

## 7. 分期与派工边界

### 阶段一，迁移基线与契约

以第2.2.1节现场可用实现和第8节已决定事项为基线，补全受限broker平台业务操作契约。所有新增契约经非作者A级独审，精确HEAD完整CI合格后交主控处理合并。UAC相关各线按现有实现迁移，以替身加日志断言验收。现场日志回传用于后续修正。

### 工作线 A，IAM UAC 身份源

文件范围为 services/iam/api、impl 的连接器/登录/会话，企业适配器模块，以及 services/org 的成员关联策略接口。contracts 的进一步变动由主控统一协调。依赖阶段一。验收包括三语言生成、严格员工映射/邀请关联、复验/撤销、共享存储并发消费、RLS和跨租户负例。复验调度测试覆盖第2.4节唯一周期配置的默认值、上下界、非整数拒绝，以及配置修订和滚动重启生效。可控时钟验证nextRevalidateAt持久化、登录周期内零查询、仅领取到期会话、重启保留期限、抖动分批、租约崩溃接管及成功后更新期限。配置与邀请提交失败可安全重试，单个挑战最多创建一个session。按最新流程迁移挑战与扫码取token/工号，部门查询仅在到期复验链执行，并以替身逐项断言调用次数、日志与脱敏。A同时负责第9节IAM日志、安全诊断序列化器、audit和OpsSignals接线，以及infra/ctl诊断导出命令和deploy/compose、deploy/nas观测文件的必要接线。导出单独对接现有audit读取与Loki受信查询，避免新增公网日志采集入口。

### 工作线 B，门户登录和 edge

范围为 web/portal/src/native-login、原门户登录入口、web/shared 渲染器与 API，infra/edge 仅调整路由/新事务保护接线。负责迁出插件 UI 的唯一写入，管理台与 Desktop 消费它。依赖 A 的 API，可先用替身。验收双入口audience隔离、二维码状态/取消/过期、额外验证、正常本人实例准备与进入、退出后HTTP/WS失效，domain/IP两模式。UAC分支接同一现场迁移替身，并核对页面所见correlationId与日志一致，错误响应无上游秘密。

### 工作线 C，Desktop 外壳认证

范围为 clients/installer/runtime、windows/launcher、打包脚本与新 shell-auth 模块，guardian 认证状态接线。依赖 A 及 ShellAuth/业务代理契约，B 提供共享 UI。验收正式安装包正常启动入口、DPAPI/ACL、跨用户拒绝、cookie/CSRF、S256、人工设备确认、令牌不入页面、日志只记哈希前缀、随机端口清理、多窗口及更新后凭据恢复。与资源guardian共享单一状态协调，不新增并行停用写者。C按第9节输出本机壳登录、身份推送和停权日志，复用本机脱敏sink，UAC验收使用替身并断言跨端关联。

### 工作线 D，插件继承身份并删除旧登录

范围为 clients/dsh-plugin/src/login、identity/local/managed 的平台分支和 web 设置/侧栏，现有资源与模型适配层只作必要调用迁移。依赖 C 的状态与业务代理、B 的 UI 迁出提交。验收旧平台登录页和路由确实删除，托管无未登录页，未配置/跳过/离线的真实本地 DSH 可用，稍后登录推送生效，所有原平台功能走受限代理，旧凭据迁移与旧异步请求围栏完整。D校验broker事件与本地状态诊断，包中采用相同关联字段，禁止插件重新获取UAC token。

### 工作线 E，管理台配置

范围为 web/admin/src/views/IdentityProvidersView.vue、admin/providersApi、providersForm、providersMessages 与对应测试。依赖 A 配置 API、B 共享 UI。管理台说明唯一复验周期配置默认7200秒及第2.4节部署配置的合法范围，工作线A负责实际配置绑定。验收开关/baseUrl/systemCode、ETag冲突、权限和CSRF、启用前错误提示、关闭后会话失效，普通成员不能读取或修改配置。E负责配置页的详细诊断开关、自动到期状态与correlationId展示，CLI作为一键导出入口，页面提供对应操作说明。实现派工时先为诊断开关新增独立配置契约并生成类型，保持本稿既有UAC必填字段和登录事务语义。测试断言开关默认关闭、脱敏响应和权限拒绝。

每线经针对性测试形成可部署产物通知主控，由主控串行安排开发环境部署与正常入口验收。本稿交付物是可审阅文件，实施部署属于后续派工。

## 8. 已决定

按任务第2轮“7项按推荐”的指令固化，理由随决定保留。

1. 首次成员关联仅允许管理员核实员工号、双认证绑定或有效邀请，默认关闭自动入团队注册，避免自填员工号接管既有账号。
2. UAC Desktop首期沿现有RFC8628人工核码，沿当前受限设备授权边界取得device-only凭据。
3. 扫码取得token和工号即完成UAC登录，复验周期默认7200秒，可通过第2.4节部署配置调整。每个会话持久化nextRevalidateAt，到期后用保存的token和工号查部门，单次查不到部门立即撤销平台会话。服务重启仅处理已到期会话，日志分别记录网络与业务原因，本地DSH能力保持可用。
4. 已激活共享内容的退出/跨身份切换采用受控停止、隔离和新会话，避免旧身份内容残留在上下文。普通补登录通过快照推送生效。
5. 独立插件制品同包提供最小外壳broker，支持现有DSH独立安装且将登录页归外壳。
6. 企业适配器留企业模块，公共仓库保留通用Uac/ShellAuth契约及示例配置，符合身份源架构与公开仓库规范。
7. UAC先限定受信团队试用，外壳明确展示目标系统名。手机展示与抗转发行为通过现场日志和用户观察反馈改进，沿现有发起端绑定与人工设备核码控制风险。

## 9. 日志与一键导出诊断包

本节作为A至E各实现线的硬性要求。日志和诊断导出共用安全序列化器，先脱敏再交给logger、outbox或文件writer。诊断故障不改变认证判定，写入失败通过现有安全日志分类与ops信号可见。

### 9.1 登录、复验与撤销结构化日志

每个实际执行阶段开始和结束均输出JSON事件，phase为start/end。登录链的stage只有challenge_start、scan_status、token_acquire，department_lookup只出现在复验链中，隶属于session_revalidate。session_revoke用于退出、停用和复验失败后的撤销。每条事件增加chain字段，值为login、revalidation或revocation。token_acquire是verify.serv成功响应的本地解析阶段，HTTP元数据引用对应pollId。上游终态未取得token时也写token_acquire失败，waiting/scanned阶段继续等待。waiting/scanned也逐轮记end，不以去重丢掉阶段终态。停止轮询、撤销排队和完成各有结果，耗时用单调计时。

每条必须包含以下字段，无法取得的值显式为null。

- timestamp为UTC RFC3339，service、level、stage、phase、result，durationMs为本阶段耗时，start事件为0。
- correlationId为我方生成的本次认证/复验操作ID，eventId唯一，transactionId在存在时记录，pollId/attempt区分轮询与重试，parentCorrelationId连接复验、撤销或跨壳操作。
- trace_id、spanId沿docs/dev/observability.md接线，requestId沿现有edge/common链。后台复验创建新trace，显式关联已存操作ID，不复用过期HTTP span。对UDS的出站不添加内部身份或秘密追踪头。
- tenantId、providerId、configRevision、empNo及chain，tokenHashPrefix在token存在时记录，tokenMissing仅用于登录凭据解析诊断。部门诊断仅在复验事件中记录departmentPresent、匹配条数和字段分支。会话创建与复验事件记录nextRevalidateAt、实际intervalSeconds，调度事件记录dueLagMs。所有这些是JSON字段，禁止作为Loki高基数标签。
- endpoint为固定路由模板和受信源别名，httpStatus、udsCode、udsSubCode、udsMessage、transportError、responseBytes。UDS错误消息先按第9.2节脱敏。challenge_start等无HTTP阶段保持httpStatus/udsCode为null，token_acquire可继承本轮上游HTTP状态。

阶段结果为success、waiting、scanned、failure、expired、cancelled。撤销日志区分requested/committed/propagated，记录目标epoch或修订与关闭连接结果，确保能判断“已判失效但停权尚未完成”。首个失败原因保留，后续重试另写attempt。正常阶段INFO，失败WARN，意外内部异常ERROR，详细原始响应结构DEBUG。

### 9.2 脱敏和详细级别

工号按用户授权可记录。token只记UTF-8原值SHA-256的小写十六进制前12位，同一token跨取回、部门查询、复验可关联。无token时hash字段为null。哈希前缀仅用于诊断。cookie、Authorization、X-Auth-Value、设备码、verifier、挑战key/value/binding、完整URL查询串、证书私钥与密钥全部屏蔽。

采用最小必要原则，正常和详细模式都省略姓名、部门全称、组织全称、邮箱、电话，复验事件的departmentPresent及字段分支足够判断该次结果。未知响应字段的值默认替换为[REDACTED]，只保留已知字段键与容器结构，未知键若含个人值或凭据也替换为固定占位键并记录计数。UDS code字段经长度和格式白名单后保留，若与已知秘密或个人值重合则屏蔽。udsMessage保留已知安全字面量映射，其余记[REDACTED]及messageRedacted=true，避免上游把姓名或token嵌进错误消息。换行/控制字符转义，消息最多512字节。

详细开关名拟定为auriga.iam.uac.diagnostics.detailed，默认false，按provider和当前租户限定，启用时记录expiresAt，默认15分钟自动关闭。管理台需iam.providers.write和现有管理Origin/CSRF校验，开启/关闭均记audit。全局AURIGA_LOG_LEVEL与该开关共同控制专用详细sink，开关启用时该sink可输出DEBUG而无需提高全局等级。重启后回到关闭，明确启用只对后续请求生效。

开关开启时，在DEBUG事件增加udsRawSanitized，记录解析后的UDS原始响应结构。先递归处理大小写/别名敏感键及所有秘密匹配，再对未知值屏蔽，只保留经过允许列表验证的业务码、已脱敏消息、列表数量和必要字段存在性。token值一律替换为[REDACTED]，哈希前缀只在固定字段出现。HTML、非JSON、重复键、深度超限或不能安全解析的响应仅记内容类型、字节数和原因，不回显body。单次处理上限2MiB、单事件上限64KiB，超出记truncated及数量，禁止先将原文写临时文件后再清洗。

脱敏测试须将合成token、工号、姓名、部门全称、邮箱和电话植入header、嵌套对象、数组、未知字段、异常消息与转义字符串，断言允许的工号和token哈希之外无原值泄漏。日志写入、采集与导出前各自执行脱敏，采集端脱敏只作为补充。

### 9.3 audit、ops与Alloy/Loki接线

按现有contracts/events/audit.schema.json写audit.recorded，经common审计写入器和outbox进入auriga.audit.iam，交audit收集。各实际执行阶段结束写一条最小审计投影，action拟定为iam.uac.challenge_start、iam.uac.scan_status、iam.uac.token_acquire、iam.uac.department_lookup、iam.uac.session_revalidate、iam.uac.session_revoke，result按既有success/failure/denied映射。等待轮询投影标result=success并在details.outcome写waiting/scanned，不表示认证成功。身份未建立时actor使用type=system、id=null，details只含上述脱敏诊断字段。数据库事务外执行上游HTTP，结果审计在短事务写outbox，outbox失败留同eventId供可靠重试。详细响应结构只进受控运维日志。iam.uac.department_lookup只由到期复验产生，登录审计的chain=login不含部门查询阶段。

IAM/edge在现有JSON stdout输出阶段日志，NAS与compose的observability/config.alloy已按compose project发现容器并送Loki。沿用service/container/job低基数标签，以JSON字段查询correlationId或trace_id。Desktop broker复用本机Auriga脱敏日志sink，托管插件沿现有容器日志或auriga-dsh-*.jsonl只读导入点采集，命名及轮转沿docs/dev/observability.md。Desktop日志仅在用户主动导出时打包到诊断包。链路验证分别断言本地落盘、Alloy接收、Loki查询与缺源提示。

IAM在复验失败、上游持续不可达、撤销传播失败时通过既有OpsSignals.observe按tenant/provider聚合告警，恢复时写active=false，source拟定为iam_uac，subject为providerId。连续不可达60秒或撤销传播30秒未完成触发warning，认证失效立即执行，与告警阈值分别处理。audit记录所有失败及恢复，ops不按工号/token建告警。沿既有去重、静默与通知渠道，消息只含分类和关联ID。

### 9.4 一键导出入口

首期选择CLI入口，集成infra/ctl现有命令分发。拟定单命令为 `aurigactl diagnostics export --component uac --since 2h --correlation-id <id> --output <目录>`，correlation-id可省略。该命令是后续实现接口，本轮仅交设计。平台侧由安装机已授权运维用户运行并读部署受信配置，凭据从既有受保护文件或会话读取，禁止放argv或包内。Desktop同命令可用local范围仅导出当前OS用户broker/插件日志。管理台展示correlationId、详细开关和CLI操作说明。

一次执行完成范围校验、audit最小投影读取、Loki受信查询、本机受控日志读取、再次脱敏、清单校验和压缩。仅从受信配置取得数据源地址，用户输入只选择时间窗、provider与关联ID，不接受任意查询语句、URL或文件路径作为采集源。默认两小时、最大24小时，跨租户范围拒绝。安装级运维读取也只查询已指定的单租户，不用Loki无租户日志默认放大范围。现有audit读取权限由身份核验，安装本机数据源访问属于已授权运维边界，导出动作记录操作者和范围。

包名为uac-diagnostics-<UTC>-<随机ID>.zip，内容包括manifest.json、uac-steps.jsonl、audit.jsonl、ops-alerts.json、sanitized-config.json和可选的uds-detail.jsonl。manifest包含版本、时间窗、安装/组件版本、脱敏器版本、来源清单、各文件SHA-256、行数、缺源、截断与导出审计ID。配置只含源别名、固定接口路径、systemCode存在性、originSystemCode是否为空、复验周期的生效秒数及修订、脱敏会话的nextRevalidateAt和调度积压统计，省略token、真实凭据和个人目录路径。包中不得包含DSH journal、私有会话、vault、用户资源或完整环境。

先在当前用户独享临时目录生成已经脱敏的条目，拒绝符号链接/重解析点和输出覆盖，再扫描秘密与个人字段，生成摘要后原子发布最终zip。日志损坏或无法安全脱敏时省略该条并记录计数，不输出原文。临时目录受用户ACL/0700保护，完成或失败后清理。单包上限50MiB，超限按时间分段或标明确截断，输出partial=true并以非零退出码提示缺源，绝不以空包声称成功。关闭详细开关时包里仍有已执行的登录、复验和撤销阶段基础信息。

诊断包保存到用户指定目录，现场用户主动发回。导出过程仅访问受信本地/部署观测源，默认不上传。

### 9.5 日志与诊断包验收

A与E交付前用同一替身分别测试登录链、到期复验链和撤销链。扫码成功时部门接口即使不可达也完成登录，chain=login只有challenge_start、scan_status、token_acquire且部门接口调用计数为0。复验前推进可控时钟，断言未到期及重启后的未来到期会话均无部门查询，已到期会话才出现chain=revalidation的session_revalidate和department_lookup。复验使用独立correlationId，通过parentCorrelationId与原登录关联，部门查询不复用登录pollId。初次登录缺token、复验查不到部门、HTTP错误/超时、撤销重试及乱序/重复轮询均断言字段、状态和调用次数。

详细开关默认关闭、启用/自动到期/重启关闭均需测试，关闭时无udsRawSanitized。开启时字段结构可诊断，任意嵌套秘密和个人值不可见。audit和ops必须断言实际接收的脱敏字段，Alloy/Loki局部测试使用本机替身或隔离采集器验证配置和查询，不访问真实部署。

一键命令覆盖完整包、Loki缺源、audit拒绝、日志畸形、超过时间窗/大小上限、目录越界/符号链接、详细关闭以及重复执行，检查清单摘要与秘密扫描。真实UDS联调不要求，报告保留替身响应、日志断言、采集链证据及缺源状态。
