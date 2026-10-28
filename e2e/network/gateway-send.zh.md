# SEND-2 gateway 验收

本驱动复用 T3b/T4 的真实 Java IAM/collab、PG、Broker、device-provider 和私有 WSS。
`source-gateway-send.mjs --run` 在原矩阵中增加 HTTPS Submit、同键原结果、目标 FETCH、
ReceiveSet/AttemptBegin/ExecutionAdmit 及跨身份零写断言；原 T3b/T4 断言全部保留。
它是传输接线验收，不调用宿主 prompt，不伪造宿主持久证据，也不提交 RECEIPT。
插件默认收发与真实双 DSH 闭环属于后续工作线。

生产 `--network-config` 可在原配置中增加 `submitHTTP`：

```json
{
  "submitHTTP": {
    "listen": "127.0.0.1:8443",
    "tlsCertFile": "/run/secrets/network-cert.pem",
    "tlsKeyFile": "/run/secrets/network-key.pem",
    "clientCAFile": "/run/secrets/submit-client-ca.pem"
  }
}
```

该 HTTPS listener 必须与 callout/router 在同一进程，共用受验证 READY 连接登记。
`clientCAFile` 必填并强制校验客户端证书；缺失或无效时拒绝启动，不兼容旧单向 TLS 配置。
source-assembly 在 Java 已创建的私有临时目录内生成独立 submit 客户端 CA 和驱动叶证书，
只保存 CA 公钥、客户端证书/私钥（0600），CA 私钥不落盘，不复用 IAM 或 Broker CA。
`gateway-ready` 仅携带 `submitClientCertFile`、`submitClientKeyFile` 的私有路径及固定
`submitServerName=localhost`，不输出证书私钥内容。SEND 驱动使用该客户端身份和
`gateway.ca` 明确校验服务端；不会借用进程的 IAM 扩展信任或关闭 TLS 校验。
Control 与 TrustClientIdentity 均经相同 mTLS 请求入口，精确 HEAD 归档及业务反证断言保持不变。
入口 `POST /api/ai/v1/network/submit` 要求 `Authorization: Bearer <device access token>`
和 `Content-Type: application/json`。body 是完整 SubmitRequest；身份字段、连接 tuple、
身份头与 query 参数均不能选取来源。IAM 验证当前 device/owner/backing session 后，
gateway 只选该 tenant/device 唯一有效 READY 连接；零连接拒绝，歧义连接冲突。
collab 校验 source_binding 所属与 SOURCE_GET 有效观察，解析目标，并按原语义调用第五 RPC。
成功 HTTP 200 是 collab 原 SendResult 字节；客户端遇到响应丢失须保留原请求与幂等 key。

在干净工位的精确提交上执行（完整环境使用既有 auriga-env.ps1）：

```powershell
.\e2e\network\verify-gateway-send.ps1 -ExpectedHead <40位HEAD> -CompileOnly
.\e2e\network\verify-gateway-send.ps1 -ExpectedHead <40位HEAD> -Run
```

脚本分别解包 Control/TrustClientIdentity，安装锁定独立依赖、编译 Java 测试装配和 Go，
保存 archive SHA256、测试/产品源码哈希、完整命令、退出码与日志。
CompileOnly 不启动 PG，不证明业务断言通过。Run 的 Control 必须完整通过原矩阵及 SEND；
突变仅在 gateway 故意信任客户端 tenant/node/connection，并删除这些自报字段后调用 Submit。
另一有效设备伪装受害者的真实连接时，必须由 `SEND-2 cross-identity zero writes` 检出新增 PG 行。
初始化、编译、SQL、清理失败都不计反证。业务快照 `gateway-send.json` 保存前后四表计数；
不导出设备凭据。Java 测试装配仅增加已有 IamGrpcDevices 服务挂载，不改产品授权逻辑。

若 wrapper 下载被本机 TLS 环境阻断，可指定 `-MavenCommand mvn` 使用已安装的同版本
Maven 3.9.16；脚本记录实际命令和退出码，不将下载失败计为测试通过。

FETCH continuation、签名下行及 RECEIPT expected_state_version 在 gateway 中都按原字节转发；
单测检查 continuation 和超出 JavaScript 安全整数范围的服务端版本不被重写。
本驱动无真实宿主证据，RECEIPT 只由对应 Go 转发测试覆盖。

不启动 PG 的 TLS/驱动回归：

```powershell
node --test e2e/network/submit-client.test.mjs e2e/network/gateway-send.test.mjs
# 从 infra/ai-gateway 执行：
go test -count=1 -run '^TestAssemblySubmitTLS' ./cmd/network-source-assembly
```

Go 用真实 Broker/runtime 和 SubmitHandler 验证客户端证书边界；Node 验证驱动携带证书、
错误 CA/服务名/缺材料拒绝、请求及响应字节保持。它们不替代上述精确 HEAD 的 PG 全链验证。
