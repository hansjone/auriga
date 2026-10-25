# 本机登录适配 v1

本契约只覆盖安装者配置和当前 DSH 原生登录适配，不提供模型可调用的签会话接口。`local-login.schema.json` 是请求形状来源。接线不得将设备凭据当成门户 USER 凭据，或把账号、租户、源会话由模型参数指定。

安装者在 `auriga-dsh` loader row 的 `config.connect` 写 ConnectConfig；未配置时保留独立模式壳。只接受受信 HTTPS 平台纯 origin，`allowLocalHttp=true` 仅准本机隔离测试。issuer 必须同 origin，JWKS 固定取 issuer + `/oauth2/jwks`，不消费 JWT 自带的远端 key URL。`clientId` 必须由 IAM 登记 nativeConnect 公共设备客户端，providerId 必须是真实 team-account。

本地路由前缀 `/auriga/local-login`：

- GET `/status`：保持原生 DSH 启动 cookie/Peer 的认证，再创建当前进程的登录尝试。返回 Status，浏览器只得到会话 CSRF；尝试 cookie 为 HttpOnly/SameSite=Strict、300 秒。
- POST `/login`：严格同 Origin、JSON、尝试/会话 CSRF。Begin 创建全新独立平台 jar 与 S256 verifier；Advance 只承接原 FormInput；Resume 仅在 Node 已有加密设备凭据时可用，重新 JWKS 验证及在线远程 Team 认证成功才重新建立本地浏览器会话。
- POST `/logout`：严格同 Origin、JSON、会话或本次尝试 CSRF。匿名尝试只取消自身并清除该 attempt cookie，不修改已有设备、Team、其他浏览器会话或 HTTP/WS 流，取消后可重新获取尝试。全局设备退出要求已认证本机浏览器会话的 sub/tid/did 与当前设备一致，并在原子持久更新时再次核对。先失效同一设备的本机浏览器会话和其 WS、共享原生 API 已绑定的 HTTP 响应，再原子持久退出状态与加密撤销补偿；不停止 DSH Agent、会话任务或进程。本地持久失败不报告成功。Node 随后 POST `/oauth2/revoke`，form-urlencoded `{client_id,token}`，只用本设备 refresh，不传 did/浏览器 cookie；远端不可达返回 `remoteRevocation=pending` 并重试，不能冒称撤销完成。成功/未知/重复幂等200后才持久移除补偿。重启只对相同平台 origin/clientId 重试，避免配置更换泄露旧凭据。
- GET `/` 与固定 `/assets/{adapter,index,styles}.js`：原生步骤视图与适配器，不接受任意文件路径、redirect、平台 URL、主体或令牌。

Host 独立内存 jar 保存平台事务 cookie/门户 backing session cookie，仅当前认证尝试可用。nativeDevice、verifier、access/refresh、平台 cookie 不进 UI、URL、日志。UI complete 只提示步骤；Host 还须兑换设备 token、校验 ES256/JWKS/iss/aud/ptyp/ctx/did/tid/sub/scope/到期、原子持久设备凭据、在线 Team 初始化与原生工具注册，然后才发本机 cookie。成功清 jar 不调用平台 logout；仅未持久的失败尝试可用本次 jar 尽力补偿。已持久设备遇临时 Team 失败/浏览器取消时保留 backing，经重新在线验证后可 resume，不发未就绪的浏览器会话。取消须等待 commit 结果再决定补偿；显式退出仍串行持久 tombstone/设备撤销，不声称跨 IAM/FS 两阶段提交。

浏览器 bearer 仅在进程内；重启不从磁盘恢复旧 bearer。设备凭据与通信投递映射放在 profile 私有目录 AES-GCM 文件，copy-on-write + 同目录原子 rename 后才发布内存快照。设备 refresh 单飞、检查同一 did/tid/sub 并先持久后发布；401/403 或到期关闭会话 WS 及共享原生 API 已绑定的 HTTP 响应，网络不可用显式显示断开。写操作不自动重放。

Team 的 pull 投递是迁移期适配路径。开线程前先持久记录可信源 endpoint/session/binding、目标端点、幂等键摘要、请求指纹和固定 60 秒期限；同键重试不续期。远端回包确认源映射后，在同一次原子更新中移除意图及其历史结果。网络结果不确定或重启后保留意图，需原始来源以相同幂等键及参数显式重试以恢复，不自动重放模型写操作，也不允许模型指定来源。包括 `isError` 在内的重试失败不能证明前一次请求未提交，因此不会提前移除意图。

意图在启动、pull 或入站处理时到期，转为 `result_unknown` 终态并释放未决容量；没有期限的旧版意图立即按结果未知迁移。未决意图及结果历史各最多 256 条，历史只保留最近记录。认证状态 `connection.pendingSourceOpens` 和 `connection.unknownSourceOpens` 分别提供未决及结果未知计数。终态只说明本地无法确认远端结果，不表示远端取消或投递成功。

未决意图不阻塞整个端点。协议只出租线程最早未 ACK 的消息，因此新的接收线程从服务端 `sequence=1` 开始，无论是否存在同一对端或其他对端的未决 open 都正常接收。未知线程的后续消息可能是未决源 open 的回复：只有目标端点与消息作者吻合的活跃意图会被视为该来源的待决保护；没有对应意图或意图已经到期时，也不能把后续消息猜作新会话首条，保持“源映射结果未知”并等待原来源显式恢复。到期不创建随机源映射，不影响其他独立线程的首条入站。

同一 pull 页逐项隔离失败：普通暂时错误保留重试机会，其余投递继续；401/403 或连接失效立即终止本轮。当前 ordinary 适配器对已知 subagent-origin 的发送与接收均明确拒绝；冷恢复后才发现该来源也保持拒绝。接收失败单独持久为本地 `unsupported` 终态及 `subagent_origin` 原因，不 prompt 重试、不 ACK，不改变远端投递状态；重复租约和重启不影响同页普通会话。已认证状态的 `connection.unsupported` 可观察终态数量，不公开会话或消息标识。此终态不自动恢复；需要普通会话重新发起新的线程，不支持绕过原生 ownership 或把子会话静默改绑为普通会话。

当前冻结协议的 ACK 只表示实际、持久的 `host_accepted`，没有拒绝终态、排除列表或 pull 游标。客户端不能用成功 ACK 清除 unsupported，也不能在收到一页后撤销已经由服务端发出的租约。因此本地终态不能保证远端重租次数有界；满页 unsupported 可能饿死尚未进入该页的普通投递。消除此限制需要独立契约及服务实现任务：增加受 endpoint/binding/delivery/lease 校验的幂等拒绝终态（例如 host_rejected），服务端租出逻辑排除终态，并验证超过一页拒收后普通线程仍能入站。本任务保持现有成功 ACK 语义，不私增服务路由或以停止整个端点接收替代修复。

真实 DSH HTTP/WS 继续使用 rc.3 原生 admission/Peer，叠加本机会话、精确 Host/Origin 与跨站限制。共享原生 /api 在 admission 后、HTTP bridge 前按本机会话绑定 ServerResponse，finish/close 释放索引；仅关闭已绑定该身份的 WS 和 HTTP 响应，退出路由自身、匿名尝试及其他会话不纳入该身份索引。父准入门锁在条件注入依赖未齐或子运行时卸载时持续拒绝；依赖恢复不恢复旧浏览器会话。既有任务继续运行。JS、资源和管理权限仍各自校验，不以本机 gate 代替远端授权。
