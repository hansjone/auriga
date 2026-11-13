# UAC 身份源与外壳登录设计草案

## 1. 范围与依据

本稿基于 `51beea24b3e42619f8d8b5879ba158176235fa87`，第11轮按用户决定采用单IP、单域名部署，沿用企业凭据读取预留向本人会话的skill提供最新企业登录材料，并保留SSO第一期和Desktop系统浏览器设备授权路径，保留第8轮token-verify默认策略及N1–N4故障分类，保留第6轮独审返工的Desktop、身份源写入、设备绑定及交付接线，沿用扫码现场实现和已定产品决定，供后续拆分实现。契约新增项均为草案，尚无运行时接线。审查等级 A，涉及 contracts，交主控标记【待审查】。

本轮的用户要求是 UAC 的SSO免扫码优先、扫码降级，与团队账号并列，登录页归云端门户或 Desktop 外壳，插件继承身份。Desktop 可跳过并稍后登录，平台认证失败不得封锁本地 DSH。云端托管入口要求登录。桌面登录仅认证并返回当前 DSH，进入本人云端实例由门户现有工作空间流程负责。

依据为任务文件、`docs/architecture/identity.zh.md`、`contracts/README.md`、`contracts/deployment/README.md`、`contracts/plugin/local-login.md`、`contracts/resource/unified-protocol.md`，以及 memwiki 的《Auriga 工作台产品设计》《DSH 宿主兼容与连接模式认证》。公开文档中的地址均使用示例域名。企业真实地址只进入部署配置。

### 1.1 现场协议与公开文档边界

登录页服务端优先检查浏览器SSO Cookie并验证，缺失或验证不通过则显示扫码。设备确认页先要求用户显式继续。扫码取得token和工号即完成UAC登录。两种方式登录成功后，均执行一次purpose=login_info的部门查询用于展示，失败只记WARN。到期的purpose=revalidate按部署策略执行：默认token-verify验证token，department部门查询保留为可切换备用策略。本设计保留用户最终规则，验收使用替身与日志断言。

真实接口主机、完整路径和系统标识只存部署或企业适配器私有配置，扫码与部门查询的报文编解码细节也保留在私有适配器。公开仓库的地址只使用 `<qrVerifyPath>`、`<userSearchUrl>`、`<tokenVerifyUrl>` 占位符；token-verify按第2.4.3节公开通用字段名、请求头及摘要算法，不包含真实地址、注册编码或凭据。适配器将本稿Uac挑战与上游报文互转，返回waiting/scanned、token、empNo和部门信息。来源文件、逐字段核对及原型与新规则差异记入未提交执行报告，企业专用细节不复制到公共契约。

第7轮还只读核对了内部UAC接入资料：其入口依赖公司域SSO Cookie，与现场扫码属于不同接入方式。资料中的真实主机、路径、Cookie名称和系统编码均不进入公开仓库。SSO Cookie名称及Portal/UDS体系字段映射按该资料放企业适配器私有配置，公开仓库只描述角色与优先级。用户已确认扫码取得的token被token验证接口接受，因此默认采用token-verify；clientIP取值、失败码及返回用户标识格式等继续按第2.5节现场观察。

通用边界保持HTTPS、受信地址允许列表、禁止重定向、15秒扫码请求超时、3秒资料请求超时、2MiB响应上限、按各协议限定的工号比对及120秒挑战有效期。企业部署通过私有映射延续现场可用协议。

### 1.2 当前 Auriga 接缝

IAM 已有 IdentityConnector 的 descriptor/validateConfig/begin/advance、身份源管理、登录事务 cookie 围栏、会话与邀请策略。现有 `nativeConnect` 只适用于登记的第一方团队账号加 TOTP，不包含 UAC。

门户 `src/native-login/page.ts` 当前从插件导入渲染器，主要支持表单，认证后调用现有本人实例 open，进入 `/open/{id}`。管理台有独立 LoginView 与 IdentityProvidersView。两入口的 audience、cookie、权限与 step-up 分离。

插件 `src/login/` 有视图和本地控制器，`src/local/` 持有平台认证流程与凭据，`src/identity/` 同时承担本地 owner/standalone 准入，`src/managed/` 承担托管验签、门禁与会话。Desktop 的 `runtime/core.mjs`、`cli.mjs`、Windows launcher 已有启动及升级生命周期，新增外壳认证服务需接入这些正常入口。

## 2. IAM UAC 身份源

### 2.1 配置与协议边界

连接器类型取 `uac`，与团队账号并列。通用步骤调度、成员策略、session、审计仍归 IAM。企业适配器通过既有连接器机制装配，企业地址及专用代码留在企业模块。管理台为已有 provider 提供 enabled、baseUrl、systemCode 配置。提供商的名称、顺序、创建和删除继续走现有管理接口。

baseUrl 必须 HTTPS、无 userinfo/query/fragment，按部署受信允许列表校验 hostname、端口与解析地址，拒绝重定向。上游可能位于企业私网，允许列表由部署管理员明确登记范围。管理台不可任意开启内网访问。配置更新执行既有 iam.providers.write、ctx=admin、Origin/CSRF 和 ETag。enabled=false 立即阻止新挑战并撤销该源事务及关联平台会话和设备授权。

UAC配置只有 `/admin/providers/{providerId}/uac` 一个enabled/config写入口，底层复用同一Provider行、修订和ETag。通用PATCH含enabled或config时对UAC类型返回400 common.validation_failed，元数据变更仍可走通用入口。通用创建UAC类型时省略enabled取false，显式true或非空config拒绝，通用创建必须带config={}以满足ProviderInput必填，建立未配置占位实例时暂不执行完整连接器配置校验，随后唯一UAC写入口执行完整验证。未配置时UAC GET返回404，E通过通用Provider读取ETag后初始化关闭的配置表单，再经UAC接口配置并启用。其他连接器沿通用默认true，UAC界面明确默认关闭，不能将通用生成模型默认值当作UAC的服务器授权。实现保留原JSON字段存在性区分省略与显式值。

baseUrl与systemCode由UAC管理接口保存，真实值只在受信部署数据库/企业配置中。完整扫码路径、资料URL、其他系统标识及扫码/部门查询报文字段映射由企业适配器私有配置注入。公开示例仅用占位符。通用配置读取与UAC读取使用同一事实源，停用与撤销在同一权威事务协调。

新增 `/login/uac/challenges` API 明确区分 UAC 二维码。现有 QrStep 要求手机输入 matchCode，上游原型缺此交互，因此本稿新增 Uac schema，保留既有步骤完整定义。

### 2.2 挑战生命周期

1. 外壳发现已启用身份源，用户选择UAC。创建请求purpose默认login，可匿名。purpose=link要求同入口已有user类型portal/admin会话且10分钟内完成新鲜认证，设备和服务账号凭据拒绝，绑定该账号、sessionId和认证代次，缺会话返回401，新鲜度不足或主体变化拒绝；不得降级成匿名login。可信入口解析tenant、规范Origin及portal/admin audience，按原事务cookie保护挑战。邀请代码只用于login，link与invitationCode同时提交拒绝。
2. 返回 UacChallenge 的公开扫码内容和有效期。页面不持有 UAC token、浏览器绑定秘密或 PKCE verifier。二维码过期后只能新建，刷新会取消旧事务。TTL 固定120秒，现场差异由关联日志定位后调整。
3. IAM 后台有界轮询上游，同一挑战单飞，按两秒起步和上游限流退避。GET 状态只读取本地事务，不执行登录或上游副作用。多副本用共享持久存储及 CAS，进程内 Map 不作为生产唯一存储。
4. 手机确认后，IAM按第2.2.1节取得非空token和工号，HTTP/业务成功且挑战绑定有效时写入已验证身份。扫码验证成功即完成UAC认证。
5. 扫码校验成功写verified。发起端以绑定cookie和精确Origin调complete，原子消费挑战并执行现有成员策略，必要时继续MFA。会话建立时同事务持久化authenticatedAt和nextRevalidateAt，按第2.4节从登录时间起安排到期复验。
6. complete的唯一消费记录与会话创建同事务提交，响应丢失可在原绑定端读取既有完成结果。登录成功后排队一次purpose=login_info部门查询，成功记录展示资料，失败只记分类WARN。信息查询不阻塞成功响应，不撤销会话，也不更改nextRevalidateAt。晚到上游响应必须匹配会话与认证代次。

首期固定采用轮询。手机目标系统展示与扫码状态作为现场日志观察项。接入范围限定受信团队，外壳明确展示目标系统名，保留挑战发起端绑定和人工设备授权核码。二维码转发的诱导扫码风险纳入试用提示与诊断，不增加 UDS 团队确认门禁。

### 2.2.1 扫码登录迁移协议

发起挑战在IAM本地生成两份各16字节随机坐标，另有32字节发起端绑定秘密，仅存其哈希。二维码内容由企业适配器编码，保持上游现场兼容，IAM向页面仅返回用于显示的qrContent。TTL为120秒，绑定provider修订、事务purpose、入口与发起端。

IAM调用受信baseUrl加 `<qrVerifyPath>`，私有适配器注入系统标识、报文字段和上游格式需要的校验值。HTTP须为2xx，再按已核对的私有映射判断waiting、scanned或成功。等待状态不消费挑战，成功需同时取得非空token和empNo，长度分别上限16384与128字符。缺失凭据、业务拒绝、非法结构及运输错误分别返回安全失败分类。取消操作返回204，保留到期前的状态墓碑供绑定端读取cancelled，解析/凭据缺失返回failed并提供failureCode，日志不回显原文。

扫码取得token和工号即完成UAC身份认证。IAM原子消费绑定挑战，按现有成员策略建立会话或完成双认证绑定；link只把已验证身份关联当前账号，不新建匿名账号，不替换浏览器现有会话主体。显示名从已关联平台成员资料读取，缺失显示成员ID。

登录成功响应直接返回，随后异步执行一次purpose=login_info部门查询。正常完整流程调用恰好1次。查询成功记录部门展示资料，失败仅记分类WARN，登录结果、会话与nextRevalidateAt保持原值。登录成功后的初次周期内不做判定性复验，平台会话期限、成员/身份源停用及用户退出仍生效。

信息作业与会话建立同事务登记，以sessionId和认证代次唯一。重复complete与状态读取复用作业，CAS领取后最多调用一次，禁止自动重试。已领取后进程崩溃恢复为unavailable并记interrupted，尚未领取可由恢复工作者执行。资料写入复核tenant/provider、sessionId和认证代次，切换或退出后的晚结果丢弃。UacLoginInfo是当前会话的只读展示投影，不含token。

### 2.3 首次成员关联

主键使用 `(tenantId, providerId, verifiedSubject)` 唯一约束，verifiedSubject 为绑定挑战的成功扫码响应中取得的员工号字符串，保留前导零与大小写，仅去首尾空白。

登录信息查询成功后，将department和更新时间记录到当前会话的可选展示资料，供外壳身份区、本人资料区显示所属部门，辅助用户辨认已登录身份。通过UacLoginInfo返回status=available及department，页面收到资料更新后刷新展示。日志和诊断包只记录departmentPresent、状态与更新时间，省略部门全称。

部门资料仅供界面展示及已关联成员的本次会话资料补充，成员关联继续依员工号、邀请或双认证完成。信息查询处于pending或失败unavailable时，界面显示“部门信息暂不可用”或省略部门行，保留已验证的平台成员显示名，缺失时显示成员ID。当前会话资料不沿用旧身份部门，不自动改写成员的持久部门、角色、组或权限。资料存储或通知失败也只记WARN并降级为unavailable。

- 已绑定身份直接解析原成员，先检查成员在职/启用与 provider 状态。
- 预置且已由管理员核实的员工号可以关联同租户唯一成员。任意团队账号的自填员工号不能用于自动接管。
- 邀请必须仍有效、属于同租户、用途匹配、未消费，且受邀员工号与已验证主体相同。邀请与身份关联在 org 策略授权及 IAM 唯一约束下原子协调，重试返回同一成员。
- 已有团队账号的用户可从已认证账号发起 purpose=link，再验证 UAC。双方认证均须新鲜，保留账号原角色，不根据上游 role/department 提权。
- 无唯一可信映射和有效邀请时拒绝注册，展示申请邀请。禁止按姓名、邮箱模糊合并。重复、跨租户或员工号回收冲突进入管理员处理，审计只记最小主体标识。

### 2.4 复验与撤销

IAM使用既有 `services/iam/impl/.../Secrets.java` 的AES-256-GCM机制保存UAC材料，AAD绑定tenant、provider和用途。密钥来自受保护的auriga.iam.encryption-key部署配置，多副本共享同一密钥，缺失拒绝启动。当前Secrets仅有单活动密钥，不能宣称已具备在线轮换。AUTH-K定义密文keyId、兼容解密密钥环与轮换步骤，A负责实现。先分发旧/新解密集，再切活动keyId，断点分批重加密并验证，确认所有密文完成后才移除旧键，回滚保留可解密材料。

IAM加密保存登录时取得的token和工号。在建立会话的同一数据库事务中持久化authenticatedAt、intervalSeconds和nextRevalidateAt，计算式为 `nextRevalidateAt = authenticatedAt + intervalSeconds`，时间为服务端可信UTC。该记录绑定sessionId、tenant、provider及当前认证代次。初次周期从实际建立会话的登录时间开始，期间不执行purpose=revalidate。登录后的purpose=login_info只补充展示资料，其成功、失败和完成时间均不改动本次会话的nextRevalidateAt。

复验配置由安装管理员在IAM的application.yaml中设置，作用于该部署全部UAC身份源。`auriga.iam.uac.revalidation.interval-seconds` 默认7200秒，默认值沿原型会话复验常量的两小时周期，核对记录见执行报告。合法值为整数60至86400秒，含边界。配置省略时使用默认值，非整数、负数和超界均拒绝启动并记录配置键。

新增同前缀配置：`auriga.iam.uac.revalidation.strategy`为token-verify（默认）或department（备用）；`auriga.iam.uac.revalidation.retry-interval-seconds`默认30，合法整数5至300；`auriga.iam.uac.revalidation.max-retries`默认3，合法整数0至10，表示首次请求以外的重试次数。缺省使用默认，未知策略及非法数值拒绝启动。后两项用于token-verify上游故障（含运输、HTTP及响应格式故障），department始终单次失败撤销。参数形成同一配置修订，重试最大等待为retry-interval-seconds乘max-retries；默认首次之后最多重试3次，约在第30、60、90秒执行，单次HTTP超时另为3秒。

选择部署配置是因为周期与策略控制IAM后台调度，由运维统一调整便于多副本协调。修改通过部署配置更新及正常IAM滚动重启生效，多副本使用同一配置修订。既有会话已持久化的nextRevalidateAt保持原值，新会话及一次复验成功后的后续周期使用当时生效的周期配置，并持久化所用intervalSeconds及配置修订。每轮到期领取时持久化本轮strategy、重试参数和配置修订，进行中的重试不随滚动更新换策略或重置次数，下一轮才采用新策略。扫码token的接口兼容性已由用户确认，默认token-verify。部署方可显式切换到备用department，其单次失败撤销的取舍不变。策略切换只影响下一轮，不能借切换清除失败预算。重启时直接读取存储的到期时间，禁止用进程启动时间重算期限。

调度器启动后和运行中只选择 `nextRevalidateAt <= now` 的有效会话。未来到期会话保持原时间，不能提前执行purpose=revalidate。信息性作业独立消费，避免混入复验到期队列。到期会话按nextRevalidateAt升序，每批最多50条，使用数据库租约领取，全部署同一provider最多4个并发查询。department策略下，同一tenant/provider、token哈希和工号的并行到期会话可以合并本轮查询，结果仅应用于已到期且认证代次未变的会话。token-verify按第2.4.3节的凭据版本串行，防止旋转竞争。

到期后的发起时间加入0至30秒稳定散列抖动，取sessionId与原nextRevalidateAt的哈希确定偏移。批量领取时同时检查原到期时间与该发起时间均已到达，领取成功后才占查询槽。积压按最早到期优先继续分批，观测dueLagMs和积压量。排队、领取和重启均保留原到期标记，成功查询前不得预写新的nextRevalidateAt。租约期间崩溃由其他工作者到期接管，结果提交需CAS检查session与认证代次，退出或重登后的旧结果丢弃。

department策略以purpose=revalidate使用第2.4.2节查询，取得唯一工号严格匹配且非空的部门才成功；token-verify按第2.4.3节判定并替换token，不要求部门查询成功。成功时持久化lastRevalidatedAt，并原子更新 `nextRevalidateAt = lastRevalidatedAt + intervalSeconds`。该截止时间跨服务重启保留，复验成功不会延长平台会话或访问令牌自身期限。

department策略判定性复验单次查不到部门立即判定token失效并撤销会话，包括HTTP/业务拒绝、无结果、员工号不符、重复匹配、空部门、超时、网络失败和畸形响应。日志分别记录原因，界面区分上游不可达与凭据拒绝。部署方选择备用department策略后，purpose=revalidate的单次查询拿不到部门，包括超时、网络失败，就判定失效。这会导致UDS短暂不可达时全员被退出登录。已激活共享资源的Desktop用户还会触发guardian停止整个DSH进程树、隔离共享内容，再以private_only重新启动，原有任务可能中断且需新会话。这是备用department策略的已知取舍，现场日志重点观察，以后有更好的方案再改。

失效立即停止关联平台能力并撤销对应session/device grant，原子提交权威状态及outbox，传播到edge、外壳和旧HTTP/WS连接。撤销失败须重试和告警，本地先停平台能力，旧结果不能复活新身份。用户重新扫码取得token和工号后建立新会话及其新的nextRevalidateAt。

本地owner会话、私人内容、个人模型及本地工具继续可用。共享资源按第5节guardian协议停用和隔离。Auriga退出撤销本入口session及对应本机设备授权，门户与管理台分别退出。来源停用、成员冻结同样触发对应平台会话撤销。

### 2.4.1 UAC来源与设备授权绑定

IAM在UAC会话创建时分配单调递增authGeneration。设备人工核码批准时，服务器从批准者权威会话读取sourceSessionId、authGeneration和providerId，持久化到grant及旋转refresh-token家族，响应与设备JWT可带只读uacSessionBinding。浏览器、插件和OAuth请求不能指定这些字段。这些关联字段在通用DTO中为可选以兼容非UAC设备，UAC来源grant若缺完整绑定必须拒绝兑换或刷新，不能降级当作非UAC授权。设备授权仍要求人工核码，扫码完成平台身份认证后还需这一步确认。

token-verify尚在有界上游故障重试预算内时，来源会话仍为有效，绑定设备可按原期限刷新，不因一次上游故障拒绝；预算耗尽或明确失效后立即拒绝。权威会话检查同时核对retryDeadline，超过截止即拒绝平台请求与刷新并排队撤销，不能等待后台调度恢复才停权。重试不得延长平台会话、设备凭据的原有最大期限。

每次初始兑换、刷新、换取AI/网络等audience凭据时，IAM重新校验来源会话未过期/撤销、认证代次相同、provider和成员可用，再验证device/client/scope。UAC复验失败、会话退出/过期、成员停用、身份源关闭均撤销绑定grant/refresh家族，旧认证代次的刷新立即拒绝。复验任务即使只从来源会话开始，也必须沿绑定索引覆盖所有设备。

关闭门户窗口或broker清理本次设备授权材料不等于服务器logout。调用服务器logout会撤销来源会话及绑定设备。成功设备授权后broker清device_code等临时材料，保留服务器来源会话至其正常期限；到期需要重新登录，device refresh不能延长来源会话上限。事件通知推动broker清材料和旧连接，通知失败时刷新端的权威检查仍拒绝，快照记录对应reason。

### 2.4.2 部门查询协议及用途

登录信息作业以purpose=login_info调用一次，复验作业在nextRevalidateAt已到期并获租约后以purpose=revalidate调用。两者共用受信私有配置的 `POST <userSearchUrl>`，超时3秒、响应最多2MiB。purpose属于我方业务上下文。token、工号及其他头/body字段由企业适配器私有映射注入，真实URL不写入公共文档；受信出口策略延续现场已验证实现。

HTTP须为2xx且企业映射的业务码成功。适配器归一化列表和员工号，工号去首尾空白并与会话扫码工号严格相等，恰好一条匹配才继续，保留主体拒绝。

department按企业适配器字段优先级归一化并trim，须为非空字符串。姓名、邮箱和电话不参与认证。员工号不符、重复匹配、空列表、空部门、非法结构及业务失败在purpose=login_info时只记分类WARN并更新展示状态为unavailable，在department策略的purpose=revalidate时触发会话撤销。成功的login_info记录部门用于第2.3节展示，成功的revalidate仅推进复验期限。日志仅记录purpose、departmentPresent、匹配条数及字段分支。

### 2.4.3 token-verify协议与有界重试

IAM通过受信私有配置的 `POST <tokenVerifyUrl>` 调用验证接口，沿用HTTPS、出口允许列表、禁止重定向、3秒超时和2MiB响应限制。请求头为Content-Type: application/json;charset=UTF-8及X-Emp-No: <account>。JSON体仅构造account、token、clientIP、systemCode、verifyCode五字段，account取会话已验证工号。适配器输入若名为clientIp，出站必须改成clientIP（大写IP）；摘要与发送体使用同一个确定字符串，空值按空字符串处理，不允许隐式null或任意浏览器输入。

`verifyCode = lowercaseHex(MD5(UTF8(account + token + clientIP + systemCode)))`，四字段按此顺序无分隔拼接，输出32位小写十六进制。MD5是对方协议规定的完整性校验字段，不作为Auriga认证或防伪的安全依据；我方仍依赖TLS、受信目的地、服务端凭据保管及会话绑定。前端展示systemCode可以与后端不同，后端必须使用部署方在UAC注册的真实编码，不能接受前端覆盖或沿用公开示例。

扫码来源的clientIP由部署私有配置决定为空字符串或固定的服务端出口地址，必须现场确认哪种被接受；SSO来源按第3.1.1节优先读取私有映射中的客户端IP资料，缺失时用部署配置。登录时验证采用的clientIP及其来源加密随凭据记录保存，后续复验使用同值，变更按配置修订显式迁移，不混用浏览器地址或任意转发头。不得采信任意X-Forwarded-For；将来采用真实客户端地址时只能经过受信代理解析。token、verifyCode、摘要拼接原文、clientIP实际值及orgId不进入普通日志。真实地址、编码和Cookie名只来自私有配置。

token-verify按以下顺序分类，先检查HTTP与响应结构，不能只凭某一个可解析code就撤销：

- 明确业务失败：只有HTTP 200、合法JSON且code.code和bo.code两层返回码齐全（均为非空字符串），其中任一不是成功码"0000"，才立即判失效并撤销，reason=revalidation_rejected、failureClass=business_rejected。不硬编码某个特定失败码。
- 上游故障：HTTP 3xx、4xx（包括401/403/429）、5xx、超时、网络错误、非JSON/畸形JSON、超大响应、缺少任一层返回码或码类型非法，一律不立即判失效，failureClass=upstream_fault，进入下述同一有界重试预算。非200即使body带失败码也属于上游故障；禁止跟随重定向，不无限遵从Retry-After。
- 成功候选：HTTP 200、合法JSON且 `code.code == "0000"`、`bo.code == "0000"`同时成立后，再校验bo.user和bo.token。bo.user必须为字符串、去首尾空白后非空且不超过128字符，bo.token必须为非空字符串且不超过16384字符；缺失、空值、非法类型或超长均为上游响应故障，进入同一预算，不能以不完整成功报文续期或覆盖旧token。bo.orgId为可选资料，不授予成员权限。

成功候选中的bo.user与已验证account均去首尾空白，并按与语言环境无关的ASCII大小写折叠进行比对（仅A–Z转a–z），允许工号大小写及首尾空白差异。保留前导零，不转数字，不猜测长短工号或其他格式映射，也不改写第2.3节持久化身份主键。两个非空有效标识归一化后仍不一致，视为明确身份失败并立即撤销，reason=revalidation_rejected、failureClass=identity_mismatch；此项是完整成功码响应的额外主体校验，不将缺失用户字段当作明确失败。日志记录比对规则版本、匹配结果和工号，字段原文仍遵守第9节脱敏。

成功原子加密保存bo.token替换旧token（允许新旧相同），递增credentialVersion，写lastRevalidatedAt并推进nextRevalidateAt，清除重试记录；bo.orgId不写日志，也不替代登录时部门展示。IAM持久化凭据记录及session引用，同一tenant/provider/account及同一来源token的会话共享该凭据版本。每个凭据记录用数据库租约串行验证，HTTP前读取版本、提交时CAS校验版本和认证代次，旧响应不能覆盖新token。只推进本轮已到期会话的复验时间；未来到期会话仅引用新的凭据版本，不提前作判定性复验。不同tenant/provider不合并。登录信息查询若读取旧版本晚返回，仅可补同代会话展示，其失败仍不撤销、不覆盖token。

上述所有上游故障均不立即判失效：记录WARN及retry_pending，保持原会话/设备绑定有效，不发送revoked，也不预写nextRevalidateAt。首次故障同事务持久化attempt、retryAt、retryDeadline、strategy/configRevision与凭据版本，retryDeadline=首次故障时间+间隔乘最大重试数+一次请求超时。此后按配置间隔重试，最多max-retries次；达到次数或截止时间仍未成功则撤销，reason=revalidation_unreachable。max-retries=0表示首次上游故障立即耗尽。重启、租约接管、排队及配置切换不得重置次数/截止；截止后恢复的工作者直接处理耗尽，不重新获得宽限。始终优先服从会话自身过期、用户退出和管理员停用。

重试领取同时检查retryAt已到达且未超过retryDeadline，仍使用数据库租约及provider并发上限。原nextRevalidateAt已到期不能绕过retryAt反复领取；初次到期抖动只应用一次，不在每次重试重新抽取。次数在出站前持久化递增，领取后崩溃不能获得额外尝试。截止时仍在途的成功响应也必须通过截止、会话代次和凭据版本CAS校验，超期不能复活会话。

重试若收到符合上述条件的明确业务失败，或成功候选中的有效用户标识归一化后不匹配，立即撤销，reason=revalidation_rejected。日志以failureClass区分business_rejected、identity_mismatch、upstream_fault、retry_exhausted；upstream_fault再以faultReason区分transport_error、http_error、invalid_json、missing_code、invalid_code和invalid_success_payload，保留httpStatus及安全业务码。超时可能发生在上游已旋转token但我方尚未持久化之后，重试旧token可能被拒而要求重登；本设计不宣称上游幂等或网络故障永远无感。现场继续观察token是否每次更换及旧token宽限，默认策略仍为已确认兼容的token-verify。多副本并发、崩溃后旋转不确定性及保存失败必须以替身验证，不允许用失效旧响应恢复已撤销会话。

### 2.5 替身验收与现场日志重点观察项

测试使用随机回环端口的协议替身，由测试显式注入transport，使用合成token和工号，结束关闭服务。登录替身覆盖waiting/scanned/success/denied/expired、成功码缺token或工号、错误业务码、HTTP错误、畸形及超大响应、并发complete、响应丢失、停用与到期晚响应。成功扫码后立即建立会话。验收“登录时部门查询成功并记录”和“登录时部门查询失败仍登录成功且nextRevalidateAt不变”，包括空部门、HTTP/业务错误、超时、网络失败、畸形响应及资料写入失败。正常完整登录流程purpose=login_info调用恰好1次，重复complete不增加次数，失败仅写WARN且不发session_revoke。

采用可控时钟验证登录信息查询恰好1次、初次周期内purpose=revalidate调用为0、nextRevalidateAt持久化，重启仅领取到期复验会话，到期边界、批次抖动、多副本租约、成功复验后推进期限和失败后撤销。复验替身覆盖唯一匹配部门、工号不符/多条、空部门、HTTP/业务错误、超时、网络失败及畸形响应。备用department策略purpose=revalidate的上述失败立即撤销，部门查询不重试。token-verify替身覆盖双层成功码及任一非成功码、未知失败码、clientIP大小写、UTF-8无分隔MD5小写摘要、前端编码不能覆盖后端、bo.user大小写/首尾空白差异仍匹配、有效标识归一化后仍不匹配立即撤销、bo.user/bo.token缺失进入故障预算及原子替换。验证默认token-verify；网络失败/超时、3xx/4xx/5xx（包括代理401/403及429，即使body有失败码）、HTTP200非JSON、缺任一层码、非法码类型和不完整成功字段均不立即失效且设备仍可刷新、后续成功清预算、新token相同/不同、明确失败立即撤销、次数/时间上限耗尽撤销、重启不重置预算及并发/晚响应不能覆盖新token。MD5夹具只用合成值，验收是后续A实现要求，本PR不冒充已实现。日志断言核对两条链的purpose、级别、动作和关联，真实UDS联调不作为设计及后续迁移实现的验收门禁。

现场部署后重点观察以下项目，用户回传脱敏诊断包后据事实调整。

- 挑战创建时间、TTL、轮询次数，waiting/scanned/成功码序列，超时阶段和响应长度。
- 扫码验证占位路径的配置修订、HTTP状态和业务码，systemCode配置修订、originSystemCode是否为空、MD5输入字段存在性。日志省略实际签名输入和二维码坐标。
- 登录时token/工号字段的实际分支、token哈希前缀及扫码耗时。
- 资料源标识及路径、purpose、HTTP/业务结果、列表分支、匹配数量、dept字段分支与departmentPresent。日志省略姓名、部门全称和完整响应中的个人值。
- clientIP为空或服务端出口地址是否通过，双层失败码实际取值，新token是否每次更换、旧token是否有宽限，以及验证接口返回的用户标识与工号的格式是否一致（包括长短工号差异）。诊断只比较哈希前缀与存在性，不回传原token或地址。
- token验证接口换发新token后，浏览器里其他公司应用是否被登出、旧Cookie token是否立即失效。Auriga始终不回写或覆盖企业Cookie；不回写不能保证旧token仍有效。若观察到跨应用登出，仅在上游已注册并确认支持“不换发/只读校验”方式时才用该方式，不能臆造请求参数或拿旧token冒充验证后的新token；不支持时关闭SSO免扫码并回落扫码。若扫码凭据复验也影响同账号其他应用，可由部署方显式切换既有department备用策略，保留其单次失败撤销取舍，待有不轮换的上游能力后再恢复。
- 云端SSO Cookie的Domain/Path/Secure是否覆盖门户后端、私有Cookie映射、注册回跳地址是否可用，是否满足第3.1.1节第一期SSO前提、默认开关是否生效、退出抑制和显式继续行为。
- 实际复验strategy、重试预算/attempt/retryAt/retryDeadline、tokenReplaced、复验周期、nextRevalidateAt、重启领取的到期会话、批量抖动与dueLagMs、失效原因、撤销传播时间，多副本重复或晚响应情况。
- 手机目标系统展示差异、员工号变更/回收、网络出口/代理/TLS故障、响应结构变化与速率限制。观测资料作为现场修正依据。

## 3. 登录页归属与迁移

### 3.1 云端门户和 edge

门户提供统一登录入口，身份源选择包含团队账号和已启用 UAC。表单渲染器先迁到 web/shared，再解除 portal/admin 对插件源码依赖。UAC 使用新增协议，不虚构两位码。管理台可复用渲染器，但登录事务与管理权限独立。

门户成功后复用 `POST /api/workspace/v1/me/instance/open`、就绪等待和 `/open/{id}`。edge 校验本人实例、一次性票据、实例 cookie、权威 session，并注入签名身份头。托管插件保留身份头验签、准入与当前会话读取，删除第二套托管登录界面。会话失效由外壳引导回登录入口，插件显示“正在恢复平台会话”或服务异常。托管插件永远不呈现可跳过的未登录状态。

domain 与纯 IP 部署均沿现有入口契约。IP 同源实例保留已知弱隔离，端口无法隔离 cookie。edge 转实例剥离全部平台凭据，管理写请求保持独立 Origin 与 CSRF 校验。

### 3.1.1 第一期SSO优先与扫码降级

现有web/portal是静态前端，不新增假定已存在的Node门户后端。由B在edge的门户登录页服务端接缝创建受保护导航事务，A在IAM承担门户服务端的Cookie解析、UAC验证及建会话。企业Cookie沿现有同域路由传到门户登录后端或本人实例，SSO字段由私有配置解析，日志按第9.2节脱敏。前端脚本不读取、不拼接、不转发Cookie、account或token，仅携带浏览器自动附带的Cookie与我方导航证明。公开配置只出现角色，真实Cookie名及企业域范围由企业适配器私有配置注入。

部署配置 `auriga.iam.uac.sso-cookie.enabled`默认true、只接受布尔值，非法值拒绝启动；运维关闭即直接扫码，不改变UAC provider的enabled。只有门户部署于公司域名体系且Cookie的Domain/Path/Secure实际覆盖门户登录链，浏览器才会带来SSO Cookie；host-only或Path不覆盖也可能收不到。公司域外、纯IP及仅HTTP等不满足Cookie条件的部署自然走扫码，不弹故障，不通过复制Cookie或放宽域绕过浏览器限制。现场按单IP、单域名部署，SSO开关按配置正常启用，企业Cookie的Domain/Path/Secure决定浏览器是否附带Cookie。B/A在部署说明写清TLS/代理、私有Cookie映射与开关位置，E展示生效值。

用户主动打开门户登录页时先检查现有我方会话：已有有效会话继续原目的；无会话才尝试SSO。设备确认及其登录续接始终禁止自动SSO，页面展示发起设备信息与用户码，由用户点击“用公司账号继续”后提交mode=continue并核码。mode=auto在设备确认用途返回scan_required，不调用上游验证。普通门户自动尝试仅限顶层、非预取导航，服务端验证Sec-Fetch-Dest=document、Sec-Fetch-Mode=navigate、Sec-Fetch-Site为none或same-origin及Sec-Fetch-User=?1，禁止iframe、跨站和缺证据请求自动登录；证据不足仍显示扫码及“用公司账号继续”。导航响应只设置我方HttpOnly事务cookie并返回120秒一次性navigationProof（32字节随机base64url，存哈希，绑定入口完整Origin、tenant/provider、portal audience、login用途和浏览器事务）；GET不建会话。可信页面自动或按钮触发同源 `POST /api/iam/v1/login/uac/sso`，同时验证精确Origin、现有CSRF规则、事务cookie及navigationProof，CAS单次消费。mode=auto仍须权威检查非设备确认目的、导航资格与退出抑制，不能由body自行宣称用户操作。mode=continue由本页明确按钮触发，同样校验事务与CSRF。门户会话失效时以location.assign等整页导航回规范登录页，B同步处理HTTP401、会话读取失效及权威撤销，整页导航重新取得Fetch Metadata与导航证明。脚本跳转缺少用户手势证明时显示显式继续/扫码。自动尝试每事务最多一次，不因页面轮询无限重发；UacSsoLoginResult.transactionId指向既有LoginTransaction，必要MFA沿原GET/advance继续；scan_required后创建扫码挑战替换当前导航事务。丢失响应只读取/恢复原登录事务，不再次验证旧token。

IAM按内部资料映射选择完整token/account对：Portal体系优先，缺完整配对再取UDS体系，均无则fallback扫码；不把不同体系的token与工号交叉拼接。两体系同时存在时只验证优先的完整配对，该配对验证失败直接扫码，避免静默切到另一个账号。语言按Portal、UDS、默认简体中文；客户端IP按私有UDS资料映射取值，仅视为上游校验所需的非可信资料，校验长度和IP字面量格式，不做解析访问、不将其用于我方授权或审计客户端地址。缺失时用第2.4.3节部署值。版本等非必要字段不外发，序列号不采集。缺字段、重复同名Cookie、格式非法均安全回落扫码，不向浏览器暴露具体Cookie名或原文。

后端计算verifyCode，调用第2.4.3节token验证接口，双层成功码、归一化工号及新token均通过才执行成员/邀请准入和必要MFA，保存验证返回的bo.token，不能保存未验证Cookie旧token。首次SSO验证任意明确拒绝、上游故障或资料不完整都回落扫码，不为尚未成立的会话启动复验重试，也不撤销浏览器里其他账号会话。成员准入拒绝仍按既有策略提示申请邀请，不能用扫码绕过；MFA未完成仍是additional_step_required。成功后轮换我方session ID、清事务证明，并与扫码共用一次login_info作业、nextRevalidateAt、设备绑定、撤销及日志。SSO用于login，不自动执行link/stepup，也不从普通业务GET建立新会话。

退出的同源受保护POST在清会话前，将“本次浏览器会话禁止自动SSO”的标记写入服务端浏览器会话记录，配套独立host-only、HttpOnly、SameSite=Lax的会话cookie（HTTPS用__Host-前缀，无Domain/Expires/Max-Age，不含UAC材料）。该标记与平台session分离、跨标签页共享，登出与浏览器返回登录页不会清掉。普通重载、重定向、会话轮换及现存企业Cookie都不能绕过标记；同一浏览器会话内仅显示“用公司账号继续”和扫码，不再自动尝试。显式继续仅为本事务授权一次，不清抑制标记，不退出公司其他应用。浏览器恢复会话可能保留session cookie，届时继续抑制属安全降级。标记存储不可用时禁止自动SSO、保留显式继续/扫码。登录/退出响应no-store，页面frame-ancestors 'none'，外部returnTo严格受信相对路径允许列表，防CSRF与登录固定。

### 3.1.2 向DSH提供企业登录信息

用户已决定采用现场单IP、单域名方案。公司域内的实例、插件及生成内容可以读取脚本可读的企业SSO Cookie，用户接受这一取舍。企业Cookie沿现有同域部署接线传递，实例继续使用正常/open入口。SSO免扫码由既有开关及Cookie可用性决定。

当前插件的 `src/managed/sdk.js` 提供ctx.get('auriga').identity()身份投影，`src/local/host.js`、`src/local/native-rpc.js`和原生工具注册代码提供宿主准入及调用上下文，托管模式已注入公开credentials服务。企业凭据读取沿已核对原型的handleAgentCredentials、resolveCredentialsForSession和shellEnv.register预留源码迁入这些现有接缝。原型提供“受控读取工号/token”及工具出站调用，shellEnv按本次工具执行传递会话权限。来源文件与核对细节记入执行报告。

沿用这一读取形态，插件后端仅为当前已认证用户自己的DSH会话内skill/工具提供empNo、当前有效token和updatedAt。内部应用确需Cookie格式时，额外返回该用户所需的私有配置Cookie键值。调用者身份、会话归属取宿主已验证的执行上下文及会话绑定。客户端工号参数不参与选择凭据。skill在工具进程内调用现有凭据读取handler，插件使用当前平台会话或已绑定设备凭据向 `POST /api/iam/v1/me/uac/credentials` 读取来源UAC会话材料，复用第2.4.1节来源会话/认证代次校验。

该接口是本轮唯一新增的接口定义，用于取得IAM保存的当前值。请求仅空对象，响应为UacEnterpriseCredentials的empNo、token、updatedAt及可选cookies。插件的读取handler仍采用既有会话权限与owner核对，原型shellEnv调用预留只向执行进程传临时会话权限和接口位置。skill在内存中读取响应并直接用于内部应用请求，工具给AI的结果仅含业务结果或安全状态。交付给skill的凭据副本不写静态配置、文件、日志、stdout/stderr或会话记录，诊断只留token哈希前缀。IAM权威材料仍按第2.4节加密保存。

每次调用读取当前会话对应的权威凭据，发送前复核当前认证代次，复验换发并提交bo.token后立即返回新值，按需组装的Cookie token字段也采用该当前值。退出、会话撤销/到期、成员或身份源停用后拒绝读取。并发切换账号时，沿既有认证代次检查丢弃旧结果，禁止把其他账号的最近登录缓存当作当前用户凭据。原型有按调用取值的handler及内存用法，本次交给skill的副本按上述要求即时使用。

A负责IAM读取接线，C负责Desktop现有broker读取与退出状态衔接，D负责把原型凭据handler/shellEnv预留迁入插件的现有本地/托管工具入口。验收本人skill读取、跨用户会话拒绝、token轮换后返回新值、退出/失效不可读，以及配置/临时文件/日志/工具结果与会话记录中没有凭据原文。真实域名、Cookie名与系统编码继续放私有配置。

### 3.2 Desktop外壳与loopback控制页

核对当前main的clients/installer/runtime/cli.mjs、core.mjs及Windows launcher，现状为Node启动器拉起DSH，并由系统浏览器显示DSH页面，我方没有自有窗口。PR #319、分支agent/codex/auriga-P1E正在增加runtime/guardian.mjs及supervisor.mjs。下述认证broker为C线新增，依赖guardian的停用/隔离部分必须等P1-E合并并完成集成验证，不能把PR分支代码视为现成发布能力。

确定默认方案为系统浏览器打开云端门户，再经RFC8628设备授权把平台身份交给Desktop broker；浏览器访问真实门户域名时可带已有SSO Cookie，本机127.0.0.1页无法取得公司Cookie。第6轮的loopback页保留为本机状态、跳过、取消和拉起门户的控制页，不再代理或渲染远程登录步骤。相比在loopback页登录，默认方案复用浏览器SSO与同一门户认证流程；代价是需要可达门户及一次人工设备确认，但不增加本地重定向接收token的攻击面。平台不可达仍可跳过使用本地DSH。

启动器先启动当前OS用户的Node认证broker，监听127.0.0.1随机端口；主动登录可直接打开受信云端设备验证页，初始跳过/状态操作可用loopback控制页。页面静态资源由broker自带，CSP限制同源脚本/连接、禁止第三方内容及iframe嵌入，Referrer-Policy=no-referrer、Cache-Control=no-store。GET静态页不回个人数据，敏感状态和写操作全部走受控POST。

每次拉起登录页创建32字节随机一次性启动票据，TTL30秒，服务端保存哈希并绑定brokerEpoch、完整Origin、端口和操作目的。系统浏览器URL fragment携带这枚票据，页面读取后立即replaceState清除。票据可能短暂出现于OS打开URL的参数和浏览器历史，这是采用系统浏览器的实际交付边界，不能承诺不进argv。它不包含平台令牌，不记录到应用日志，结束或交换后立即作废，读取OS打开参数的已失陷本用户进程属于信任边界。

页面精确同源POST交换票据，Host必须为127.0.0.1及当前端口，Origin必须完整匹配，拒绝localhost别名、跨源CORS及异常Fetch Metadata。CAS只允许消费一次。成功返回独立随机Shell页面令牌和CSRF值，均仅存页面内存，绑定brokerEpoch、Origin、端口、客户端nonce和15分钟期限。后续POST必须同时有X-Auriga-Shell-Token与X-Auriga-Shell-CSRF，服务端逐次核绑定。无票据/无令牌、过期、端口不符、重放和跨源都拒绝。

loopback认证不使用cookie、localStorage或sessionStorage。浏览器cookie跨127.0.0.1端口共享，Path和随机名字也不能给其他本机服务建立可靠隔离。内存令牌从不附加到DSH或其他Origin请求，broker禁止把它转发到IAM。浏览器刷新需重新拉起登录页，UI提供清晰恢复动作。未认证原生HTTP不能凭伪造Origin放行，原生插件后端使用下一节的OS IPC。

broker先用已登记第一方client向既有标准设备授权端点申请device_code/user_code，device_code只在broker内，随后以系统浏览器打开经过HTTPS目的地允许列表校验的verification_uri，默认不使用verification_uri_complete自动填码。Desktop同步显示user_code、有效期和“只在自己刚发起的设备确认页输入或核对”的提示。设备确认页在任何SSO尝试前醒目显示发起设备名称、发起时间、客户端类型、scope与用户码，并提示“若不是你本人刚在Auriga Desktop发起请拒绝”；设备信息来自IAM权威请求，不从URL信任。确认页及其登录续接不自动SSO，用户点击“用公司账号继续”才用SSO；没有Cookie或验证不通过则扫码/团队账号。已有我方会话也不自动批准，仍展示同一确认内容。用户仍须手动输入或逐字核对user_code、发起端与scope，要求现有10分钟内step-up；SSO不替代设备批准和MFA。IAM按第2.4.1节绑定来源会话/认证代次，broker有界轮询标准token端点，以自己的device_code兑换device-only凭据；页面不返回平台令牌，完成页仅提示可关闭。默认浏览器设备流沿现有RFC8628公开client绑定与轮询边界，不冒称该流有PKCE；已有nativeConnect的S256契约保留但不再作为Desktop默认路径。该设备确认是Desktop的额外一步，门户直接进入实例的路径没有该设备确认。

设备授权延续只允许服务端生成的待批准请求，不能用任意外来设备码绑定登录事务。门户登录续接目的只允许服务器登记的本次设备确认页面，不接受任意returnTo、loopback回调或token URL参数；默认Desktop路径不调用workspace instance/open、不创建云实例。关闭页面、取消、超时或broker epoch变化后停止轮询并作废本次设备请求，晚到凭据丢弃/撤销，不能替换新身份。浏览器来源会话在批准后保留供绑定复验，broker不持有其cookie jar。Desktop本地退出立即停权并撤销旧设备，持久化manual-login-required；下次拉起门户指定只减少自动行为的manual模式，即使浏览器已有来源会话仍先显示继续/核码，不自动批准设备。门户自身退出按第3.1.1节抑制本浏览器自动SSO。

平台access/refresh token、device_code（及已有其他流程中的verifier）由broker持有，持久化采用当前用户DPAPI与用户ACL，UAC上游token由IAM权威保存，企业skill按第3.1.2节按需读取。broker通过AUTH-K的固定操作代理供应业务能力，拒绝任意URL/header/凭据解析输入。插件后端通过OS IPC取得状态、受限代理结果及第3.1.2节的本人企业凭据读取结果。

### 3.2.1 分发与本用户IPC边界

broker唯一源码目录拟定clients/shell-auth/，实现与独立制品构建归C。Windows命名管道的本用户DACL及客户端token校验由同包Windows受控管道helper承接，Node缺少该原生能力时不得伪称已校验SID。helper源码与打包归clients/shell-auth/windows/，复用安装器既有编译/签名流程。Unix使用用户私有0700目录/0600 socket并核peer uid。IPC凭据不传给页面，管道拒绝远程和其他OS用户。

C构建版本化broker制品，Desktop构建放入versions/<id>/auriga/shell-auth/，以当前版本manifest摘要校验。独立插件发行也携带同一制品，位于插件包shell-auth/，D唯一负责clients/dsh-plugin/scripts/及package.json中的引用/打包接线，C不并行修改插件打包。可用时连接当前用户已有broker，否则由公开启动入口拉起同包broker。平台功能在缺broker/helper时给出诊断，本地DSH继续按owner准入。P1-E的guardian由安装器C线接线，独立插件的共享资源必须探测其受信监督能力，不能伪装拥有guardian。

### 3.3 插件代码归属清单

- `src/login/page.js`、`styles.js`、`index.js`、`index.d.ts` 的 UI 迁入 web/shared 或 Desktop 自有资源，调用方迁完后从插件删除。`local-adapter.js`、`local-controller.js` 的平台登录事务控制迁 broker，插件旧登录路由与表单入口删除。
- `src/login/logout.js` 的入口改成外壳 logout 命令，服务端撤销和 cookie 清理迁外壳，不遗留插件第二生命周期。`errors.js` 可迁共享文案层。
- `src/local/platform.js` 的 LoginJar、登录/刷新/设备授权逻辑以及 vault 中平台材料迁 broker。`local/team.js`、`runtime.js`、`routes.js`、`native-rpc.js` 的平台分支改读取 ShellAuth 状态和请求受限代理能力。旧平台凭据文件由已认证迁移流程导入 broker 后安全清理，导入失败清除旧平台能力并提示重登，保留本地内容。
- `src/identity/owner-admission.js`、owner 准入、standalone 所需身份源/应急逻辑继续服务本地身份。其余 core/oidc/session/store 按调用点拆出平台部分，不能整目录删除导致离线 owner 失效。
- `src/managed/door.js`、`host-auth.js`、`guards.js`、keys/session 验签与 edge 身份读取继续保留，managed/panel 取消登录视图入口，logout 跳外壳。模型、心跳、设置权限沿当前认证通道。
- `web/portal/src/native-login/page.ts`、管理台登录页改用共享渲染器。插件 web/设置侧栏只展示状态及“登录/退出”动作，通过受准入的后端桥接 broker。

独立安装的分发位置、helper及打包归属按第3.2.1节执行，插件动作通过IPC请求broker拉起浏览器登录页。

## 4. ShellAuth 本地通道契约

UacLoginInfo在contracts/auth/uac-login-info.schema.json中定义，Me.uacLoginInfo和ShellAuthSnapshot.uacLoginInfo通过同一引用提供可选只读资料。purpose固定login_info，status为pending/available/unavailable，department只在available时出现且同时有updatedAt。身份切换、注销及非authenticated快照清除该字段，禁止把部门展示状态解释为认证状态。

生产方按 schema 严格检查字段。消费方允许扩展响应字段，但命令的未知操作、任意 URL/header/凭据输入在授权前拒绝。authenticated/identity、事件type/snapshot/code及UacLoginInfo的状态约束已在生产方schema中用条件表达式校验，接收端仍执行身份绑定与授权检查。

权威消息定义在 `contracts/plugin/shell-auth.schema.json`，OpenAPI `shell-auth.yaml` 是三语言生成用的 IPC 投影。`/messages` 禁止成为公网或普通 DSH HTTP 接口。它只投影 OS 认证后的消息，发布订阅持续推送同一 ShellAuthEvent。

Windows通过第3.2.1节helper使用禁远程访问的命名管道，DACL 仅当前用户 SID 和必要系统主体，验证对端 token SID、受控子进程启动记录及每次启动的随机能力，避免仅凭 PID 信任。Unix 对应用户私有0700目录中的0600 socket，并核对 peer uid。能力只经继承的私有句柄或管道交给插件后端，不放 argv/env/网页。相同 OS 用户的恶意代码属于同用户信任边界，不能声称该管道隔离已被攻陷的用户账户。

### 4.1 字段与生命周期

- ShellAuthCommand 包含 protocolVersion=1、requestId、shellEpoch、expectedRevision、action。支持 snapshot、subscribe、open_login、cancel_login、logout。首次 snapshot 的 shellEpoch 使用全零 UUID、expectedRevision=0，仅此只读动作允许发现 epoch。其余动作须与当前 epoch/revision 一致，否则 common.conflict。
- ShellAuthSnapshot 包含 shellEpoch、单调 revision、state、expiresAt，可选 identity/reason。identity 绑定 tenant/member/device/provider，empNo 可选。authenticated 必须包含当前 identity。expiresAt 为快照使用截止，最长30秒且不能超过平台凭据有效期；订阅每10秒重发当前快照或客户端到期前重取，属于本地健康检查。signed_out/authenticating/revoked 不含 identity。unreachable/reauth_required 可保留上次脱敏 identity 供显示，不授予平台操作。
- ShellAuthEvent 含 requestId、type，snapshot/changed 必须包含 snapshot，error 必须有白名单 code，ack 确认动作已接受。退出完成以 signed_out/revoked 快照为准。订阅主动事件 requestId 使用订阅命令 id。消费方按 epoch/revision 丢弃旧事件，重连先 snapshot，再订阅，服务端原子注册并补发当前快照消除间隙。
- requestId 在同 epoch 内幂等，缓存有界保存至命令到期，同一 id 不同内容拒绝。logout 必须先本地停权、递增 revision，再排队远程撤销。网络失败不恢复本地身份，重新登录生成新代次，旧排队撤销只针对旧设备/会话。
- reason新增revalidation_rejected、revalidation_unreachable、user_logout、admin_disabled和member_disabled，保留source_disabled表示身份源关闭。明确拒绝的复验、department单次不可达或token-verify重试耗尽、管理员/成员停用及来源关闭映射revoked，用户退出完成映射signed_out，reason=user_logout；user_action保留为旧草案的通用用户操作值，仅供消费方识别历史消息，不能据此推断已退出，当前生产方不再发出该值；token-verify上游故障仍在预算内时保持authenticated，不发revoked，用安全状态说明正在重试；普通平台网络断开映射unreachable/offline，信息性查询失败只更新uacLoginInfo，不把认证改为revoked。界面按白名单原因显示。
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

状态消息没有凭据传递功能。现有市场、网络、AI、Team MCP 的调用需要补齐 broker 代理端口契约，按当前受限 device audience/scope 分派固定操作，身份由 broker 当前快照确定，响应只返业务数据。长连接由 broker 持有或提供受限流桥，平台字节不得经任意目标转发。AUTH-K在所有实现线之前盘点令牌调用点并补全操作schema，C/D依赖其已定契约。

企业skill凭据按第3.1.2节已有handler与shellEnv接缝取得。模型凭据仍通过公开 DSH credentials/操作作用域适配，页面不能 resolve 平台票。需要与现有模型身份绑定协议一起评审，避免将 shell token 放入全局环境或模型配置。代理未接好时对应平台功能按不可用呈现，本地个人模型照常工作。

## 5. 跳过、登录、退出与跨身份内容

首次 Desktop 启动提供“跳过，使用本地 DSH”。跳过不创建平台会话、云实例或设备授权。设置和侧栏平台区域显示“未登录”和“登录”，本地 owner 准入照常执行。

之后用户点击登录，插件后端向 broker 发 open_login，默认拉起系统浏览器中的门户设备验证页，已有SSO可免扫码，否则扫码或团队账号登录。登录及人工设备批准完成后改变快照并推送，当前 DSH 内平台 UI、固定代理调用与新网络连接立即更新，无需为身份刷新重启 DSH。用户取消扫码或设备授权仅取消尝试。平台不可达时展示故障分类，有可恢复凭据的离线状态显示自动恢复，避免反复要求用户登录。

本地私有会话、个人 Skill、个人模型、工具配置按 DSH owner 持久保留，平台账号切换不改写或重新绑定本地 owner。服务器私有资源仍按平台 owner 隔离，切换后立即失去旧平台身份可见性。

共享资源按 `unified-protocol.md` 串行处理。退出/切换先停平台请求与旧连接，阻止旧异步响应提交，通知 guardian 进入 stopping，确认 DSH 进程树退出后隔离 `skills-active/current`，持久化 disable 回执并进入 private_only，再按新身份在线重验和重新授权。清空失败为 blocked，不能把旧共享内容交给新身份。旧身份 cache/ledger 放原身份根，禁止自动恢复到 current。历史会话可查看，private_only 使用新会话。

已知复验失效取舍还包含已激活共享资源的Desktop用户会被guardian停止DSH进程树并隔离共享内容，需要private_only重启及新会话，任务可能中断。该链依赖P1-E。

已决定采用以下体验边界。从未登录且 private_only 的运行中补登录通过推送新身份生效，DSH进程继续运行，共享资源由用户后续明确激活。已有共享资源参与上下文时，退出或跨账号切换执行统一资源包协议的停止、隔离及新会话流程。

云端无法跳过。托管退出使 edge 实例 cookie、HTTP/WS 和重连认证失效，回门户登录页，默认不停止实例任务。托管共享资源变化另走既有云端可信执行器的 guardian 等价流程。

### 5.1 管理台配置与会话恢复

复验策略、周期和token-verify重试采用第2.4节部署配置，默认token-verify、7200秒（整数60至86400）、30秒间隔（整数5至300）及最多3次重试（整数0至10），管理台配置页提供配置位置、范围及重启生效说明。工作线E维护该说明，工作线A负责IAM配置绑定、会话nextRevalidateAt持久化及到期调度。默认token-verify的上游故障保持会话并在预算内自动重试；明确业务/身份失败或预算耗尽后才要求重登。部署方切到备用department后，已到期会话的单次部门查询失败仍立即撤销。重新扫码按新登录时间安排nextRevalidateAt，本地DSH权限沿本节处理。

## 6. 安全与验收要求

二维码内容可能包含上游轮询所需的公开坐标，上游兼容校验值由公开参数即可计算。看到或转发扫码内容的人可能直接向UDS轮询并在受害者确认后拿到token，我方发起端绑定只保护Auriga事务，不能保护该上游token。门户没有Desktop的设备人工核码，外壳显示目标系统名也不能保证手机扫码人看到同一提示。当前受信团队试用保留这一残余风险，不能宣称二维码不可劫持。

缓解包括短TTL、完成/取消立即作废我方挑战和清屏、二维码页提醒避免屏幕共享/转发、同挑战异常轮询频率及可取得时的扫码地点异常告警。上游全局作废依赖企业协议实际支持，不能把我方作废等同UDS作废。后续若要求门户额外核对或上游签名绑定，由独立产品/契约变更处理。


挑战绑定 cookie、完整 Origin、tenant、audience、provider 修订和 TTL，多副本原子消费，公开二维码内容不承担轮询授权。complete/取消采用写请求，严格 Origin 和事务秘密校验，已认证管理操作再校现有 CSRF。GET 状态无登录副作用，校验绑定 cookie、受信 Host、同源 Fetch Metadata，并在 Origin 存在时核精确值。

平台访问/刷新令牌由IAM/edge/外壳保管，插件后端通过能力代理继承平台身份。用户授权的企业登录信息由第3.1.2节读取handler交给本人skill进程即时使用。外壳状态消息继续返回脱敏状态。日志中的token仅记录SHA-256哈希前缀，cookie、设备码、verifier、二维码坐标和URL query均不记录。UDS响应仅按第9节在详细开关开启时记录脱敏结构，原始字节不落盘。身份类日志保留员工号与内部ID，姓名、部门全称、邮箱、电话省略，二维码与认证页面禁止第三方遥测和缓存。

实现验收必须覆盖跨源/跨用户管道访问、DNS rebinding、CSRF、登录 CSRF、跨 audience/tenant 事务交换、挑战重放、并发消费、员工号冒认、多义关联、邀请重复消费、source停用、token过期/吊销、晚响应与外壳重启。刷新请求结果不确定时沿现有旋转令牌围栏要求重登，只在能证明未发出请求时安全重试。

云端从正常门户完成团队/UAC登录、实例准备、进入DSH及退出后旧HTTP/WS拒绝。Desktop 从正式启动器验证跳过、本地正常使用、侧栏拉起登录、无重启状态更新、多窗口、外壳崩溃和断网恢复。共享资源已激活时验证停止/隔离/新会话和崩溃恢复。容器healthy、源码与内部API只作局部证据。

## 7. 分期与派工边界

### 阶段一，迁移基线与契约

以第2.2.1节现场可用实现和第8节决定为基线，首先完成AUTH-K。所有新增契约经非作者A级独审，精确HEAD完整CI合格后交主控处理合并。UAC相关各线按现有实现迁移，以替身加日志断言验收。现场日志回传用于后续修正。

### AUTH-K契约线，全部实现线的前置

由主控单独派契约作者/审查人，先于A/B/C/D/E冻结接口。范围为contracts/openapi/iam.yaml及shell-auth.yaml、contracts/auth/、contracts/plugin/及生成接线，盘点clients/dsh-plugin的市场/资源、网络长连接、AI凭据和Team MCP真实调用点。定义固定业务操作白名单、输入/输出schema、权限/audience、流式与长连接背压/关闭、重试/幂等、身份代次、撤销及取消。禁止任意URL、header或请求身份透传。

AUTH-K同时冻结第3.1.1节SSO导航证明、退出抑制与门户/设备续接约束，并定义loopback票据交换与端口绑定页面令牌、UAC来源设备绑定、详细诊断开关及密钥轮换契约。诊断开关持久化于IAM的独立tenant/provider诊断设置记录，使用独立revision以免改变认证配置代次，带enabled、expiresAt、revision，所有副本按权威修订读取，默认关闭，到期即关闭，重启继续遵守已有有效期。开关API只接受管理会话、iam.providers.write与CSRF，写审计。A实现后端，E只实现UI，不由E另造接口。

验收包括操作清单覆盖全部现有平台调用点、producer/consumer正负向夹具、跨用户/tenant、过期/撤销/代次错配、URL注入、流关闭和日志脱敏，以及标准lint/TS/Java/Go生成和编译。契约提交须全新非作者A级独审。此PR不展开完整业务代理协议，AUTH-K为明确的独立交付和实施依赖。

### 工作线 A，IAM UAC 身份源

文件范围为 services/iam/api、impl 的连接器/登录/会话，企业适配器模块，以及 services/org 的成员关联策略接口。contracts 的进一步变动由主控统一协调。依赖AUTH-K。验收包括三语言生成、严格员工映射/邀请关联、复验/撤销、共享存储并发消费、RLS和跨租户负例。复验调度测试覆盖第2.4节周期、strategy及重试配置的默认值、上下界、非法值拒绝，以及配置修订和滚动重启生效。可控时钟验证nextRevalidateAt持久化、初次周期内零判定性复验、仅领取到期会话、重启保留期限、抖动分批、租约崩溃接管及成功后更新期限。配置与邀请提交失败可安全重试，单个挑战最多创建一个session。按最新流程迁移挑战与扫码取token/工号，登录后执行一次login_info信息查询，先实现SSO Cookie配对优先级、验证新token及回落扫码，SSO/扫码共用登录会话和信息作业；到期默认按token-verify执行revalidate，显式配置department时走备用查询，并以第2.5节替身逐项断言调用次数、purpose、明确失败/上游故障分类、有界重试、用户标识归一化、新token替换、日志与脱敏。A同时负责第9节IAM日志、安全诊断序列化器、audit和OpsSignals接线，以及IAM诊断下载控制器、web/admin按钮所需接口和deploy/compose、deploy/nas观测发布接线。导出单独对接现有audit读取与Loki受信查询，避免新增公网日志采集入口。

### 工作线 B，门户登录和 edge

范围为 web/portal/src/native-login、原门户登录入口、web/shared 渲染器与 API，infra/edge 仅调整路由/新事务保护接线。负责迁出插件 UI 的唯一写入，管理台与 Desktop 消费它。依赖 A 的 API，可先用替身。验收双入口audience隔离、二维码状态/取消/过期、额外验证、正常本人实例准备与进入、退出后HTTP/WS失效，domain/IP两模式。UAC分支第一期接SSO优先/扫码回落及同一现场迁移替身，验收设备确认显式继续并核码、门户会话失效整页导航、单IP单域名SSO正常使用，并验收顶层导航证明、CSRF/登录固定、Cookie优先级、域外无Cookie降级、退出抑制及显式继续、设备确认续接不创建实例，并验收部门展示成功与失败降级、登录响应不等待信息查询及当前身份元数据更新，并核对页面所见correlationId与日志一致，错误响应无上游秘密。

### 工作线 C，Desktop 外壳认证

范围为 clients/installer/runtime、windows/launcher、打包脚本与新 shell-auth 模块，guardian 认证状态接线。依赖AUTH-K、A及P1-E合并后的guardian集成，B提供共享UI。C唯一实现clients/shell-auth/及helper、版本制品构建、clients/installer/runtime/cli.mjs中的诊断导出子命令，并维护Desktop发布清单。验收设备确认显式继续后才用SSO、Desktop同步显示用户码，再进行系统浏览器门户SSO/扫码加设备核码、来源会话绑定、断网跳过和仅本机控制页，以及正式安装包正常启动入口、DPAPI/ACL、跨用户拒绝、端口绑定内存令牌/CSRF、device_code仅broker持有、人工设备确认、平台令牌不入页面、日志只记哈希前缀、随机端口清理、多窗口及更新后凭据恢复。与资源guardian共享单一状态协调，不新增并行停用写者。C按第9节输出本机壳登录、身份推送和停权日志，复用本机脱敏sink，UAC验收使用替身并断言跨端关联。

### 工作线 D，插件继承身份并删除旧登录

范围为 clients/dsh-plugin/src/login、identity/local/managed 的平台分支和 web 设置/侧栏，现有资源与模型适配层只作必要调用迁移。依赖AUTH-K、C的状态/业务代理制品及B的UI迁出提交。D唯一维护插件内broker制品打包与启动适配。验收旧平台登录页和路由确实删除，托管无未登录页，未配置/跳过/离线的真实本地 DSH 可用，稍后登录推送生效，所有原平台功能走受限代理，旧凭据迁移与旧异步请求围栏完整。D校验broker事件与本地状态诊断，包中采用相同关联字段，企业skill凭据读取按第3.1.2节复用同一来源会话，插件诊断仅记哈希前缀。

### 工作线 E，管理台配置

范围为 web/admin/src/views/IdentityProvidersView.vue、admin/providersApi、providersForm、providersMessages 与对应测试。依赖 A 配置 API、B 共享 UI。管理台说明SSO优先/扫码降级、默认开启的部署开关及Cookie域条件、退出抑制、默认token-verify及备用department策略、上游故障与明确失败的行为区别及第2.4节周期/重试部署配置的默认值和合法范围，工作线A负责实际配置绑定。验收开关/baseUrl/systemCode、ETag冲突、权限和CSRF、启用前错误提示、关闭后会话失效，普通成员不能读取或修改配置。E负责配置页的详细诊断开关、自动到期状态、correlationId及诊断下载按钮，调用AUTH-K定义、A实现的接口。测试断言开关默认关闭、脱敏响应和权限拒绝。

每线经针对性测试形成可部署产物通知主控，由主控串行安排开发环境部署与正常入口验收。本稿交付物是可审阅文件，实施部署属于后续派工。

### 第一期交付顺序

SSO免扫码按第3.1.1节进入第一期且默认开启，支持现场单IP、单域名部署，与扫码共用准入、信息查询、复验和诊断。AUTH-K先冻结导航证明/退出抑制和设备续接边界，A/B随后并行实现后端与门户；C/D接系统浏览器设备流。后续增强仅包括现场日志驱动的兼容修正，不再把SSO列为后续可选功能。

## 8. 已决定

既有产品决定及本轮用户确认的单域部署取舍如下。

1. 首次成员关联仅允许管理员核实员工号、双认证绑定或有效邀请，默认关闭自动入团队注册，避免自填员工号接管既有账号。
2. Desktop首期默认系统浏览器打开云端门户，通过SSO或扫码/团队账号完成身份认证，再沿现有RFC8628人工核码取得device-only凭据；本机页只控制状态/取消/跳过，平台不可达时本地DSH仍可使用。
3. 门户第一期默认先检查SSO Cookie并验证，无Cookie或验证失败回落扫码。扫码取得token和工号即完成UAC登录，两种成功方式随后一次purpose=login_info部门查询只补充展示，失败仅WARN，不影响登录或nextRevalidateAt。复验周期默认7200秒，可通过第2.4节部署配置调整。每个会话持久化nextRevalidateAt，到期后按部署策略执行purpose=revalidate，默认token-verify（扫码token兼容性已由用户确认），上游故障按有界预算重试，完整响应中的明确业务/身份失败或预算耗尽撤销；department保留为备用，单次查不到部门立即撤销。服务重启仅处理已到期复验会话，日志分别记录网络与业务原因，本地DSH能力保持可用。
4. 已激活共享内容的退出/跨身份切换采用受控停止、隔离和新会话，避免旧身份内容残留在上下文。普通补登录通过快照推送生效。
5. broker源码归C的clients/shell-auth/，Desktop与独立插件携带同一版本制品，目录及D线唯一打包职责按第3.2.1节执行。
6. 企业适配器留私有模块，真实路径、主机、系统标识及扫码/部门查询编解码来自受信私有配置；token-verify通用字段名、请求头及摘要算法按第2.4.3节公开，公共仓库地址使用占位符并采用通用Uac/ShellAuth契约。
7. 用户接受同域部署下实例页面可以读取公司SSO Cookie的风险，现场采用单IP、单域名，SSO免扫码按第3.1.1节正常使用。本人会话skill通过第3.1.2节既有读取接缝取得企业凭据。
8. UAC先限定受信团队试用。第6节明确公开扫码坐标的上游token泄露风险和门户缺少额外核对的边界，Desktop设备授权保留人工核码，日志观察不能替代防劫持能力。

## 9. 日志与一键导出诊断包

本节作为A至E各实现线的硬性要求。日志和诊断导出共用安全序列化器，先脱敏再交给logger、outbox或文件writer。诊断故障不改变认证判定，写入失败通过现有安全日志分类与ops信号可见。

### 9.1 登录、复验与撤销结构化日志

每个实际执行阶段开始和结束均输出JSON事件，phase为start/end。登录链按入口分支：先执行sso_cookie_check，SSO成功则执行token_verify，扫码分支执行challenge_start、scan_status、token_acquire；任一成功分支随后一次department_lookup，后者携带purpose=login_info。SSO登录的token_verify携带purpose=login_sso、chain=login，复验链按strategy记录department_lookup或token_verify，隶属于session_revalidate，携带purpose=revalidate；token_verify不混入登录信息查询。session_revoke用于退出、停用和复验失败后的撤销。每条事件增加chain字段，值为login、revalidation或revocation。token_acquire是私有扫码验证成功响应的本地解析阶段，HTTP元数据引用对应pollId。上游终态未取得token时也写token_acquire失败，waiting/scanned阶段继续等待。waiting/scanned也逐轮记end，不以去重丢掉阶段终态。停止轮询、撤销排队和完成各有结果，耗时用单调计时。

每条必须包含以下字段，无法取得的值显式为null。

- timestamp为UTC RFC3339，service、level、stage、phase、result，durationMs为本阶段耗时，start事件为0。
- correlationId为我方生成的本次认证/复验操作ID，eventId唯一，transactionId在存在时记录，pollId/attempt区分轮询与重试，parentCorrelationId连接复验、撤销或跨壳操作。
- trace_id、spanId沿docs/dev/observability.md接线，requestId沿现有edge/common链。后台复验创建新trace，显式关联已存操作ID，不复用过期HTTP span。对UDS的出站不添加内部身份或秘密追踪头。
- tenantId、providerId、configRevision、empNo及chain，tokenHashPrefix在token存在时记录，tokenMissing仅用于登录凭据解析诊断。每个department_lookup/token_verify的start/end及对应审计均必填purpose，值为login_info、login_sso或revalidate；sso_cookie_check记录cookiePresent（不输出Cookie名）、cookieFamily=portal/uds/none、eligible、suppressed、fallbackReason、验证结果与correlationId，token仅记哈希前缀，并记录departmentPresent、匹配条数和字段分支。会话创建与复验事件记录nextRevalidateAt、实际intervalSeconds，调度事件记录dueLagMs；session_revalidate/token_verify及审计记录strategy、credentialVersion、tokenReplaced、attempt/maxRetries、retryAt/retryDeadline、retry_pending/retry_exhausted，替换前后仅记oldTokenHashPrefix/newTokenHashPrefix；以failureClass/faultReason区分明确业务/身份失败与上游故障，在purpose=revalidate时前者立即撤销、后者在预算内重试；purpose=login_sso失败仅回落扫码、不撤销其他会话，另记identityComparisonVersion和identityMatched。所有这些是JSON字段，禁止作为Loki高基数标签。
- endpoint为固定路由模板和受信源别名，httpStatus、udsCode、udsSubCode、udsMessage、transportError、responseBytes。UDS错误消息先按第9.2节脱敏。challenge_start等无HTTP阶段保持httpStatus/udsCode为null，token_acquire可继承本轮上游HTTP状态。

阶段结果为success、waiting、scanned、failure、expired、cancelled。撤销日志区分requested/committed/propagated，记录目标epoch或修订与关闭连接结果，确保能判断“已判失效但停权尚未完成”。首个失败原因保留，后续重试另写attempt。正常阶段INFO，失败WARN，意外内部异常ERROR，详细原始响应结构DEBUG。login_info的HTTP、业务、解析及资料写入失败统一为WARN并记reason，chain=login的认证结果保持success，不产生会话撤销。

### 9.2 脱敏和详细级别

工号按用户授权可记录。token只记UTF-8原值SHA-256的小写十六进制前12位，同一token跨取回、部门查询、复验可关联。无token时hash字段为null。哈希前缀仅用于诊断。verifyCode、MD5拼接原文、clientIP、orgId、cookie、Authorization、企业私有认证头、设备码、verifier、navigationProof、浏览器退出抑制cookie、挑战key/value/binding、完整URL查询串、证书私钥与密钥全部屏蔽。

采用最小必要原则，正常和详细模式都省略姓名、部门全称、组织全称、邮箱、电话，部门查询事件的purpose、departmentPresent及字段分支足够判断该次信息查询或复验结果。未知响应字段的值默认替换为[REDACTED]，只保留已知字段键与容器结构，未知键若含个人值或凭据也替换为固定占位键并记录计数。UDS code字段经长度和格式白名单后保留，若与已知秘密或个人值重合则屏蔽。udsMessage保留已知安全字面量映射，其余记[REDACTED]及messageRedacted=true，避免上游把姓名或token嵌进错误消息。换行/控制字符转义，消息最多512字节。

详细开关名拟定为auriga.iam.uac.diagnostics.detailed，默认false，按provider和当前租户限定，使用AUTH-K定义的IAM持久化记录，启用时记录expiresAt和revision，默认15分钟自动关闭。管理台需iam.providers.write和现有管理Origin/CSRF校验，开启/关闭均记audit。全局AURIGA_LOG_LEVEL与该开关共同控制专用详细sink，开关启用时该sink可输出DEBUG而无需提高全局等级。所有副本在输出前核对权威修订和expiresAt，缓存不得超过到期时间，存储不可用时禁止详细输出。重启沿用持久化有效期，到期即关，开启/关闭通过事务与失效通知传播，明确启用只对后续请求生效。

开关开启时，在DEBUG事件增加udsRawSanitized，记录解析后的UDS原始响应结构。先递归处理大小写/别名敏感键及所有秘密匹配，再对未知值屏蔽，只保留经过允许列表验证的业务码、已脱敏消息、列表数量和必要字段存在性。token值一律替换为[REDACTED]，哈希前缀只在固定字段出现。HTML、非JSON、重复键、深度超限或不能安全解析的响应仅记内容类型、字节数和原因，不回显body。单次处理上限2MiB、单事件上限64KiB，超出记truncated及数量，禁止先将原文写临时文件后再清洗。

脱敏测试须将合成token、工号、姓名、部门全称、邮箱和电话植入header、嵌套对象、数组、未知字段、异常消息与转义字符串，断言允许的工号和token哈希之外无原值泄漏。日志写入、采集与导出前各自执行脱敏，采集端脱敏只作为补充。

### 9.3 audit、ops与Alloy/Loki接线

按现有contracts/events/audit.schema.json写audit.recorded，经common审计写入器和outbox进入auriga.audit.iam，交audit收集。各实际执行阶段结束写一条最小审计投影，action拟定为iam.uac.sso_cookie_check、iam.uac.challenge_start、iam.uac.scan_status、iam.uac.token_acquire、iam.uac.department_lookup、iam.uac.token_verify、iam.uac.session_revalidate、iam.uac.session_revoke，result按既有success/failure/denied映射。等待轮询投影标result=success并在details.outcome写waiting/scanned，不表示认证成功。身份未建立时actor使用type=system、id=null，details只含上述脱敏诊断字段。数据库事务外执行上游HTTP，结果审计在短事务写outbox，outbox失败留同eventId供可靠重试。详细响应结构只进受控运维日志。iam.uac.department_lookup复用同一action，以details.purpose=login_info/revalidate及chain区分。信息查询失败时只写该action的failure投影和WARN，登录审计仍为success，不生成撤销action。audit.details不包含部门全称。

IAM/edge在现有JSON stdout输出阶段日志，NAS与compose的observability/config.alloy已按compose project发现容器并送Loki。沿用service/container/job低基数标签，以JSON字段查询correlationId或trace_id。Desktop broker复用本机Auriga脱敏日志sink，托管插件沿现有容器日志或auriga-dsh-*.jsonl只读导入点采集，命名及轮转沿docs/dev/observability.md。Desktop日志仅在用户主动导出时打包到诊断包。链路验证分别断言本地落盘、Alloy接收、Loki查询与缺源提示。

IAM在复验失败、上游持续不可达、撤销传播失败时通过既有OpsSignals.observe按tenant/provider聚合告警，恢复时写active=false，source拟定为iam_uac，subject为providerId。连续不可达60秒或撤销传播30秒未完成触发warning，department失败及token-verify明确拒绝/预算耗尽立即停权，与告警阈值分别处理；token-verify预算内的上游故障仅告警及重试，不撤销会话。audit记录所有失败及恢复，ops不按工号/token建告警。沿既有去重、静默与通知渠道，消息只含分类和关联ID。

### 9.4 现场诊断下载与Desktop导出

服务器由IAM提供 `POST /api/iam/v1/admin/uac/diagnostic-bundles`，管理台身份源页面提供“导出诊断包”按钮。IAM校验当前管理会话、tenant、iam.providers.read、audit.events.read、ops.alerts.read及CSRF，读取受信audit/Loki来源，先完成脱敏及清单校验，再重新核权后直接下载zip。API和资源随NAS/compose默认部署包发布，E负责按钮，A负责后端和制品发布验收。部分缺源在manifest及X-Auriga-Diagnostic-Partial标注，权限失败不能导出部分越权内容。

Desktop由C在现有启动器新增 `diagnostics export` 子命令，通过随安装包自带Node执行 `auriga/runtime/cli.mjs diagnostics export --root <安装目录> --output <目录>`，Windows的auriga-desktop.cmd增加对应诊断参数分发。打包当前用户broker/插件脱敏日志、有效配置和调度元数据，缺源显式标注。独立插件同包broker也提供同一个导出入口。以上是待实现命令，当前cli.mjs仍只有install/rollback/uninstall/launch。开发工具实际名为auriga-ctl，位于infra/ctl，不承担现场导出，也不要求用户安装它。

一次执行完成范围校验、audit最小投影读取、Loki受信查询、本机受控日志读取、再次脱敏、清单校验和压缩。仅从受信配置取得数据源地址，用户输入只选择时间窗、provider与关联ID，不接受任意查询语句、URL或文件路径作为采集源。默认两小时、最大24小时，跨租户范围拒绝。安装级运维读取也只查询已指定的单租户，不用Loki无租户日志默认放大范围。现有audit读取权限由身份核验，安装本机数据源访问属于已授权运维边界，导出动作记录操作者和范围。

包名为uac-diagnostics-<UTC>-<随机ID>.zip，内容包括manifest.json、uac-steps.jsonl、audit.jsonl、ops-alerts.json、sanitized-config.json和可选的uds-detail.jsonl。manifest包含版本、时间窗、安装/组件版本、脱敏器版本、来源清单、各文件SHA-256、行数、缺源、截断与导出审计ID。诊断导出对Me/ShellAuth的展示资料只投影状态、更新时间与departmentPresent，省略department原值。部门查询日志及审计条目保留purpose和分类原因，诊断包按login_info/login_sso/revalidate分别统计成功/失败数量，并记录SSO缺失/抑制/回落计数，并断言login_info失败未出现关联撤销。配置只含sso-cookie.enabled生效值和Cookie体系存在性（不含Cookie名/域原值）、源别名、固定接口路径、systemCode存在性、originSystemCode是否为空、strategy、复验周期及重试参数的生效值和修订、retryAt/retryDeadline/attempt、tokenReplaced、脱敏会话的nextRevalidateAt、loginInfo状态和调度积压统计，省略token、真实凭据和个人目录路径。包中不得包含DSH journal、私有会话、vault、用户资源或完整环境。

先在当前用户独享临时目录生成已经脱敏的条目，拒绝符号链接/重解析点和输出覆盖，再扫描秘密与个人字段，生成摘要后原子发布最终zip。日志损坏或无法安全脱敏时省略该条并记录计数，不输出原文。临时目录受用户ACL/0700保护，完成或失败后清理。单包上限50MiB，超限分段或明确截断。Desktop命令以partial=true和非零退出码提示缺源，IAM下载以响应头及manifest标明partial，界面展示不完整原因，不能以空包声称成功。关闭详细开关时包里仍有已执行的登录、复验和撤销阶段基础信息。

Desktop诊断包保存到用户指定目录，服务器诊断包通过已认证下载交用户保存，现场用户主动发回。导出仅访问受信本地/部署观测源，默认不向第三方上传。

### 9.5 日志与诊断包验收

A/B/C/E交付前用同一替身分别测试SSO、扫码、到期复验及撤销链。SSO必须覆盖Portal/UDS优先级及禁止混配、Cookie存在且验证通过、验证失败/上游故障回落扫码、无Cookie、域外部署、单IP单域名部署、开关关闭、退出后跨标签页/刷新不自动登录、显式继续、登录session ID轮换、会话失效整页导航、跨站/iframe/预取/导航证明重放拒绝。Desktop以正式启动器验收系统浏览器已有SSO免扫码、无Cookie扫码、设备确认页auto不调用上游、显式点击后才验证、两端用户码一致、设备人工核码/step-up、取消及晚结果丢弃、不创建云实例、平台不可达跳过；不能用单纯broker内部API冒充完成。登录信息查询成功时，断言记录部门及更新时间，当前会话的界面投影status=available且正常登录链purpose=login_info调用恰好1次。登录信息查询失败时，断言登录仍成功、展示status=unavailable、分类WARN、无撤销，nextRevalidateAt与登录事务提交值严格相等，调用也恰好1次。重复complete和资料读取不增加调用，过期会话或旧身份的晚响应不写入新身份。

使用可控时钟断言周期内purpose=revalidate调用为0。重启仅领取已到期复验会话，未来到期会话保留原时间，独立信息查询作业按其持久状态恢复。到期后才出现purpose=revalidate的session_revalidate及按strategy选择的department_lookup/token_verify；department成功推进期限，单次失败撤销。token-verify成功原子替换token并推进期限，上游故障不推进期限也不撤销，完整响应中的明确业务/身份失败或预算耗尽撤销。复验使用独立correlationId，通过parentCorrelationId与原登录关联，部门查询不复用登录pollId。两种purpose各自断言HTTP/UDS码、分类、级别、审计action及脱敏诊断包计数。

详细开关默认关闭、启用/自动到期/重启关闭均需测试，关闭时无udsRawSanitized。开启时字段结构可诊断，任意嵌套秘密和个人值不可见。audit和ops必须断言实际接收的脱敏字段，Alloy/Loki局部测试使用本机替身或隔离采集器验证配置和查询，不访问真实部署。

一键命令覆盖完整包、Loki缺源、audit拒绝、日志畸形、超过时间窗/大小上限、目录越界/符号链接、详细关闭以及重复执行，检查清单摘要与秘密扫描。真实UDS联调不要求，报告保留替身响应、日志断言、采集链证据及缺源状态。

### 10. 返工后的实施验收补充

A/E必须验证UAC初始关闭、通用创建不能启用或写config、通用PATCH修改UAC enabled/config返回400、UAC唯一写入口的允许列表/ETag/停用撤销，以及读取的修订一致。link匿名401、认证过旧拒绝、会话/账号/tenant代次变化拒绝，login仍允许匿名，取消和失败终态可区分。

A/C验证设备授权来源绑定由IAM产生，伪造请求字段无效；UAC绑定缺失或伪造、来源会话退出/过期、复验拒绝、department上游不可达或token-verify预算耗尽、管理员停用和provider关闭后，刷新及其他audience兑换均拒绝，旧token连接关闭，快照reason准确。信息性部门查询失败仍保持登录成功和nextRevalidateAt。

C验证浏览器一次性票据30秒、重复消费、错误端口/Host/Origin、缺内存令牌/CSRF、跨端口DSH页面与其他用户访问拒绝，静态页不泄身份，刷新重新拉起可恢复。IPChelper实际DACL/peer身份须在真实受支持OS验证，不能用伪造请求头替代。

AUTH-K/A/E验证详细开关跨两个IAM副本一致、到期同时停输出、重启保留有效期、存储故障关闭详细输出及撤销通知丢失后的权威校验。A/E验证NAS默认安装的管理台下载按钮可用、拒绝跨tenant/provider/无权限、日志先脱敏和partial标志。C验证正式Desktop安装包的导出命令无需开发工具、同一脱敏器、目录越界/重解析点拒绝和缺源报告。C/D验证broker目录、版本及manifest摘要一致，只有D写插件打包接线。
