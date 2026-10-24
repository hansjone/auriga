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

Team 为平台远程 Streamable HTTP MCP，客户端不启动本地 MCP server。initialize 协商 2025-06-18，发送 initialized 通知；四个工具从真实远程 tools/list 获取 schema，再注册到当前 DSH `ctx.tools`。执行只读取真实 `exec.agent.session.id/callId/rootCallId`，构造 ADR0016 `_meta`，Node 私有端点/绑定引用走受信请求头。Node register/bind/pull/ack/revoke 不暴露给模型。访问令牌每请求从 Node 取，refresh 不回放模型写入。

接收端先持久 thread→真实 DSH session ID，再调用 rc.3 `sessionController.create` 与绑定；以服务器 canonical delivery.id 为 `prompt.requestId`。明确 `accepted=true` 后，取实际 `ctx.sessions.get(sessionId)` 并等待 `ctx.sessions.flush(session)===true`，才持久 accepted 并 ACK；无持久化监听时 rc.3 返回 false，必须拒绝确认。ACK 响应丢失重新取 lease 时跳过已持久 accepted 的 prompt。原生 inbox/spliced 为会话事件，源码已核；仍须用当前实际版本的完整重启测试验明整条链，不仅靠内存测试宣称恰好一次。

本机 AES-GCM 文件密钥与 ciphertext 均属于 profile OS 用户；这不替代 OS 用户隔离，也不宣称防同用户恶意进程。正常写入 fsync 临时文件并原子 rename；目录 fsync 的跨平台支持尚未引入。关键失败关闭 gate，重启旧 bearer 不会恢复。退出先持久 tombstone 与加密补偿，再调用已冻结 `/oauth2/revoke`；暂不可达显示 pending，十秒周期及重启重试，当前平台/client 不符的旧记录不发送。持久存储失败返回错误、封闭当前进程，不能承诺磁盘不可写时永久提交退出。详见本机契约。

验证分开报告：普通状态/负向测试、真实 rc.3 原生 HTTP/Desk 准入、真实 IAM 与双 DSH 收发。当前前两层的通过不能替代最后一层。隔离测试随机端口、自建 profile，关闭自有 fiber，保留 profile/日志；不接触用户 DSH_HOME 或其他进程。
