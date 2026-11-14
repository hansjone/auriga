# 门户 UAC 与设备确认入口

正常入口为 `/login`、`/instance-login.html` 和 `/device/{userCode}`。`/device` 提供手动输入用户码的入口。设备页的登录只返回设备确认流程，不创建、启动或打开云端实例；用户须核对发起端、权限和用户码，再显式批准。服务器要求 step-up 时，页面验证同一门户会话并最多重试一次批准。

edge JSON 配置的 `uacProviderId` 指向本安装启用的 UAC 身份源。为空时不签发 UAC 导航上下文，团队账号及显式扫码仍走原 IAM 接口。`uacSsoCookieNames` 是只向 IAM SSO 接口转发的企业 Cookie 名允许列表；实际名称与完整配对由受保护部署配置提供，IAM 负责配对优先级及验证，edge 不混配或向页面暴露这些值。名单启动时校验，不接受空名、非法 Cookie 名、重复名或含 `auriga_` 的保留名。允许的企业 Cookie 值按原始请求头转发，保留 ASCII 空格、引号、反斜杠、逗号及等号；分号作为 Cookie 分隔符，控制字符、非 ASCII、未闭合引号或重复值显式拒绝。公司 Cookie 不可用时由 IAM 返回扫码降级。

必须配置安装 Access 的完整门户 Origin/tenant、现有 `iamHop` 受信传输，以及 `iamRpc.serviceTokenFile` 中 svc:edge 的内部服务凭据。分离部署使用现有 mTLS hop；数字回环 HTTP 仅在显式开发配置允许时使用。IAM 必须允许 svc:edge 委派该安装租户。浏览器不能调用 `/api/iam/v1/internal/**`，携带服务 Bearer 也不能绕过。

普通门户 GET 调用内部 `navigation-proofs`；设备 GET 只向 `device-navigation-proofs` 传路径中的公开用户码，由 IAM 解析权威设备事务，不接受浏览器 grant ID。iframe、预取、错误 Origin、重复导航元数据及重复认证 Cookie 被拒绝。跨站顶层导航正常显示页面，将跨站证据传给 IAM，并确保不具备自动登录资格；缺少导航元数据时由 IAM 判为不具备自动登录资格。IAM 明确返回合法的 4xx problem 时正常输出 HTML，不注入导航上下文，保留团队登录及设备页自己的过期提示；传输失败或响应畸形仍返回 503。

内部签发响应的事务 Cookie 仅设置为 HttpOnly/SameSite=Lax/Path=/；匿名 CSRF Cookie 对同源 JS 可读。浏览器 CSRF 值必须符合 43 位 base64url 格式，否则视为不存在，由 IAM 重新签发。复用既有值也重写同值 Cookie，寿命按新证明剩余期限向上取整，覆盖证明有效期。HTTPS 均 Secure 并使用 `__Host-` 前缀。页面响应 no-store、禁止嵌入，并只注入冻结的导航 context 和经过 UUID 校验的关联 ID。

门户 UAC 登录成功（包括额外验证）后优先返回合法的本地相对 `returnTo`，否则进入 `/instance-login.html`。设备页续接仍只回到核码确认，不创建或打开云端实例。

退出抑制由 IAM 持久化并签发 `auriga_portal_sso_suppression`（HTTPS 加 `__Host-`），为无 Domain、Path=/、HttpOnly、SameSite=Lax 的会话 Cookie，不设置 Max-Age/Expires。edge 将其传给 IAM 的 UAC 操作和门户退出，不在 continue 或扫码成功后清除。管理台退出不改变门户抑制。IAM 是抑制状态、CSRF 和证明消费的权威来源，页面不得用本地标志代替。

所有 UAC 操作关联 ID 来自 IAM 的 `X-Auriga-Correlation-Id`，edge 保留响应头并剥离浏览器同名输入；未到达 IAM 的拒绝使用 edge 新生成的 UUID。内部导航的同一 ID 和受信配置的 provider ID 进入初始 HTML 的独立 meta，未修改冻结 context。多个 UAC 源并列时只为匹配该 provider 的证明启用 SSO，不能把列表首项当作证明归属。浏览器仅投影 UUID、固定阶段和结果，不记录二维码内容、导航证明、Cookie 或任何企业令牌。

本目录测试使用随机端口与运行时生成材料。真实部署仍需 IAM 实现、共享状态和撤销传播，并由主控从这些正常页面验证登录、设备批准及旧 HTTP/WebSocket 停权；静态页面或容器健康状态不能替代全链验收。
