# 原生本地登录与远程 Team 接线

本批复用 Auriga `src/login` 的原生步骤视图，仅迁入 LOCALPORT1 prepared 的 `atomic-write.js` 与 `secret-box.js` 两个自有 MIT 改编文件。出处和完整许可见 `clients/dsh-plugin/src/local/SOURCES.md`。没有旧 dsh-team/Hub/API 运行依赖，也未迁入占位 login503 或存在撤销风险的会话 store。

示例仅用于公开安装说明；不代表存在可用账号或已登记客户端：

```yaml
- id: auriga-dsh
  name: auriga-dsh
  config:
    connect:
      platformOrigin: https://portal.example.com
      issuer: https://portal.example.com
      clientId: auriga-dsh
      providerId: team
      deviceName: Auriga DSH
```

配置从当前插件 loader row 读取；私有设备状态在 `ctx.profileContext.dir/auriga-connect/`。原生启动授权仍必需，普通登录只回当前 DSH，不调用云端 instance/open。账号与 TOTP 在原步骤视图完成，不增加可见核码或 consent 按钮。详见 `contracts/plugin/local-login.md`。

连接模式的父插件先持有本机准入门锁，再由真实 Cordis 条件注入的子上下文等待 profileContext、tools、sessions 与 sessionController。运行时与工具属于该子上下文；依赖未齐或被卸载时，父门锁继续拒绝原生 API，登录路由返回 503。依赖恢复后须重新登录或 resume，旧浏览器会话不复活。不配置 connect 的壳与 managed 模式不增加这些依赖。

共享原生 `/api` 的 `connection/request(req,res,next)` 在 admission 后、HTTP bridge 前按本机会话关联 ServerResponse。退出、到期或在线认证拒绝会关闭该会话已开的 HTTP/SSE 响应及 WS；finish/close 释放索引。不会关闭共享 Peer、原生 server、其他会话的响应或退出路由自己的响应，也不调用 Agent 任务取消。此覆盖范围不包含绕过该原生 hook 自建的 HTTP 端点。

Team 为平台远程 Streamable HTTP MCP，客户端不启动本地 MCP server。initialize 协商 2025-06-18，发送 initialized 通知；包括通知在内的 MCP POST 均声明 Accept application/json, text/event-stream，通知接受空202响应。四个工具从真实远程 tools/list 获取 schema，再注册到当前 DSH `ctx.tools`。执行只读取真实 `exec.agent.session.id/callId/rootCallId`，构造 ADR0016 `_meta`，Node 私有端点/绑定引用走受信请求头。Node register/bind/pull/ack/revoke 不暴露给模型。访问令牌每请求从 Node 取，refresh 不回放模型写入。

开线程成功返回前，校验服务器响应message/thread UUID与authorEndpointId、已核验的source binding，并原子写入thread→当前可信DSH会话/绑定；禁止覆盖不同会话/绑定。远端提交而本地落盘失败时仍返回失败，调用者可用相同idempotencyKey安全重试，不凭模型输入补源会话。接收prompt分为两个text part：第一个仅有服务器验证过的threadId/messageId JSON上下文，第二个完整保留发送正文。接收模型据入队上下文使用reply/read，不依赖测试额外告知线程，不包含lease/token/私有身份，也不强制业务回复。

真实设备验证且原子持久完成后，临时Team失败或浏览器取消只清本次jar/临时证明，保留device与backing供后续受门锁的resume重新在线验证；此时不走平台logout补偿。未持久完成的尝试仍补偿自己的登录。取消清理等待本次commit Promise的最终结果，不能在异步写尚未结束时先注销backing；显式本地logout仍立即封闭代际，并在同一vault串行队列中写tombstone/设备撤销补偿，防止迟到写恢复认证。Team未就绪或已取消时不签浏览器session；原有owner/did冲突检查保留。

接收端先持久 thread→真实 DSH session ID，再调用 rc.3 `sessionController.create` 与绑定；以服务器 canonical delivery.id 为 `prompt.requestId`。明确 `accepted=true` 后，取实际 `ctx.sessions.get(sessionId)` 并等待 `ctx.sessions.flush(session)===true`，才持久 accepted 并 ACK；无持久化监听时 rc.3 返回 false，必须拒绝确认。ACK 响应丢失重新取 lease 时跳过已持久 accepted 的 prompt。原生 inbox/spliced 为会话事件，源码已核；仍须用当前实际版本的完整重启测试验明整条链，不仅靠内存测试宣称恰好一次。

本机 AES-GCM 文件密钥与 ciphertext 均属于 profile OS 用户；这不替代 OS 用户隔离，也不宣称防同用户恶意进程。正常写入 fsync 临时文件并原子 rename；目录 fsync 的跨平台支持尚未引入。关键失败关闭 gate，重启旧 bearer 不会恢复。退出先持久 tombstone 与加密补偿，再调用已冻结 `/oauth2/revoke`；暂不可达显示 pending，十秒周期及重启重试，当前平台/client 不符的旧记录不发送。持久存储失败返回错误、封闭当前进程，不能承诺磁盘不可写时永久提交退出。详见本机契约。

验证分开报告：普通状态/负向测试、真实 rc.3 原生 HTTP/Desk 准入、真实 IAM 与双 DSH 收发。当前前两层的通过不能替代最后一层。隔离测试随机端口、自建 profile，关闭自有 fiber，保留 profile/日志；不接触用户 DSH_HOME 或其他进程。
