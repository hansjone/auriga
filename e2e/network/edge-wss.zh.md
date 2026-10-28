# Edge WSS 真实链路验收

从干净的工位根目录执行，先加载工作区构建环境：

```powershell
$head = (git rev-parse HEAD).Trim()
./e2e/network/verify-edge-wss.ps1 -ExpectedHead $head -CompileOnly
./e2e/network/verify-edge-wss.ps1 -ExpectedHead $head -Run
```

两个模式均归档精确 HEAD，记录 archive、测试和生产源码 SHA256、命令退出码。
CompileOnly 安装锁定依赖，编译真实 Go 装配，并执行非 PG Java bridge 测试；不启动 PG、Broker 或业务链路，不能称为正控或反证通过。

Run 串行执行 Control 与 BypassUpstreamTLS 两个独立归档。Java bridge 复用真实 IAM、Org、Collab 和隔离 PG；已有 network-source-assembly 启动锁定 Broker 2.15.1 及生产 runtime/callout。edge 的测试 main 通过 Go overlay 装配生产 Gateway、Access、WSS 路由和 TLS listener，不改鉴权实现。只读 overlay 观察实际 callout transport、绑定标识及 IAM RPC 状态。

原生 Node 客户端使用生产 device provider 经 edge HTTPS 登记、取票和刷新签名公钥；使用生产 socket 工厂、NatsLink、bootstrapReady 验证真实签名 READY。socket Origin 来自校验后的 platformOrigin，独立于 WSS 地址。正控不手工补 Origin，也不关闭 TLS 验证。

必须完整通过以下八项：wrong-origin、admin-entry、wrong-ca、wrong-name、invalid-ticket、verified-ready、websocket-ingress-binding、replayed-ticket。入口负例要求 HTTP 403、无 Broker 拨号和无 IAM Redeem；上游 CA/名称负例要求真实 typed x509 错误、502、无 IAM Redeem；无效和重放票据要求真实 websocket Upgrade、有效客户端 TLS、Broker CONNECT 鉴权拒绝及 IAM PermissionDenied。该拒绝必须来自本次 socket 的精确 `-ERR Authorization Violation` 协议行，或关闭码1008且原因为固定白名单 `Authentication Failure`；Broker INFO、唯一 callout decision、唯一 Redeem 的 serverID/clientID 必须一致，Redeem ingress 必须等于本次预期。SDK 的 errorCode 只保留原始诊断，模糊连接丢失、超时、无关或重复callout均不能算拒绝。无效票据固定向量由生产 provider 的真实 subjects 投影逻辑在局部守卫中校验，不能把被测 callout 返回值当预期。观察器正确解码SDK的ArrayBuffer模式，只输出有界身份与白名单枚举，禁止归档原始帧、CONNECT、JWT或票据。正控要求 IAM OK、实际 callout clientType=websocket、保留 anchor 的 ingress/serverID/clientID 与观测一致，以及签名 READY；重放不得增加 anchor。

反证只改归档内 `infra/edge/network_wss_tls.go` 的 TLS config，加入 InsecureSkipVerify。测试保持完全相同；只有 wrong-ca 的 `upstream TLS must reject HTTP upgrade` 断言失败、各真实子进程均正常退出且无其他失败才算检出。编译、初始化、证书生成、SQL或清理错误均不算反证。归档日志和每个 probe 的阶段证据保存在输出目录。

边界：本验收为真正的 `127.0.0.1` IP SAN 模式，不覆盖现场域名、DNS、NAS/VPS或公开证书。edge→Java hop 使用专门签发的 clientAuth 证书和受信 CA，但 Java fixture HTTPS 没有要求客户端 mTLS；不能据此宣称该 hop 已验证双向 mTLS。可信登录完成身份沿现有 bridge 种子；本验收不覆盖交互式登录、真实 DSH 宿主或模型对话。低层 NatsLink 的无 Origin socket 工厂仍供独立 transport 使用；生产 device runtime 必须有 provider 提供的可信 Origin。
