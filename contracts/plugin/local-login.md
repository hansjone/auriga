# 本机登录适配 v1

本契约只覆盖安装者配置和当前 DSH 原生登录适配，不提供模型可调用的签会话接口。`local-login.schema.json` 是请求形状来源。接线不得将设备凭据当成门户 USER 凭据，或把账号、租户、源会话由模型参数指定。

安装者在 `auriga-dsh` loader row 的 `config.connect` 写 ConnectConfig；未配置时保留独立模式壳。只接受受信 HTTPS 平台纯 origin，`allowLocalHttp=true` 仅准本机隔离测试。issuer 必须同 origin，JWKS 固定取 issuer + `/oauth2/jwks`，不消费 JWT 自带的远端 key URL。`clientId` 必须由 IAM 登记 nativeConnect 公共设备客户端，providerId 必须是真实 team-account。

本地路由前缀 `/auriga/local-login`：

- GET `/status`：保持原生 DSH 启动 cookie/Peer 的认证，再创建当前进程的登录尝试。返回 Status，浏览器只得到会话 CSRF；尝试 cookie 为 HttpOnly/SameSite=Strict、300 秒。
- POST `/login`：严格同 Origin、JSON、尝试/会话 CSRF。Begin 创建全新独立平台 jar 与 S256 verifier；Advance 只承接原 FormInput；Resume 仅在 Node 已有加密设备凭据时可用，重新 JWKS 验证及在线远程 Team 认证成功才重新建立本地浏览器会话。
- POST `/logout`：严格同 Origin、JSON、会话或本次尝试 CSRF。先失效本人本机浏览器会话和其 WS，再原子持久退出状态与加密撤销补偿；不停止 DSH Agent、会话任务或进程。本地持久失败不报告成功。Node 随后 POST `/oauth2/revoke`，form-urlencoded `{client_id,token}`，只用本设备 refresh，不传 did/浏览器 cookie；远端不可达返回 `remoteRevocation=pending` 并重试，不能冒称撤销完成。成功/未知/重复幂等200后才持久移除补偿。重启只对相同平台 origin/clientId 重试，避免配置更换泄露旧凭据。
- GET `/` 与固定 `/assets/{adapter,index,styles}.js`：原生步骤视图与适配器，不接受任意文件路径、redirect、平台 URL、主体或令牌。

Host 独立内存 jar 保存平台事务 cookie/门户 backing session cookie，仅当前认证尝试可用。nativeDevice、verifier、access/refresh、平台 cookie 不进 UI、URL、日志。UI complete 只提示步骤；Host 还须兑换设备 token、校验 ES256/JWKS/iss/aud/ptyp/ctx/did/tid/sub/scope/到期、原子持久设备凭据、在线 Team 初始化与原生工具注册，然后才发本机 cookie。成功清 jar 不调用平台 logout；仅未持久的失败尝试可用本次 jar 尽力补偿。已持久设备遇临时 Team 失败/浏览器取消时保留 backing，经重新在线验证后可 resume，不发未就绪的浏览器会话。取消须等待 commit 结果再决定补偿；显式退出仍串行持久 tombstone/设备撤销，不声称跨 IAM/FS 两阶段提交。

浏览器 bearer 仅在进程内；重启不从磁盘恢复旧 bearer。设备凭据与通信投递映射放在 profile 私有目录 AES-GCM 文件，copy-on-write + 同目录原子 rename 后才发布内存快照。设备 refresh 单飞、检查同一 did/tid/sub 并先持久后发布；401/403 或到期关闭会话 WS，网络不可用显式显示断开。写操作不自动重放。

真实 DSH HTTP/WS 继续使用 rc.3 原生 admission/Peer，叠加本机会话、精确 Host/Origin 与跨站限制。仅关闭已绑定该身份的 WS；既有任务继续运行。JS、资源和管理权限仍各自校验，不以本机 gate 代替远端授权。
