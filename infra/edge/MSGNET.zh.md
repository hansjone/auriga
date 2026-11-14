# MSGNET v2：I → D/N 接口

此处是 edge 的受控安装配置，不是新增网络 wire 模型。尚未部署 NAS。K3 启用必须与 N 的 edge 专属 projector、D 的 UID/卷配置同批进行。

在现有 edge 配置设置 `msgnetProjection` 后，submit、WSS 上游与 presence 使用根验证后的内存句柄。`networkSubmitHop.serverName`、`networkWss.serverName`、`devicePresence.serverName` 仍来自固定安装域；submit 上游必须 HTTPS，presence 必须 `tls://`。这些消息跳的旧证书文件不再是信任来源，可清空；collab 的独立管理 mTLS hop 仍需有效，用来读取公有控制与内部台账。

`msgnetProjection` 字段：

- `root`：本 edge 材料卷，只读，固定 selector/generations 布局。
- `control`：本 edge 与专属 projector 共享的私有控制卷，固定 reload.sock。
- `state`：只挂给 edge 的独立耐久状态卷；不得与前两者重叠或互为子目录。
- `schemaFile`：K3 冻结 `contracts/network/auto-renewal-v2.schema.json` 的只读安装副本。
- `rootSPKI`、`rootKid`、`tenant`、`realm`：独立安装的消息信任根和域；SPKI 是规范 Ed25519 DER 的 base64url，不从材料目录获得根。
- `installation`：D 已登记的 InstallationPin，component=edge，身份必须与根授权 scope 相同。
- `submitLeafSAN`、`presenceLeafSAN`：分别选择 edge、edge-presence 客户端叶子的固定 DNS SAN，二者不能相同。
- `ledgerOrigin`、`tokenFile`：固定内部 HTTPS origin 与具只读历史/全局确认权限的服务 JWT 文件。JWT 不随 NodeControl 下发。公有 bootstrap/controls 不依赖 JWT。

reader/projector 均 UID/GID65532。材料/控制/状态目录0700、材料文件与 socket0600；在线 cap_drop ALL/no-new-privileges，不 chown。reader 材料卷只读、控制/状态卷读写，其他组件不挂这些卷。

reader 每5秒检查控制与固定 selector，失败持续重试；60秒未恢复即撤下消息句柄。根撤销立即拒绝新连接并关闭受管的 submit/WSS/presence 连接。精确 selector 重试不关闭连接。重启恢复耐久控制与 CA 墓碑，但不会恢复 loaded/handshake_verified 的内存确认；两个叶子用途均真实握手才确认 handshake_verified。公有/NodeControl TLS 和 DSH 生命周期由原入口管理。

N 继续按冻结本地模型发送 reload/confirm。C 的 /transport/history、/transport/transitions 和 /transport/confirmation 必须提供 no-store 的冻结对象。全局确认签名代表根授权运行钥对 C 授权台账的声明，消费端不从证据行反填 expected_selectors。

本线测试包含两轮阶段推进、新 CA/新叶子、真实 mTLS/WSS/NATS presence 握手、退休 CA 复活拒绝与撤销断连；不代表 C/N/D 的整线或真实 NAS 验收已完成。
