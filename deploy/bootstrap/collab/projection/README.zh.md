# 消息网络本地投影组件

本目录属于 MSGNET-N。只消费 `contracts/network/auto-renewal-v2.schema.json` 和生成的 `infra/gen/msgnetapi`；没有复制契约类型或引入测试参考判定器。当前有两项跨线契约阻塞，见末节，不能把本组件的单测结果作为整线无人值守已验收。

## 构建和装配交接

使用 Go 1.27.3，在 `deploy/bootstrap` 执行：

```sh
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o target/msgnet-projector-linux-amd64 ./collab/cmd/msgnet-projector
```

一个二进制提供 `projector`、`broker`、`control-import` 三种固定模式。运行 UID10001；非 Linux 的持久落盘/peer 验证失败关闭。D 应单独打包此二进制、冻结 schema、安装根 SPKI、InstallationPins 和仅本身份可读的安装签名/密封私钥，不打包 KEK 或其他组件的私钥。

gateway 通过模块依赖引用本目录。D 修改 NAS 镜像构建时，须在构建 ai-gateway **之前**复制 `deploy/bootstrap`；原 Dockerfile 在构建 gateway 之后才复制它，需要调整此顺序。N 不修改 D 的 Dockerfile、Compose 或现场。

network/broker 的材料卷 UID10001，目录0700、文件0600；reader 只读挂载材料卷。独立控制卷由 reader 与其专属 projector 挂载，root0710、组10001、socket0660；reader 必须可以在控制卷写自己的高水位和信任账本。不要让两个组件共享卷，也不要让 projector 与 reader 使用同一个 `trust-ledger.json`：CLI 的 projector 使用 `projector-state/`，broker 使用 `broker-state/`；network 的信任账本位于其控制卷根。

投影器示例（路径均由 D 的受控配置提供）：

```sh
msgnet-projector --mode projector \
  --schema /etc/msgnet/auto-renewal-v2.schema.json \
  --pins /run/msgnet/installation-pins.json \
  --root-spki /etc/msgnet/root.spki.txt \
  --identity network-projector \
  --materials /run/msgnet/materials --control /run/msgnet/control \
  --origin https://collab.example.com \
  --signing-key /run/secrets/projector-signing.pkcs8 \
  --sealing-key /run/secrets/projector-sealing.pkcs8 \
  --service-token-file /run/secrets/projector.jwt
```

`--https-ca` 可指定独立 HTTPS 信任池；默认使用系统根，不跳过验签。受保护回执端口需要时，额外配置 `--https-client-cert` / `--https-client-key`。服务 JWT 每次回执发送重新读取，mTLS 客户端身份每次新握手重新读取。I 必须提供该独立投影身份的令牌/证书轮换接线，不能复用网关的宽权限。公有 bootstrap GET 与安装签名 POST 不发送服务 JWT、不使用该 mTLS 客户端身份，避免旧身份到期阻断密封取材。

## 文件、重入与本地协议

固定布局为 `selector.json`、`generations/<manifest_digest>/manifest.json`、manifest 列出的文件和组件控制卷 `reload.sock`。完整一代先写持久 `transaction.json`，fsync 后原子替换 selector。同代精确重试复用完整字节和 nonce。崩溃重启会恢复事务；若 C 已发布更新版本，先验证旧事务的历史根链、私钥匹配与完整材料，再保存到 `transaction-history/`，保留材料 revision 高水位，安装新的权威材料。篡改/不可验证事务继续失败，不删除证据来绕过。

本地 socket 的长度帧、JCS、selector 回显和失败码均用 K2 模型。客户端写完请求后 half-close 写侧，服务端拒绝尾随字节后返回一帧并关闭。双方有5秒截止，检查 SO_PEERCRED。reader 持文件锁后只清理属于自己的失效 socket，重启不依赖人工删除锁文件。

reader 持久高水位与实际已加载句柄分开：持久写入后、内存切换前崩溃，重启必须重新加载才能确认。`confirm` 不能触发其他代加载；失败不确认新代。每5秒检查 selector，重试1秒起指数退避至60秒。签名快照、protocol 私钥和 TLS 集整代切换；私钥用途、active/next、公私匹配、叶子 SAN/EKU/组件、CA 和期限都另行校验。

## network 接线

`network.RuntimeConfig.projection` 提供以下受控配置：materialsDir、controlDir、schemaFile、rootSpki、tenantId、realm、pinsFile、installationId、fenceOrigin、fenceCAFile、serviceTokenFile、controlSnapshotFile。启用后不再从旧静态 protocol 私钥配置建立该签名器。

每次 protocol 签名前核独立根控制和 C 当前状态/完整清单摘要，C 不可达、时钟错误、暂停、撤销、落后或同版冲突均拒签。状态使用 RenewalStatusOutput 分支，不伪造首次发布版本。root-control 独立导入文件优先于 C 的旧响应；较低控制不能覆盖已持久高控制。

submit 新握手原子使用新 server 叶子和 client CA；broker-client 原生 NATS STARTTLS 每次连接使用新 client 叶子/server CA。材料切换触发三条 NATS 连接重新连接，使用当前快照重新验完整 TLS 链。broker 重启更换 ServerID 后，认证只跟随同一受控 server_name 且三条连接一致的新 ID；旧绑定直接移除，不对新进程复用的 CID 发旧 KICK。只有这一精确代的所有 network 叶子角色都实际握手成功，才确认 handshake_verified。超过60秒失败后拒绝受影响 submit 流量/握手和 protocol 签名；恢复后自动放行。

## broker 监督器

`--mode broker` 额外接收受控 `--broker`、`--broker-sha256` 和 `--broker-template`。摘要来自 D 打包的固定二进制清单，还核 `nats-server: v2.12.6`（仓库当前 go.mod 的版本，非测试专用 assembly 的2.15.1）。socket 不能指定可执行文件、参数、路径或 PID。

模板是 D 提供的受信 NATS 配置，其中证书路径固定用 `{{MSGNET_CERT}}`、`{{MSGNET_KEY}}`、`{{MSGNET_CA}}` 占位符并放在引号内。监督器只生成自己的运行配置、broker 叶子和公有 CA；先执行固定 `-t -c` 配置检查，再管理自己启动的 `-c` 子进程。采用保守的受控重启路径：SIGTERM，最多5秒后仅终止自己的子进程，再启动新配置。不访问 docker.sock，不驱动外部 PID。

真实 mTLS 握手仍须由连接方/验收方验证，不能把子进程启动当 handshake_verified。broker 监督器仅回 loaded。独立根控制撤销或过期令该子进程退出，材料恢复后 reader 的活动句柄检查会重新启动它。

## 独立根控制导入

`--mode control-import --control-package <公有ControlPackage文件>` 使用安装根、pin 和独立安装签名私钥，持久更新公有 `root-control.json`，再生成 InstallationReceipt；默认写入组件控制卷，`--control-snapshot` 可以由 D 指定同一受控路径。回执缓存按包摘要保存，重试返回相同签名和时间；标准输出仅为公有回执 JSON，D 将它送 C 的控制确认入口。此命令不解除隔离、不创建身份。network 设置 controlSnapshotFile，broker 配置同一路径，以便独立管理机导入后直接采用更高控制，而非等运行清单更换。

## 测试与尚需冻结的接口

Go 生产判定独立通过共享向量：消费端174、签发时钟12、独立边界93，本地投影24。测试运行时生成私钥；HPKE 使用 Go 标准库 RFC9180 实现。Linux UID10001 的测试还覆盖密封 bootstrap、回执丢失后的原字节重试、权威版本已推进时的事务保留和追平、协议真实签名、TLS 换证、独立控制导入、真实 broker mTLS 与受控子进程重启。独立测试时钟覆盖735天、147次5天换代、444把历史钥落盘后重启及归档 SPKI 复用拒绝，不改变真实宿主时钟。

以下问题需主控/K/C/I/D 决定；当前失败关闭，未绕过：

1. 共享消费规则要求 readers_persisted / handshakes_verified 来自安装回执台账，不能取运行 bundle 的 phase 声明代替。现有 OpenAPI 只有写回执端口，没有读取绑定 bundle/目标集合/确认事实的接口或证明对象。`Trust.SetTransportConfirmation` 留出接缝；没有可验证全局确认时，新 CA 阶段返回 projection_pending。本地 loaded 或一次 TLS 成功不能证明全体 required_reader_ids 已完成；尤其 broker 没有理由伪造自己的 handshake_verified。未接通这一来源，不能宣称90天/10年无人值守轮换全部通过。
2. K2 固定 projector UID10001、edge reader UID65532，同时材料目录0700/文件0600归 reader 实际 UID，且 projector cap_drop ALL。普通10001不能在65532私有目录写文件，也不能 chown 新文件为65532；edge 控制卷0710归65532同样不能让10001在卷根创建原子状态文件。需要冻结可落地的每组件 UID/peer 检查或其他明确隔离方案，不能由 N 放宽权限。network/broker 的10001路径已实测，edge 仍交 I/D，未验。

真实 C/I/P 联调、正常页面/真实 DSH、NAS部署和长期虚拟时间门禁由主控在解除这些阻塞后继续完成；本目录测试不替代这些证据。
