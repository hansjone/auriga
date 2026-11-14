# AUTH-K 冻结契约

本契约与 `docs/design/auth-shell.zh.md` 第8节共同约束 A/B/C/D/E。现有 UacChallenge、UacLoginInfo、ShellAuthCommand/Event/Snapshot 及 UacSessionBinding 保留；新增均使用 Uac/ShellAuth 前缀。此处只定义端口，尚无运行时实现。

AUTH-K2 增补优先见 [导航与 Cookie](../auth/navigation-cookie-policy.md)、[插件后端宿主 IPC](shell-auth-host.md) 和 [标准设备来源/刷新/撤销](../auth/standard-device.md)。下面的首轮全文摘要规则不再作为门禁；迁移状态按 navigation-cookie-policy 执行。设备用户码入口使用新增 device-navigation-proofs，浏览器不提供 grant ID。

## 固定业务代理

`shell-business-operations.json` 是固定操作、路由、输入/输出定义、audience、权限和重试规则的清单；`shell-business.schema.json` 是对应生产方校验契约。运行 `node contracts/plugin/generate-shell-business.mjs` 生成，`--check` 检查漂移。资源和 AI 请求/结果从已有 OpenAPI 引用投影，不另造业务字段。跨版本消费响应允许额外字段；请求操作名和输入始终严格校验。清单中权限是最低要求，不赋予调用者权限，远端现有每项权限检查照常执行。

盘点基线为 PR #320 的精确 base。以下覆盖全部已有平台调用族：

- `resources/catalog-api.js`、`catalog-service.js`、`catalog-routes.js`：catalog 包列表、创建、版本上传/查询/提交、manifest、下载票据/下载、安装选择/列表/报告/续租及制品下载，对应清单 `Unified*` 操作。
- `resources/member-api.js`：旧版插件包/版本读取、member 票据、下载、安装选择/列表/报告、撤销集；`resources/channel.js`：包/版本、distribution 创建/读取。对应清单同名 OpenAPI operationId。保留两代资源协议的验签和 wire 原文，不在 broker 中重序列化受签字节。
- `local/member-profile.js`：`profile_read` 及 `listNetworkSystems`；设备/实例 ID 由 broker 注入，查询不能指定其他 owner。
- `network/device-provider.js`：`network_open` 内部完成 `/network/register`、`/network/tickets`，持有短票和 NATS 凭据，绝不把原响应交插件；keys/announcement 使用清单固定操作；`network_send` 传已有 protocol 帧；`network_submit` 传 durable-send 定义的请求。
- `network/transport/nats-link.js`、`session/session.js`、`durable/*`：WSS 目标与 subject 来自受信安装和服务端已验证绑定，不接受客户端目标、凭据或任意 subject；流中只交业务帧。
- `ai/index.js`、`managed/models.js`：`listAiModels`、`createAiChatCompletion` 和 SSE 的 `createAiChatCompletionStream`；个人模型继续本地，平台模型只经 broker。`ai/refresh-broker.js` 的 token exchange 改由 broker 持有并核 AI audience，不对插件返回兑换结果。
- `local/team.js`：`team_initialize`、`team_list_tools`，四个固定工具操作及五个 `team_node_*`；远程入口仍为 `/ai/mcp`、`/ai/team/node/*`。endpoint/binding、session/toolCall/rootCall 元数据由可信 executionContextId 查得，禁止模型或网页声明。
- `network/diagnostic-probe.js`、`base-tools.js`：`provider_diagnostic` 仅返回可达性，不返回平台凭据。
- `local/runtime.js`、`local/platform.js`：providers、登录事务、token/revoke、logout、JWKS；`settings/store.js`：JWKS；`network/managed-device.js`：托管设备兑换。这些为壳控制面，C 持有，D 删除平台认证生命周期；不作为任意 HTTP 代理提供。
- `managed/heartbeat.js`：workspace instance heartbeat 保留托管宿主可信通道，不能让本地设备调用；`managed/host-auth.js` 的 edge 身份头验证保留。
- 企业 skill 的两条兼容路径见下节，它们是受任务能力保护的独立端口，不能经普通业务代理调用。

输入只带业务参数。broker 从受认证 IPC peer、当前 snapshot、可信宿主执行上下文构造 tenant/member/device/session/provider、clientId、node 和身份头。资源 body 中既有签名 identity、clientId 或 Team arguments 中目标 ID 是业务断言，须与权威绑定比对，绝不作为请求者身份；错配拒绝，不能盲透传。业务对象中的 URL（例如模型内容）不是出站路由，代理禁止据此选目标。顶层 URL/header/token/身份字段拒绝。

每个固定操作的 `requestHeaders`/`responseHeaders` 是逐操作白名单，不接受任意头。允许的请求头放在 `input.headers`，键统一小写；生成器保留上游 If-Match/Idempotency-Key 的必填约束，资源写操作额外允许这两项可选兼容头。缺必填头在发上游前拒绝，不自动补造 revision。broker 自行生成 Authorization、Cookie、tenant/member/设备身份头，调用者不能覆盖。成功 `output` 统一为 `{statusCode, headers?, body}`，headers 仅该操作清单列出的安全头（例如 etag）；插件读取 etag 后按既有安装/包 revision 发送 if-match。不得缓存或返回认证头。

业务失败通过 `ShellAuthBusinessResult.error` 返回，含固定 code、retryable 和可选 upstreamStatus/retryAfterSeconds；与成功 output 互斥。远端返回非2xx时保留实际 400–599 状态。404→common.not_found、409→common.conflict、412→common.precondition_failed、400/422→common.validation_failed、401/403→common.unauthenticated/forbidden，以上 retryable=false；429→common.rate_limited，500→common.internal，502/503/504→common.unavailable，retryable=true。已知且合法的 resource.digest_mismatch/signature_invalid/storage_unavailable 保留对应码（前两者不重试，存储暂不可用可重试）。其余4xx映射 common.internal/false，其余5xx映射 common.unavailable/true；不得信任未列出的上游 code。无上游响应的网络故障返回 common.unavailable/true，不填 upstreamStatus；本地校验/绑定失败使用相应 common.* 安全码。retryable 仅表示故障性质，写操作仍受操作清单幂等与截止约束。Retry-After 只解析为0–300秒整数；上游 message/detail/body/header、堆栈和 URL 不进入错误结果。建立流前失败用此分支，建立流后用 error 帧；IPC 准入失败还可用安全 Problem，不能把远端业务错误误报成 IPC 鉴权失败。

网络 `frameWire`、`requestWire`、`resultWire` 和密钥发布 `wire` 使用规范 base64 包装原始 JSON 字节（当前单次上限65536字节）。输入解码后必须按字段描述指向的既有 network JSON Schema 严格校验，再核当前来源/目标/连接权限；只允许原协议客户端可发送的帧，拒绝客户端伪造 server_auth、服务端事件或请求者身份。返回也须先核签、期限及业务关联。wire 不是任意网络转发：目标与 subject 始终由 broker 构造。它避免 Go/Java 日期或数字转换破坏原始签名字节。此上限也适用于本代理的完整密钥发布，超过上限失败关闭并报告不可用，不截断受签发布。

64KiB 沿用 `clients/dsh-plugin/src/network/device-provider.js` 现有响应读取上限。契约测试以128项、每个kid长128、最大uint64十进制值、最长purpose及完整根签名字段构造紧凑 UTF-8 JSON：SignedKeyset 45763字节，公告418字节。合成签名只用于结构与大小验证，不声称验签通过；实际发布仍以原始字节计数，含空白超过65536同样拒绝。base64解码后须核实际字节数并核规范编码，不能只看编码字符串长度。

清单中的 scope 为设备授权范围；permission 继续执行远端业务权限。Team/网络必须具有 auriga.connect.collab.use，模型设备另外需要 auriga.connect.models.invoke；Team 与模型的 audience 为 auriga.ai-gateway，平台资源/网络管理读取为 auriga。不得将平台令牌直接用于 AI。

每次请求检查当前 authenticated、epoch/revision、凭据有效期及来源绑定；请求 expiresAt 最长30秒。操作开始、每次外部副作用和响应提交均重核；跨用户/tenant、撤销、旧代次、重启及晚响应一律拒绝。requestId 同 epoch 内容幂等，变更内容冲突。读取仅可在截止内重试；写操作没有明确幂等键不能自动重放；丢失下载票据/刷新响应不重放原凭据。业务幂等键经既有远端语义处理，不能把 IPC ack 当业务成功。

流 ID 绑定当前 OS 用户、DSH owner、平台身份、epoch/revision。开始零 credit，credit 命令每次增加至多1MiB，未确认缓冲上限1MiB；单 data 帧解码不超过64KiB，sequence 严格递增，只有 data 可带 data。credit 必须带 creditBytes，cancel/close 不带；队列溢出以 common.rate_limited 关闭。cancel 必须中止 HTTP/模型 reader 或关闭当前 NATS 订阅；close/end/error 终止且不可恢复旧流。流空闲30秒关闭；身份变化立即取消所有旧流、清队列，断开不自动重发有副作用帧。断线恢复按已有 durable-send 对账，不能声称恰好一次。

`createAiChatCompletion` 仅接受 stream 缺省/false，返回完整JSON；`createAiChatCompletionStream` 必须 stream=true，先返回 `{streamId, encoding:sse_utf8}`，再由 `/business/events` 发送 data。data 是上游原始 SSE UTF-8 字节的 base64 分块，保留换行、event/id/data 和多行边界；分块可切开 UTF-8/SSE 帧，消费端按 sequence 拼接并增量解码，不能把每块当独立 JSON。收到并交付 `data: [DONE]` 后发唯一 end；EOF 前未出现结束标记视为 common.unavailable 错误，不能伪报完成。cancel/close 发唯一 closed；上游或本地失败发唯一 error，必须带同值 code 与完整 ShellAuthBusinessError。终止后不再发 data，取消/错误时释放 reader 与缓冲。

二进制上传使用 `upload_open`→`upload_chunk`→`upload_finish`。open 声明整个文件 sizeBytes（0–67108864，与上游64MiB上限一致）、小写十六进制SHA256和固定mediaType；返回 uploadId/streamId、maxChunkBytes=65536、creditBytes=1048576。客户端按从0递增的 sequence 提交规范base64块，每块解码≤65536且消耗相应credit；broker 持久暂存成功后发 upload_ack（ackSequence为累计已收块序号，creditBytes为本次返还字节，事件sequence独立递增），不得重复累计返还。超出credit、乱序/重复、累计超声明长度立即错误并清理。upload_finish 必须等全部块确认，再核累计长度和全文件SHA256，错配返回 resource.digest_mismatch/false；成功返回 ShellAuthUploadReference 并 end，不能只校验传入摘要格式。零字节文件可直接finish。cancel/close/过期/身份变化清理暂存；单次上传最多5分钟。uploadId及暂存引用绑定同一owner/epoch/revision，不能跨用户使用；finish本身不发起平台写入，后续固定业务操作才消费一次引用，按原 multipart 字段名组装字节并重新核文件/总请求上游限额。JSON IPC中的manifest/archive/artifacts等binary位置全部改用此引用，禁止File/Blob或任意文件路径。

## SSO 导航与退出抑制（A/B）

B 渲染门户登录页时调用内部 `POST /api/iam/v1/internal/uac/navigation-proofs`，仅传 continuation=portal。设备续接只能调用 `/internal/uac/device-navigation-proofs` 并传 userCode，两个请求都拒绝 deviceGrantId；IAM 自行解析权威设备事务。共同字段是 requestId/providerId/精确origin、实际 Fetch Metadata 证据及当前事务/抑制/CSRF cookie；不从外部body捏造。B以svc:edge内部服务JWT（aud=auriga.internal、ptyp=internal_service、ctx=service）及受认证内部传输调用A；必填auriga-tenant-id头从受信入口配置取值并剥离浏览器同名头，A核委派白名单，不能用服务JWT平台tid替代目标tenant。公开edge禁止转发两个内部路径。A核受信入口、tenant/provider及设备事务，自己计算auto资格，设备恒false，缺导航证据也false。B不得指定autoEligible或proofHash。

A原子签发并持久化 UacNavigationProofBinding，只存32字节 proof 的 SHA-256，TTL120秒，绑定完整 Origin、tenant/provider、portal audience、login 用途、浏览器事务及 portal/device 续接。响应 UacNavigationIssued 含context、transactionCookie和ttlSeconds=120；B设置事务HttpOnly cookie，将context放入no-store登录HTML。GET仅签发导航证明，不建立登录会话、不调企业验证。requestId重用冲突；丢失签发响应重新渲染时作废旧证明。外部不可提交autoEligible/continuation或绑定记录，SSO消费仍由既有POST /login/uac/sso检查并CAS一次消费；过期/重复消费拒绝，不因重试续期。

auto 资格严格要求 Fetch-Dest=document、Mode=navigate、Site=none/same-origin、User=?1，非 iframe/预取；SSO POST 检查事务 cookie、精确 Origin、CSRF，CAS 一次消费 proof。设备确认 auto 只回 scan_required 且零上游调用；continue 必须本页显式点击，再完成人工核码，永不创建云实例。普通门户成功继续原 workspace/open 流程。丢响应读原 LoginTransaction，不重新验证；扫码回落替换导航事务。returnTo 只许受信相对路径，拒绝绝对/协议相对/编码绕过与反斜线。

退出同源受保护 POST 在清 session 前写 UacLogoutSuppression。独立 host-only HttpOnly SameSite=Lax 会话 cookie，无 Domain/Expires/Max-Age，HTTPS 用 __Host- 前缀。不含企业材料，跨标签共享，不随登录 session 轮换清除。continue 只授权当前事务且不清抑制；存储失败禁止 auto。会话失效整页导航，脚本跳转无手势则显示继续/扫码。Cookie 字段从私有配置取名，完整 Portal 对优先，其次完整 UDS 对，不混配；优先对失败直接扫码，不切另一账号。SSO 成功保存验证返回新 token，轮换平台 session ID，与扫码共用恰好一次 login_info。

## loopback 备用控制页（C）

绑定 127.0.0.1 随机端口，静态 GET 无身份；仅状态/跳过/取消/拉起受信门户，不代理登录步骤。启动票据32字节随机，哈希存储，TTL30秒，绑定 epoch、完整 Origin、端口、目的。票据放 URL fragment，页面立即 replaceState 清除。POST `/loopback/exchange` CAS 单次交换 ShellAuthPageSession。nonce32字节；pageToken/CSRF 各独立32字节，仅页面内存，绑定同一 nonce/epoch/Origin/端口及15分钟截止。

后续受控 POST 同时检查 X-Auriga-Shell-Token、X-Auriga-Shell-CSRF、精确 Host=127.0.0.1:当前端口与 Origin，拒绝 localhost、跨源/异常 Fetch Metadata、错端口、重放和过期，不用 cookie 认证。刷新失去内存令牌必须重新由启动器拉起。no-store、no-referrer、frame-ancestors 'none'、仅同源 CSP。IPC 仍必须真实 OS SID/uid、DACL/peer 准入，不以请求头冒充对端。

`/loopback/messages` 仅用独立 ShellAuthPageCommand：snapshot/open_login/cancel_login/skip_login，schema拒绝logout、subscribe和任意URL。skip_login只在未认证/认证中取消当前尝试并返回本地DSH，不撤销有效平台身份；已认证时返回conflict。页面不获得完整IPC命令能力。

## 来源绑定、复验、密钥（A/C）

UacSessionBinding 保留 sourceSessionId/authGeneration/providerId；UacCredentialBinding 新增完整 tenant/member/device 及有效期投影，只在受信内部通道读取。credentialVersion可选，仅为凭据写入CAS/读取最新值的元数据，绝不作为设备或外壳准入条件；token-verify换发新token只递增此版本，来源会话/认证代次/provider不变时已有绑定继续有效。授权批准由 IAM 从权威 session 产生，刷新及每个 audience 兑换重核来源会话、代次、provider/member/device 状态。禁用/退出/过期/复验拒绝后，所有派生授权与旧连接撤销。不能从请求 body 接受绑定。

部署配置使用 UacRevalidationPolicy：默认 token-verify、7200秒、30秒重试间隔、3次额外重试。边界与默认值由 schema 定义；配置键为 auriga.iam.uac.revalidation 下 strategy/interval-seconds/retry-interval-seconds/max-retries。配置整组 revision 持久化到当轮；重启不重置 nextRevalidateAt、attempt/retryDeadline；只有到期才做判定复验。token-verify 仅完整 HTTP2xx 双层明确失败码/有效主体不符立即撤销，其余运输/格式故障进入有界重试，耗尽 reason=revalidation_unreachable；department 单次失败撤销。登录取得 token/工号即成功，异步 login_info 失败仅 WARN，不改到期时间、不撤销。reason 使用已有 ShellAuth 枚举，新 failureClass/faultReason 使用 UacRevalidationFailureClass/UacFaultReason；旧 UacRevalidationReason 同枚举保留兼容。

UacCredentialEnvelope 为 AES256-GCM，nonce12字节随机，AAD 使用明确 UTF-8 编码的 tenant/provider/purpose（长度前缀，禁止分隔拼接歧义）；ciphertext 为 base64url 的密文与16字节 tag。keyId 非秘密，实际32字节密钥只从受保护部署密钥源取，不进 API/日志。UacDecryptionKeyring 只公开键标识，activeKeyId 必须在解密集；未知 keyId 拒绝，不尝试全环猜测。旧无 keyId 密文只能映射部署显式 legacyKeyId，不可静默当新活动键。

轮换：先向全部副本分发旧+新解密集并确认就绪，再切 activeKeyId；新写使用新键，分批断点重加密旧记录并 CAS credentialVersion；验证全量可解密及无旧引用后再移除旧键。回滚保留旧/新解密材料；轮换不改认证代次，不覆盖并发换发的新 token。

## 诊断（A/E/C）

GET/PUT `/api/iam/v1/admin/providers/{providerId}/uac/diagnostics` 使用独立 tenant/provider 记录和 ETag/revision。仅管理会话；读 iam.providers.read，写 iam.providers.write、Origin/CSRF、If-Match；与 provider 配置修订完全独立。默认关闭，启用最长/默认900秒，到期自动关闭；所有副本输出前查权威修订及期限，缓存不越期，存储故障关闭详细输出。重启继续原期限，不刷新15分钟。开启/关闭写 audit。

沿已有 POST `/admin/uac/diagnostic-bundles` 下载，必须额外 audit.events.read、ops.alerts.read，生成后重新核权。范围默认2小时、最多24小时/50MiB，仅当前 tenant 和指定 provider；不接受查询语句/URL/采集文件路径。manifest 使用 UacDiagnosticManifest，版本/组件版本和每源缺失原因按扩展字段提供；SHA256 对应包内最终脱敏文件，partial 同时出现在响应头和 manifest；权限失败拒绝整个包。Desktop diagnostics export 同一脱敏器，缺源非零退出，禁止重解析点、覆盖和越界；临时目录用户独享，失败清理。

正常/详细/导出均只允许固定安全字段；token 只留 UTF-8 SHA256 小写前12位，Cookie/Authorization/proof/verifier/能力/二维码/密钥/完整 query 一律省略。工号允许，其余姓名/部门原值/邮箱/电话/未知值屏蔽。udsMessage 仅固定安全字面映射，未知为[REDACTED]。详细结构只在有效开关下递归清洗后 DEBUG，不可解析 body 只记类型/大小/原因；上限2MiB输入、64KiB事件。日志、采集、导出各自脱敏，不先落原始临时文件。

`auth/uac-diagnostic-log.schema.json` 定义最小导出投影的生产方白名单，不替代 A/C 的递归脱敏器。`test/auth-k-fixtures.json` 提供固定权威上下文输入/expected及数据式acceptanceSchema，包括正常token轮换仍有效正例。调用点清单和保留通道由独立维护的 `shell-auth-call-sites.json` 描述；sources摘要仅历史记录，不再锁源码。当前legacy/broker规则及限制以 [迁移门禁](shell-auth-migration.md) 为准，D迁移提交更新状态，白名单变更交独审。`test/auth-k-transport-fixtures.json` 供实现线复用错误映射、skill头丢弃/超时截断和HTTP状态向量；这里仅校验契约，不声称执行归一化handler。两个IAM副本、真实IPC对端、上游替身及采集链的运行时验收归A/C/D/E。

## 企业 skill 兼容（A/C/D）

ShellAuthAgentCredentialsRequest/AgentCredentials/OutboundRequest/OutboundResult/AgentError 保留原型 `oclaw/dsh-team/lib/agent-auth.js` 的接口字段；只有这里允许 HTTPS 出站 URL。GET/POST `/dsh-team/agent-credentials`、POST `/dsh-team/outbound` 保持现场 helper 路径。真实 socket 回环对端、任务能力 audience/600秒期限/scope/撤销、本人 DSH session owner 必须全部通过。header X-DSH-Session-Id、X-Dsh-Team-Task-Capability 与 body sessionId/dshSessionId、taskCapability/capability 兼容，互相冲突拒绝。raw 需要 credentials，代发需要 outbound。allowRawAgentToken 默认 true，实时关闭下次 raw 返回403 raw_token_disabled，不改变 outbound。

url 必须 HTTPS、无 userinfo，hostname 精确匹配部署 outboundAllowedHosts；缺省=空表=全拒绝，主机只来自私有配置，禁止重定向及 DNS 重绑定。为兼容现场skill，输入headers接受未知键；handler只保留content-type/accept/accept-language/x-lang-id/x-requested-with（不分大小写），非null值按String转换并截1024字符，Origin/Referer/X-Origin-ServiceName/Authorization/Cookie等其余头静默丢弃，不返回400。企业身份头由handler读取当前权威凭据重建，空Content-Type且有body时补application/json;charset=UTF-8。timeoutMs按原型 `Math.min(Number(value)||30000,60000)` 处理，60001截到60000；不是schema拒绝。响应剥离token/Cookie/认证头；上游4xx/5xx仍返回ShellAuthOutboundResult，HTTP状态与statusCode都取上游，X-Dsh-Team-Outbound-Status保留该状态。raw成功no-store，返回empNo/token/updatedAt（兼容null），只取当前权威凭据。

仅本地错误沿原型 `{error,message,sessionId?}`（sessionId兼容null），用error字段与出站结果区分：raw关闭403、认证或归属失败401、非本机403、非法400、未就绪503、请求运输失败502 upstream_failed、host_not_allowed403；不是Problem。上游HTTP500本身不是本地upstream_failed。message只给本地安全映射，不拼接可能带凭据的异常原文。这里是loopback HTTP加任务能力认证，业务代理端口才是受认证OS IPC。

DSH_TEAM_BASE/DSH_TEAM_TASK_CAPABILITY、已有 DSH_SESSION_ID、DSH_WEB_URL 回退与 DSH_TEAM_HELPERS 保持；uds-skill-auth/scripts/uds_skill_auth.py 的 request()/resolve(apply_env_aliases=False) 不变。默认任务 outbound+credentials，关闭 raw 后仅 outbound；执行结束/撤销/jobs结束清能力。平台 token 不进全局环境，读取/代发前与结果提交时核认证代次，换发后下次读新值，退出后拒绝。凭据副本仅进本人 skill 进程内存，工具结果只交业务数据。

## AUTH-K3：注册投影、下载票原始字节与发布制品

`network_open` 输入仍只有 `executionContextId`，必须是当前已验证插件连接的存活原生 session 句柄。
成功 body 为 `{streamId,registration,binding}`。registration 冻结为平台确认的
`{tenant,realm,node,device,systemAddress,created}`；created 仅触发本次注册的首次 network_open 可消费一次；同连接再次打开或 requestId 重放均返回 false，不能持久化历史 true。结构直接引用 network/register-result.schema.json。
C 核验 tenant/device 与当前登录、realm 与安装配置、systemAddress 与规范系统地址一致；realm/node 为正 uint64 十进制字符串。
`binding={executionContextId,shellEpoch,revision}` 来自 C 当前上下文，句柄经 K2 绑定实际 hostSessionId 和 OS peer。
D 先核对 binding 与当次请求再使用身份；注销、换代、会话撤销/到期、流关闭均须清除此投影。
缓存重放重新检查会话句柄与流。选择扩展 network_open，使身份与已完成注册的连接原子返回，避免独立注册操作重复消费注册或产生脱离连接的身份。

`UnifiedCreateDownloadTicket`、`UnifiedRenewInstallation`、`createMemberResourceDownloadTicket` 成功输出 body 改为 `{wire}`。
wire 是完整 HTTP 成功响应 body 的标准 Base64，非某个 JSON 字段；C 在任何 JSON.parse 之前保留，非空、最多 65536 字节，返回前检查当前代次。
D 解码后须严格词法解析，再依原资源契约校验签名封套、主体、用途和期限；重复键、1e0 等非法词法由 D 拒绝，不可先 JSON.parse 再 stringify。
C 不替 D 宣称验签通过；错误响应仍走既有安全错误映射。共享向量 `test/auth-k3-fixtures.json` 要求 wire 逐字节保真。
其余操作没有 wire 通道，D 不得提供任意 URL/header 或注册身份字段。

`profile_read` 固定读取 `/api/iam/v1/me/device-profile`，memberId 取已验证 device-session；account.id 与设备 JWT sub（accountId）比较，account.tenantId 与当前 tenant 比较，displayName 取 account。accountId/memberId 可不同；IAM 设备在线资料接口和迁移依据见 K2 shell-auth-host.md。
`provider_diagnostic.available` 表示已启用 providers.items 中存在当前登录的 providerId；空表或仅其他源为 false。
非法结构拒绝，输出仍是严格固定 DTO，额外 account/session/provider 数据不传给 D。

两个发布入口复用 `.github/workflows/auth-release-artifacts.yml`：每次调用只构建一次 broker/helper，以该目录打包插件，再传 auth-broker 与 auriga-dsh 制品给 Desktop 组装或插件发布。
Desktop 显式传 `--broker-artifact` 和该次 `--plugin-tgz`；插件打包也必须传 `--broker-artifact`。
消费者检查 manifest 文件摘要、路径及 helper 必需项；缺失、篡改、未列出文件均拒绝，不回退重新编译。
Desktop 按 manifest 校验插件内全部文件，并逐文件比较插件与安装 broker 字节相同。不同发布运行不假设 C# 重编结果一致。
本地验证：`node --test clients/shell-auth/test/artifact.test.mjs scripts/plugin/package.test.mjs`，检查一次构建的两消费者字节、打包后 manifest/helper 及缺失/篡改拒绝。

迁移说明：以上三个下载票操作原有 DTO 输出已移除，不再双发；D 必须从 wire 解码原始字节、严格词法解析及验签后自行生成业务 DTO。所有消费者应与此契约同步升级，不得将 wire 当旧 DTO 使用。

## MSGNET v2 固定控制操作

`getNetworkKeys` / `getNetworkKeyAnnouncement` 分别读取 `/api/collab/v2/network/keys` 和 `/api/collab/v2/network/keys/announcement`，消费 `Publication` / `SignedKeysetAnnouncementV2`，权限 `collab.msgnet.read`。受签原文以 base64 wire 返回，解码上限 32MiB；协议帧和信封仍沿用 64KiB 上限。

`getPublicNetworkControl` 独立 GET `/api/collab/v2/network/bootstrap/controls`，平台请求不带 Authorization，不依赖消息连接或 `network_open` 权限。`confirmNetworkControl` 独立 POST `/api/collab/v2/network/controls/receipts`，权限 `collab.msgnet.control.install`，仅发送本地已持久化的安装签名回执；响应 `IsolationRecord` 的 scope、revision、digest、challenge 完全匹配后才清除待报标记。它们与调用点清单一一对应，均不接受 URL、header、pin 或私钥路径参数。本地 broker 的主人/epoch/revision 门禁仍适用。

桌面 broker 使用同一生产 v2 信任库，tenant/realm 和安装身份从可信设备及安装配置派生，根控制隔离立即关闭现存连接；公有控制与回执操作可在没有 NATS 连接时执行。安装器通过受保护 pin/私钥文件提供独立身份，与插件安装说明一致。
