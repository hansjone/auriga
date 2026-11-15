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

N 继续按冻结本地模型发送 reload/confirm。C 的 /transport/history、/transport/transitions、/transport/expected-selectors 和 /transport/confirmation 必须提供 no-store 的冻结对象。reader 先通过独立 HTTPS+mTLS/JWT 读取 ExpectedSelectors，核精确 bundle、最新根控制摘要、随机 challenge、60秒有效期及冻结 reader/身份集合，再核全局确认签名与逐行 selector。消费端不从证据行反填 expected_selectors；无 selector 的实际隔离行仅在独立台账显式 null 且身份精确匹配时接受。HTTP202 不推进阶段，台账缺失或失效继续等待投影，重试重新查询。

本线测试包含两轮阶段推进、新 CA/新叶子、真实 mTLS/WSS/NATS presence 握手、退休 CA 复活拒绝与撤销断连；不代表 C/N/D 的整线或真实 NAS 验收已完成。

## 消息隔离执行

edge 配置 `msgnetIsolation` 可启用独立的 Linux 私有 mTLS 监听器。字段为 `listen`、`certFile`、`keyFile`、`caFile`、`callerSpki`、`targetsFile`、`schemaFile`。`callerSpki` 固定 IAM 执行服务叶子的公有 SPKI。私钥和 target 文件的父目录须为当前 edge 用户私有目录，Linux0700/0600。该监听器不注册到普通 Gateway Handler。服务端叶子与私钥在每次握手按受保护文件重新加载。

`targetsFile` 是 D 提供的本地数组，每行包含 `tenant_id`、`realm`、`target_id`、`devices`。`devices` 为该目标的 IAM device UUID 数组。物理节点目标列全体所辖设备，插件目标列自身设备。映射与 IAM 的受保护 target 配置应同批安装，更新映射需重启读取快照。

仅已登记 IAM mTLS 身份可 POST 冻结 IsolationRecord 到 `/api/collab/v2/network/isolation`。IAM 先提交自己的拒连 fence，再请求此端口。edge 从 IAM 的内部 ReadBindings RPC 读取真实连接归属，使用自身最小权限 presence 身份执行 CONNZ/KICK，确认目标连接全部离线后才返回成功。空连接集、已退役 broker generation 可确认离线，未知归属、监控不可用、超时和断连失败均拒绝确认。C 的 enforceIsolation 由 IAM 在此成功之后调用。

IAM 的 V18 表按 tenant/device/target 保存独立消息 fence，启用 FORCE RLS。签发、兑换、活跃检查和消息源授权核对该 fence。物理节点与插件目标各自确认 READY 后分别解除各自记录。普通设备与实例生命周期继续由原权限管理。
