# 管理台现场终端在线观测

管理入口 `/runtime/devices` 经正常 edge 管理会话、CSRF、IAM 权限与近期身份验证访问。
设备绑定通过已有 IAM mTLS gRPC 通道读取，沿用 edge 内部 JWT 文件和租户委托；
IAM 的 `AdminDeviceBindingsService.ReadBindings` 只允许 `svc:edge` 与非平台委托租户。
旧的 `/admin/devices/connection-bindings` HTTP 入口已移除，edge 继续对外拒绝此路径。
绑定仅含本租户现场设备成功兑换的 server/CID/account/ingress，不含票据、token 或 user key。

## 配置与部署材料

edge 增加可选 `devicePresence`（JSON 配置）：

```json
{
  "devicePresence": {
    "url": "tls://network-broker:14224",
    "username": "edge-presence",
    "passwordFile": "/etc/auriga/pki/network-presence.password",
    "caFile": "/etc/auriga/pki/network-server-ca.pem",
    "certFile": "/etc/auriga/pki/network-presence.pem",
    "keyFile": "/etc/auriga/pki/network-presence.key",
    "serverName": "broker.network.internal"
  }
}
```

也可使用 `credsFile`，与 `username`/`passwordFile` 二选一。不得复用浏览器或 DSH 凭据。
非回环地址必须使用 TLS、私有 CA 与客户端证书，校验服务器名称；不支持关闭证书校验。
环境变量前缀为 `AURIGA_EDGE_DEVICE_PRESENCE_`，后缀分别为 `URL`、`USERNAME`、
`PASSWORD_FILE`、`CREDS_FILE`、`CA_FILE`、`CERT_FILE`、`KEY_FILE`、`SERVER_NAME`。
仅数字回环 IP 可用明文 NATS，供本机测试。

部署生成独立 SYS 用户 `edge-presence`、独立随机密码和客户端证书，不复用带 LDM 的 ops 用户。
该用户仅可发布 `$SYS.REQ.SERVER.*.CONNZ`、`$SYS.REQ.SERVER.*.KICK`，订阅私有 `_INBOX.>`；
Broker 的 auth_callout 排除列表同步加入该用户，DSH 无系统账户权限。
compose 的 Broker 位于独立 `network-control` 内部 bridge，TCP 和 WSS 仅监听容器接口，不使用 host 网络或发布宿主端口。
edge 加入该内部网络后访问 Broker；使用 host 网络的网关通过 Broker 的固定私有地址访问。
这样容器中的 edge 可达特权端口，宿主外部网络不能通过端口发布访问它；同网容器仍须通过 mTLS 和专用 SYS 账户鉴权。
仅 Broker 服务端证书包含连接地址的 IP SAN，两张客户端证书不包含 IP SAN；edge 校验固定 DNS 名称。
该拓扑使用新的 compose 封存配方；已有旧配方的 M2 测试安装须使用新的材料目录，prepare 会拒绝静默复用旧监听地址。
NAS 模板使用 `network-broker:14224`，脚本将新材料复制进 edge 专用目录，不把 ops 秘密交给 edge。

旧 NAS 安装缺少新增专用证书、密钥和密码时，init/render 跳过这组可选材料，沿用原封存配置。
历史材料仍须通过原始收据的完整哈希校验；从新安装删除这组文件不会被当作旧安装接受。
旧安装重入时不补签证书或轮换 Broker 凭据，在线观测在带完整专用材料的新安装中启用。
旧配置未启用观测且设备仍有绑定时，页面显示状态未知；管理员撤销凭据后可见断开待确认。

## 在线、心跳与撤销

在线来自按账户过滤的完整 Broker CONNZ 快照，逐条核对 server 世代、CID、账户和 ingress。
当前 server 的查询优先；仅在有效 Broker 连接明确返回旧 server 的 no-responders 时认定旧世代已失效。
单组超时显示该组设备“状态未知”，不拖垮当前 server 的其他设备；权限、证书、归属或截断异常不会冒在线。
有绑定时，未配置观测或 Broker 不可达显示未知。实现面向现有单 Broker 拓扑，不将网络分区当作集群成员下线证明。

`lastSeenAt` 是 Broker 业务活动时间，不作为心跳。
`lastHeartbeatAt` 只来自实际经过本 edge WSS 的 NATS PING/PONG 或 WebSocket PING/PONG。
观测器跳过 PUB/HPUB 内容，不记录凭据或应用载荷；首次观测前及重启后不补造时间。
匹配身份后的心跳按完整连接绑定保留在内存，最多一万条，满时淘汰最旧记录。
为解析真实心跳，WSS 代理不协商压缩；这会增加可压缩流量的带宽，后续支持压缩需同步实现帧解压观测。

撤销先由 IAM 提交设备与节点吊销，再由 edge 核对归属并 KICK。
只有回读确认全部匹配连接离线才返回 204；已吊销但断开未确认返回 202，包含
`credentialRevoked: true`、`disconnectStatus: pending`。UUID 大小写归一化。
绑定读取成功且目标设备没有绑定时，表示从未连接或连接已过期，直接确认离线并返回 204。
前端立即回读并显示已撤销状态、断开未确认提示和“重试断开”，不把部分完成显示为全部失败或全部成功。
此类已撤销设备读回 `offline`、`confirmed`；绑定读取失败或有绑定而 Broker 查询失败时保持未知和 pending。
重复撤销幂等，保留近期身份验证门禁。

在线筛选在 edge 补充状态后执行。每请求新建观测连接以读取当前秘密和快照，完成即关闭；
最多四个并发观测、每请求三秒预算、每组 250ms、最多 64 组 CONNZ，溢出显示未知。
未实现跨请求连接池/缓存；大量设备时 IAM 列表仍有 N+1 成员查询且未分页，后续应单独优化。
成功兑换提交到 Broker CONNZ 可见之间仍有竞态；吊销阻止后续兑换，但单次 KICK 不承诺覆盖尚未出现的连接。
断开确认只代表该次完整快照，必要时再次刷新/重试，由现有策略更新机制最终收敛。

验证：`go test -run TestDevice -count=1 ./...`，含私有 CA+mTLS 真实 Broker/WSS、证书负例、
旧世代隔离、真实心跳、大小写撤销、空绑定确认、断开回读、旁路存活及权属负例。
