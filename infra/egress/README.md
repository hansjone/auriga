# 实例出网策略

Compose 与 NAS 的共享出口通过两个独立命名卷接收本安装租户的策略。平台独占写入策略卷，出口只读；出口独占写入状态/回报卷，平台只读并将回报存入 Org 数据库。实例不挂载这两个卷，也没有新增网络管理端口。

首次部署前，请在管理台 `/security/egress` 核对允许域名。**允许列表为空会拒绝全部实例出网**，包括初始数据库中的空策略。策略更新会关闭现有 CONNECT 隧道，客户端需重新连接。

平台配置（两套 Compose 已接入）：

- `AURIGA_EGRESS_POLICY_DIRECTORY=/var/auriga-egress/policies`
- `AURIGA_EGRESS_REPORT_DIRECTORY=/var/auriga-egress/reports`
- `AURIGA_EGRESS_NODE_ID=egress-primary`
- `AURIGA_SECURITY_EDGE_CONFIG_FILE=/etc/auriga/edge.config.json`：仅投影中转入口/后端地址，不返回凭据。

出口配置：

- `AURIGA_EGRESS_POLICY_FILE=/var/auriga-egress/policies/policy.json`
- `AURIGA_EGRESS_STATE_FILE=/var/auriga-egress/reports/state.json`
- `AURIGA_EGRESS_REPORT_FILE=/var/auriga-egress/reports/report.json`
- `AURIGA_EGRESS_NODE_ID` 必须与平台配置一致。

策略格式由 `contracts/deployment/egress.schema.json` 定义。出口每 2 秒读取策略，拒绝版本回退、同版本换内容、租户变化和未知字段；先持久化版本，再应用和回报。冷启动在获得策略之前拒绝出网，重启恢复本安装已有的策略与版本底线。勿跨安装复用状态卷。

仅支持精确域名与 `*.example.com` 子域匹配；通配符不包含根域。拒绝项优先。分类尚无注册表，只接受空数组。无论允许列表如何设置，环境拒绝列表、私网、回环、链路本地、CGNAT、元数据地址等原有边界均继续生效；每次连接重新检查解析结果并固定拨号 IP。

管理台分别展示保存、下发和生效版本。`effective` 仅代表相同版本的节点成功回报，30 秒未更新后为 `stale`；刷新页面获取新回报。失败或未知不能当作生效。中转默认上游出口为部署配置的直连，提供商单独设置的代理仍以中转站配置为准，不受实例策略管理。

本通道只支持单宿主、单安装租户、单共享出口节点。多节点分发、浏览器 TLS 证书到期观测和公网出口探测需另接可信观测源。未设置策略文件的独立启动模式保持原有公网代理行为。
