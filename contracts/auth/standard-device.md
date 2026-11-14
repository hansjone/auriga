# 标准 RFC 8628 Desktop 设备流（AUTH-K2 第二轮）

此文件是新增的 `standard_device` 路径，不改变 `device-ai-resource.md` 中已有 native_tx 模型专票。原文件的 native-only 限制仍适用于该旧 Beta；新的 Desktop 系统浏览器登录按本文件接线。现有实现中 `OAuthProvider.token/refresh/revoke` 的 nativeConnect/native_tx 检查不足以支持此路径，A 必须实现以下分支，不能直接去掉旧围栏。

协议依据：[RFC 8628 §3](https://www.rfc-editor.org/rfc/rfc8628.html#section-3)、[RFC 7009 §2](https://www.rfc-editor.org/rfc/rfc7009.html#section-2)、[RFC 8707 §2.2](https://www.rfc-editor.org/rfc/rfc8707.html#section-2.2)。以下来源绑定、权限与设备家族撤销是 Auriga 的产品限制，不能把它们误称为标准自带保证。

## A/C 的标准路径

1. C 是部署登记的 platform 公共设备客户端（例如 auriga-dsh），非 nativeConnect 专用客户端。部署必须授予 device_code 与 refresh_token grant、所需 scope，客户端不能自报 platform/tenant。沿已有 `/oauth2/device_authorization`，scope 包含 auriga.connect；openid/offline_access 只在登记允许时请求，不因请求它们授予门户身份。响应 device_code 仅 C 持有，user_code/过期时间投影沿第一轮接口。验证 URI 只取受信安装同源的固定 `/device` 页面，不能打开未经核验的任意响应 URL。
2. B 的人工设备确认及 step-up 保留。A 在批准时从已验证的当前门户 session 与 grant 原子产生 tenant/account/member/provider/device 来源关系；UAC 时由 IAM 写入 sourceSessionId/authGeneration/providerId。请求 body、userCode、插件、页面或模型不能声明这些身份字段。绑定保存到权威 grant、设备及后继 refresh lineage，绝不从请求 token 之外的自报字段重建。批准与 source 撤销并发时以锁定权威状态裁决。
3. C 按 interval 轮询 `/oauth2/token`，device grant_type/client_id/device_code 与原流程一致；pending/slow_down 按 RFC 处理。成功唯一兑换且原子记录 standard_device lineage 与设备/来源绑定。平台 access 为唯一 aud=auriga、ptyp=device、ctx=device、did，无浏览器 sid、instanceId、门户 Cookie 或平台会话转移。只有当前有效权限与授权 scope 的交集可进入设备 JWT。
4. `GET /api/iam/v1/me/device-session` 是新增的设备身份投影，要求当前 aud=auriga 设备 Bearer。返回 StandardDeviceIdentity，由 IAM 在线定位并核来源有效性，不接受 deviceId/sessionId 等选择参数；UAC 来源必带与 JWT/权威行一致的 uacSessionBinding。C 将返回 tenant/member/device/provider 和来源绑定与验签 JWT 比较。不为设备伪造 `Me.session`，既有浏览器 GET /me 返回结构保持不变。该接口不 touch idle、不返回任何令牌。

## 来源、刷新和 AI audience

每次平台刷新、AI resource 换票、凭据读取、网络准入及在线核验都检查 tenant/client/account/member/device/provider/source session 当前状态、来源期限和认证代次。UAC 设备缺绑定或有伪造/错配均拒绝，旧历史设备不能自动补信任，须重登。团队账号来源也要保存并验证其权威 source session；不能因没有 UAC 字段就跳过来源检查。

普通刷新不带 resource，继续得到 aud=auriga。AI 沿已有 refresh_token 请求加唯一 `resource=urn:auriga:ai-gateway`，不是 token-exchange 或任意 audience 参数。`StandardDeviceRefreshRequest` 定义公共客户端单值表单；原始表单先拒绝重复键、多 resource、audience、未知身份参数。scope 若提供只能缩小原授权，不增加权限。非法目标在消费 refresh 前拒绝 invalid_target，字段非法 invalid_request，scope 扩大 invalid_scope。无有效来源、缺权限、伪造/到期/撤销 refresh 返回 invalid_grant；控制面故障失败关闭，不签票、不消费旧 refresh。

标准设备 AI JWT 为 at+jwt/ES256、唯一字符串 aud=auriga.ai-gateway、ptyp=ctx=device、真实 did/sub/tid，寿命为 now+300秒、device/source绝对及 idle 截止的最小值；iat/nbf 沿已有最多30秒偏差，到期无宽限。perms 是当前有效授权与 `{auriga.connect.models.invoke, auriga.connect.collab.use}` 的非空交集，不含 roles、管理、instance 或其他能力。UAC 来源绑定由 IAM 签入且须与权威 lineage 一致，auth_time 保留原登录时间。资源指示符只选 audience，不是授权来源。Team 与模型各自仍须对应权限；模型 ACL/预算及组织业务权限不由此豁免。

新增内部 `DeviceAccessService.VerifyStandardDeviceAiAccess` 仅 svc:ai-gateway 使用内部服务 JWT、受委派的非平台 tenant 和既有受认证内部传输调用。请求为原始 JWT 和固定所需 permission（只许 models.invoke 或 collab.use 的完整权限键）。IAM 验签、issuer/typ/时间、AI单audience、standard_device lineage、该权限同时在签名票与当前有效授权中，及上述权威来源状态；跨委托tenant拒绝。有效返回 tenant/account/device 与最早截止，invalid 返回 active=false 且其他字段为空；依赖故障 UNAVAILABLE，错误调用方 PERMISSION_DENIED，缺内部身份 UNAUTHENTICATED。不更新 idle，不消费 refresh。原 VerifyDeviceAiAccess 保留 native/独立模型凭据语义，原 VerifyDeviceAccess 保留平台 audience；不把 AI票放到旧平台核验入口。

平台和 AI 必须共用 C 的单一串行 refresh broker；每次成功原子保存后继 refresh，再处理下一请求。标准设备后继 refresh 保留 client、设备、来源、scope、绝对期限及 lineage，不固定于本次 audience；普通标准设备刷新和 AI 换票均不扩展来源 idle/绝对期限。正常轮换、AI换票均不修改来源 authGeneration。请求已发出而结果不确定时不盲重试旧 refresh；合法重放沿既有家族撤销纪律，签发失败整笔回滚。只在可证明未发出时可安全重试。

## 设备自撤销和本地取消

已签票设备通过 `/oauth2/revoke`，表单 `StandardDeviceRevokeRequest`，使用本登记 client 的 access 或 refresh token 作 possession proof，不需要浏览器 Cookie 或管理员会话。IAM 从已验证 JWT 或已存 refresh lineage 在线定位设备并核 client/tenant/account 归属；AI access 也须是本标准设备当前有效签名票。不能接受 did/sourceSessionId 作为撤销选择器。撤销本设备、其所有 refresh 代际和派生平台/AI票及连接；不撤来源门户 session、其它设备或独立管理会话。非法 client/参数返回标准 OAuth 错误；未知、到期、无效、重复或不属于本 client 的 token 统一 200 空对象，不泄露存在性。有效 access 自撤销不得成为跨 client 攻击入口。

退出、源会话到期/撤销、成员/provider 停用、复验明确失败或预算耗尽会使设备及全部 audience 派生权限失效；撤销事件/通知用于加速断开，在线权威检查保证通知丢失也拒绝。已吊销后刷新不得触发来源会话扩大撤销或复活。GET设备信息、换票及核验不续来源期限。

尚未批准的设备流，RFC 8628 没有通用的客户端取消端口。本轮不发明新HTTP路径：C 本地取消立即停轮询、清页面尝试，服务端未批准 grant 等待其 TTL（沿现有5分钟）；不能报告为服务端即时撤销。取消竞态中迟到已签发 token 由 C 安全撤销队列调用上述 revoke，仅在本机受保护存储保留最小排队材料。网络不可用也先本地停权，清除可用 token，不向旧页面交结果。用户可在已验证门户沿既有 deny 拒绝 pending grant；此操作仍须其原有会话/Origin/CSRF/step-up边界。

本契约不把 schema/夹具通过当作已实现标准客户端。A/C 需做共享存储并发批准/轮询/刷新/撤销、跨 tenant/client、来源失效、旧票连接关闭与撤销通知丢失等运行时验收；B/D 沿既有受信入口消费，不持有平台令牌。

## 标准设备资料投影（AUTH-K3）

`GET /api/iam/v1/me/device-profile` 使用当前 aud=auriga 标准设备 Bearer，并复用 device-session 的在线准入与最终来源复核；不接受查询身份选择器。StandardDeviceProfile 仅返回当前账号 id、tenantId、displayName，no-store、无浏览器 session/令牌，不 touch idle。账号 id 对应设备 JWT sub，不作为 memberId 的别名。C 将其名字与已验证 device-session.memberId 合成固定 profile_read DTO。浏览器 `/me` 不增加设备分支，旧浏览器返回结构不变。
