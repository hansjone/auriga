# 共享 DSH 首期契约 v1

第二批增量管理接口、逐条裁决及 W/IAM/U 接法见 [ADMIN.md](ADMIN.md)；旧 v1 控制面和数据面语义保持不变。

本目录冻结 K 线控制面与数据面。schemaVersion=1；共享入口身份单独为2，绝不复用旧 owner 身份 v1。所有整数须为 JSON 安全整数，时间为 UTC Unix 秒。生成类型不代替 schema 和语义校验。未知模式、action、scope、签名字段和版本拒绝。旧独立/桌面协议保持原义。

protobuf仅用于内部传输；签名及schema JSON须显式映射，不能直接签protojson（int64字符串/缺省字段与本协议不同）。适配器将有界int64转为JSON整数、输出全部必填字段，再按schema和语义检查；未知字符串枚举及空缺省值拒绝。Capability 的 credentialVersion 是对应 audience 授权材料的当前版本，企业场景对应 IAM 企业材料版本；模型/消息由各自权威适配维护，不含任何上游密钥。

proto的schema_version/cancel_wait_seconds/archive_retention_seconds均为int64，mode复用已有DshInstanceMode：DSH_INSTANCE_MODE_SHARED映射shared、DSH_INSTANCE_MODE_DEDICATED映射dedicated；UNSPECIFIED和未知数值均拒绝，不能透传为字符串模式。

本契约只约束自研路径。首期互信团队接受原生 owner 旁路、互读文件、全池故障，以及 A 驱动 B 会话后以 B 企业身份操作、审计归 B 的风险；签名和能力验证通过不代表这些风险消失。

## 权威与端口

- W：`openapi/workspace.yaml` 引用 `shared-dsh.yaml` 的4个接口；`proto/auriga/workspace/v1/shared_dsh.proto` 提供 Resolve/Register/Revoke/GetResourceView/ReportResource 五个内部 RPC。内部服务认证沿原有 auriga.internal 和可信租户 metadata，跨租户对象404；Resolve 必须检查 IAM 成员及平台会话有效性，不依赖失效推送缓存。只在 ready 签票或准入。Register 在会话对外发布前事务登记，重复完全相同绑定幂等，异绑定冲突；parentSessionId 非空时继承父全部归属，子 sessionId 独立，不接收客户端自报 owner。未登记会话拒绝能力签发。Revoke 持久化增加 epoch/authGeneration/revision，旧代次永不恢复。客户端收到晚ACK按完整绑定和修订丢弃。
- E：消费 OpenTicket/Identity/Binding；W 签 OpenTicket，E 消费并签 Identity。`POST /me/placement/open` 仅 portal，源头做 Origin/CSRF/成员检查；Idempotency-Key 按租户/成员/请求指纹24小时去重，但不重发已消费/过期票据，需新键再开。返回 exchangeUrl 是可信 domain 或 IP 入口的 `/_auriga/placement-ticket`（IP前缀 `/i/<physicalInstanceId>`），通过 POST body `{ticket}` 交换，避免 URL 查询日志；E 原子消费 jti 并再核 W 与 IAM，302到正常根页面。cookie 沿部署契约实例 cookie，服务端记录完整 binding/platformSessionId，绝不只记录物理池。每 HTTP 请求及 WS 建连重新核权威代次，撤销主动断开 WS；核权威不可用拒绝。清除外部所有 Auriga-Identity；只接受单值的新签名头，ingress mTLS 只信 edge。
- R：消费 RuntimeIntent，产出 RuntimeReceipt/ResourceReport/GuardianImpact；节点唯一落盘。目录固定 `/var/lib/auriga/pools/<physicalInstanceId>/{host,control,members/<tenant>/<member>/<epoch>/workspace}`，不接收调用者路径。每池20登记/4活跃/每人1活跃、每人5368709120字节/100000 inode、80%内存停止准入为初始预算。相同 operationId/action/binding 幂等，异指纹拒绝。stop-pool 必须由进程外 supervisor 确认整个执行树停止。取消30秒超时请求停全池，30秒不是完成停池保证。无收据/超时/晚收据不推进；成功切换还须全部处置步骤及正常入口探测通过。R→W收据经既有节点 mTLS 控制流承载本proto消息（SessionResponse字段9/10/11下发意图、资源视图和撤销，SessionRequest字段9/10/11回报收据、资源状态和guardian影响；未实现不得启用共享）。
- P：消费 Identity/SessionBinding/Capability/ResourceView；sessionId 为服务器登记的宿主会话 UUID。受控 helper 沿既有 `/dsh-team/outbound` 与 `/dsh-team/agent-credentials` 接口，shared要求 taskCapability 和 dshSessionId，别名并存不一致拒绝；共享能力为本协议，不改变桌面旧helper。每调用从归属表读最新不可变上下文、IAM 最新企业材料；authGeneration/credentialVersion 变化立即拒绝旧能力、丢弃迟到响应。保留 raw 默认true，关闭后下一次 credentials 拒绝，仅outbound；raw不作为工具结果。不得修改 process.env/global shellEnv，不允许任意cwd、方法、action、动态安装，执行工具及guard外控制器均关闭。
- N：消费 SessionBinding/Capability；model.invoke仅模型 audience，message.send/receive仅消息 audience，credentials/outbound仅企业 audience。不接受自报成员、缺sessionId或owner回落。模型 callId 作为计量去重键，重试不重复计量；物理连接复用，逻辑消息邮箱与背压/去重键含完整 SessionBinding + messageId，发送者和目标均逐次查权威，父子/桌面授权沿 msgnet-v2，切换旧积压过期不可执行，不自动重放有副作用命令。
- U：引用 workspace.ts 中 DshPlacementOpen 与原切换接口；GuardianImpact全池范围，202只表示登记，未停确认显示blocked。必须显示设计3.1完整风险、30天封存/空目录/丢会话及独立容器入口。IAM配套仍用既有企业凭据生命周期，内置owner/admin权限目录 `workspace.placements.read/write`，写操作 admin+有效权限+600秒内step-up，`now-stepupTime < 600`，未来时间拒绝，UAC/邀请一致。

## 签名、期限与撤销

紧凑 JWS，Ed25519/EdDSA，header严格为alg/kid/typ；typ分别为 `auriga-shared-ticket+jwt`、`auriga-shared-identity+jwt`、`auriga-shared-capability+jwt`。密钥只能按受信配置kid查找，忽略/拒绝外部jku等字段。票据W密钥、身份E密钥、能力由既有授权服务适配签发（企业IAM，模型/消息分别网关）；各用途独立受信key map，不自行建立新token存储。两个JSON段必须按 `ports.mjs canonical()` 排序紧凑编码，base64url无padding；重复键、非规范JSON、未知字段拒绝后才验签。协议仅安全整数和ASCII字段名，禁止通用浮点数。签名对象完整覆盖所有字段。

时间：issuedAt <= now < expiresAt，票据和身份最大60秒，能力最大600秒，零宽期限拒绝，不宽限未来iat。Identity issuer/audience固定且包含物理实例及逻辑分配；method精确大写，canonicalPath为去部署前缀后的上游转发路径、不含查询；E只接受规范路径（拒绝点段、反斜线、重复斜线、编码分隔符/点段和无效编码），E/ingress/P复核同一字节串，不做二次解码。身份每请求新jti，WS升级为GET；重放缓存原子按用途/签发者/jti保存至到期。能力允许同任务内多次调用，不做一次性消费，但每次核成员、平台会话、DSH会话、taskId、authGeneration、credentialVersion、placement epoch及撤销。member/tenant/session/epoch任何差异拒绝。失效或核权威不可用即拒绝；退出、任务结束、成员禁用、切换撤销相应能力、缓存、连接。不能用有效期代替主动撤销。

资源：只选平台统一签名、全池成员共同获权的资源。authorizationSetDigest是规范有序授权集合摘要，匹配不一致拆池或拒绝，不取并集。viewId+binding逐placement回报；公共制品缓存复用不等于共享授权。R固定customSkillDirs活跃根，禁user私有根、动态安装、离线启动。任一资源到期/撤销触发GuardianImpact(pool)→外部停池收据；清理失效视图、重核租约与撤销且确认执行树停止后才重启，污染会话不恢复。

切换固定 discard/archive-and-reset。`archive-reset` confirmed必须新目录为空、archiveId存在、archiveExpiresAt=observedAt+2592000；快照不自动挂新会话，过期删除失败告警。先围栏递增epoch、撤销能力，再合作取消/必要时停池、删除会话、封存本人文件、空目录准备、CAS唯一绑定和正常页面探测。blocked不提交；回滚保留更高epoch，空目录/空会话，不恢复旧任务。

## 替身与验证

票据交换的机器契约为 `openapi/shared-dsh-edge.yaml`，端点 `POST /_auriga/placement-ticket`，IP模式使用server前缀 `/i/{physicalInstanceId}`。严格body引用本目录TicketExchangeRequest，成功302的Location/Set-Cookie/Cache-Control与400/401/403/409/503均有定义。E使用 `exchangeTicket` 替身检验签名用途、期限、入口实例绑定、撤销和一次性消费，替身不签实际浏览器cookie；真实HTTP/WS和cookie验收仍由E线完成。TS为`shared-dsh-edge.ts`，Java为target中的sharededge接口和模型，Go为SharedDshTicketExchangeRequest消息。

`node contracts/shared-dsh/build-contract.mjs --check`只读检查schema/proto漂移，已由 `services/contracts/scripts/check-schemas.mjs` 接入现有Contracts CI；prettier使用services/contracts锁定工具依赖，无需安装根pnpm依赖。生成时不带--check，后续再走Java/Go/TS生成链。

`ports.mjs` 是契约oracle，不是生产授权实现。W/E可用 EntryDouble 模拟完整绑定、60秒、重放及撤销；P/N用 authorizeCapability 模拟600秒/归属/任务/凭据版本/raw开关；W/R用 confirmReceipt 模拟各操作确认与blocked；U以OpenAPI响应替身验证。签名测试运行时生成密钥。后续线均钉住本批精确git HEAD（见工位报告），不可手写/修改生成类型。Java生成在 services/contracts/target，Go在 infra/gen/auriga，TS在 web/shared/src/generated/{workspace.ts,shared-dsh.d.ts}。测试双只在内存不监听端口，不占真实PG/DSH槽；接线后须补真实端口和正常页面验收。

错误：schema/未知方法 common.validation_failed；跨租户 common.not_found；错绑定 shared.binding_mismatch(403)；过期 shared.expired(401)；撤销 shared.revoked(403)；签名 shared.signature_invalid(401)；无法确认停止 shared.stop_unconfirmed(409)；授权不可核 common.unavailable(503)。结果事件沿原placement.switched/placement.switch_failed；新增撤销、资源回报、guardian事件均由workspace发布，后两项源数据由受信节点上报，消费者不能把事件推送当实时授权真源。节点撤销走NodeControl，不订阅NATS；节点事件只发audit，不列workspace自消费。
