# 托管实例接入网络

协议及风险边界见 [设计](../design/managed-network-credential.zh.md) 和
[凭据契约](../../contracts/auth/managed-device.md)。本页描述接线，不代表真实验收通过。

combined 部署由 `WorkspaceManagedBindings` 提供当前实例绑定，IAM 通过本服务公开 API
在线检查，workspace 通过 IAM 的 `ManagedDeviceCredentials` API 签发及吊销。下发在独立
重试循环中进行，不持有 workspace 行锁调用 IAM。停止/删除先改变权威代次，再异步撤销
IAM 旧记录；换取、设备在线检查与网关批量存活检查均核当前代次，不能凭旧 JWT 存活。

部署者提供私密文件 `auriga.workspace.managed-network-installation-file`，内容恰为
`platformOrigin`、`issuer` 和 `network`（沿桌面 ConnectNetwork 的 realm/WSS/根公钥定义）。
该文件只含安装材料，无设备机密。公开 compose 的 HTTPS + collab bootstrap 自动从批准的
collab 根生成 `network-platform/managed-network.json` 并启用配置；文件纳入完整性材料。
旧封存部署不自动补文件，使用公开 prepare 全新生成。镜像须包含本次插件和 agent。
HTTPS DNS 部署中，agent 从同一受信 Access 派生门户别名，将其与控制别名一同挂到已校验的
edge 内部网络端点，供凭据换取和 WSS 访问；仍保持原内部网络和成员校验。旧端点缺少新别名
会失败封闭，需用新栈/新实例网络验证。外部端口转换或 IP 路由需按部署的可达性单独验收。

split 部署还需配置 workspace→IAM 和 IAM→workspace 的 mTLS 证书/key/CA，分别为
`auriga.workspace.iam-client-certificate-file`、`iam-client-key-file`、`iam-ca-file`，以及
`auriga.iam.workspace-client-certificate-file`、`workspace-client-key-file`、`workspace-ca-file`；
目标使用既有 `auriga.remote.iam.target` / `auriga.remote.workspace.target`。common 内部服务
JWT/租户委托须分别授权 svc:workspace、svc:iam，不能使用 edge 或 instance token。

插件使用当前宿主 profile 的私有目录，按 device id 保存同一套网络账本。文件变动或移除
会关闭旧连接及工具，访问令牌到期前自动换取；已证明吊销的材料不会自动恢复。暂时故障
失败封闭并在1–30秒退避后重试。浏览器 consent 和接收控制仍走原 `/auriga/local-login/`
子路由，由 managed 身份门与精确 Origin 检查保护；写操作必须携带匹配 Origin。

真实验证须使用 HTTPS/WSS 和共享栈锁。测试 CA 仅通过测试进程的受信 CA 文件载入，
不改系统信任，不关闭 TLS 校验。健康状态、模块测试和工具出现不能代替严格 READY；
双端互发须另留真实会话证据。

网关的连接对账探针预算（最多 250ms）不用于会话地址登记。`local_register` 的
collab RPC 使用独立 4 秒硬上限，容纳在线授权、耐久提交及响应签名，并给客户端
5 秒登记尝试留下返回时间；更早的父请求取消或截止仍优先。超时不代表未提交，
客户端保留原 pending 操作并按同一操作恢复，不能删账本或重新分配号码。

网络是可选能力。永久拒绝标记 `disabled/permission_denied`，该代不再重试，照常启动无网络实例；
权限恢复后下次start/restart重新申请。暂时故障最多等待30秒，随后 `deferred/issuer_unavailable` 放行，
后台每30秒至多一次签发/探测；材料到位后排配置重启下发。状态和原因通过Instance的
`managedNetworkState`、`managedNetworkReason` 返回，日志仅记录实例、状态和公开原因码。

用户自助的会话列表、登出其他会话和凭据变更不处理managed_device会话。workspace每30秒检查
已有材料的后端会话，单独会话失效会换代重签；设备本身被管理员吊销时记录永久拒绝，不自动绕过。
插件本地logout关闭当前材料，恢复需实例正常stop/start取得新材料。

公开换取只为凭据核验失败计入5分钟失败桶：实例60、租户3000、可信edge客户端IP300；
成功凭据不读取失败桶。来源取已认证hop的 `X-Auriga-Client-IP`，不采信任意直连/转发头。
429时插件保持未过期Bearer及现有lifecycle，以1–30秒退避；到期仍不能换取则关闭网络。
split未启用托管网络时无需配置新增workspace→IAM凭据通道。
