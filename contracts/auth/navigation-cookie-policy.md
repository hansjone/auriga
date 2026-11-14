# 导航与浏览器 Cookie（AUTH-K2 增补）

设备入口：浏览器 GET `/device/{userCode}` → edge → 受认证内部 POST `/internal/uac/device-navigation-proofs` → IAM。edge 只转公开用户码和真实导航证据；完整 Origin、tenant、provider 来自受信入口配置。IAM 在权威设备事务中解析，原子绑定 grant 和导航事务。不得先把 grant ID 交浏览器再传回。`/internal/uac/navigation-proofs` 仅接受 portal 续接，已移除 deviceGrantId 和 device 分支，不兼容旧设备调用；设备续接只能走新的 userCode 端点。无效、过期、终态、跨租户及 provider 不符统一 404。设备证明不会取得 auto 资格；导航或登录成功均不等于批准设备，仍需本人核码后显式确认及既有 step-up。

所有 UAC 操作的响应统一使用必有的 `X-Auriga-Correlation-Id`（UUID）。IAM 创建事务时生成并保存，导航、SSO、二维码挑战/状态/取消/完成和重试沿用；写入阶段日志，edge 原样传递，同源页面读取响应头显示。事务尚未确定的拒绝另生成 ID；禁止信任客户端同名头。它与 transactionId 独立。原响应 body 不变。内部导航响应的 ID 由 edge 随初始上下文展示（不改变 context 的冻结字段）。

退出抑制 Cookie：HTTPS 为 `__Host-auriga_portal_sso_suppression`，HTTP 为 `auriga_portal_sso_suppression`。Path=/、无 Domain、HttpOnly、SameSite=Lax；HTTPS 必须 Secure，HTTP 不设 Secure。值为随机不透明浏览器会话标识，仅服务端保存摘要并绑定 tenant、完整 Origin 与 suppression revision。会话 Cookie，不设 Max-Age/Expires；浏览器会话结束即失效，服务端记录最多保留 24 小时，过期后旧值不能恢复 auto 资格，须经显式登录重新建立状态。退出时设置/轮换，所有标签页共享；刷新、SSO continue、扫码成功不清除本浏览器会话的抑制。只有新的浏览器会话（旧会话记录失效）重新判断 auto。抑制失效或存储故障默认禁止 auto。管理退出不改门户抑制。

匿名 SSO 的 CSRF：edge 返回正常门户导航 HTML 前，在内部签发成功时同步初始化 `auriga_csrf`（HTTPS 为 `__Host-auriga_csrf`），Path=/、无 Domain、SameSite=Lax，HTTPS Secure，供同源 JS 读取所以不设 HttpOnly。至少 32 随机字节，仅服务端保存摘要并与本导航事务/Origin/tenant/audience绑定；生命周期不超过证明的 120 秒。IAM 将令牌通过新增的内部 `csrfCookie` 字段交 edge，edge 只 Set-Cookie，不放 HTML，不记录日志。有效现有门户 CSRF 不轮换也不覆盖；IAM 应复用已验证的浏览器事务 CSRF，否则签发新值并作废该 audience 旧导航。SSO POST 仍须精确 Origin、`X-Auriga-CSRF` 与 Cookie/服务端绑定全部匹配；GET 初始化不是任何写接口的 CSRF 豁免。登录成功轮换为既有会话 CSRF，取消/到期清理匿名状态，不影响独立管理会话。

Desktop：`ShellAuthSnapshot.deviceAttempt` 只在 authenticating 状态提供，并仅回送发起票据对应的 pageOwner。attemptId 是本次公开尝试 ID，非 device_code；页面展示 userCode、expiresAt 和人工核码提示。取消、跳过、超时、成功、失败、失联终止、页面关闭和 shellEpoch 变化立即清除；其它页快照不含该字段。晚到结果按 attemptId/owner/epoch 丢弃或撤销。缺该可选字段只表示无本页有效尝试，不能以旧码填充。pageOwner 在票据交换返回，命令不接受自报 owner。

插件迁移清单：legacy 按规范化字面路由特征核对，允许删除和格式变化，不比较全文摘要；动态拼接、方法和别名分析有明确局限，legacy 不证明完成迁移。D 完成迁移时更新 migrationState=broker 并清空 calls，sources 仅历史证据。broker 扫描全部非 generated 的 JS/TS，只有 retainedChannels 的精确文件/指定传输/锚定路由可以保留；其它已识别直连拒绝。保留本地 owner/OIDC、托管心跳/准入、页面/个人模型等通道的逐项理由见清单。固定 broker 接缝约定、正负用例及局限见 [迁移门禁](../plugin/shell-auth-migration.md)；仍须 D 的行为测试和独审证明真实代理路径及无凭据泄露。
