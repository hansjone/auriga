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

Windows 局部差分确认，锁定 Broker 2.15.1 的坏票据拒绝尾帧可能在首轮 CONNECT、PING 分两个 WebSocket 帧发送时遇到 ECONNRESET，且重置先于客户端关闭；单帧对照没有该缺帧现象。这是当前平台的实测边界，不外推所有操作系统或所有 Broker 版本。生产 NatsLink 因此仅合并初次同步调用轮的两次写入，保持原始字节和顺序；不解析鉴权、不重试、不改变拒绝判据，后续应用消息不合并。

首轮快照用 Node 22 的 ArrayBuffer.transfer 显式分离旧存储。令 B 不超过原队列预算与单帧 handoff 预算的较小者，可能的旧+新复制峰值不超过 2B；在应用尚未 ready、queue/handoff 均为空的阶段使用这两项既有预算，native 发送仍受原 socket hardCap 限制。已发送的快照保留至 native send 回调或实际 close 后才 detach，应用发送等待释放，不能把 GC 当即时回收。过大的首写直接沿原写法；无法合并的第二写先按原 guard 发首帧，若首 flush 失败不继续第二写。预算数值与总额不增加。

局部诊断可单独运行 `node e2e/network/support/edge-wss-auth-probe.mjs --compare` 对照 CONNECT-only、单帧、双帧和碎片；`--direct-fixed` 对生产 NatsLink 连续120次真实坏 token 拒绝及首轮单 native write 逐次断言。这些探针只起临时真实 Broker，不起 IAM/PG，不算业务验收；每次结果写入独立 `edge-wss-auth-probe-*` 目录，保留全部失败与成功对照。
