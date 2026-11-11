# 管理台现场终端在线观测

管理入口 `/runtime/devices` 经正常 edge 管理会话、CSRF 和 IAM 权限校验访问。
IAM 的 `/admin/devices/connection-bindings` 只供该代理的管理员委托读取；edge 对外拒绝此路径。
它只返回本租户现场设备兑换成功的 server/CID/account/ingress 绑定，不含票据、token 或 user key。

## 配置

edge 增加可选 `devicePresence`（JSON 配置）：

```json
{
  "devicePresence": {
    "url": "nats://broker.example.com:4222",
    "username": "network-ops",
    "passwordFile": "/run/secrets/network-ops.password"
  }
}
```

也可使用 `credsFile`，与 `username`/`passwordFile` 二选一。不得复用浏览器或 DSH 凭据。
这是现有网络网关系统账户 CONNZ/KICK 模式；凭据需属于 Broker 的系统账户，最小权限为：

- 发布 `$SYS.REQ.SERVER.*.CONNZ` 和 `$SYS.REQ.SERVER.*.KICK`。
- 订阅该凭据自身的私有 `_INBOX.>` 响应；不向 DSH 授予系统账户权限。

环境变量与字段对应：`AURIGA_EDGE_DEVICE_PRESENCE_URL`、
`AURIGA_EDGE_DEVICE_PRESENCE_CREDS_FILE`、`AURIGA_EDGE_DEVICE_PRESENCE_USERNAME`、
`AURIGA_EDGE_DEVICE_PRESENCE_PASSWORD_FILE`。
未配置、Broker 不可达、快照截断或绑定不匹配时，页面显示“状态未知”，不据此认定离线。
需要为部署的 edge 配置上述凭据及 WSS 上游；本实现不修改 Broker 的 ACL 或部署秘密。

## 在线、心跳与撤销

在线状态来自完整的、按账户过滤的 Broker CONNZ 快照，逐条核对 server 世代、CID、账户、ingress。
`lastSeenAt` 是 Broker 的业务活动时间；它不作为心跳。
`lastHeartbeatAt` 只来自实际经过当前 edge WSS 的客户端 NATS PING/PONG 或 WebSocket PING/PONG。
观测器有固定缓冲及条目上限，跳过 PUB/HPUB 内容，不记录凭据或应用载荷。
未经过本 edge、首次观测前或 edge 重启后没有心跳记录时，该字段缺省；不会用查询时间补造。
最近一次已经匹配身份的心跳在进程内按完整连接绑定保留，断连后仍可展示；不持久化。

撤销先由 IAM 校验近期身份验证并提交设备和节点吊销，再由 edge 核对归属后 KICK。
只有再次查询确认相关连接均已离线才返回成功；失联或无法确认时返回 503，凭据吊销可能已经提交，管理员应刷新确认。
普通列表在线筛选在 edge 完成真实状态补充后执行。对 Broker 的读取/断连有统一 3 秒预算及容量上限。

验证入口：`go test -run TestDevice -count=1 ./...`，包含真实随机端口 Broker/WSS、心跳、断连读回、旁路终端存活及权属负例。
