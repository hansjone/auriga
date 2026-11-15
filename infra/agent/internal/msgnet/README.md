# MSGNET 公有控制与节点执行

`auriga-agent msgnet-pin --data-root <已有节点目录> --target-id <D固定目标> --identity-id <独立确认身份> --identity-revision <递增序号>` 输出 NodeControlPin，供 D 的独立可信安装通路登记。独立 Ed25519 PKCS8 确认钥位于 `agent/msgnet-identities/<identity_id的SHA256>/msgnet-confirmation.pk8`。同身份重试保持原钥，替换身份使用新目录、新钥、更高 revision 和新根控制。native ECDSA node.key 只用于原生 mTLS。

`agent run` 从普通 JSON 配置的 `msgnetConfig` 或 `AURIGA_AGENT_MSGNET_CONFIG` 加载受保护的本地 JSON。字段为 `rootSpki`、`rootKid`、`nodePinsFile`、`schemaFile`、`proofOrigin`、`proofCaFile`。根、公有 pin、schema 和证明中继 origin 均由 D 固定安装。配置、pin 和 CA 文件须通过当前运行用户的私有 owner/ACL 检查。`run` 要求已存在且与 pin 匹配的独立确认钥。

Installer 验独立根，先持久化控制 fence，再由 NodeExecution 调固定中继。中继以原生 node 证书鉴别 node_id，并在服务端通过可信 pin 映射 target。IAM 提交消息拒连记录，edge 完成真实 CONNZ/KICK 和离线确认，随后 C 提交隔离事实，节点才返回独立 Ed25519 InstallationReceipt。执行失败会继续重试。该通路保留 NodeControl 和实例生命周期。

公有控制包与回执沿既有 NodeControl 消息运输。历史、过渡证明、全局确认和隔离执行使用单独的原生 mTLS HTTPS 中继，沿用冻结的模型和 `/api/collab/v2/network` 路径。节点不会取得内部服务 JWT。中继只开放 bootstrap/keys、transport/history、transport/transitions、transport/confirmation 和本节点的 isolation。所有请求重新检查原生证书的当前序列号与撤销状态。节点每次使用当前证书握手，中继服务端叶子按每次握手热加载。

安装新运输包后，恢复请求还须经过 C 已验证的独立回执、当前控制、精确运输包和新鲜完整 reader 证据检查。new_leaves/retire_old 阶段要求真实 handshake_verified 或已执行的隔离。物理节点和各插件目标分别保留自己的 IAM fence，各插件仍须完成自己的新链确认。C 保留的历史挑战按队首先逐项确认，服务端在此期间持续执行最新的、更严格的隔离。

服务端安装项如下。

- `auriga.iam.msgnet.node-control-pins-file` 加载 D 的 NodeControlPins。
- `auriga.iam.msgnet.targets-file` 加载本地数组，每行包含 `tenant_id`、`realm`、`device_id`、`target_id`，托管实例另含物理 `node_id`。IAM 用自己的 network_node 表把消息节点号解析为真实 device_id。该文件按私有 pin 文件规则读取。
- `auriga.iam.msgnet-ledger.origin/key-store/trust-store/password-file/token-file` 使用独立内部 HTTPS 身份。JWT 需要 C 的 `collab.msgnet.read`、控制回执及 `collab.msgnet.isolation.enforce` 权限。
- `auriga.iam.msgnet-ledger.isolation-origin` 指向 edge 专用私有执行监听器。broker SYS 身份留在 edge。
- `auriga.workspace.msgnet-proof.host/port` 启用节点证明监听器。TLS 叶子使用 workspace 安装的 certificate/private-key 文件，客户端 CA 沿用原生 node CA。D 将其安装为固定 HTTPS origin。

组合部署由 C 的 PlatformMsgnetNodeControl 使用认证 node_id 调 pendingNodeControl/confirmNodeControl。C 的普通 `/controls/receipts` 拒绝 node target。I 的远端适配仅在显式设置 `auriga.workspace.msgnet.remote-node-control=true` 且提供 node-aware ledger 时启用，普通 HTTP ledger 无此确认权限。C 未合并时，按同一 API 用替身测试。

K4 的 selector/ExpectedSelectors 接缝仍按冻结契约失败封闭。部署与正常用户入口验收由主控串行完成。
