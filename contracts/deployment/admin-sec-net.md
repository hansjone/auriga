# 管理台入口与出网策略边界

`GET /api/org/v1/admin/security/entries` 读取平台已验证的 DeploymentAccess 投影，要求管理上下文及 org.policies.read。只返回当前租户的门户、管理 Origin、入口模式、实例父域和代理种类，不公开内部监听器、控制地址或凭据。安装配置缺失或租户不匹配时返回 common.unavailable。HTTPS 配置不证明证书健康或实时可达。

出网编辑复用 `/api/org/v1/admin/policies/egress`。所有服务层 egress 写入要求管理上下文、org.policies.write 和十分钟内 step-up，失败采用现有 common.forbidden / iam.step_up_required。前端必须确认并使用读取的 ETag 提交 If-Match，写失败不自动重放。域名仅支持小写 ASCII DNS 名和最左侧 `*.`；拒绝 IP（包括私网、元数据和 CGNAT）、URL、端口、单标签、内部域名、未知字段及重复项。未有可用分类目录前只接受空 categories。校验失败为 common.validation_failed。现有 EgressPolicy 类型不变，原存储接口的可选 If-Match 语义保留。

平台保存版本与实际出口应用版本分别处理。本批不更改 infra/egress 的网络执行边界：该进程目前只读取环境拒绝列表，仍在每次连接解析后拒绝非公网 IP，包括私网、CGNAT、元数据地址及非公网 IPv6。策略不能覆盖这个边界。

尚需主控接线：出口订阅、版本化下发、持久化应用回报与中转上游出口观测；中转公网域名映射和 TLS 证书健康读取。当前页面明确展示尚未接入，不能将已保存版本称为已生效，或把平台安装配置当作 edge 运行回报。
