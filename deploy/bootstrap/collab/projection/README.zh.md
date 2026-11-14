# 消息网络本地投影组件

本目录属于 MSGNET-N。只消费 `contracts/network/auto-renewal-v2.schema.json` 和生成的 `infra/gen/msgnetapi`；没有复制契约类型或引入测试参考判定器。已按 K3 接入运输转换、全局确认验证和 edge UID 裁决；独立材料台账读取仍缺契约，见末节，不能把单测结果作为整线无人值守已验收。

## 构建和装配交接

使用 Go 1.27.3，在 `deploy/bootstrap` 执行：

```sh
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o target/msgnet-projector-linux-amd64 ./collab/cmd/msgnet-projector
```

一个二进制提供 `projector`、`broker`、`control-import` 三种固定模式。network/broker 的 UID/GID 为10001，edge 的专属 projector 和 control-import 为65532；组件从已安装 pin 确定，禁止命令行自报 UID。非 Linux 的 peer/材料权限验证失败关闭。D 应单独打包二进制、冻结 schema、安装根 SPKI、InstallationPins 和仅本身份可读的安装签名/密封私钥，不打包 KEK 或其他组件的私钥。

gateway 通过模块依赖引用本目录。D 修改 NAS 镜像构建时，须在构建 ai-gateway **之前**复制 `deploy/bootstrap`；原 Dockerfile 在构建 gateway 之后才复制它，需要调整此顺序。N 不修改 D 的 Dockerfile、Compose 或现场。

每组件有独立材料卷与控制卷，均归该组件 UID/GID，目录0700、材料文件和 socket0600。reader 只读材料卷，projector 可写；双方只读写本组件控制卷。reader 的信任账本和 selector 高水位另放 reader 专用耐久状态卷，不挂给 projector。`NewReader(materials, control, state, prepare)` 的第三参数是此独立目录；network 配置 `stateDir`，broker 模式必须提供 `--reader-state`。CLI 的 projector 状态位于控制卷的 `projector-state/`。D 在安装时预建正确 owner，在线进程不 chown，使用 cap_drop ALL/no-new-privileges；不能把多个组件的卷挂给同一 projector。

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

`network.RuntimeConfig.projection` 提供以下受控配置：materialsDir、controlDir、stateDir、schemaFile、rootSpki、tenantId、realm、pinsFile、installationId、fenceOrigin、fenceCAFile、serviceTokenFile、fenceClientCertFile、fenceClientKeyFile、controlSnapshotFile。后两项证书字段用于 K3 受保护读取端口的独立服务 mTLS 身份，每次连接重读。启用后不再从旧静态 protocol 私钥配置建立该签名器。

每次 protocol 签名前核独立根控制和 C 当前状态/完整清单摘要，C 不可达、时钟错误、暂停、撤销、落后或同版冲突均拒签。状态使用 RenewalStatusOutput 分支，不伪造首次发布版本。root-control 独立导入文件优先于 C 的旧响应；较低控制不能覆盖已持久高控制。

submit 新握手原子使用新 server 叶子和 client CA；broker-client 原生 NATS STARTTLS 每次连接使用新 client 叶子/server CA。材料切换触发三条 NATS 连接重新连接，使用当前快照重新验完整 TLS 链。broker 重启更换 ServerID 后，认证只跟随同一受控 server_name 且三条连接一致的新 ID；旧绑定直接移除，不对新进程复用的 CID 发旧 KICK。只有这一精确代的所有 network 叶子角色都实际握手成功，才确认 handshake_verified。超过60秒失败后拒绝受影响 submit 流量/握手和 protocol 签名；恢复后自动放行。

## broker 监督器

`--mode broker` 额外接收受控 `--broker`、`--broker-sha256` 和 `--broker-template`。摘要来自 D 打包的固定二进制清单，还核 `nats-server: v2.12.6`（仓库当前 go.mod 的版本，非测试专用 assembly 的2.15.1）。socket 不能指定可执行文件、参数、路径或 PID。

broker 模式同时需要 `--reader-state`、固定 `--origin`、独立 `--https-ca`（系统根可省略）、`--https-client-cert/key` 和 `--service-token-file`，以读取 K3 历史运输包与全局确认证据。它仍不需要安装签名/密封私钥。

模板是 D 提供的受信 NATS 配置，其中证书路径固定用 `{{MSGNET_CERT}}`、`{{MSGNET_KEY}}`、`{{MSGNET_CA}}` 占位符并放在引号内。监督器只生成自己的运行配置、broker 叶子和公有 CA；先执行固定 `-t -c` 配置检查，再管理自己启动的 `-c` 子进程。采用保守的受控重启路径：SIGTERM，最多5秒后仅终止自己的子进程，再启动新配置。不访问 docker.sock，不驱动外部 PID。

真实 mTLS 握手仍须由连接方/验收方验证，不能把子进程启动当 handshake_verified。broker 监督器仅回 loaded。独立根控制撤销或过期令该子进程退出，材料恢复后 reader 的活动句柄检查会重新启动它。

## 独立根控制导入

`--mode control-import --control-package <公有ControlPackage文件>` 使用安装根、pin 和独立安装签名私钥，持久更新公有 `root-control.json`，再生成 InstallationReceipt；默认写入组件控制卷，`--control-snapshot` 可以由 D 指定同一受控路径。回执缓存按包摘要保存，重试返回相同签名和时间；标准输出仅为公有回执 JSON，D 将它送 C 的控制确认入口。此命令不解除隔离、不创建身份。network 设置 controlSnapshotFile，broker 配置同一路径，以便独立管理机导入后直接采用更高控制，而非等运行清单更换。

## K3 验证与跨线接缝

Go 生产判定独立通过共享向量：消费端179、签发时钟12、独立边界242（含 K3 运输转换49、全局确认40、追平25、节点绑定8、权限27），本地投影24，共457条。测试运行时生成私钥；HPKE 使用 Go 标准库 RFC9180。Linux UID10001 的测试另覆盖密封 bootstrap、丢回执重试、事务保留追平、真实协议签名/TLS 换证、独立控制导入及真实 broker mTLS/受控重启；UID65532 实测独立 edge 目录、0600 socket 和双向 peer 检查。735天、147次5天换代和444把历史业务钥的虚拟时钟测试仍保留。

`TransportSource` 读取固定 `/transport/history/{digest}`、`/transport/transitions/{authority_digest}`、`/isolation/{target_id}` 和 POST `/transport/confirmation`。历史链只验证，不替换最高根控制。跨授权先验根锚起点，再验当前快照和本地高水位；运输版本与业务版本独立，更高运输 generation 允许根授权的新版本空间。耐久 CA 历史保留已退休标记，拒绝旧 CA 重现或换 ID 复用证书。restart_recovery 对未成熟 next 保留原身份/期限并置 retired/revoked，成熟期等号按原规则。

确认使用第四种运行签名域 `Auriga.NetworkTransportConfirmation.v2` 和 network_transport 能力。随机32字节 challenge、当前控制 revision、精确历史 bundle/阶段/冻结集合、唯一逐端行、selector 摘要、实际隔离和60秒新鲜度都参与验证；new_leaves 查前一 dual_trust/loaded，retire_old 查前一 new_leaves/handshake_verified。同代重试和重启后仍重新查询，已保存的本地 loaded/handshake 状态不代替全局证明。生产默认无独立台账时继续 projection_pending。

剩余需 K/C 裁决：K3 明确要求 `expected_selectors` 来自独立材料台账且不能从证据行反填，但当前只读契约没有返回这份映射的模型/端点。SignedTransportConfirmation 只有待核的 readers，TransportPredecessor/TransitionProof 只有公有包；单机 selector 也不覆盖整个冻结集合。`TransportSource.ExpectedSelectors(ctx, previousBundle)` 是明确的耐久材料台账端口，测试以独立预存映射替代 C，CLI/network 不从响应行构造它。需要冻结并实现此只读来源（或明确修改消费者信任裁决），才能接通线上 new_leaves/retire_old。该来源应精确绑定 tenant/realm、bundle完整摘要/版本、当前控制与冻结 reader 集合，并支持离线追平。N 不自行扩展契约或放宽比较。

`ValidateNodeControlPins` / `BindNodeControl` 提供独立节点 Ed25519 pin 的规范性、唯一性及认证连接绑定检查，不扩展 InstallationPins 的组件枚举；I 负责将它接到真实 NodeControl 认证连接和独立登记通路。节点始终不能领取 ProjectionMaterial。

I/D 须同批接 edge65532、独立卷/状态卷与证书令牌自动轮换。真实 C/I/P 联调、跨10年 CA 恢复、正常页面/真实 DSH 和 NAS部署由主控集成后验收；本目录测试不替代这些证据。
