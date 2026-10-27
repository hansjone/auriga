# T3b 来源链装配：子进程边界阻塞

核对基线：`87b9e1f1e365d596aa1da7ba7bfea8ccffa263cb`。
此文是装配前检查结果，不是 T3b 实现、独立审查或全链通过证据。

## 首个阻塞：gateway 没有受信的协议签名私钥输入

现有两端要求无法通过 T3a 公开的子进程接口对接：

- `infra/ai-gateway/internal/network/runtime.go` 的 `StartRuntime` 无条件调用
  `newErrorSigner(cfg.ProtocolSigning)`；`error_signer.go` 要求有效的
  Ed25519 PKCS#8 PEM 私钥文件，以及 `kid`、keyset 版本和真实有效期。
  此签名器用于 gateway 生成的固定 ERROR 帧，不代签 collab 业务响应。
- `services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaHost.java`
  的 `installNetworkKeys` 在局部变量中生成 `protocol_response` 密钥对，将其交给
  生产 `NetworkSigningKeys`，经真实 `NetworkKeyOperations` 和离线根批准发布。
  私钥没有写入子进程可消费的文件。
- 同文件 `export` 写出的 `bridge.json` 包含设备凭据路径、服务 JWT 路径、realm、
  audience、根公钥路径及 pin，没有协议签名私钥出口。
  `NetworkSigningKeys.persist` 也只保存公钥和元数据，不能从状态文件恢复私钥。
- 原入口只接受 `refresh`、`revoke-member`、`stop`/EOF；没有导出该材料的控制命令。

因此，原样复用 `launch-java-bridge.ps1` / `NetworkSourceJavaHost`，不能给生产
gateway 提供与该桥已发布 keyset 对应的 ERROR 签名配置。另生成一个测试 signer
不能获得该根的批准；跳过 gateway 启动校验或插件验签也不能算验收。

任务明确禁止改 T3a 文件、重造 Java 宿主/认证；本次到此停止装配实现。
没有读取 Java 内存、反射提取私钥、改写 keyset 或使用假业务回包绕过。

## 请主控先确定的最小上游补齐

在 T3a 同一个隔离宿主里，将本次真实发布的 `protocol_response` 私钥提供给
gateway 子进程。可沿用现有私有临时目录和原子私有文件机制，在密钥生成时输出
PKCS#8 PEM；同时提供精确 `kid`、keyset 版本、有效期及文件路径。只交配置路径，
不把私钥放 stdout、argv、报告或共享 profile；关闭时继续清理本次目录。
这需要修改当前明确禁止改动的 T3a 文件，须由主控拆出上游补齐或明确扩大允许路径。

补齐时应验证导出的私钥与真实 HTTPS 发布的 keyset 相符、正常 ERROR 可由生产插件
验签，错误 key/kid/期限则拒绝，并保留现有私有文件和关闭生命周期断言。
本建议没有定义或添加新的产品契约。

## 后续矩阵还需确认的观测与故障边界

原 37 例以及 T3a 的 generated Redeem 检查不包含完整 Broker/WSS/生产插件来源链。
复用它们的绿测不能证明以下新增事实：

- 真实 Broker 2.15.1 callout 触发 Redeem，真实 C1/C2 七字段及永久出处、READY、
  typed-local 登记、完整原操作回执与私有文件 ledger 的对应关系。
- F1/F2/F7 的 IAM 提交、collab 保存及提交确认未知的确定边界，以及恢复前后的
  DB 持久事实。仅在 gateway 处丢外层包，不能声称命中了 IAM 提交后的特定间隙。
- F3–F6 的实际 Read/Fence/Register 调用路径、纯 Read 零写、精确拒绝分类和最终 CAS。

当前子进程接口没有 DB 事实快照或这些边界闩锁。需要先确定由同一宿主提供的最小
测试观测/控制出口，或经批准的外部验证方案；此项是矩阵装配问题，尚未试运行。
不向生产服务添加测试开关，不预塞来源/操作回执，也不把网络任意错误当业务终态。

## 验证口径

本工位运行的命令、退出码和结果见工位根 `.agent-report.md`（按工作法不提交）。
已有 gateway 配置拒绝测试使用 Go 内嵌 Broker 2.12.6 和 RPC fixture，只能证明
配置缺失拒绝；它不证明 Broker 2.15.1 或 T3b 全链。
真实 PG/Broker 全链、业务坏实现反证、P1–P6/F1–F7 与第五 RPC 尚未运行。
不接 SEND，不声明真实 DSH、Beta 或跨 Broker 版本通过。
