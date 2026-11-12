# 实例出网策略

升级和冷启动默认沿用原有公网出网行为，仅执行环境拒绝列表与地址边界。管理员从未保存的 v1 空种子不会触发收紧下发，页面显示“未配置，沿用默认出网”。platform 未启动、无安装租户或暂时不可用，不影响出口健康检查和 agent 启动；已有状态则继续执行最后应用的策略。

在管理台 `/security/egress` 选择模式并确认保存：

- `open`：允许公网目标，平台拒绝项优先。恢复默认操作清空平台允许与拒绝项。
- `allowlist`：仅允许列表内域名；空列表明确拒绝全部，页面会再次提示。
- 旧格式无模式时，非空允许列表保持白名单，否则开放。`*.example.com` 不包含根域。

无论采用哪种模式，部署环境拒绝列表、私网、回环、链路本地、CGNAT、元数据与非公网 IPv6 都不能放开。每次连接校验解析结果并固定拨号 IP。应用新版本只关闭新策略拒绝的连接，仍被允许的流式连接保留。

Compose/NAS 通过两个命名卷通信。平台独占写策略卷，出口独占写状态/回报卷，另一方只读；实例不挂载这两个卷，也不发布网络管理端口。两种镜像均设置相同卷目录 UID，因此出口不依赖平台先启动。

平台配置：

- `AURIGA_EGRESS_POLICY_DIRECTORY=/var/auriga-egress/policies`
- `AURIGA_EGRESS_REPORT_DIRECTORY=/var/auriga-egress/reports`
- `AURIGA_EGRESS_NODE_ID=egress-primary`
- `AURIGA_SECURITY_EDGE_CONFIG_FILE=/etc/auriga/edge.config.json`：仅投影中转公网入口。

出口配置：

- `AURIGA_EGRESS_POLICY_FILE=/var/auriga-egress/policies/policy.json`
- `AURIGA_EGRESS_STATE_FILE=/var/auriga-egress/reports/state.json`
- `AURIGA_EGRESS_REPORT_FILE=/var/auriga-egress/reports/report.json`
- `AURIGA_EGRESS_NODE_ID` 与平台一致。
- 可选 `AURIGA_EGRESS_TENANT_ID`：绑定安装租户的 UUID，拒绝不符的首份快照和缓存状态；不配置时仍依赖私有只读卷与首次租户固定。

出口每 2 秒拉取策略，拒绝版本回退、同版本换内容、租户变化和未知字段。状态先持久化，再应用与回报。变化时立即回报，未变化时每 30 秒心跳；平台以 90 秒判断过期。发布和回报导入分事务，不一致回报保留真实下发记录并显示失败。

旧保存值由兼容投影保留有效规则、忽略不支持项并显示警告；管理员保存后确认当前规则。V3 提升旧保存版本，未配置种子不变。契约见 `contracts/deployment/egress.schema.json` 与 `admin-sec-net.md`。

仅支持单宿主、固定安装租户、单共享出口。勿跨安装复用状态卷。Bifrost 保留原有代理环境与提供商配置，实例策略不管理中转上游出口。TLS 到期、实际公网出口与多节点分发需另接可信能力。
