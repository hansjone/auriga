# UAC 后端部署与交接

UAC 是显式配置后才能启用的身份源。通用 provider 创建只能创建关闭的空配置；唯一配置写入口是 `PUT /api/iam/v1/admin/providers/{providerId}/uac`，请求只接受 `enabled`、`baseUrl`、`systemCode`，使用同一 provider 整行 ETag / `If-Match`。停用经既有会话撤销链传播到来源设备和刷新家族。配置值、私有映射和密钥不提交源码，不交给浏览器诊断接口。

## 部署输入

Compose 只读挂载 `deploy/compose/var/uac`，NAS 制品只读挂载 `${AURIGA_DATA_ROOT}/config/materials/uac` 到 `/etc/auriga/uac`。目录由部署方创建，限制为部署管理员与平台运行 UID 可读，不授予其他账号；配置文件建议 0640、目录 0750。两份 Compose 均可选导入其中 `uac.properties`。没有这个文件时保留原默认配置、UAC 仍不能启用；不要把这些私有文件放进制品或日志包。

`uac.properties` 支持下列部署属性；值由部署方填写：

- `auriga.iam.uac.adapter-config-file=/etc/auriga/uac/adapter.json`：私有企业协议映射。
- `auriga.org.uac-verifications-file=/etc/auriga/uac/members.json`：严格员工核实记录，见 [成员核实说明](../../org/impl/UAC-members.zh.md)。工号只 strip，保留大小写与前导零。token-verify 的 ASCII 忽略大小写比较不改变身份主键。
- `auriga.iam.uac.sso-cookie.enabled=true`：默认开启，Compose/NAS 同样默认开启；运维可关闭。浏览器需要在可读到企业 Cookie 的受信同域顶层页面导航，域外自动回落扫码。门户 Cookie 完整配对优先，其次 UDS 完整配对，禁止混配；优先配对验证失败不切换另一账号。
- `auriga.iam.uac.revalidation.strategy=token-verify`：另可选 `department`；`interval-seconds=7200`（60–86400），`retry-interval-seconds=30`（5–300），`max-retries=3`（0–10）。非法值拒绝启动。新配置在后续复验轮次生效，已开始轮次保留原预算；滚动重启不改变已存截止时间。
- `auriga.iam.uac.encryption.active-key-id`、`encryption.revision`、`encryption.keys.<keyId>`：密钥环，密钥材料为运行时安全生成的 32 字节 Base64。未显式配置环时沿用部署的 IAM 加密密钥及 `initial` ID，不能用临时随机 key 代替已有材料。

SSO 退出抑制使用 host-only 会话 Cookie；HTTPS 带 `__Host-`、Secure、HttpOnly、Path=/、SameSite=Lax，无 Max-Age/Expires。退出后自动尝试停止，跨标签页/刷新仍抑制；用户明确继续才能重新验证。未知或旧抑制 Cookie 仍抑制自动尝试。管理台退出不影响门户抑制。设备确认页 auto 永不出站，明确继续后仍须设备人工核码。

## 私有企业适配器

`UacEnterpriseAdapter` 是受信 Java 扩展，默认实现 `UacConfiguredAdapter` 读取部署 JSON；不提供浏览器上传映射或自选 URL 的接口。以下仅为**合成协议**结构，字段名、路径和编码要按部署方真实协议在私有文件中填写：

```json
{
  "allowedDestinations": [{"origin":"https://identity.example.com","addresses":["192.0.2.10"]}],
  "clientIP":"",
  "constants":{"origin":""},
  "diagnostics":{"originSystemCodeEmpty":true},
  "tokenVerifyUrl":"https://identity.example.com/verify",
  "qr":{
    "path":"/scan",
    "contentTemplate":"example:${x}:${y}:${systemCode}",
    "derived":{"integrity":{"algorithm":"md5","template":"${x}${y}${clientIP}${systemCode}${origin}"}},
    "body":{"a":"${x}","b":"${y}","check":"${integrity}"},
    "statusPointer":"/state","waiting":"waiting","scanned":"scanned","success":"verified",
    "tokenPointer":"/token","empNoPointer":"/employee"
  },
  "department":{
    "url":"https://identity.example.com/info",
    "body":{"credential":"${token}","employee":"${empNo}"},
    "successPointer":"/ok","successValue":true,"listPointer":"/rows",
    "empNoPointer":"/employee","departmentPointers":["/department","/fallback"]
  },
  "cookies":{
    "portal":{"token":{"cookie":"example_portal_token"},"account":{"cookie":"example_portal_account"}},
    "uds":{"token":{"cookie":"example_uds_token"},"account":{"cookie":"example_uds_account"}}
  }
}
```

部署 JSON 的 `constants` 最多 32 个字符串，操作 `derived` 最多 16 项，仅允许 md5/sha256 与模板拼接，无脚本求值；不能覆盖系统坐标/凭据变量，未知模板拒绝启用。`headers` 可在 qr/department 中配置私有静态或模板头，不从浏览器透传任何 header；代码不会自动附加我方内部身份/追踪头，部署方也不得在私有映射中配置这些内部凭据。Cookie 项支持 `urlEncoded`、JSON `pointer`；UDS 可配置独立 `clientIP` Cookie 项，缺失时使用服务器固定 IP 或空串。IP 必须为合法数字地址，不查 DNS。`diagnostics.originSystemCodeEmpty` 只声明布尔能力；未声明则导出 null，不导出私有常量原值。自定义适配器 `diagnosticCapabilities()` 同样仅允许固定布尔能力。

允许列表同时核对 origin 和 socket 实际 DNS 地址；地址集合变化时必须显式更新私有配置。生产仅允许 HTTPS，禁止重定向、自动重试和自动 Cookie jar。连接/响应/整体超时及响应体大小有界。token-verify 使用固定请求规范与响应判定：合法完整响应的明确业务/身份拒绝才失效，HTTP/解析/协议异常按持久预算重试；新 token 以凭据版本 CAS 原子替换。MD5 仅用于既有企业协议的请求校验。

## 密钥轮换

先向所有副本分发包含旧、新 keyId 和原材料的完整解密集合，再滚动切换 activeKeyId/revision。迁移后启动检查逐页解密全部历史密文引用，keyId 缺失或同名材料错误均拒绝启动；worker 在 ApplicationReady 后运行，每批最多 50 条重加密，以行锁/版本围栏避免覆盖并发更新，不改变认证代次和原期限。确认所有引用已迁到新 key、所有旧副本已退出后才能删除旧 key。回滚期间保留新旧解密集合；禁止直接替换同名 keyId 的材料。

## 诊断与观测

每次实际阶段都有 JSON start/end，逐轮扫描保留 waiting/scanned 与 pollId；信息查询一次且不阻断登录。中断的信息查询不重发上游，记录 WARN/interrupted；停止轮询记录取消/过期结果。最小事件持久化并经稳定 eventId 重试到真实 audit outbox；OpsSignals 聚合上游故障、重试耗尽、撤销投影故障与恢复。详细响应只经闭合脱敏投影进入专用 DEBUG sink，启用不要求全局 DEBUG。

复验日志及审计保留当轮 strategy、attempt/maxRetries、凭据版本、dueLagMs、重试时间与状态、新旧 token 哈希前缀、身份比较版本与结果；部门字段分支仅用 primary/fallback 别名。已被代次或租约围栏丢弃的晚响应在 session_revalidate 中记 skipped/late_result。撤销开始记 requested，只有真实事务提交后才记 committed；回滚记 rolled_back，目标 authGeneration 可关联。IAM 不假定下游连接已经关闭，connectionsClosed 保持 null；实际传播与关闭结果由消费 identity.session_revoked 的 edge/连接所属模块记录，并在主控跨线验收中关联。

`GET/PUT /api/iam/v1/admin/providers/{providerId}/uac/diagnostics` 使用独立 ETag/revision，默认关闭，默认有效期 900 秒。写入需当前管理会话、`iam.providers.write`、Origin/CSRF；输出前再次核对权威 revision/expiry，存储不可用时禁止输出详细字段。启用仅影响之后开始的请求，关闭/到期后不会泄出先前捕获的详细响应。正常事件、audit、Ops、ZIP 始终省略姓名、部门原值和完整秘密，token 仅保留 SHA-256 前 12 位。

`POST /api/iam/v1/admin/uac/diagnostic-bundles` 接受可选 `providerId`、`correlationId`、`sinceSeconds`（默认 7200、最大 86400）。需要当前管理 Cookie 会话及 `iam.providers.read`、`audit.events.read`、`ops.alerts.read`，含 Origin/CSRF，收集前/生成前/发送前重新授权。每账号每分钟最多 3 次、每进程同时最多 2 次，单包最大 50 MiB。返回 ZIP 附件、no-store、`X-Auriga-Diagnostic-Partial`。空源拒绝成功，缺源/损坏/截断在 manifest 和配置统计中明确记录；权限拒绝直接失败。详细开关在导出过程中撤销则终止下载。

文件为冻结 manifest、`uac-steps.jsonl`、`audit.jsonl`、`ops-alerts.json`、`sanitized-config.json` 及条件性 `uds-detail.jsonl`；清单记录导出审计真实 CloudEvent ID、时间窗、组件版本、摘要、字节/行数及缺源。文件在服务用户独享 0700/0600 或 Windows 保护 DACL 临时目录生成，拒绝链接/重解析点和覆盖，原子发布 ZIP，成功/失败均清理。服务器不接受输出路径或用户 LogQL/URL，不上传第三方。

两份观测配置均先走 `loki.process.uac_safe`，普通 JSON 与 Docker 外包装中的转义 JSON 各按实际 RE2 捕获组脱敏，再写 Loki。高基数身份仅为 JSON 字段。固定查询只访问部署受信 Loki origin，并限定 tenant/provider/correlation/time；无内部 token 或追踪头。NAS 的平台加入内部 observability 网络；Compose host-network 平台只访问 Loki 的 loopback 13100 端口。既有 `observability` profile 仍由部署方启用，未启用时下载明确 partial/Loki 缺源，不能声称已采集到 Loki。NAS 镜像继续遵守既有摘要固定流程。

## 标准设备客户端（AUTH-K2）

Compose 与 NAS 的 `auriga-dsh` 登记为 platform 公共设备客户端：`native-connect=false`，grant 为 `urn:ietf:params:oauth:grant-type:device_code,refresh_token`，scope 默认 `auriga.connect`。tenant 从部署登记取得；插件不能自报身份。`openid`、`offline_access` 只有部署登记允许时才能请求。旧 Beta 的 `native_tx` 分支保留原围栏，不能以客户端配置变化把标准 lineage 转入旧分支。

公共客户端同时登记浏览器授权码时，IAM按权威grant区分refresh：浏览器家族保留user/portal/sid；standard_device及历史设备模式继续受设备围栏约束。不能仅凭客户端scope把浏览器refresh解释成设备授权，也不能给历史设备补信任。

设备导航只走 `POST /internal/uac/device-navigation-proofs`，以 `userCode` 在线解析并绑定，返回 201；旧 `/internal/uac/navigation-proofs` 只接受 portal。设备确认仍须人工核码与原有 step-up，auto 不出站。所有浏览器关联号只读 `X-Auriga-Correlation-Id` 响应头。

批准从当前权威门户 session 保存设备、provider、账号与 UAC 来源绑定。标准平台票只有字符串 `aud=auriga`、`ptyp=ctx=device`、`did`，没有浏览器 `sid` 或实例身份。C 用 Bearer 访问 `GET /api/iam/v1/me/device-session` 取得冻结的 `StandardDeviceIdentity`，逐项比较 JWT 与来源绑定；此入口不接受身份选择参数、不返回凭据、不延长来源 idle。本人 UAC 凭据读取也在线验证标准设备来源，原始凭据仍只允许受信后端消费。

普通 refresh 与 `resource=urn:auriga:ai-gateway` 换票共用同一 refresh 家族，C 必须使用单一串行 broker，每次先原子保存后继 refresh。AI 票只含当前模型/协作权限交集，字符串 `aud=auriga.ai-gateway`，寿命不超过 300 秒及来源/设备截止。所有刷新、身份读取和准入核验检查来源、成员、provider、客户端与 UAC 认证代次；不延长来源期限。参数非法、Org 不可用或签发失败均不消费旧 refresh；合法重放仅撤本设备家族。

网关须使用新 `VerifyStandardDeviceAiAccess` RPC，内部身份限定 `svc:ai-gateway`，委派业务 tenant，传原始 AI JWT 和完整的 `auriga.connect.models.invoke` 或 `auriga.connect.collab.use` 权限键。旧 `VerifyDeviceAiAccess` 拒绝标准设备，继续承接 native/managed 专票；旧平台核验不接受 AI audience。目前网关消费者仍调用旧 RPC，需其所属工作线接入后才能声称标准设备模型请求已贯通。

`/oauth2/revoke` 用本客户端 access（平台或 AI）或 refresh 证明撤销本设备，不接收 did/source 选择器；其它设备与来源门户保持有效。IAM 的网络 active/batch/check 在通知丢失时仍查询当前标准设备权限与来源并拒绝旧连接继续使用；现有网关 reconcile 消费批量停权结果并 KICK 已建立的 NATS 连接，明确 inactive 不走故障宽限。真实 PG 下的标准设备停权与网关真实 NATS/IAM 替身踢线分别有测试，完整跨线部署仍由主控验收。现有 `identity.session_revoked` 的空 `sessionIds` 表示账号全部会话，不能用它发送设备单独撤销，本实现未新增该通知。未批准设备取消由 C 停轮询，服务端 grant 等 TTL；迟到令牌按契约加入安全撤销队列。

## 主控集成验收

本线不部署真实环境；合并与部署由主控串行执行。AUTH-K2 契约已合入本线，由 B/C/D/E 对接正常门户导航、设备 userCode 导航证明、标准设备串行刷新与身份投影、IPC DTO 和管理台下载按钮。内部导航证明仅允许 edge 专用 audience 与明确安装 tenant；`csrfCookie` 只由 edge 设置，不能进 HTML。浏览器 correlationId 以服务端响应头为准。

主控部署后从正常门户入口验证：SSO 成功/拒绝回扫码、扫码并发 complete、MFA、信息失败仍登录、退出后刷新抑制及明确继续、设备明确继续与核码；管理台验证唯一配置入口和 ETag、停用关闭既有会话、诊断开关到期及 ZIP 缺源提示。对两种复验策略用替身/可控时钟检查调用、撤销、刷新拒绝和日志关联。域名及纯 IP/HTTP 正常页面均须验收；容器健康和本工位后端测试不替代页面证据。所有受保护路径变更须【待审查】，交全新非作者 A 级完整独审及精确 HEAD CI。
