# 原生设备 AI resource 换票（Beta）

这是插件认证协议和 [AI D1](../ai/README.md) 的设备模型通道补充。
复用原生登录所得的设备 refresh 凭据，不新增用户可见登录或授权步骤。

## 请求与响应

唯一资源标识是绝对 URN `urn:auriga:ai-gateway`，与签出 JWT 的
audience `auriga.ai-gateway` 是两个不同值，不能互换。

```http
POST /oauth2/token HTTP/1.1
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&client_id=auriga-dsh&refresh_token=<current-refresh>&resource=urn%3Aauriga%3Aai-gateway
```

`client_id` 必须是安装中已登记的公共 `nativeConnect` 客户端。
refresh grant 和真实设备都必须来自 `authorizationMode=native_tx`，绑定同一当前有效
租户、账号、门户会话和客户端，scope 为 `auriga.connect`。
不支持手动设备码授权、浏览器授权码 refresh、服务账号、其他 grant 或其他 resource。
`resource` 必须恰好出现一次，重复相同值也拒绝；不接受空值、空格拼接、多目标、URL 或 `audience` 参数。
可省略 `scope`，若给出只能精确为 `auriga.connect`；其他身份/权限/目标参数拒绝。

成功返回 HTTP 200，`Cache-Control: no-store`、`Pragma: no-cache`：

```json
{
  "access_token": "<signed-ai-jwt>",
  "token_type": "Bearer",
  "expires_in": 120,
  "scope": "auriga.connect",
  "refresh_token": "<new-one-time-refresh>"
}
```

`expires_in` 为实际剩余整数秒，正数且不超过 300；示例不是固定寿命。
JWT 复用 IAM 当前 ES256 签名器和 JWKS，JOSE `typ=at+jwt`、带 `kid`。
claims：`iss` 为当前 issuer，`sub/tid` 为真实账号/租户，`aud` 为唯一字符串
`auriga.ai-gateway`，`ptyp=device`、`ctx=device`、`did` 为真实设备 id、
`scope=auriga.connect`。`perms` 仅含当前有效的 `auriga.connect.models.invoke`；
没有该权限则拒绝，不从 roles 扩权，不包含 collab、admin、instance 权限或 `sid/instanceId`。
`mtyp` 取在线有效成员类型，`auth_time` 保留原登录时间。
`exp` 不超过签发时间加 300 秒、当前门户会话绝对期限和 idle 期限的最小值。
AI 换票不更新绝对或 idle 期限。数据面每次准入仍须按 D1 在线核成员、设备、模型 ACL 和预算。

## 每次准入的专用在线核验

网关每次设备 AI 准入调用 IAM `DeviceAccessService.VerifyDeviceAiAccess`，复用
`VerifyDeviceAccessRequest/Response`。仅允许 `svc:ai-gateway` 的内部服务 JWT，
必须携带非平台委托 tenant，并使用既有受认证内部传输；公网设备 JWT 仅放在请求体中，
不能替代调用方内部凭据。JWT 的 `tid` 必须与委托 tenant 相同，跨 tenant 拒绝。

此方法复用 IAM 验签、schema、issuer、`typ=at+jwt`、ES256 和 kid 校验，
仅接受唯一 AI audience（字符串或单元素数组）、`ptyp/ctx=device`、规范 UUID 的
`tid/sub/did`，权限恰为 `auriga.connect.models.invoke`；不接受非空 roles、sid 或实例身份。
不依赖平台 `scope` 字段作为 AI 准入条件。显式检查 `exp>iat`、寿命不超过 300 秒，
`iat/nbf` 最多允许既有 30 秒未来时钟偏差；到期没有宽限。
平台/Team、instance、服务票、`ait_`、坏签、宽 audience 或宽权限均不能混作设备 AI 票。

每次读取当前账户、设备和 backing 门户会话，并在线读取成员状态、成员类型及有效 invoke 权限；
成员必须 active 且为 member/external。远程成员查询期间不持 IAM 行锁，完成前再次核设备/会话。
未知成员、无 invoke、停用/离职、设备撤销、账户/会话失效均返回 `active=false`，
其余身份字段为空且无 `expires_at`。控制面缺失、null 或异常返回脱敏 `UNAVAILABLE`，
不能用缓存批准或安装静态权限兜底。跨委托 tenant 返回 `PERMISSION_DENIED`。

`active=true` 返回真实 `tid/sub/did`；`expires_at` 为本 JWT 与当前 backing 绝对/idle
期限的最小值，完成核验时仍须未到期。不更新 idle，不读取、消费或签发 refresh。
该核验仅证明设备与成员的当前 AI 资格，模型 ACL 和预算仍由 D1 其他准入检查承担。
原 `VerifyDeviceAccess` 保持平台 audience / Team 语义；`VerifyDeviceCredential` 仍仅供
`svc:collab`，不扩展 SessionService 或 NetworkGatewayService 白名单。

错误沿 OAuth JSON 风格，HTTP 400（客户端认证错误为 401）：

- 非法/重复目标或 `audience`：`{"error":"invalid_target"}`。
- 非法额外参数：`{"error":"invalid_request"}`；scope 不符：`{"error":"invalid_scope"}`。
- refresh 伪造、过期、已用、吊销、错误设备绑定、非 native lineage、无有效模型权限/成员：`{"error":"invalid_grant"}`。
- 未登记/跨租户/认证失败客户端：`invalid_client`；不允许的 grant：`unauthorized_client`。
- 在线控制面不可确认时失败关闭；不签票、不轮换，也不退回安装配置中的静态权限。

目标和参数校验先于消费 refresh；非法目标即使引用已用 refresh 也不触发家族吊销。
目标合法的已用 refresh 保持原有重放检测和会话家族吊销纪律。

## 一次性轮换与客户端接线

AI 换票成功即消费旧 refresh，并在同一事务返回新的单次 refresh。
新 refresh 保留原客户端、会话、设备、scope、native 标记及绝对期限；不固定到 AI resource。
它可用于不带 `resource` 的普通平台刷新，普通刷新产生的新 refresh 也可继续 AI 换票。
不带 `resource` 时保持原平台刷新语义（包括原有 native idle 活动更新）。
签名/写入异常导致整笔事务回滚，旧 refresh 不被消费。
并发竞争同一 refresh 至多一个成功；后到的合法重放按既有规则吊销家族，可能使胜者的新 refresh 也立即失效。
服务端不允许旧 refresh 重用、不设“仅 AI”宽限或旁路。

客户端必须通过**单一串行刷新 broker**协调平台和 AI token 的获取、提前续期、休眠恢复与退出：
每次成功先原子保存新 refresh，再处理下一次刷新；不得为两种 audience 各保留一份旧 refresh。
若响应丢失，不可盲重试已消费 refresh；按既有失效/重新登录策略处理。
普通平台 JWT 不能用作 AI JWT；AI JWT 也不能用于网络注册或平台权限入口。
`DeviceAccessAuthority.platformAudience` 的平台边界保持 `auriga`。
该 Beta 仅授予模型调用和由 D1 映射的本人用量读取，不授予 Team MCP/collab 能力。
