# MSGNET 公有控制接线

`auriga-agent msgnet-pin --data-root <已有节点目录> --target-id <D固定目标> --identity-id <独立确认身份> --identity-revision <递增序号>` 只输出 NodeControlPin，供 D 的独立可信安装通路登记。公钥由已有公有 node.crt 绑定 node_id；独立 Ed25519 PKCS8 确认钥持久保存在 agent/msgnet-identities/<identity_id的SHA256>/msgnet-confirmation.pk8，Linux0600或 Windows私有ACL，不读取/替换 native ECDSA node.key，不自行上传或信任新 pin。新身份使用新目录生成独立密钥；同身份重试保持原钥，不能把受侵旧钥改名作为替代者。

`Installer` 消费固定 NodeControl 公有模型。装配方必须提供独立根、可信 NodeControlPin、上述本地确认钥、冻结 Schemas、以及真实消息隔离 Backend。公有包必须先落盘再返回 Ed25519 InstallationReceipt；身份替换要求更高 revision、新根控制和挑战，旧身份回执不能授权替代者。Backend 失败不发成功回执，不能用空实现代表实际隔离。

`PublicAuthorityResolver` 只读固定 HTTPS /bootstrap/keys，不携带服务 JWT/投影私钥；返回的授权仍由 Installer 独立核根签名和最新根控制。K3 运输历史与全局确认由 TransportSource 端口提供，不能把后台服务 JWT 发给节点。跨进程 source 中继和 agent 启动 Backend 尚需接线；当前 run 未启用 Installer，不能据此宣称节点隔离已交付。

workspace 的 PinnedMsgnetNodeControl 使用 IAM 的可信 NodeControlAuthority 与 MessageNetworkControlLedger 中继公有包/回执，不产生本地 READY。IAM 可通过 auriga.iam.msgnet.node-control-pins-file 加载 D 的私有 pin 文件；auriga.iam.msgnet-ledger.origin/key-store/trust-store/password-file/token-file 配置专用内部 HTTPS 客户端。客户端仅操作公有控制和隔离记录，绝不提供私钥材料方法。C 仍须用真实台账原子验证挑战、撤销、隔离和新链 READY。
